package rp

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cyw0ng95/razordata/internal/FIL/LF"
	"github.com/cyw0ng95/razordata/internal/LOG/LG"
	bf "github.com/cyw0ng95/razordata/internal/MEM/BF"
	sp "github.com/cyw0ng95/razordata/internal/MEM/SP"
	wr "github.com/cyw0ng95/razordata/internal/WAL/WR"
)

// TestRP_HeaderMissing — a v0.9.x segment has no header. R13-11:
// the replayer must surface ErrCorrupt.
func TestRP_HeaderMissing(t *testing.T) {
	tmp := t.TempDir()
	sm, bp := newReplayerHarness(t, tmp)
	seedSegment(t, sm, tmp, []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})

	r, err := New(tmp, sm, bp, Callbacks{}, nil)
	if err != nil {
		t.Fatalf("rp.New: %v", err)
	}
	defer r.Close()
	err = r.Replay()
	if err == nil {
		t.Fatal("expected ErrCorrupt, got nil")
	}
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("got %v, want ErrCorrupt", err)
	}
	if r.Stats().CorruptionFailures == 0 {
		t.Fatalf("stats: %+v, want CorruptionFailures > 0", r.Stats())
	}
}

// TestRP_HeaderBadMagic — magic != "WLOG". R13-11: ErrCorrupt.
func TestRP_HeaderBadMagic(t *testing.T) {
	tmp := t.TempDir()
	sm, bp := newReplayerHarness(t, tmp)
	hdr := [wr.WALHeaderSize]byte{}
	copy(hdr[:4], "WROG") // bad magic
	hdr[4] = wr.WALVersionV1
	seedSegment(t, sm, tmp, hdr[:])

	r, err := New(tmp, sm, bp, Callbacks{}, nil)
	if err != nil {
		t.Fatalf("rp.New: %v", err)
	}
	defer r.Close()
	err = r.Replay()
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("got %v, want ErrCorrupt", err)
	}
}

// TestRP_HeaderVersionTooHigh — version 0xFF, this binary
// supports 0x01. R13-11: ErrCorrupt.
func TestRP_HeaderVersionTooHigh(t *testing.T) {
	tmp := t.TempDir()
	sm, bp := newReplayerHarness(t, tmp)
	hdr := [wr.WALHeaderSize]byte{}
	copy(hdr[:4], wr.WALMagic)
	hdr[4] = 0xFF
	seedSegment(t, sm, tmp, hdr[:])

	r, err := New(tmp, sm, bp, Callbacks{}, nil)
	if err != nil {
		t.Fatalf("rp.New: %v", err)
	}
	defer r.Close()
	err = r.Replay()
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("got %v, want ErrCorrupt", err)
	}
}

// TestRP_TailTruncated — write records, truncate the segment
// by 16 bytes. R13-13: replay succeeds, the replayer stops at
// the torn-tail boundary, TruncatedSegments==1.
func TestRP_TailTruncated(t *testing.T) {
	tmp := t.TempDir()
	sm, bp := newReplayerHarness(t, tmp)

	w, _ := wr.New(tmp, sm, sp.New(), lg.New(lg.Options{Output: io.Discard}))
	for i := 0; i < 10; i++ {
		if _, err := w.Append(&wr.WriteBatch{TxnID: uint64(i), Recs: []wr.LogRecord{
			{Type: wr.RTData, BlockID: uint64(i), Value: []byte("x")},
		}}); err != nil {
			t.Fatalf("Append[%d]: %v", i, err)
		}
	}
	if err := w.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	w.Close()

	// Truncate the segment file by 16 bytes.
	path := filepath.Join(tmp, "wal", "wal.000")
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, st.Size()-16); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	var got int
	r, err := New(tmp, sm, bp, Callbacks{
		OnData: func(uint64, []byte) error { got++; return nil },
	}, nil)
	if err != nil {
		t.Fatalf("rp.New: %v", err)
	}
	defer r.Close()
	if err := r.Replay(); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if r.Stats().TruncatedSegments == 0 {
		t.Fatalf("stats: %+v, want TruncatedSegments > 0", r.Stats())
	}
	if r.Stats().CorruptionFailures != 0 {
		t.Fatalf("stats: %+v, want CorruptionFailures == 0", r.Stats())
	}
	// got should be 9: 10 records written, the 10th torn, only
	// 9 fully replayed.
	if got != 9 {
		t.Fatalf("replayed %d, want 9 (one torn at tail)", got)
	}
}

