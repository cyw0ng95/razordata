package wr

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/cyw0ng95/razordata/internal/FIL/LF"
	"github.com/cyw0ng95/razordata/internal/LOG/LG"
	"github.com/cyw0ng95/razordata/internal/MEM/SP"
	"golang.org/x/sys/unix"
)

// TestRecordTypeValues pins the RecordType enum values (R01). The
// numeric values are part of the on-disk format — changing them is a
// breaking change.
func TestRecordTypeValues(t *testing.T) {
	cases := []struct {
		got, want RecordType
	}{
		{RTData, 0},
		{RTCommit, 1},
		{RTRollback, 2},
		{RTCheckpoint, 3},
		{RTMerge, 4},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("RecordType value mismatch: got %d, want %d", c.got, c.want)
		}
	}
}

// TestSegSize is the documented segment size (R01, R06): 64 MB.
func TestSegSize(t *testing.T) {
	const want = int64(64 * 1024 * 1024)
	if SegSize != want {
		t.Errorf("SegSize: got %d, want %d", SegSize, want)
	}
}

// testDeps wires the dependencies for Foundation tests.
type testDeps struct {
	sm  *lf.SegmentManager
	sp  sp.SyncPool
	log lg.Logger
}

func newTestDeps(t *testing.T) *testDeps {
	t.Helper()
	dir := t.TempDir()
	sm, err := lf.New(filepath.Join(dir, "wal"))
	if err != nil {
		t.Fatalf("lf.New: %v", err)
	}
	t.Cleanup(func() { sm.Close() })
	return &testDeps{
		sm:  sm,
		sp:  sp.New(),
		log: lg.New(lg.Options{Output: &nullWriter{}}),
	}
}

// newTestWriter creates a writer and returns it as the concrete
// *writer (not the Writer interface) so tests can call package-
// private helpers like flushForTest. Foundation tests that only need
// the public API can keep using New() directly.
func newTestWriter(t *testing.T) (*writer, *testDeps) {
	t.Helper()
	d := newTestDeps(t)
	w, err := New(t.TempDir(), d.sm, d.sp, d.log)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return w.(*writer), d
}

// TestNewRejectsEmptyArgs verifies the constructor enforces required
// arguments (R35). Foundation contracts.
func TestNewRejectsEmptyArgs(t *testing.T) {
	d := newTestDeps(t)

	if _, err := New("", d.sm, d.sp, d.log); err == nil {
		t.Error("expected error for empty dir")
	}
	if _, err := New("/tmp", nil, d.sp, d.log); err == nil {
		t.Error("expected error for nil SegmentManager")
	}
	if _, err := New("/tmp", d.sm, nil, d.log); err == nil {
		t.Error("expected error for nil SyncPool")
	}
}

// TestNewAcceptsValidArgs verifies a fully-wired New() succeeds (R35).
// The Foundation stub returns a *writer that has stub Append/Sync/Close.
func TestNewAcceptsValidArgs(t *testing.T) {
	d := newTestDeps(t)

	w, err := New(t.TempDir(), d.sm, d.sp, d.log)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if w == nil {
		t.Fatal("New returned nil writer")
	}
}

// TestWriterImplementsInterface is a compile-time check that *writer
// satisfies the Writer interface (R03). The `var _ Writer = ...` line
// in wr.go catches regressions; this test documents the intent.
func TestWriterImplementsInterface(t *testing.T) {
	var _ Writer = (*writer)(nil)
}

// TestWriterCloseIdempotent verifies Close can be called multiple
// times safely (R22).
func TestWriterCloseIdempotent(t *testing.T) {
	d := newTestDeps(t)
	w, err := New(t.TempDir(), d.sm, d.sp, d.log)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Errorf("second Close should be no-op, got %v", err)
	}
}

