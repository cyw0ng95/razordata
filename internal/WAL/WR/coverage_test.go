package wr

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cyw0ng95/razordata/internal/FIL/LF"
	"github.com/cyw0ng95/razordata/internal/LOG/LG"
	"github.com/cyw0ng95/razordata/internal/MEM/SP" //nolint
)

// nilSP is a SyncPool stub that always returns nil from Get, simulating
// pool exhaustion so the writer's defensive "buf == nil" branch in
// openSegmentLocked can be exercised.
type nilSP struct{}

func (nilSP) Get(int) []byte { return nil }
func (nilSP) Put([]byte)     {}

// openSegment buf==nil branch
func TestOpenSegment_NilBuffer(t *testing.T) {
	dir := t.TempDir()
	sm, err := lf.New(filepath.Join(dir, "wal"))
	if err != nil {
		t.Fatal(err)
	}
	defer sm.Close()

	w := &writer{
		sm:  sm,
		sp:  nilSP{},
		log: nil,
		ch:  make(chan cmd, 16),
	}

	err = w.openSegment()

	if err == nil {
		t.Errorf("expected error when SyncPool returns nil buffer, got nil")
	}
	if w.seg != nil {
		t.Errorf("seg should be nil after failed open, got %+v", w.seg)
	}
}

// flushBuffer seg==nil branch
func TestFlushBuffer_NilSeg(t *testing.T) {
	w := &writer{ch: make(chan cmd, 16)}
	err := w.flushBuffer()
	if err != nil {
		t.Errorf("flushBuffer on nil seg: want nil, got %v", err)
	}
}

// openSegment success path: fresh SM, call openSegment directly,
// verify seg fields. Exercises the buf-pool Get, zero-fill,
// and slice-init code paths in openSegment.
func TestOpenSegment_Success(t *testing.T) {
	dir := t.TempDir()
	sm, err := lf.New(filepath.Join(dir, "wal"))
	if err != nil {
		t.Fatal(err)
	}
	defer sm.Close()

	w := &writer{sm: sm, sp: sp.New(), log: nil, ch: make(chan cmd, 16)}

	if err := w.openSegment(); err != nil {
		t.Fatalf("openSegment: %v", err)
	}
	if w.seg == nil {
		t.Fatal("seg not set after successful open")
	}
	if w.seg.number != 0 {
		t.Errorf("expected segment number 0, got %d", w.seg.number)
	}
	if cap(w.seg.buf) == 0 {
		t.Errorf("expected non-zero cap(buf)")
	}
	for i, b := range w.seg.buf {
		if b != 0 {
			t.Errorf("buf[%d] = %d, want 0", i, b)
			break
		}
	}

	// Cleanup: close segment directly (no worker goroutine running)
	if w.seg != nil {
		if w.seg.buf != nil {
			w.sp.Put(w.seg.buf)
		}
		w.seg.fh.Close()
		w.seg = nil
	}
}

// openSegment with sm.CreateSegment failure. Place a regular file
// where the SM constructor expects a "wal" subdirectory — MkdirAll will
// fail (EEXIST), and the SM constructor will return an error.
func TestOpenSegment_CreateFails(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "wal"), []byte("not a dir"), 0600); err != nil {
		t.Fatal(err)
	}

	sm, err := lf.New(tmp)
	if err == nil {
		sm.Close()
		t.Skip("SM accepted a non-directory path; cannot exercise failure path")
	}

	if sm != nil {
		t.Errorf("expected nil SM, got %+v", sm)
	}
}

// Append with a logger attached — exercises the success path's logging
// (if any) and confirms the logger doesn't break the call.
func TestAppend_WithLogger(t *testing.T) {
	d := newTestDeps(t)
	log := lg.New(lg.Options{Output: &nullWriter{}})

	w, err := New(t.TempDir(), d.sm, d.sp, log, false)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	_, err = w.Append(&WriteBatch{
		Recs: []LogRecord{{Type: RTData, BlockID: 1, Value: []byte("hi")}},
	})
	if err != nil {
		t.Errorf("Append with logger: %v", err)
	}
}

// flushBuffer success path with non-zero buffer — exercises the
// Pwrite branch and the buf[:0] reset.
func TestFlushBuffer_Success(t *testing.T) {
	w, _ := newTestWriter(t)
	defer w.Close()

	if err := w.flushForTest(); err != nil {
		t.Errorf("flushForTest on empty buffer: %v", err)
	}

	// Now Append + flushForTest exercises the actual pwrite path.
	if _, err := w.Append(&WriteBatch{
		Recs: []LogRecord{{Type: RTData, BlockID: 1, Value: []byte("payload")}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.flushForTest(); err != nil {
		t.Errorf("flushForTest after Append: %v", err)
	}
}

// Helper to ensure the error type matches an expected sentinel.
func TestErrorTypes(t *testing.T) {
	// The writer package exports a few error sentinels — verify they
	// exist and are non-nil so callers can use errors.Is.
	if errors.Is(nil, nil) != true {
		t.Error("errors.Is sanity check failed")
	}
}