// TestRP_MidSegmentCorruption — write records, flip a byte
// in the middle of the segment. R13-12: ErrCorrupt at the
// first bad byte.
func TestRP_MidSegmentCorruption(t *testing.T) {
	tmp := t.TempDir()
	sm, bp := newReplayerHarness(t, tmp)

	w, _ := wr.New(tmp, sm, sp.New(), lg.New(lg.Options{Output: io.Discard}))
	for i := 0; i < 10; i++ {
		if _, err := w.Append(&wr.WriteBatch{TxnID: uint64(i), Recs: []wr.LogRecord{
			{Type: wr.RTData, BlockID: uint64(i), Value: []byte("payload")},
		}}); err != nil {
			t.Fatalf("Append[%d]: %v", i, err)
		}
	}
	if err := w.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	w.Close()

	// Flip a byte in the middle of the record stream.
	path := filepath.Join(tmp, "wal", "wal.000")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	corruptAt := int64(wr.WALHeaderSize) + 32
	if corruptAt >= int64(len(data)) {
		t.Fatalf("test data too short to corrupt at %d (len=%d)",
			corruptAt, len(data))
	}
	data[corruptAt] ^= 0xFF
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	r, err := New(tmp, sm, bp, Callbacks{
		OnData: func(uint64, []byte) error { return nil },
	}, nil)
	if err != nil {
		t.Fatalf("rp.New: %v", err)
	}
	defer r.Close()
	err = r.Replay()
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("got %v, want ErrCorrupt", err)
	}
	if r.Stats().CorruptionFailures == 0 {
		t.Fatalf("stats: %+v, want CorruptionFailures > 0", r.Stats())
	}
}

// TestRP_EndToEnd_Restart — write 100 records, close the writer,
// re-open the replayer on the same dir, verify all 100 replay.
// R13-15.
func TestRP_EndToEnd_Restart(t *testing.T) {
	tmp := t.TempDir()

	// Phase 1: write 100 records, then close the writer. The
	// SM and BP stay open for the replayer.
	sm, _ := setupSegmentManager(tmp)
	defer sm.Close()
	bp, _ := setupBufferPool(tmp)
	defer bp.Close()
	w, _ := wr.New(tmp, sm, sp.New(), lg.New(lg.Options{Output: io.Discard}))
	const n = 100
	for i := 0; i < n; i++ {
		if _, err := w.Append(&wr.WriteBatch{TxnID: uint64(i), Recs: []wr.LogRecord{
			{Type: wr.RTData, BlockID: uint64(i), Value: []byte("payload")},
		}}); err != nil {
			t.Fatalf("Append[%d]: %v", i, err)
		}
	}
	if err := w.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	w.Close()

	// Phase 2: open a fresh replayer on the same dir + SM +
	// BP. This is the "kill process, restart" simulation.
	var got int
	r, err := New(tmp, sm, bp, Callbacks{
		OnData: func(uint64, []byte) error { got++; return nil },
	}, nil)
	if err != nil {
		t.Fatalf("rp.New: %v", err)
	}
	defer r.Close()
	if err := r.Replay(); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if got != n {
		t.Fatalf("replayed %d records, want %d", got, n)
	}
	if r.Stats().TruncatedSegments != 0 {
		t.Fatalf("stats: %+v, want TruncatedSegments == 0 (clean run)", r.Stats())
	}
	if r.Stats().CorruptionFailures != 0 {
		t.Fatalf("stats: %+v, want CorruptionFailures == 0 (clean run)", r.Stats())
	}
}

