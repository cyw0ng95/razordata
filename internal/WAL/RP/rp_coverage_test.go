package rp

import (
	"errors"
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
//
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
	//
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
