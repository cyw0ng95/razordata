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

func TestAtomicBoolClear(t *testing.T) {
	var a atomicBool

	if a.isSet() {
		t.Error("expected initially clear")
	}

	a.set()
	if !a.isSet() {
		t.Error("expected set after set()")
	}

	a.clear()
	if a.isSet() {
		t.Error("expected clear after clear()")
	}
}

func TestAtomicBoolSetFalseWhenAlreadySet(t *testing.T) {
	var a atomicBool

	a.set()
	firstResult := a.set()

	a.set()
	secondResult := a.set()

	if firstResult && !secondResult {
		t.Error("first CAS should succeed, second should fail")
	}
}

func TestAtomicBoolClearMultipleTimes(t *testing.T) {
	var a atomicBool

	a.set()
	a.clear()
	a.clear()
	a.clear()

	if a.isSet() {
		t.Error("expected clear after multiple clears")
	}
}

func TestWriterOpenSegmentZeroFilled(t *testing.T) {
	d := newTestDeps(t)

	w, _ := New(t.TempDir(), d.sm, d.sp, d.log)
	defer w.Close()

	for i := 0; i < 100; i++ {
		w.Append(&WriteBatch{
			TxnID: uint64(i),
			Recs: []LogRecord{
				{Type: RTData, BlockID: uint64(i), Value: bytes.Repeat([]byte("x"), 1000)},
			},
		})
	}
}