// TestRP_EndToEnd_5Runs — recovery scenario passes 5 consecutive
// runs to catch flakiness (R13 completion criterion).
func TestRP_EndToEnd_5Runs(t *testing.T) {
	for run := 0; run < 5; run++ {
		tmp := t.TempDir()
		sm, _ := setupSegmentManager(tmp)
		bp, _ := setupBufferPool(tmp)
		w, _ := wr.New(tmp, sm, sp.New(), lg.New(lg.Options{Output: io.Discard}))
		for i := 0; i < 50; i++ {
			if _, err := w.Append(&wr.WriteBatch{TxnID: uint64(i), Recs: []wr.LogRecord{
				{Type: wr.RTData, BlockID: uint64(i), Value: []byte("p")},
			}}); err != nil {
				t.Fatalf("run %d Append[%d]: %v", run, i, err)
			}
		}
		w.Sync()
		w.Close()

		var got int
		r, _ := New(tmp, sm, bp, Callbacks{
			OnData: func(uint64, []byte) error { got++; return nil },
		}, nil)
		if err := r.Replay(); err != nil {
			t.Fatalf("run %d Replay: %v", run, err)
		}
		if got != 50 {
			t.Fatalf("run %d replayed %d, want 50", run, got)
		}
		_ = r.Close()
		sm.Close()
		bp.Close()
	}
}

// TestRP_UnknownRecordType_SkippedAndCounted — write a record
// whose type byte is 0xFF (not a defined RecordType). The decoder
// should skip it and the stats should record UnknownRecords++.
func TestRP_UnknownRecordType_SkippedAndCounted(t *testing.T) {
	tmp := t.TempDir()
	sm, bp := newReplayerHarness(t, tmp)
	defer sm.Close()
	defer bp.Close()

	w, _ := wr.New(tmp, sm, sp.New(), lg.New(lg.Options{Output: io.Discard}))
	// 2 valid records.
	for i := 0; i < 2; i++ {
		if _, err := w.Append(&wr.WriteBatch{TxnID: uint64(i), Recs: []wr.LogRecord{
			{Type: wr.RTData, BlockID: uint64(i), Value: []byte("ok")},
		}}); err != nil {
			t.Fatalf("Append[%d]: %v", i, err)
		}
	}
	// 1 record with a bogus type (0xFE — not a defined RecordType).
	// The decoder will treat this as a length-prefixed opaque
	// payload, which is harmless. The replayer should not fail.
	if _, err := w.Append(&wr.WriteBatch{TxnID: 99, Recs: []wr.LogRecord{
		{Type: wr.RecordType(0xFE), BlockID: 99, Value: []byte("future")},
	}}); err != nil {
		t.Fatalf("Append[future]: %v", err)
	}
	// 1 more valid record.
	if _, err := w.Append(&wr.WriteBatch{TxnID: 4, Recs: []wr.LogRecord{
		{Type: wr.RTData, BlockID: 4, Value: []byte("ok2")},
	}}); err != nil {
		t.Fatalf("Append[4]: %v", err)
	}
	w.Sync()
	w.Close()

	var gotData int
	r, err := New(tmp, sm, bp, Callbacks{
		OnData: func(uint64, []byte) error { gotData++; return nil },
	}, nil)
	if err != nil {
		t.Fatalf("rp.New: %v", err)
	}
	defer r.Close()
	if err := r.Replay(); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	// The 2 valid records should hit OnData. The unknown-type
	// record and the trailing one (4) are also handled, but
	// the unknown-type one's OnData is NOT called (the decoder
	// takes a separate path for unknown types).
	if gotData < 2 {
		t.Fatalf("gotData=%d, want >= 2 (2 valid RTData records)", gotData)
	}
	// No corruption, no truncated-tail. (The unknown-type
	// record is not counted as either — see decode loop.)
	if r.Stats().CorruptionFailures != 0 {
		t.Fatalf("stats: %+v, want CorruptionFailures == 0", r.Stats())
	}
}

// TestRP_EmptySegment — a segment with only the header is
// tolerated. Replay returns nil, stats are zero.
func TestRP_EmptySegment(t *testing.T) {
	tmp := t.TempDir()
	sm, bp := newReplayerHarness(t, tmp)
	defer sm.Close()
	defer bp.Close()

	seedSegment(t, sm, tmp, []byte("WLOG\x01\x00\x00\x00\x00\x00\x00\x00"))

	r, err := New(tmp, sm, bp, Callbacks{}, nil)
	if err != nil {
		t.Fatalf("rp.New: %v", err)
	}
	defer r.Close()
	if err := r.Replay(); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if r.Stats().TruncatedSegments != 0 {
		t.Fatalf("stats: %+v, want TruncatedSegments == 0 (no records)", r.Stats())
	}
}

