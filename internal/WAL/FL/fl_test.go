package fl

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/cyw0ng95/razordata/internal/FIL/FS"
	"github.com/cyw0ng95/razordata/internal/FIL/LF"
	"github.com/cyw0ng95/razordata/internal/LOG/LG"
)

func TestAtomicBoolClear_FL(t *testing.T) {
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

func TestAtomicBoolClearMultiple_FL(t *testing.T) {
	var a atomicBool

	a.set()
	a.clear()
	a.clear()
	a.clear()

	if a.isSet() {
		t.Error("expected clear after multiple clears")
	}
}

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

// TestLSNCounterReserveSingleClaim verifies Reserve(1) returns
// monotonic LSNs equivalent to Next() and advances Current() by 1.
// REQ000541: the batched path is equivalent to the single-record
// path for n=1.
func TestLSNCounterReserveSingleClaim(t *testing.T) {
	c := newLSNCounter()
	if got := c.Reserve(1); got != 1 {
		t.Errorf("Reserve(1) first: got %d, want 1", got)
	}
	if got := c.Current(); got != 1 {
		t.Errorf("Current after Reserve(1): got %d, want 1", got)
	}
	if got := c.Reserve(1); got != 2 {
		t.Errorf("Reserve(1) second: got %d, want 2", got)
	}
	if got := c.Current(); got != 2 {
		t.Errorf("Current after second Reserve(1): got %d, want 2", got)
	}
}

// TestLSNCounterReserveRange verifies Reserve(n) claims the inclusive
// range [start, start+n-1] and a subsequent Reserve picks up at
// start+n. REQ000541.
func TestLSNCounterReserveRange(t *testing.T) {
	c := newLSNCounter()
	start := c.Reserve(10)
	if start != 1 {
		t.Errorf("first Reserve(10) start: got %d, want 1", start)
	}
	if got := c.Current(); got != 10 {
		t.Errorf("Current after Reserve(10): got %d, want 10", got)
	}
	next := c.Reserve(5)
	if next != 11 {
		t.Errorf("subsequent Reserve(5) start: got %d, want 11", next)
	}
	if got := c.Current(); got != 15 {
		t.Errorf("Current after Reserve(10)+Reserve(5): got %d, want 15", got)
	}
}

// TestLSNCounterReserveInterleavedWithNext verifies that Reserve and
// Next can be mixed freely — both consume from the same atomic
// counter and never overlap. REQ000541.
func TestLSNCounterReserveInterleavedWithNext(t *testing.T) {
	c := newLSNCounter()
	if got := c.Next(); got != 1 {
		t.Errorf("Next first: got %d, want 1", got)
	}
	if got := c.Reserve(3); got != 2 {
		t.Errorf("Reserve(3): got %d, want 2", got)
	}
	if got := c.Next(); got != 5 {
		t.Errorf("Next after Reserve(3): got %d, want 5", got)
	}
	if got := c.Reserve(1); got != 6 {
		t.Errorf("Reserve(1) last: got %d, want 6", got)
	}
	if got := c.Current(); got != 6 {
		t.Errorf("Current final: got %d, want 6", got)
	}
}

// TestLSNCounterReserveZeroAndNegativePanics verifies the
// contract that Reserve must be called with n > 0. Both 0 and
// negative values are programming errors and must panic rather
// than silently regress the counter. REQ000541.
func TestLSNCounterReserveZeroAndNegativePanics(t *testing.T) {
	c := newLSNCounter()
	c.Next() // advance to 1 so we can confirm the counter does not regress
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("Reserve(0) must panic")
		}
	}()
	c.Reserve(0)
}

// TestLSNCounterReserveNegativePanics is the negative-input branch
// of the contract check. REQ000541.
func TestLSNCounterReserveNegativePanics(t *testing.T) {
	c := newLSNCounter()
	c.Next()
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("Reserve(-1) must panic")
		}
	}()
	c.Reserve(-1)
}