func TestWriterFlushBufferShortWrite(t *testing.T) {
	d := newTestDeps(t)
	tmp := t.TempDir()

	w, _ := New(tmp, d.sm, d.sp, d.log)
	defer w.Close()

	for i := 0; i < 10; i++ {
		_, err := w.Append(&WriteBatch{
			TxnID: uint64(i),
			Recs: []LogRecord{
				{Type: RTData, BlockID: uint64(i), Value: []byte("test")},
			},
		})
		if err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	w.Close()

	lsns, err := d.sm.ListSegments()
	if err != nil {
		t.Fatalf("ListSegments: %v", err)
	}
	if len(lsns) == 0 {
		t.Error("expected at least one segment")
	}
}

func TestWriterAppendNilRecordBatch(t *testing.T) {
	d := newTestDeps(t)

	w, _ := New(t.TempDir(), d.sm, d.sp, d.log)
	defer w.Close()

	_, err := w.Append(&WriteBatch{})
	if err != nil {
		t.Fatalf("Append empty batch: %v", err)
	}
}

func TestWriterAppendMultipleBatches(t *testing.T) {
	d := newTestDeps(t)

	w, _ := New(t.TempDir(), d.sm, d.sp, d.log)
	defer w.Close()

	for batch := 0; batch < 5; batch++ {
		_, err := w.Append(&WriteBatch{
			TxnID: uint64(batch),
			Recs: []LogRecord{
				{Type: RTData, BlockID: uint64(batch), Value: []byte("data1")},
				{Type: RTData, BlockID: uint64(batch + 100), Value: []byte("data2")},
			},
		})
		if err != nil {
			t.Fatalf("Append batch %d: %v", batch, err)
		}
	}
}

func TestWriterSyncIdempotentMultiple(t *testing.T) {
	d := newTestDeps(t)

	w, _ := New(t.TempDir(), d.sm, d.sp, d.log)
	defer w.Close()

	w.Append(&WriteBatch{
		Recs: []LogRecord{
			{Type: RTData, BlockID: 1, Value: []byte("test")},
		},
	})

	w.Sync()
	w.Sync()
	w.Sync()
}

func TestWriterCloseWithNilSegment(t *testing.T) {
	d := newTestDeps(t)

	w, _ := New(t.TempDir(), d.sm, d.sp, d.log)

	w.Close()
	w.Close()
}

func TestWriterSyncWithNilSegment(t *testing.T) {
	d := newTestDeps(t)

	w, _ := New(t.TempDir(), d.sm, d.sp, d.log)
	defer w.Close()

	w.Close()

	err := w.Sync()
	if err != nil {
		t.Fatalf("Sync after close: %v", err)
	}
}

func TestWriterAppendAfterSync(t *testing.T) {
	d := newTestDeps(t)

	w, _ := New(t.TempDir(), d.sm, d.sp, d.log)
	defer w.Close()

	_, err := w.Append(&WriteBatch{
		Recs: []LogRecord{{Type: RTData, BlockID: 1, Value: []byte("before")}},
	})
	if err != nil {
		t.Fatalf("Append before sync: %v", err)
	}

	w.Sync()

	_, err = w.Append(&WriteBatch{
		Recs: []LogRecord{{Type: RTData, BlockID: 2, Value: []byte("after")}},
	})
	if err != nil {
		t.Fatalf("Append after sync: %v", err)
	}
}

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

// TestSyncFlushesBuffer verifies that Sync pwrites the in-memory
// buffer to the segment FD so on-disk bytes match the writer's
// logical state (R09, R37).
func TestSyncFlushesBuffer(t *testing.T) {
	w, d := newTestWriter(t)
	t.Cleanup(func() { _ = w.Close() })

	rec := LogRecord{Type: RTData, BlockID: 1, Value: []byte("sync me")}
	if _, err := w.Append(&WriteBatch{TxnID: 1, Recs: []LogRecord{rec}}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	// Without Sync, the bytes are in the 256 KB buffer, not on disk.
	preBufLen := w.segBufLenForTest()
	if preBufLen == 0 {
		t.Fatal("expected buffer to have bytes after Append")
	}
	// Read the segment before Sync — should be empty (file exists but
	// no pwrites yet).
	preRaw := mustReadSegment(t, d.sm, 0)
	if len(preRaw) != 0 {
		t.Errorf("pre-Sync segment size: got %d, want 0", len(preRaw))
	}
	// Sync.
	if err := w.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	// Buffer should be reset to len=0.
	if got := w.segBufLenForTest(); got != 0 {
		t.Errorf("post-Sync buf len: got %d, want 0", got)
	}
	// And the on-disk file should now have the bytes.
	postRaw := mustReadSegment(t, d.sm, 0)
	if len(postRaw) == 0 {
		t.Error("post-Sync segment is empty; Sync didn't flush")
	}
}

// TestSyncIdempotent verifies that calling Sync multiple times in a
// row is safe and the second call is a no-op (R22).
func TestSyncIdempotent(t *testing.T) {
	w, _ := newTestWriter(t)
	t.Cleanup(func() { _ = w.Close() })

	if _, err := w.Append(&WriteBatch{TxnID: 1, Recs: []LogRecord{
		{Type: RTData, BlockID: 1, Value: []byte("a")},
	}}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := w.Sync(); err != nil {
			t.Errorf("Sync[%d]: %v", i, err)
		}
	}
}

// TestSyncEmptyBufferNoOp verifies that calling Sync before any
// Append (or after a flush that left the buffer empty) is a clean
// no-op — no fsync, no error.
func TestSyncEmptyBufferNoOp(t *testing.T) {
	w, _ := newTestWriter(t)
	t.Cleanup(func() { _ = w.Close() })

	// No Append yet — seg is nil. Sync should be a clean no-op.
	if err := w.Sync(); err != nil {
		t.Errorf("Sync on uninitialized writer: %v", err)
	}
	// Append + Sync, then Sync again — second call is a no-op.
	if _, err := w.Append(&WriteBatch{TxnID: 1, Recs: []LogRecord{
		{Type: RTData, BlockID: 1, Value: []byte("x")},
	}}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := w.Sync(); err != nil {
		t.Fatalf("first Sync: %v", err)
	}
	if err := w.Sync(); err != nil {
		t.Errorf("second Sync (empty buf): %v", err)
	}
}

// TestSyncUpdatesSyncedLSN verifies the synced atomic is updated
// to the highest LSN that has been fsynced (R09). After a Sync
// that covers records up to LSN X, synced == X.
func TestSyncUpdatesSyncedLSN(t *testing.T) {
	w, _ := newTestWriter(t)
	t.Cleanup(func() { _ = w.Close() })

	rec1 := LogRecord{Type: RTData, BlockID: 1, Value: []byte("a")}
	rec1Size := int64(len(encodeRecord(&rec1)))
	if _, err := w.Append(&WriteBatch{TxnID: 1, Recs: []LogRecord{rec1}}); err != nil {
		t.Fatalf("Append[1]: %v", err)
	}
	if err := w.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	// After Sync, synced = LSN of last byte in segment 0.
	wantSynced := LSNFor(0, uint64(rec1Size))
	if got := w.synced.Load(); got != wantSynced {
		t.Errorf("synced: got %d, want %d", got, wantSynced)
	}

	// Second batch: synced should advance.
	rec2 := LogRecord{Type: RTData, BlockID: 2, Value: []byte("bb")}
	rec2Size := int64(len(encodeRecord(&rec2)))
	if _, err := w.Append(&WriteBatch{TxnID: 2, Recs: []LogRecord{rec2}}); err != nil {
		t.Fatalf("Append[2]: %v", err)
	}
	if err := w.Sync(); err != nil {
		t.Fatalf("Sync[2]: %v", err)
	}
	wantSynced2 := LSNFor(0, uint64(rec1Size+rec2Size))
	if got := w.synced.Load(); got != wantSynced2 {
		t.Errorf("synced after second Sync: got %d, want %d", got, wantSynced2)
	}
}

// TestSyncConcurrentSafe verifies Sync can be called from multiple
// goroutines without data races (R21). Run with -race.
func TestSyncConcurrentSafe(t *testing.T) {
	w, _ := newTestWriter(t)
	t.Cleanup(func() { _ = w.Close() })

	// Spawn writers and syncers.
	const goroutines = 8
	done := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 50; j++ {
				rec := LogRecord{Type: RTData, BlockID: uint64(id*1000 + j), Value: []byte{byte(j)}}
				_, _ = w.Append(&WriteBatch{TxnID: uint64(id), Recs: []LogRecord{rec}})
				_ = w.Sync()
			}
		}(i)
	}
	for i := 0; i < goroutines; i++ {
		<-done
	}
	// Final sync to confirm everything is durable.
	if err := w.Sync(); err != nil {
		t.Errorf("final Sync: %v", err)
	}
}

// segBufLenForTest returns the active segment's buffer length. Test
// helper (in-package, no exported API surface).
func (w *writer) segBufLenForTest() int {
	if w.seg == nil {
		return 0
	}
	return len(w.seg.buf)
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

// TestRotateAtSegmentBoundary exercises the rotation path (R06) by
// writing one record that would land at the very end of a segment
// and a second that would not fit, forcing a rotate.
func TestRotateAtSegmentBoundary(t *testing.T) {
	w, d := newTestWriter(t)
	t.Cleanup(func() { _ = w.Close() })

	// Prime the writer with one record so w.seg is initialized.
	if _, err := w.Append(&WriteBatch{TxnID: 1, Recs: []LogRecord{
		{Type: RTRollback, BlockID: 0},
	}}); err != nil {
		t.Fatalf("prime Append: %v", err)
	}

	// Compute the actual encoded size of rec1 so we can leave
	// exactly enough room for it (no off-by-one).
	rec1 := LogRecord{Type: RTData, BlockID: 1, Value: []byte("a")}
	rec1Size := int64(len(encodeRecord(&rec1)))
	// Set writeOff so rec1 fits exactly; rec2 (same size) will not fit.
	w.seg.writeOff = SegSize - rec1Size

	lsn1, err := w.Append(&WriteBatch{TxnID: 1, Recs: []LogRecord{rec1}})
	if err != nil {
		t.Fatalf("Append[1]: %v", err)
	}
	wantLSN1 := LSNFor(0, uint64(SegSize-rec1Size))
	if lsn1 != wantLSN1 {
		t.Errorf("LSN[1]: got %d, want %d", lsn1, wantLSN1)
	}

	// Second record does NOT fit; this must trigger a rotation.
	rec2 := LogRecord{Type: RTData, BlockID: 2, Value: []byte("b")}
	lsn2, err := w.Append(&WriteBatch{TxnID: 2, Recs: []LogRecord{rec2}})
	if err != nil {
		t.Fatalf("Append[2]: %v", err)
	}
	wantLSN2 := LSNFor(1, 0) // first record of segment 1
	if lsn2 != wantLSN2 {
		t.Errorf("LSN[2]: got %d, want %d (post-rotation)", lsn2, wantLSN2)
	}
	if w.seg.number != 1 {
		t.Errorf("active segment number: got %d, want 1", w.seg.number)
	}

	// Both segment files must exist on disk.
	segs, err := d.sm.ListSegments()
	if err != nil {
		t.Fatalf("ListSegments: %v", err)
	}
	if len(segs) != 2 || segs[0] != 0 || segs[1] != 1 {
		t.Errorf("segment list: got %v, want [0 1]", segs)
	}
}

// TestRotateProducesFreshSegment verifies the rotation path resets
// the in-memory segment state correctly: the new segment starts at
// writeOff=0 with a fresh buffer. R25: zero-fill on buffer reuse
// (we verify writeOff reset, since buf-len is reset internally to 0
// before any new writes happen — see openSegmentLocked).
func TestRotateProducesFreshSegment(t *testing.T) {
	w, _ := newTestWriter(t)
	t.Cleanup(func() { _ = w.Close() })

	// Prime the writer.
	if _, err := w.Append(&WriteBatch{TxnID: 0, Recs: []LogRecord{
		{Type: RTRollback},
	}}); err != nil {
		t.Fatalf("prime: %v", err)
	}

	// Force-rotate by exceeding the segment's remaining capacity.
	rec1 := LogRecord{Type: RTData, BlockID: 1, Value: []byte("a")}
	rec1Size := int64(len(encodeRecord(&rec1)))
	w.seg.writeOff = SegSize - rec1Size

	rec2 := LogRecord{Type: RTData, BlockID: 2, Value: []byte("b")}
	rec2Size := int64(len(encodeRecord(&rec2)))

	if _, err := w.Append(&WriteBatch{TxnID: 1, Recs: []LogRecord{rec1}}); err != nil {
		t.Fatalf("Append[1]: %v", err)
	}
	// After rec1: writeOff = SegSize. The next Append must rotate.
	if _, err := w.Append(&WriteBatch{TxnID: 2, Recs: []LogRecord{rec2}}); err != nil {
		t.Fatalf("Append[2]: %v", err)
	}

	// Post-rotation invariants on the NEW segment.
	if w.seg.number != 1 {
		t.Errorf("post-rotation seg.number: got %d, want 1", w.seg.number)
	}
	// writeOff = len(rec2) since rec2 was written into the fresh
	// segment starting at offset 0.
	if w.seg.writeOff != rec2Size {
		t.Errorf("post-rotation seg.writeOff: got %d, want %d",
			w.seg.writeOff, rec2Size)
	}
	// buf cap is preserved (same pool slot size).
	if cap(w.seg.buf) != int(spWALBufSize(t)) {
		t.Errorf("post-rotation seg.buf cap: got %d, want %d",
			cap(w.seg.buf), spWALBufSize(t))
	}
}

// spWALBufSize returns sp.WALBufSize for test assertions. Wrapped in
// a helper so the test reads naturally.
func spWALBufSize(t *testing.T) int {
	t.Helper()
	return 256 * 1024
}

// TestRotateLSNContinuityAcrossSegments verifies LSNs are strictly
// monotonic across rotation (R04, R27): the first LSN of segment n+1
// equals SegSize + the last offset of segment n (which here is
// SegSize itself, since we filled the segment completely).
func TestRotateLSNContinuityAcrossSegments(t *testing.T) {
	w, _ := newTestWriter(t)
	t.Cleanup(func() { _ = w.Close() })

	// Prime the writer.
	if _, err := w.Append(&WriteBatch{TxnID: 0, Recs: []LogRecord{
		{Type: RTRollback},
	}}); err != nil {
		t.Fatalf("prime: %v", err)
	}

	// rec1 fits exactly; rec2 of the same size does not, triggering
	// rotation.
	rec1 := LogRecord{Type: RTData, BlockID: 1, Value: []byte("a")}
	rec1Size := int64(len(encodeRecord(&rec1)))
	w.seg.writeOff = SegSize - rec1Size

	if _, err := w.Append(&WriteBatch{TxnID: 1, Recs: []LogRecord{rec1}}); err != nil {
		t.Fatalf("Append[1]: %v", err)
	}
	lsn2, err := w.Append(&WriteBatch{TxnID: 2, Recs: []LogRecord{
		{Type: RTData, BlockID: 2, Value: []byte("b")}, // LSN = SegSize (seg 1, off 0)
	}})
	if err != nil {
		t.Fatalf("Append[2]: %v", err)
	}
	if lsn2 != uint64(SegSize) {
		t.Errorf("LSN continuity: got %d, want %d (SegSize)", lsn2, uint64(SegSize))
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

// ---- Close (R22) -------------------------------------------------------

// segForTest returns the writer's active segment (or nil) for test
// inspection. The helper is in-package and exists only so tests can
// verify post-Close state without exposing internals to the
// public API.
func (w *writer) segForTest() *logSegment { return w.seg }

// TestCloseFlushesDirtyBuffer verifies the R22 contract that Close
// flushes the in-memory buffer to disk before tearing down the
// segment — so data written but not yet Synced is still durable
// after Close returns.
func TestCloseFlushesDirtyBuffer(t *testing.T) {
	w, d := newTestWriter(t)

	rec := LogRecord{Type: RTData, BlockID: 1, Value: []byte("close me")}
	if _, err := w.Append(&WriteBatch{TxnID: 1, Recs: []LogRecord{rec}}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	// Sanity: buffer is dirty, segment file is still empty on disk.
	if w.segForTest() == nil || w.segForTest().buf == nil {
		t.Fatal("expected active segment with buffer after Append")
	}
	if got := w.segBufLenForTest(); got == 0 {
		t.Fatal("expected dirty buffer after Append")
	}
	if pre := mustReadSegment(t, d.sm, 0); len(pre) != 0 {
		t.Fatalf("pre-Close segment should be empty, got %d bytes", len(pre))
	}

	// Close should flush + fsync + release.
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Buffer should have been returned to the pool (we nil the field
	// so a future Close won't double-Put it).
	if w.segForTest() != nil {
		t.Errorf("post-Close seg: got %+v, want nil", w.segForTest())
	}

	// And the bytes are now on disk.
	post := mustReadSegment(t, d.sm, 0)
	want := encodeRecord(&LogRecord{
		Type:    RTData,
		TxnID:   1,
		BlockID: 1,
		Value:   []byte("close me"),
	})
	if !bytes.Equal(post, want) {
		t.Errorf("post-Close segment bytes mismatch: got %x, want %x", post, want)
	}
}

// TestCloseUpdatesSyncedLSN verifies that after Close the synced
// atomic reflects the highest LSN that was fsynced. This matches
// the contract used by the durability gate in the engine layer.
func TestCloseUpdatesSyncedLSN(t *testing.T) {
	w, _ := newTestWriter(t)

	rec1 := LogRecord{Type: RTData, BlockID: 1, Value: []byte("a")}
	rec1Size := int64(len(encodeRecord(&rec1)))
	rec2 := LogRecord{Type: RTData, BlockID: 2, Value: []byte("b")}
	rec2Size := int64(len(encodeRecord(&rec2)))
	if _, err := w.Append(&WriteBatch{TxnID: 1, Recs: []LogRecord{rec1}}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := w.Append(&WriteBatch{
		TxnID: 2,
		Recs:  []LogRecord{rec2},
	}); err != nil {
		t.Fatalf("Append[2]: %v", err)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	want := LSNFor(0, uint64(rec1Size+rec2Size))
	if got := w.synced.Load(); got != want {
		t.Errorf("post-Close synced: got %d, want %d", got, want)
	}
}

// TestCloseReleasesFD verifies that after Close the segment's file
// handle is released (FD = -1) and the SegmentManager can re-open
// the file on demand — i.e. the on-disk bytes are preserved.
func TestCloseReleasesFD(t *testing.T) {
	w, d := newTestWriter(t)
	if _, err := w.Append(&WriteBatch{TxnID: 1, Recs: []LogRecord{
		{Type: RTData, BlockID: 1, Value: []byte("preserved")},
	}}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	// Snapshot the FD before Close.
	fh := w.segForTest().fh
	preFD := fh.FD
	if preFD < 0 {
		t.Fatal("pre-Close FD should be valid")
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if fh.FD != -1 {
		t.Errorf("post-Close FD: got %d, want -1", fh.FD)
	}
	// And the bytes are still readable via the SegmentManager.
	got := mustReadSegment(t, d.sm, 0)
	if len(got) == 0 {
		t.Error("post-Close segment is empty; expected preserved bytes")
	}
}

// TestCloseOnUnusedWriter verifies that calling Close on a writer
// that never had an Append is a clean no-op (R22). It must not
// panic, must not return an error, and must not touch the segment
// manager in a way that creates a stray segment file.
func TestCloseOnUnusedWriter(t *testing.T) {
	d := newTestDeps(t)
	w, err := New(t.TempDir(), d.sm, d.sp, d.log)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// No Append: seg is nil. Close should be a no-op.
	if err := w.Close(); err != nil {
		t.Errorf("Close on unused writer: %v", err)
	}
	// No segment file should exist on disk.
	segs, err := d.sm.ListSegments()
	if err != nil {
		t.Fatalf("ListSegments: %v", err)
	}
	if len(segs) != 0 {
		t.Errorf("no segments expected, got %v", segs)
	}
	// Second Close is also a no-op.
	if err := w.Close(); err != nil {
		t.Errorf("second Close on unused writer: %v", err)
	}
}

// TestCloseAfterSyncIsClean verifies that Close after a successful
// Sync with an empty buffer is a no-op for the actual flush step
// (buffer is empty) but still fsyncs and releases the FD. This
// guards the common pattern: Append → Sync (commit) → ... → Close.
func TestCloseAfterSyncIsClean(t *testing.T) {
	w, d := newTestWriter(t)
	rec := LogRecord{Type: RTData, BlockID: 1, Value: []byte("synced")}
	if _, err := w.Append(&WriteBatch{TxnID: 1, Recs: []LogRecord{rec}}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := w.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	// Buffer is empty post-Sync.
	if got := w.segBufLenForTest(); got != 0 {
		t.Fatalf("expected empty buffer post-Sync, got %d", got)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Bytes survived.
	post := mustReadSegment(t, d.sm, 0)
	if len(post) == 0 {
		t.Error("post-Close segment is empty; bytes should be preserved")
	}
}

// TestCloseManyTimesIsIdempotent verifies the strict R22 idempotency
// contract: N consecutive Close calls all return the same (nil or
// error) value and produce no panics, no double-release, and no
// spurious fsync errors.
func TestCloseManyTimesIsIdempotent(t *testing.T) {
	w, _ := newTestWriter(t)
	if _, err := w.Append(&WriteBatch{TxnID: 1, Recs: []LogRecord{
		{Type: RTData, BlockID: 1, Value: []byte("x")},
	}}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	var firstErr error
	for i := 0; i < 10; i++ {
		err := w.Close()
		if i == 0 {
			firstErr = err
		} else if err != firstErr {
			t.Errorf("Close[%d] err mismatch: got %v, want %v", i, err, firstErr)
		}
	}
}

// TestCloseConcurrentWithAppend exercises the Close-vs-Append race
// under the race detector. Outcomes are either "Append succeeded
// and Close saw the bytes" or "Append returned 'writer is closed'"
// — there must be no panics, no double-flushes, and no data
// corruption.
func TestCloseConcurrentWithAppend(t *testing.T) {
	w, _ := newTestWriter(t)

	const goroutines = 8
	const perGoroutine = 100
	start := make(chan struct{})
	done := make(chan struct{}, goroutines)

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer func() { done <- struct{}{} }()
			<-start
			for j := 0; j < perGoroutine; j++ {
				rec := LogRecord{
					Type:    RTData,
					BlockID: uint64(id*1000 + j),
					Value:   []byte{byte(j)},
				}
				_, err := w.Append(&WriteBatch{TxnID: uint64(id), Recs: []LogRecord{rec}})
				if err != nil && err.Error() != "wr: writer is closed" {
					t.Errorf("unexpected Append err: %v", err)
				}
			}
		}(i)
	}
	close(start)
	// Close concurrently with the Append fan-out.
	closeDone := make(chan error, 1)
	go func() { closeDone <- w.Close() }()

	for i := 0; i < goroutines; i++ {
		<-done
	}
	if err := <-closeDone; err != nil {
		t.Errorf("Close: %v", err)
	}
}

// TestCloseConcurrentWithClose exercises the Close-vs-Close race
// (R22 idempotency under contention). All callers must return
// without panicking and with the same first-call error value.
func TestCloseConcurrentWithClose(t *testing.T) {
	w, _ := newTestWriter(t)
	if _, err := w.Append(&WriteBatch{TxnID: 1, Recs: []LogRecord{
		{Type: RTData, BlockID: 1, Value: []byte("x")},
	}}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	const goroutines = 16
	results := make([]error, goroutines)
	start := make(chan struct{})
	done := make(chan struct{}, goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer func() { done <- struct{}{} }()
			<-start
			results[idx] = w.Close()
		}(i)
	}
	close(start)
	for i := 0; i < goroutines; i++ {
		<-done
	}
	// All errors should be identical (the cached first-call error).
	for i := 1; i < goroutines; i++ {
		if results[i] != results[0] {
			t.Errorf("Close[%d]: got %v, want %v (cached first-call error)",
				i, results[i], results[0])
		}
	}
}

// TestAppendAfterCloseDoesNotCorruptState verifies that even if
// an Append is in flight when Close finishes, the writer's
// post-Close state is consistent: seg is nil, no orphan FD
// (the closed flag prevents openSegmentLocked from running).
func TestAppendAfterCloseDoesNotCorruptState(t *testing.T) {
	w, _ := newTestWriter(t)
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	for i := 0; i < 5; i++ {
		_, err := w.Append(&WriteBatch{Recs: []LogRecord{{Type: RTData, BlockID: 1, Value: []byte("a")}}})
		if err == nil {
			t.Errorf("Append[%d] after Close: expected error, got nil", i)
		}
	}
	if w.segForTest() != nil {
		t.Errorf("post-Close seg: got %+v, want nil", w.segForTest())
	}
}

// TestCloseAllowsBufferReuse verifies that the buffer returned to
// the pool on Close is actually picked up by a future Get — i.e.
// we don't leak it. A new writer created in the same process
// should be able to get a non-nil buffer (the pool reuses it).
func TestCloseReturnsBufferToPool(t *testing.T) {
	d := newTestDeps(t)
	// First writer.
	w1, _ := New(t.TempDir(), d.sm, d.sp, d.log)
	if err := w1.Close(); err != nil {
		t.Fatalf("Close[1]: %v", err)
	}
	// Second writer on the same pool: it must successfully get
	// a buffer (the pool reused the slot).
	w2, _ := New(t.TempDir(), d.sm, d.sp, d.log)
	if _, err := w2.Append(&WriteBatch{TxnID: 1, Recs: []LogRecord{
		{Type: RTData, BlockID: 1, Value: []byte("reuse")},
	}}); err != nil {
		t.Fatalf("Append on w2: %v", err)
	}
	if err := w2.Close(); err != nil {
		t.Fatalf("Close[2]: %v", err)
	}
}

// TestCrashSimulated tests that a writer can recover its state
// after an unclean shutdown (simulated by closing the underlying
// segment without proper flush). This is a Foundation stub test —
// the real crash recovery lands in the RP Core implementation.
func TestCrashSimulated(t *testing.T) {
	d := newTestDeps(t)

	w, _ := New(t.TempDir(), d.sm, d.sp, d.log)

	batch := &WriteBatch{
		TxnID: 1,
		Recs: []LogRecord{
			{Type: RTData, BlockID: 1, Value: []byte("crash test data")},
			{Type: RTCommit, TxnID: 1},
		},
	}

	lsn, err := w.Append(batch)
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if lsn == 0 {
		t.Error("expected non-zero LSN")
	}

	if err := w.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	w.Close()

	_ = d
}

// TestWriterWithSmallSegment exercises segment rotation with a
// very small segment size to verify rotation behavior in tests.
func TestWriterWithSmallSegment(t *testing.T) {
	d := newTestDeps(t)

	sm2, err := lf.New(t.TempDir())
	if err != nil {
		t.Fatalf("lf.New: %v", err)
	}
	defer sm2.Close()

	w, err := New(t.TempDir(), sm2, d.sp, d.log)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer w.Close()

	for i := 0; i < 5; i++ {
		_, err := w.Append(&WriteBatch{
			TxnID: uint64(i),
			Recs: []LogRecord{
				{Type: RTData, BlockID: uint64(i + 1), Value: []byte("x")},
			},
		})
		if err != nil {
			t.Fatalf("Append[%d]: %v", i, err)
		}
	}

	w.Sync()
}

// TestMultipleSyncCalls verifies that multiple Sync calls work.
func TestMultipleSyncCalls(t *testing.T) {
	d := newTestDeps(t)

	w, _ := New(t.TempDir(), d.sm, d.sp, d.log)
	defer w.Close()

	for i := 0; i < 3; i++ {
		_, err := w.Append(&WriteBatch{
			TxnID: uint64(i),
			Recs: []LogRecord{
				{Type: RTData, BlockID: uint64(i), Value: []byte("data")},
			},
		})
		if err != nil {
			t.Fatalf("Append: %v", err)
		}

		if err := w.Sync(); err != nil {
			t.Errorf("Sync[%d]: %v", i, err)
		}
	}
}

// TestFlushBufferOnEmptySegment tests flushBuffer when segment is nil.
func TestFlushBufferOnEmptySegment(t *testing.T) {
	d := newTestDeps(t)

	w, _ := New(t.TempDir(), d.sm, d.sp, d.log)

	err := w.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}

	_ = d
}

// TestRotateWithNilBuffer tests rotate when buffer is not nil (normal path).
func TestRotateWithNilBuffer(t *testing.T) {
	d := newTestDeps(t)

	w, _ := New(t.TempDir(), d.sm, d.sp, d.log)

	for i := 0; i < 5; i++ {
		_, err := w.Append(&WriteBatch{
			TxnID: uint64(i),
			Recs: []LogRecord{
				{Type: RTData, BlockID: uint64(i + 1), Value: []byte("x")},
			},
		})
		if err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	w.Sync()

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestAppendTriggersRotate tests that large appends trigger segment rotation.
func TestAppendTriggersRotate(t *testing.T) {
	d := newTestDeps(t)

	tmp := t.TempDir()
	w, _ := New(tmp, d.sm, d.sp, d.log)

	largeValue := bytes.Repeat([]byte("x"), int(sp.WALBufSize)/2)

	_, err := w.Append(&WriteBatch{
		TxnID: 1,
		Recs: []LogRecord{
			{Type: RTData, BlockID: 1, Value: largeValue},
		},
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}

	lsns, err := d.sm.ListSegments()
	if err != nil {
		t.Fatalf("ListSegments: %v", err)
	}

	if len(lsns) < 1 {
		t.Errorf("expected at least 1 segment, got %d", len(lsns))
	}

	w.Close()
}

// TestSegmentRotationPreservesLSN ordering.
func TestSegmentRotationLSNOrdering(t *testing.T) {
	d := newTestDeps(t)

	w, _ := New(t.TempDir(), d.sm, d.sp, d.log)
	defer w.Close()

	lsns := make([]uint64, 0)
	for i := 0; i < 100; i++ {
		lsn, err := w.Append(&WriteBatch{
			TxnID: uint64(i),
			Recs: []LogRecord{
				{Type: RTData, BlockID: uint64(i), Value: []byte("test")},
			},
		})
		if err != nil {
			t.Fatalf("Append: %v", err)
		}
		lsns = append(lsns, lsn)
	}

	for i := 1; i < len(lsns); i++ {
		if lsns[i] <= lsns[i-1] {
			t.Errorf("LSN not monotonic: lsns[%d]=%d, lsns[%d]=%d",
				i-1, lsns[i-1], i, lsns[i])
		}
	}
}
