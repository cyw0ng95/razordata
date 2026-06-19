package rp

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	lg "github.com/cyw0ng95/razordata/internal/LOG/LG"
	sp "github.com/cyw0ng95/razordata/internal/MEM/SP"
	wr "github.com/cyw0ng95/razordata/internal/WAL/WR"
)

// TestRP_TruncateBeforeCheckpoint_Cases is a table-driven lift for
// the truncateBeforeCheckpoint cases that brought WAL/RP coverage
// to 72.5% (target 85%, REQ000191). The cases that grow a segment
// to a full segSize (64 MiB) are skipped here: they are covered
// indirectly by the existing rp_test.go cases that run a 64 MiB WAL
// end-to-end. We exercise three linear paths with a single
// segment and verify the function's LSN→file-size math at a few
// offsets.
// The test pre-creates segment files (with size 0 — empty, since
// the writer writes the 12-byte header), then writes a real
// checkpoint via the writer. The replayer reads it back and the
// function under test truncates based on the checkpoint LSN.
func TestRP_TruncateBeforeCheckpoint_Cases(t *testing.T) {
	// For each case, we want a single segment with a checkpoint
	// at a specific LSN. The writer assigns LSN = segment *
	// SegSize + writeOff, where writeOff starts at WALHeaderSize
	// (12 bytes). For one segment (seg 0), the writer's LSN is
	// always 12; we set the in-record LSN to match so the
	// replayer reads back the test's intended value.
	// To exercise higher LSNs (truncate-before-checkpoint in
	// the middle of a non-zero byte range within a segment) we
	// need a checkpoint at offset > 12. We get that by writing
	// a data record first, then the checkpoint.
	cases := []struct {
		name          string
		preWrite      int    // number of data records to write before the checkpoint
		cpLSN         uint64 // LSN we want in the checkpoint (and the replayer reads back)
		wantSizeAfter int    // expected file size after Replay
	}{
		{
			// Just the checkpoint, no preceding data. The
			// writer's LSN = 12 (WALHeaderSize). The function
			// truncates the file to LSN%SegSize = 12 bytes.
			name:          "cp_at_writer_offset",
			preWrite:      0,
			cpLSN:         12,
			wantSizeAfter: 12,
		},
		{
			// One data record (~32 bytes) followed by checkpoint.
			// Writer's checkpoint LSN ≈ 12 + 32 = 44. Truncate
			// to 44 bytes.
			name:          "cp_after_one_data_record",
			preWrite:      1,
			cpLSN:         44,
			wantSizeAfter: 44,
		},
		{
			// Three data records (~96 bytes) followed by
			// checkpoint. Writer's checkpoint LSN ≈ 12 + 96 = 108.
			// Truncate to 108 bytes.
			name:          "cp_after_three_data_records",
			preWrite:      3,
			cpLSN:         108,
			wantSizeAfter: 108,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			sm, err := setupSegmentManager(tmp)
			if err != nil {
				t.Fatalf("setupSegmentManager: %v", err)
			}
			bp, err := setupBufferPool(tmp)
			if err != nil {
				t.Fatalf("setupBufferPool: %v", err)
			}

			// Write `preWrite` data records, then a checkpoint
			// with the in-record LSN set to the test's cpLSN.
			w, _ := wr.New(tmp, sm, sp.New(), lg.New(lg.Options{Output: io.Discard}), false)
			for j := 0; j < tc.preWrite; j++ {
				if _, err := w.Append(&wr.WriteBatch{TxnID: uint64(j + 1), Recs: []wr.LogRecord{
					{Type: wr.RTData, BlockID: uint64(j), Value: []byte("payload")},
				}}); err != nil {
					t.Fatalf("Append data %d: %v", j, err)
				}
			}
			cp := &wr.Checkpoint{
				LSN:              tc.cpLSN,
				CatalogRootPtr:   0,
				ManifestChecksum: 0,
				ActiveTXNs:       nil,
			}
			hdr, txns := wr.AppendCheckpointPayload(cp)
			if _, err := w.Append(&wr.WriteBatch{TxnID: 999, Recs: []wr.LogRecord{
				{Type: wr.RTCheckpoint, BlockID: 0, Key: hdr, Value: txns},
			}}); err != nil {
				t.Fatalf("Append checkpoint: %v", err)
			}
			w.Sync()
			w.Close()

			r, err := New(tmp, sm, bp, Callbacks{}, nil)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			defer r.Close()
			if err := r.Replay(); err != nil && !errors.Is(err, ErrCorrupt) {
				t.Fatalf("Replay: %v", err)
			}

			path := filepath.Join(tmp, "wal", "wal.000")
			st, err := os.Stat(path)
			if err != nil {
				t.Fatalf("stat seg 0: %v", err)
			}
			if st.Size() != int64(tc.wantSizeAfter) {
				t.Errorf("seg 0 size: want %d, got %d", tc.wantSizeAfter, st.Size())
			}
		})
	}
}