// TestLSNCounterReserveConcurrentNoOverlap stress-tests the
// atomicity guarantee: 100 goroutines each call Reserve(10); the
// union of returned ranges must be a strict partition of [1, 1000]
// with no overlap and no gaps. REQ000541.
func TestLSNCounterReserveConcurrentNoOverlap(t *testing.T) {
	c := newLSNCounter()
	const goroutines = 100
	const perClaim = 10
	starts := make([]uint64, goroutines)
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			starts[idx] = c.Reserve(perClaim)
		}(i)
	}
	wg.Wait()
	if got := c.Current(); got != uint64(goroutines*perClaim) {
		t.Errorf("Current: got %d, want %d", got, goroutines*perClaim)
	}
	seen := make(map[uint64]struct{}, goroutines*perClaim)
	for i, s := range starts {
		for k := uint64(0); k < perClaim; k++ {
			lsn := s + k
			if lsn < 1 || lsn > uint64(goroutines*perClaim) {
				t.Errorf("goroutine %d: LSN %d out of expected range", i, lsn)
			}
			if _, dup := seen[lsn]; dup {
				t.Errorf("goroutine %d: LSN %d claimed twice", i, lsn)
			}
			seen[lsn] = struct{}{}
		}
	}
	if len(seen) != goroutines*perClaim {
		t.Errorf("unique LSNs: got %d, want %d", len(seen), goroutines*perClaim)
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

// BenchmarkBatchSyncGroupCommit measures group commit overhead
// with 10 transactions per batch (REQ000176).
func BenchmarkBatchSyncGroupCommit(b *testing.B) {
	dir := b.TempDir()
	sm, _ := lf.New(dir)
	defer sm.Close()
	fm, _ := fs.New(dir)
	f, _ := New(dir, sm, fm, nil)
	defer f.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := f.Sync(); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkWriteBufferAlloc measures write buffer allocation cost.
func BenchmarkWriteBufferAlloc(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		wb := newWriteBuffer()
		wb.Reset()
		_ = wb.Available()
	}
}

// ---- Group Commit Pipeline (REQ000542) ---------------------------------

// TestGroupCommitBasic verifies the happy path: a single Sync request
// triggers an immediate flush and the caller is unblocked.
func TestGroupCommitBasic(t *testing.T) {
	fsyncCalled := 0
	gc := newGroupCommit(groupCommitOptions{Timeout: 50 * time.Microsecond})
	gc.SetFsyncFn(func() error {
		fsyncCalled++
		return nil
	})

	req := &groupCommitReq{
		done: make(chan struct{}),
		lsn:  1,
	}
	gc.Submit(req)
	<-req.done

	if req.err != nil {
		t.Errorf("req.err: got %v, want nil", req.err)
	}
	if fsyncCalled != 1 {
		t.Errorf("fsync called: got %d, want 1", fsyncCalled)
	}
}

// TestGroupCommitBatch verifies multiple concurrent Sync requests are
// batched into a single fsync.
func TestGroupCommitBatch(t *testing.T) {
	fsyncCount := 0
	var mu sync.Mutex
	gc := newGroupCommit(groupCommitOptions{Timeout: 100 * time.Millisecond})
	gc.SetFsyncFn(func() error {
		mu.Lock()
		fsyncCount++
		mu.Unlock()
		return nil
	})

	const n = 10
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			req := &groupCommitReq{
				done: make(chan struct{}),
				lsn:  uint64(idx + 1),
			}
			gc.Submit(req)
			<-req.done
			errs[idx] = req.err
		}(i)
	}
	wg.Wait()

	// All requests should succeed.
	for i, err := range errs {
		if err != nil {
			t.Errorf("req[%d].err: got %v, want nil", i, err)
		}
	}

	// Exactly one fsync should have been called (batched).
	if fsyncCount != 1 {
		t.Errorf("fsync called: got %d, want 1", fsyncCount)
	}
}

// TestGroupCommitTimeout verifies the deadline fires and unblocks
// all waiters even with few requests.
func TestGroupCommitTimeout(t *testing.T) {
	fsyncCount := 0
	gc := newGroupCommit(groupCommitOptions{Timeout: 1 * time.Millisecond})
	gc.SetFsyncFn(func() error {
		fsyncCount++
		return nil
	})

	// Submit one request and wait for timeout-driven flush.
	req := &groupCommitReq{
		done: make(chan struct{}),
		lsn:  1,
	}
	gc.Submit(req)
	<-req.done

	if req.err != nil {
		t.Errorf("req.err: got %v, want nil", req.err)
	}
	if fsyncCount != 1 {
		t.Errorf("fsync called: got %d, want 1", fsyncCount)
	}
}

// TestGroupCommitErrorPropagation verifies that an fsync error is
// propagated to all waiters in the batch.
func TestGroupCommitErrorPropagation(t *testing.T) {
	testErr := &testError{"fsync failed"}
	gc := newGroupCommit(groupCommitOptions{Timeout: 100 * time.Millisecond})
	gc.SetFsyncFn(func() error { return testErr })

	const n = 5
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			req := &groupCommitReq{done: make(chan struct{}), lsn: uint64(idx + 1)}
			gc.Submit(req)
			<-req.done
			errs[idx] = req.err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != testErr {
			t.Errorf("req[%d].err: got %v, want %v", i, err, testErr)
		}
	}
}

// TestGroupCommitClose verifies that Close unblocks waiters with
// ErrFlusherClosed.
func TestGroupCommitClose(t *testing.T) {
	gc := newGroupCommit(groupCommitOptions{Timeout: 100 * time.Millisecond})

	// Submit a request but don't wait — close before it's processed.
	req := &groupCommitReq{done: make(chan struct{}), lsn: 1}
	gc.Submit(req)
	gc.Close()
	<-req.done

	if req.err != ErrFlusherClosed {
		t.Errorf("req.err: got %v, want ErrFlusherClosed", req.err)
	}
}

// TestGroupCommitStats verifies statistics are tracked correctly.
func TestGroupCommitStats(t *testing.T) {
	gc := newGroupCommit(groupCommitOptions{Timeout: 100 * time.Millisecond})
	gc.SetFsyncFn(func() error { return nil })

	// Single request.
	req1 := &groupCommitReq{done: make(chan struct{}), lsn: 1}
	gc.Submit(req1)
	<-req1.done

	stats := gc.Stats()
	if stats.GroupsFlushed != 1 {
		t.Errorf("GroupsFlushed: got %d, want 1", stats.GroupsFlushed)
	}
}

// testError is a simple error type for test assertions.
type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }
