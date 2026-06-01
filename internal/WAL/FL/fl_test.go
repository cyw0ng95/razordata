package fl

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/cyw0ng95/razordata/internal/FIL/FS"
	"github.com/cyw0ng95/razordata/internal/FIL/LF"
	"github.com/cyw0ng95/razordata/internal/LOG/LG"
)

// nullWriter is a no-op io.Writer used to silence the logger in
// tests. (Mirrors the WR stub — duplicated rather than shared to
// keep each cluster self-contained.)
type nullWriter struct{}

func (n *nullWriter) Write(p []byte) (int, error) { return len(p), nil }

// testDeps wires the FS and LF dependencies for Flusher tests.
// The same root is shared so root/wal is created by LF and
// fm.SyncDir("wal") resolves to the same path.
type testDeps struct {
	root string
	sm   *lf.SegmentManager
	fm   *fs.FileManager
	log  lg.Logger
}

func newTestDeps(t *testing.T) *testDeps {
	t.Helper()
	dir := t.TempDir()
	root := filepath.Join(dir, "db")
	sm, err := lf.New(root)
	if err != nil {
		t.Fatalf("lf.New: %v", err)
	}
	fm, err := fs.NewOrCreate(root)
	if err != nil {
		_ = sm.Close()
		t.Fatalf("fs.NewOrCreate: %v", err)
	}
	t.Cleanup(func() {
		_ = sm.Close()
		_ = fm.Close()
	})
	return &testDeps{
		root: root,
		sm:   sm,
		fm:   fm,
		log:  lg.New(lg.Options{Output: &nullWriter{}}),
	}
}

func newTestFlusher(t *testing.T) (Flusher, *testDeps) {
	t.Helper()
	d := newTestDeps(t)
	f, err := New(d.root, d.sm, d.fm, d.log)
	if err != nil {
		t.Fatalf("fl.New: %v", err)
	}
	return f, d
}

// ---- Constructor (R35) -------------------------------------------------

// TestNewRejectsEmptyArgs verifies the constructor enforces required
// arguments.
func TestNewRejectsEmptyArgs(t *testing.T) {
	d := newTestDeps(t)
	if _, err := New("", d.sm, d.fm, d.log); err == nil {
		t.Error("expected error for empty dir")
	}
	if _, err := New(d.root, nil, d.fm, d.log); err == nil {
		t.Error("expected error for nil SegmentManager")
	}
	if _, err := New(d.root, d.sm, nil, d.log); err == nil {
		t.Error("expected error for nil FileManager")
	}
}

// TestNewAcceptsValidArgs verifies a fully-wired New() succeeds.
func TestNewAcceptsValidArgs(t *testing.T) {
	d := newTestDeps(t)
	f, err := New(d.root, d.sm, d.fm, d.log)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if f == nil {
		t.Fatal("New returned nil flusher")
	}
}

// TestFlusherImplementsInterface is a compile-time check that
// *flusher satisfies the Flusher interface (R08). The
// `var _ Flusher = ...` line in fl.go catches regressions; this
// test documents the intent.
func TestFlusherImplementsInterface(t *testing.T) {
	var _ Flusher = (*flusher)(nil)
}

// ---- Close idempotency (R22) ------------------------------------------

// TestCloseIdempotent verifies Close can be called multiple times
// safely.
func TestCloseIdempotent(t *testing.T) {
	f, _ := newTestFlusher(t)
	if err := f.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Errorf("second Close should be no-op, got %v", err)
	}
	for i := 0; i < 5; i++ {
		if err := f.Close(); err != nil {
			t.Errorf("Close[%d]: %v", i, err)
		}
	}
}

// TestSyncAfterCloseReturnsNil verifies the v1 contract that
// Sync returns nil after Close (R22: post-Close methods are
// no-ops).
func TestSyncAfterCloseReturnsNil(t *testing.T) {
	f, _ := newTestFlusher(t)
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := f.Sync(); err != nil {
		t.Errorf("Sync after Close: %v", err)
	}
}

// TestBatchSyncAfterCloseReturnsNil mirrors the Sync test for the
// BatchSync method.
func TestBatchSyncAfterCloseReturnsNil(t *testing.T) {
	f, _ := newTestFlusher(t)
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := f.BatchSync(); err != nil {
		t.Errorf("BatchSync after Close: %v", err)
	}
}

// TestSyncDirAfterCloseReturnsNil verifies SyncDir is also a no-op
// after Close (R22). This matches the contract that post-Close
// public API methods cannot error.
func TestSyncDirAfterCloseReturnsNil(t *testing.T) {
	f, _ := newTestFlusher(t)
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := f.SyncDir(); err != nil {
		t.Errorf("SyncDir after Close: %v", err)
	}
}

// ---- SyncDir (R10) -----------------------------------------------------

// TestSyncDirSucceeds verifies SyncDir fsyncs the WAL directory
// without error. The directory is created by lf.New; we then
// invoke SyncDir to make sure the path resolution works.
func TestSyncDirSucceeds(t *testing.T) {
	f, _ := newTestFlusher(t)
	t.Cleanup(func() { _ = f.Close() })
	if err := f.SyncDir(); err != nil {
		t.Errorf("SyncDir: %v", err)
	}
}

// TestSyncDirIdempotent verifies SyncDir can be called multiple
// times. The first call opens the directory FD and caches it in
// the FileManager; subsequent calls take the cached-FD fast path.
func TestSyncDirIdempotent(t *testing.T) {
	f, _ := newTestFlusher(t)
	t.Cleanup(func() { _ = f.Close() })
	for i := 0; i < 5; i++ {
		if err := f.SyncDir(); err != nil {
			t.Errorf("SyncDir[%d]: %v", i, err)
		}
	}
}