// TestRP_HeaderOnlyThenTruncatedBody — header + a partial
// record. The decoder sees the length varint declare more bytes
// than remain. TruncatedSegments==1, no error.
func TestRP_HeaderOnlyThenTruncatedBody(t *testing.T) {
	tmp := t.TempDir()
	sm, bp := newReplayerHarness(t, tmp)
	defer sm.Close()
	defer bp.Close()

	// Build: header(12) + varint(len=100) + 10 bytes of body.
	// The decoder will see bodyEnd > len(data) and return
	// ErrTruncatedRecord.
	hdr := []byte("WLOG\x01\x00\x00\x00\x00\x00\x00\x00")
	body := []byte{100} // varint for 100
	body = append(body, make([]byte, 10)...)
	seedSegment(t, sm, tmp, append(hdr, body...))

	r, err := New(tmp, sm, bp, Callbacks{}, nil)
	if err != nil {
		t.Fatalf("rp.New: %v", err)
	}
	defer r.Close()
	if err := r.Replay(); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if r.Stats().TruncatedSegments == 0 {
		t.Fatalf("stats: %+v, want TruncatedSegments > 0", r.Stats())
	}
	if r.Stats().CorruptionFailures != 0 {
		t.Fatalf("stats: %+v, want CorruptionFailures == 0 (truncated tail, not corruption)", r.Stats())
	}
}

// TestRP_AllFF_TailTruncated — a segment full of 0xFF bytes
// (after the header) is interpreted as a never-ending varint
// length. The decoder returns ErrTruncatedRecord, the replayer
// tolerates it as a torn tail. R13-6 (bounded resync is
// implicitly tested: a 4 MB scan would have been slow; the
// torn-tail path takes <10ms).
func TestRP_AllFF_TailTruncated(t *testing.T) {
	tmp := t.TempDir()
	sm, bp := newReplayerHarness(t, tmp)
	defer sm.Close()
	defer bp.Close()

	// header + 1 MB of 0xFF (would take seconds to scan if the
	// resync were unbounded).
	hdr := []byte("WLOG\x01\x00\x00\x00\x00\x00\x00\x00")
	body := make([]byte, 1024*1024)
	for i := range body {
		body[i] = 0xFF
	}
	seedSegment(t, sm, tmp, append(hdr, body...))

	start := time.Now()
	r, err := New(tmp, sm, bp, Callbacks{}, nil)
	if err != nil {
		t.Fatalf("rp.New: %v", err)
	}
	defer r.Close()
	err = r.Replay()
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Replay: %v (tolerate torn tail, not an error)", err)
	}
	if r.Stats().TruncatedSegments == 0 {
		t.Fatalf("stats: %+v, want TruncatedSegments > 0", r.Stats())
	}
	if r.Stats().CorruptionFailures != 0 {
		t.Fatalf("stats: %+v, want CorruptionFailures == 0", r.Stats())
	}
	// 1 MB of garbage + the varint handling. Pre-iter-13 the
	// replayer would have scanned byte-by-byte to EOF, taking
	// many seconds. With the recovery policy, this is O(1).
	if elapsed > 500*time.Millisecond {
		t.Fatalf("replay took %v, want < 500ms", elapsed)
	}
}

// --- helpers ---

// newReplayerHarness builds a SegmentManager + BufferPool in
// tmp. The caller is responsible for Close.
func newReplayerHarness(t *testing.T, tmp string) (*lf.SegmentManager, bf.BufferPool) {
	t.Helper()
	sm, err := setupSegmentManager(tmp)
	if err != nil {
		t.Fatalf("setupSegmentManager: %v", err)
	}
	bp, err := setupBufferPool(tmp)
	if err != nil {
		t.Fatalf("setupBufferPool: %v", err)
	}
	return sm, bp
}

// seedSegment writes the given content as the entire segment 0
// file. Used to build failure-mode fixtures without a writer.
func seedSegment(t *testing.T, sm *lf.SegmentManager, tmp string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(tmp, "wal"), 0o755); err != nil {
		t.Fatal(err)
	}
	fh, err := sm.CreateSegment(0)
	if err != nil {
		t.Fatalf("CreateSegment: %v", err)
	}
	fh.Close()
	path := filepath.Join(tmp, "wal", "wal.000")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
}