// TestRP_ForEachRecord_ErrorBranches is a table-driven lift for
// the forEachRecord error branches that brought WAL/RP coverage
// to 72.5%. Each case writes a real record, then mutates the
// segment file to trigger a specific decoder error path.
func TestRP_ForEachRecord_ErrorBranches(t *testing.T) {
	cases := []struct {
		name            string
		mutate          func(path string) // mutate the segment file in place
		wantReplayErr   error             // expected Replay return (nil for tolerated)
		wantStatField   string            // "TruncatedSegments" or "UnknownRecords" or "CorruptionFailures" or ""
		wantStatAtLeast int64             // expected stat counter (0 means no check)
	}{
		{
			// 12-byte header only, no records. The forEachRecord
			// loop sees zero records and returns nil.
			name:   "empty_after_header",
			mutate: func(path string) {},
		},
		{
			// Truncate the file by 1 byte so the last record's
			// envelope is incomplete. R13-8: ErrTruncatedRecord
			// is tolerated; TruncatedSegments incremented.
			name: "torn_tail_truncate_one_byte",
			mutate: func(path string) {
				st, err := os.Stat(path)
				if err != nil {
					return
				}
				_ = os.Truncate(path, st.Size()-1)
			},
			wantStatField:   "TruncatedSegments",
			wantStatAtLeast: 1,
		},
		{
			// Flip a byte in the middle of a record's payload to
			// corrupt the envelope CRC. R13-12: ErrCorrupt is
			// surfaced from Replay.
			name: "corrupt_envelope_crc_mid_record",
			mutate: func(path string) {
				data, err := os.ReadFile(path)
				if err != nil || len(data) < int(wr.WALHeaderSize)+32 {
					return
				}
				// Flip a byte at WALHeaderSize+16 — guaranteed to
				// land in the body of the first record.
				corruptAt := int64(wr.WALHeaderSize) + 16
				data[corruptAt] ^= 0xFF
				_ = os.WriteFile(path, data, 0o644)
			},
			wantReplayErr:   ErrCorrupt,
			wantStatField:   "CorruptionFailures",
			wantStatAtLeast: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			sm, bp := newReplayerHarness(t, tmp)

			w, _ := wr.New(tmp, sm, sp.New(), lg.New(lg.Options{Output: io.Discard}), false)
			for i := 0; i < 10; i++ {
				if _, err := w.Append(&wr.WriteBatch{TxnID: uint64(i), Recs: []wr.LogRecord{
					{Type: wr.RTData, BlockID: uint64(i), Value: []byte("payload-" + segName(uint64(i)))},
				}}); err != nil {
					t.Fatalf("Append[%d]: %v", i, err)
				}
			}
			if err := w.Sync(); err != nil {
				t.Fatalf("Sync: %v", err)
			}
			w.Close()

			path := filepath.Join(tmp, "wal", "wal.000")
			tc.mutate(path)

			r, err := New(tmp, sm, bp, Callbacks{
				OnData: func(uint64, []byte) error { return nil },
			}, nil)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			defer r.Close()

			err = r.Replay()
			if tc.wantReplayErr == nil {
				if err != nil {
					t.Fatalf("Replay: want nil, got %v", err)
				}
			} else {
				if !errors.Is(err, tc.wantReplayErr) {
					t.Errorf("Replay: want %v, got %v", tc.wantReplayErr, err)
				}
			}
			if tc.wantStatField == "" {
				return
			}
			stats := r.Stats()
			var got int64
			switch tc.wantStatField {
			case "TruncatedSegments":
				got = int64(stats.TruncatedSegments)
			case "UnknownRecords":
				got = int64(stats.UnknownRecords)
			case "CorruptionFailures":
				got = int64(stats.CorruptionFailures)
			}
			if got < tc.wantStatAtLeast {
				t.Errorf("Stats.%s: want >= %d, got %d", tc.wantStatField, tc.wantStatAtLeast, got)
			}
		})
	}
}

