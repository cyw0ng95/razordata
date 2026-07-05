package ls

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// TestCompactionStop_Graceful verifies that Stop returns nil when
// the compaction loop exits promptly. No jobs are enqueued so the
// loop is parked in the select; closing `done` unblocks it.
func TestCompactionStop_Graceful(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mf, err := newManifest(filepath.Join(dir, "manifest"))
	if err != nil {
		t.Fatalf("newManifest: %v", err)
	}
	cm := newCompactionManager(DefaultFS(), dir, mf, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := cm.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	// Second call should also succeed (idempotent).
	if err := cm.Stop(ctx); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
}

// TestCompactionStop_AlreadyCancelled verifies that Stop returns
// ctx.Err() when the context is already done before Stop is called.
func TestCompactionStop_AlreadyCancelled(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mf, err := newManifest(filepath.Join(dir, "manifest"))
	if err != nil {
		t.Fatalf("newManifest: %v", err)
	}
	cm := newCompactionManager(DefaultFS(), dir, mf, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = cm.Stop(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Stop: want context.Canceled, got %v", err)
	}
}

// TestCompactionStop_GoroutineExits pins the invariant that the
// loopDone channel is closed within a short window after Stop is
// called. This is the actual shutdown-completion signal.
func TestCompactionStop_GoroutineExits(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mf, err := newManifest(filepath.Join(dir, "manifest"))
	if err != nil {
		t.Fatalf("newManifest: %v", err)
	}
	cm := newCompactionManager(DefaultFS(), dir, mf, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := cm.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	select {
	case <-cm.loopDone:
		// good — loop exited
	default:
		t.Fatalf("loopDone not closed after Stop returned nil")
	}
}

// TestFlushStop_Graceful covers the flush manager's Stop path. The
// flush loop is parked waiting for either a job or `done`; closing
// `done` unblocks it.
func TestFlushStop_Graceful(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mf, err := newManifest(filepath.Join(dir, "manifest"))
	if err != nil {
		t.Fatalf("newManifest: %v", err)
	}
	fm := newFlushManager(DefaultFS(), dir, 1<<20, mf)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := fm.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := fm.Stop(ctx); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
}

// TestFlushStop_AlreadyCancelled exercises the timeout branch.
func TestFlushStop_AlreadyCancelled(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mf, err := newManifest(filepath.Join(dir, "manifest"))
	if err != nil {
		t.Fatalf("newManifest: %v", err)
	}
	fm := newFlushManager(DefaultFS(), dir, 1<<20, mf)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = fm.Stop(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Stop: want context.Canceled, got %v", err)
	}
}