// TestWriterAppendAfterClose verifies post-close Append returns an
// error rather than panicking (R22).
func TestWriterAppendAfterClose(t *testing.T) {
	d := newTestDeps(t)
	w, _ := New(t.TempDir(), d.sm, d.sp, d.log)
	_ = w.Close()
	if _, err := w.Append(&WriteBatch{Recs: []LogRecord{{Type: RTData}}}); err == nil {
		t.Error("expected error from Append after Close")
	}
}

// TestWriterSyncAfterClose verifies post-close Sync is a no-op (R22).
func TestWriterSyncAfterClose(t *testing.T) {
	d := newTestDeps(t)
	w, _ := New(t.TempDir(), d.sm, d.sp, d.log)
	_ = w.Close()
	if err := w.Sync(); err != nil {
		t.Errorf("expected nil from Sync after Close, got %v", err)
	}
}

// TestCheckpointStructFields verifies the documented Checkpoint
// fields are exported and settable (R14).
func TestCheckpointStructFields(t *testing.T) {
	cp := Checkpoint{
		LSN:              42,
		CatalogRootPtr:   100,
		ManifestChecksum: 0xDEADBEEF,
		ActiveTXNs:       []uint64{1, 2, 3},
	}
	if cp.LSN != 42 {
		t.Errorf("LSN: got %d, want 42", cp.LSN)
	}
	if cp.CatalogRootPtr != 100 {
		t.Errorf("CatalogRootPtr: got %d, want 100", cp.CatalogRootPtr)
	}
	if cp.ManifestChecksum != 0xDEADBEEF {
		t.Errorf("ManifestChecksum: got %x, want DEADBEEF", cp.ManifestChecksum)
	}
	if len(cp.ActiveTXNs) != 3 {
		t.Errorf("ActiveTXNs len: got %d, want 3", len(cp.ActiveTXNs))
	}
}

// TestLogRecordFields verifies LogRecord has the expected exported
// fields.
func TestLogRecordFields(t *testing.T) {
	rec := LogRecord{
		Type:    RTData,
		TxnID:   7,
		Key:     []byte("k"),
		Value:   []byte("v"),
		BlockID: 99,
	}
	if rec.Type != RTData {
		t.Errorf("Type: got %d, want %d", rec.Type, RTData)
	}
	if rec.TxnID != 7 {
		t.Errorf("TxnID: got %d, want 7", rec.TxnID)
	}
	if rec.BlockID != 99 {
		t.Errorf("BlockID: got %d, want 99", rec.BlockID)
	}
	if string(rec.Key) != "k" {
		t.Errorf("Key: got %q, want \"k\"", rec.Key)
	}
	if string(rec.Value) != "v" {
		t.Errorf("Value: got %q, want \"v\"", rec.Value)
	}
}

// TestWriteBatchFields verifies WriteBatch has the expected fields.
func TestWriteBatchFields(t *testing.T) {
	batch := WriteBatch{
		TxnID: 5,
		Recs:  []LogRecord{{Type: RTData}, {Type: RTCommit}},
	}
	if batch.TxnID != 5 {
		t.Errorf("TxnID: got %d, want 5", batch.TxnID)
	}
	if len(batch.Recs) != 2 {
		t.Errorf("Recs len: got %d, want 2", len(batch.Recs))
	}
}

// TestAppendSingleRecord verifies a single record is encoded and
// persisted to the active segment (R07: sequential append, no reads
// in the hot path; R04: LSN = segmentNumber * SegSize + offset).
func TestAppendSingleRecord(t *testing.T) {
	w, d := newTestWriter(t)
	t.Cleanup(func() { _ = w.Close() })

	rec := LogRecord{
		Type:    RTData,
		BlockID: 0xABCDEF,
		Value:   []byte("hello world"),
	}
	lsn, err := w.Append(&WriteBatch{TxnID: 1, Recs: []LogRecord{rec}})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if lsn != 0 {
		// First record in segment 0 starts at LSN 0.
		t.Errorf("LSN: got %d, want 0", lsn)
	}
	// Flush the in-memory buffer (R37: Append doesn't auto-flush;
	// the test forces a flush so we can verify on-disk bytes).
	if err := w.flushForTest(); err != nil {
		t.Fatalf("flushForTest: %v", err)
	}
	got := mustReadSegment(t, d.sm, 0)
	want := encodeRecord(&LogRecord{
		Type:    RTData,
		TxnID:   1,
		BlockID: 0xABCDEF,
		Value:   []byte("hello world"),
	})
	if !bytes.Equal(got, want) {
		t.Errorf("on-disk bytes mismatch: got %x, want %x", got, want)
	}
}