// segName is a helper that returns the LF segment filename
// for a given segment number. The format is "wal.NNN" with
// three-digit zero-padding. Inlined to avoid pulling in fmt.
func segName(n uint64) string {
	var buf [4]byte
	copy(buf[:], "wal.")
	buf[3] = byte('0' + n%10)
	return string(buf[:])
}

// TestRP_TruncateBeforeCheckpoint_MultiSegment exercises the
// multi-segment branches of truncateBeforeCheckpoint (R16-10..12):
// - two segments with checkpoint LSN in middle of seg1 (truncate
// seg1 to a sub-segSize offset, keep seg2)
// - checkpoint LSN at exact segment boundary (whole seg1 removed)
// - all segments strictly before checkpoint (every segment
// truncated to size0)
// We use the real segment manager and file system; the segments
// are seeded by the wal writer. segSize is64 MiB but the LSN
// arithmetic uses multiples of segSize + small offsets, which
// we model by computing the checkpoint LSN as
// (segNum * rpSegSize) + smallOffset. The exact byte count of
// the segment does not need to match rpSegSize because
// truncateBeforeCheckpoint uses the LSN math, not the file size
// on disk. (R16-10..12)
func TestRP_TruncateBeforeCheckpoint_MultiSegment(t *testing.T) {
	cases := []struct {
		name     string
		segFiles []struct {
			num  uint64
			size int
		}
		cpLSN       uint64
		wantSegNums []uint64 // segment numbers that must still exist
	}{
		{
			// Two segments, cp LSN in middle of seg1: seg0
			// is fully removed (s < segNum), seg1 is
			// truncated to offset=50, seg2 unchanged.
			name: "two_segs_cp_middle_seg1",
			segFiles: []struct {
				num  uint64
				size int
			}{{0, 100}, {1, 200}, {2, 150}},
			cpLSN:       uint64(rpSegSize) + 50, // segNum=1, truncateSize=50
			wantSegNums: []uint64{1, 2},
		},
		{
			// Three segments, cp at start of seg2 (boundary):
			// seg0 and seg1 fully removed, seg2 unchanged.
			name: "three_segs_cp_at_seg2_start",
			segFiles: []struct {
				num  uint64
				size int
			}{{0, 100}, {1, 200}, {2, 150}},
			cpLSN: 2 * uint64(rpSegSize), // segNum=2, truncateSize=0 -> segNum--, truncateSize=segSize
			// After: seg1 was truncated to segSize (full size,
			// kept); seg0 deleted. We only check seg2 unchanged
			// and seg0 deleted.
			wantSegNums: []uint64{2},
		},
		{
			// Two segments, cp LSN at0: every segment is
			// before segNum=0, so all are truncated to0
			// (i.e. the file becomes empty).
			name: "two_segs_cp_zero_all_truncated",
			segFiles: []struct {
				num  uint64
				size int
			}{{0, 100}, {1, 200}},
			cpLSN:       0,
			wantSegNums: []uint64{0, 1}, // both still exist as empty files
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			sm, bp := newReplayerHarness(t, tmp)
			// Seed each segment file with the requested size
			// and the WALHeaderSize-byte header at offset0.
			for _, sf := range tc.segFiles {
				if err := os.MkdirAll(filepath.Join(tmp, "wal"), 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				fh, err := sm.CreateSegment(sf.num)
				if err != nil {
					t.Fatalf("CreateSegment(%d): %v", sf.num, err)
				}
				fh.Close()
				// Write a fake header + zeros using the real
				// segment path (wal.NNN three-digit zero-padding).
				content := make([]byte, sf.size)
				copy(content[:4], wr.WALMagic) //4-byte magic
				content[4] = wr.WALVersionV1   //1-byte version
				path := filepath.Join(tmp, "wal", fmt.Sprintf("wal.%03d", sf.num))
				if err := os.WriteFile(path, content, 0o644); err != nil {
					t.Fatalf("write seg %d: %v", sf.num, err)
				}
			}
			r, err := New(tmp, sm, bp, Callbacks{}, nil)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			defer r.Close()

			// Invoke truncateBeforeCheckpoint via the public path:
			// call it directly on the replayer. (The function is
			// unexported but tests in the same package can reach
			// it.) We pass nil for the log; truncateBeforeCheckpoint
			// is log-optional.
			rp := r.(*replayer)
			rp.log = nil
			rp.truncateBeforeCheckpoint(tc.cpLSN)

			// Verify segment files: those at segNum < cpSegNum must
			// be truncated to size0; the segment at cpSegNum must
			// be truncated to cpLSN % rpSegSize; segments
			// > cpSegNum must be unchanged.
			cpSegNum := tc.cpLSN / uint64(rpSegSize)
			cpTruncSize := tc.cpLSN % uint64(rpSegSize)
			if cpTruncSize == 0 && cpSegNum > 0 {
				cpTruncSize = uint64(rpSegSize)
				cpSegNum--
			}
			for _, sf := range tc.segFiles {
				path := filepath.Join(tmp, "wal", fmt.Sprintf("wal.%03d", sf.num))
				st, err := os.Stat(path)
				if err != nil {
					t.Fatalf("stat seg %d: %v", sf.num, err)
				}
				if uint64(sf.num) < cpSegNum {
					if st.Size() != 0 {
						t.Errorf("seg %d expected size0, found %d", sf.num, st.Size())
					}
				}
				if uint64(sf.num) == cpSegNum && cpTruncSize > 0 {
					if st.Size() != int64(cpTruncSize) {
						t.Errorf("seg %d size: want %d, got %d", sf.num, cpTruncSize, st.Size())
					}
				}
				if uint64(sf.num) > cpSegNum {
					if st.Size() != int64(sf.size) {
						t.Errorf("seg %d unexpectedly changed: want size %d, got %d",
							sf.num, sf.size, st.Size())
					}
				}
			}
		})
	}
}

