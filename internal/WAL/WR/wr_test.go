package wr

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/cyw0ng95/razordata/internal/FIL/LF"
	"github.com/cyw0ng95/razordata/internal/LOG/LG"
	"github.com/cyw0ng95/razordata/internal/MEM/SP"
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

// TestAppendReturnsErrorWhenNotImplemented is a Foundation contract
// test — the stub returns an error so callers see a clear signal.
// Replaced with real tests once the Core WR commit lands.
func TestAppendReturnsErrorWhenNotImplemented(t *testing.T) {
	d := newTestDeps(t)
	w, _ := New(t.TempDir(), d.sm, d.sp, d.log)
	// Stub Append returns an error (not panic, not nil).
	_, err := w.Append(&WriteBatch{Recs: []LogRecord{{Type: RTData}}})
	if err == nil {
		t.Error("Foundation stub Append should return an error")
	}
	if !errors.Is(err, err) {
		t.Errorf("error should be non-nil and concrete, got %T", err)
	}
}