// TestAppendBatchOfRecords verifies multiple records in one batch
// are appended in order and yield monotonic LSNs (R04).
func TestAppendBatchOfRecords(t *testing.T) {
	w, d := newTestWriter(t)
	t.Cleanup(func() { _ = w.Close() })

	const n = 10
	recs := make([]LogRecord, n)
	for i := 0; i < n; i++ {
		recs[i] = LogRecord{
			Type:    RTData,
			BlockID: uint64(i + 1),
			Value:   []byte{byte(i)},
		}
	}
	lsn, err := w.Append(&WriteBatch{TxnID: 42, Recs: recs})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := w.flushForTest(); err != nil {
		t.Fatalf("flushForTest: %v", err)
	}

	// Re-read the segment and decode all records.
	raw := mustReadSegment(t, d.sm, 0)
	off := 0
	for i := 0; i < n; i++ {
		rec, consumed, derr := decodeRecord(raw, off)
		if derr != nil {
			t.Fatalf("decodeRecord[%d]: %v", i, derr)
		}
		if rec.TxnID != 42 {
			t.Errorf("rec[%d].TxnID: got %d, want 42", i, rec.TxnID)
		}
		if rec.BlockID != uint64(i+1) {
			t.Errorf("rec[%d].BlockID: got %d, want %d", i, rec.BlockID, i+1)
		}
		off += consumed
	}
	if off != len(raw) {
		t.Errorf("leftover bytes after decoding: %d", len(raw)-off)
	}

	// LSN of the last record should be at offset off in segment 0.
	wantLastLSN := LSNFor(0, uint64(off-len(encodeRecord(&recs[n-1]))))
	if lsn != wantLastLSN {
		t.Errorf("last LSN: got %d, want %d", lsn, wantLastLSN)
	}
}