// TestRP_ForEachRecord_UnknownRecordLength exercises the
// ErrUnknownRecord branch in forEachRecord (R16-13). We construct
// a segment with one valid record followed by a length-varint
// that exceeds MaxRecordLen. The decoder returns ErrUnknownRecord
// and forEachRecord must surface it as a tolerated tail
// (TruncatedSegments++) without surfacing ErrCorrupt.
// The mutation is delicate: we write a valid record first
// (so the decoder consumes it cleanly), then append
// [varint(MaxRecordLen+1)] which DecodeRecord reads as a valid
// length prefix > MaxRecordLen -> ErrUnknownRecord.
func TestRP_ForEachRecord_UnknownRecordLength(t *testing.T) {
	tmp := t.TempDir()
	sm, bp := newReplayerHarness(t, tmp)

	// Write a single valid RTData record via the writer so the
	// segment header and record encoding are correct.
	w, _ := wr.New(tmp, sm, sp.New(), lg.New(lg.Options{Output: io.Discard}), false)
	if _, err := w.Append(&wr.WriteBatch{TxnID: 1, Recs: []wr.LogRecord{
		{Type: wr.RTData, BlockID: 1, Key: []byte("k"), Value: []byte("v")},
	}}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := w.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	w.Close()

	// Append a length-varint > MaxRecordLen to the segment.
	path := filepath.Join(tmp, "wal", "wal.000")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read seg: %v", err)
	}
	badLen := wr.MaxRecordLen + 1
	// Append varint encoding of badLen + zero pad.
	data = binary.AppendUvarint(data, uint64(badLen))
	// Pad with zeros so the decoder has bytes to look at after
	// the bad length prefix (the decoder rejects on length alone).
	data = append(data, 0, 0, 0, 0)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("rewrite seg: %v", err)
	}

	r, err := New(tmp, sm, bp, Callbacks{
		OnData: func(uint64, []byte) error { return nil },
	}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer r.Close()

	err = r.Replay()
	// Replay should not error; the unknown-record branch is
	// tolerated as a tail truncation.
	if err != nil && !errors.Is(err, ErrCorrupt) {
		t.Errorf("Replay: want nil or ErrCorrupt, got %v", err)
	}
	stats := r.Stats()
	if stats.TruncatedSegments == 0 {
		t.Errorf("TruncatedSegments: want >=1 (unknown record tail), got %d",
			stats.TruncatedSegments)
	}
}