// TestSyncDirAfterSegmentLifecycle exercises the realistic pattern
// the design calls out: create a segment (LF creates the wal dir
// and wal.000), SyncDir to durably persist the directory entry,
// close the segment, SyncDir again to persist the close. The
// test passes as long as every SyncDir call returns nil — the
// durability is verified by the FS-level test that the FD was
// successfully fsynced.
func TestSyncDirAfterSegmentLifecycle(t *testing.T) {
	f, d := newTestFlusher(t)
	t.Cleanup(func() { _ = f.Close() })

	// Create a segment — this creates root/wal/wal.000.
	if _, err := d.sm.CreateSegment(0); err != nil {
		t.Fatalf("CreateSegment: %v", err)
	}
	if err := f.SyncDir(); err != nil {
		t.Errorf("SyncDir after create: %v", err)
	}
	// List segments to confirm visibility, then SyncDir again.
	if segs, err := d.sm.ListSegments(); err != nil || len(segs) != 1 {
		t.Fatalf("ListSegments: err=%v, segs=%v", err, segs)
	}
	if err := f.SyncDir(); err != nil {
		t.Errorf("SyncDir after list: %v", err)
	}
}

// TestSyncDirResolvesRelativeWalPath verifies SyncDir uses the
// "wal" relative path (matching LF's segment directory layout),
// NOT an absolute path. If this regresses, SyncDir would either
// fail validation (path traversal) or fsync the wrong directory.
func TestSyncDirResolvesRelativeWalPath(t *testing.T) {
	f, d := newTestFlusher(t)
	t.Cleanup(func() { _ = f.Close() })
	// lf.New already created root/wal. If SyncDir resolved
	// incorrectly, the FileManager's path validator would
	// reject the call.
	if err := f.SyncDir(); err != nil {
		t.Errorf("SyncDir: %v", err)
	}
	// Sanity: confirm the segment directory exists at the
	// expected location.
	if segs, err := d.sm.ListSegments(); err != nil {
		t.Errorf("ListSegments: %v", err)
	} else if len(segs) != 0 {
		// No segments created yet, but the directory should
		// exist and be listable.
		t.Errorf("unexpected segments: %v", segs)
	}
}

// ---- Concurrency (R21) -------------------------------------------------

// TestSyncDirConcurrentSafe verifies SyncDir can be called from
// multiple goroutines under the race detector.
func TestSyncDirConcurrentSafe(t *testing.T) {
	f, _ := newTestFlusher(t)
	t.Cleanup(func() { _ = f.Close() })
	const goroutines = 8
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if err := f.SyncDir(); err != nil {
					t.Errorf("SyncDir: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

// TestCloseConcurrentWithClose exercises Close-vs-Close race
// under the race detector. All callers must return the same
// first-call error (R22).
func TestCloseConcurrentWithClose(t *testing.T) {
	f, _ := newTestFlusher(t)
	const goroutines = 16
	results := make([]error, goroutines)
	start := make(chan struct{})
	done := make(chan struct{}, goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer func() { done <- struct{}{} }()
			<-start
			results[idx] = f.Close()
		}(i)
	}
	close(start)
	for i := 0; i < goroutines; i++ {
		<-done
	}
	for i := 1; i < goroutines; i++ {
		if results[i] != results[0] {
			t.Errorf("Close[%d]: got %v, want %v (cached first-call error)",
				i, results[i], results[0])
		}
	}
}

// ---- LSN counter -------------------------------------------------------

// TestLSNCounterCurrentStartsAtZero verifies the LSN counter
// starts at zero on construction.
func TestLSNCounterCurrentStartsAtZero(t *testing.T) {
	c := newLSNCounter()
	if got := c.Current(); got != 0 {
		t.Errorf("current: got %d, want 0", got)
	}
}

// TestLSNCounterNextAdvancesMonotonically verifies Next()
// returns strictly increasing LSNs.
func TestLSNCounterNextAdvancesMonotonically(t *testing.T) {
	c := newLSNCounter()
	var prev uint64
	for i := 0; i < 100; i++ {
		got := c.Next()
		if i > 0 && got <= prev {
			t.Errorf("Next[%d]=%d not > prev=%d", i, got, prev)
		}
		prev = got
	}
}

// TestLSNCounterSetSegment verifies SetSegment stores the segment
// number (the v1 Foundation contract; segment-aware LSN encoding
// lands in Core FL).
func TestLSNCounterSetSegment(t *testing.T) {
	c := newLSNCounter()
	c.SetSegment(7)
	// No public getter for segNo in v1 Foundation — we verify
	// indirectly: a subsequent SetSegment does not panic and the
	// counter's Current is unaffected (Next() drives value).
	c.SetSegment(8)
	if got := c.Current(); got != 0 {
		t.Errorf("SetSegment must not change Current; got %d", got)
	}
}

// TestFlusherLSNReturnsZeroOnNew verifies the Flusher's LSN()
// method returns zero on a fresh flusher (R14 stale-read baseline).
func TestFlusherLSNReturnsZeroOnNew(t *testing.T) {
	f, _ := newTestFlusher(t)
	t.Cleanup(func() { _ = f.Close() })
	fl := f.(*flusher)
	if got := fl.LSN(); got != 0 {
		t.Errorf("LSN on new flusher: got %d, want 0", got)
	}
}