// TestAppendAssignsTxnIDFromBatch verifies the writer sets each
// record's TxnID from the batch's TxnID (defensive: caller may leave
// individual record TxnIDs zero).
func TestAppendAssignsTxnIDFromBatch(t *testing.T) {
	w, d := newTestWriter(t)
	t.Cleanup(func() { _ = w.Close() })

	recs := []LogRecord{
		{Type: RTData, BlockID: 1, Value: []byte("a")},
		{Type: RTData, BlockID: 2, Value: []byte("bb")},
	}
	if _, err := w.Append(&WriteBatch{TxnID: 999, Recs: recs}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := w.flushForTest(); err != nil {
		t.Fatalf("flushForTest: %v", err)
	}
	raw := mustReadSegment(t, d.sm, 0)
	off := 0
	for i := 0; i < len(recs); i++ {
		rec, consumed, _ := decodeRecord(raw, off)
		if rec.TxnID != 999 {
			t.Errorf("rec[%d].TxnID: got %d, want 999", i, rec.TxnID)
		}
		off += consumed
	}
}

// TestAppendMonotonicLSNsAcrossBatches verifies LSNs are monotonic
// across multiple Append calls (R04: segmentNumber * SegSize + offset
// is strictly increasing within a segment).
func TestAppendMonotonicLSNsAcrossBatches(t *testing.T) {
	d := newTestDeps(t)
	w, _ := New(t.TempDir(), d.sm, d.sp, d.log)
	t.Cleanup(func() { _ = w.Close() })

	var prevLSN uint64
	for i := 0; i < 5; i++ {
		rec := LogRecord{Type: RTData, BlockID: uint64(i), Value: []byte("x")}
		lsn, err := w.Append(&WriteBatch{TxnID: 1, Recs: []LogRecord{rec}})
		if err != nil {
			t.Fatalf("Append[%d]: %v", i, err)
		}
		if i > 0 && lsn <= prevLSN {
			t.Errorf("LSN[%d]=%d not > LSN[%d]=%d", i, lsn, i-1, prevLSN)
		}
		prevLSN = lsn
	}
}

// TestAppendEmptyBatch verifies that an empty or nil batch is a no-op
// (returns 0, nil, no I/O).
func TestAppendEmptyBatch(t *testing.T) {
	d := newTestDeps(t)
	w, _ := New(t.TempDir(), d.sm, d.sp, d.log)
	t.Cleanup(func() { _ = w.Close() })

	if lsn, err := w.Append(nil); err != nil || lsn != 0 {
		t.Errorf("Append(nil): got (%d, %v), want (0, nil)", lsn, err)
	}
	if lsn, err := w.Append(&WriteBatch{}); err != nil || lsn != 0 {
		t.Errorf("Append(empty): got (%d, %v), want (0, nil)", lsn, err)
	}
	// No segment should have been created.
	segs, err := d.sm.ListSegments()
	if err != nil {
		t.Fatalf("ListSegments: %v", err)
	}
	if len(segs) != 0 {
		t.Errorf("no segments should exist, got %v", segs)
	}
}

// TestAppendRejectsRecordLargerThanSegment verifies that a single
// record exceeding SegSize fails loudly rather than silently looping.
func TestAppendRejectsRecordLargerThanSegment(t *testing.T) {
	d := newTestDeps(t)
	w, _ := New(t.TempDir(), d.sm, d.sp, d.log)
	t.Cleanup(func() { _ = w.Close() })

	huge := make([]byte, SegSize+1)
	rec := LogRecord{Type: RTData, BlockID: 1, Value: huge}
	_, err := w.Append(&WriteBatch{TxnID: 1, Recs: []LogRecord{rec}})
	if err == nil {
		t.Error("expected error for record larger than SegSize")
	}
}

// TestLSNForEncoding pins the LSN encoding (R04): the LSN of byte
// position `offset` in segment `seg` is seg*SegSize + offset. This
// is the contract the replayer relies on for in-LSN-order traversal.
func TestLSNForEncoding(t *testing.T) {
	cases := []struct {
		seg, off uint64
		want     LSN
	}{
		{0, 0, 0},
		{0, 100, 100},
		{1, 0, uint64(SegSize)},
		{1, 50, uint64(SegSize) + 50},
		{2, 1024, 2*uint64(SegSize) + 1024},
		{42, 7, 42*uint64(SegSize) + 7},
	}
	for _, c := range cases {
		if got := LSNFor(c.seg, c.off); got != c.want {
			t.Errorf("LSNFor(%d, %d): got %d, want %d", c.seg, c.off, got, c.want)
		}
	}
}

// mustReadSegment reads a segment file from disk and returns its
// contents. Used by tests to verify Append actually wrote the bytes.
func mustReadSegment(t *testing.T, sm *lf.SegmentManager, n uint64) []byte {
	t.Helper()
	h, err := sm.GetSegment(n)
	if err != nil {
		t.Fatalf("GetSegment(%d): %v", n, err)
	}
	t.Cleanup(func() { _ = h.Close() })
	var stat unix.Stat_t
	if err := unix.Fstat(h.FD, &stat); err != nil {
		t.Fatalf("fstat: %v", err)
	}
	buf := make([]byte, stat.Size)
	if _, err := unix.Pread(h.FD, buf, 0); err != nil {
		t.Fatalf("pread: %v", err)
	}
	return buf
}
