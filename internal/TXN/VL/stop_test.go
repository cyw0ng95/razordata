package VL

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestEpochManagerStop_Graceful constructs a fresh epoch manager,
// starts it, and verifies Stop returns nil within a short window.
func TestEpochManagerStop_Graceful(t *testing.T) {
	em := newEpochManager()
	em.Start()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := em.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	// Idempotent: second call also returns nil.
	if err := em.Stop(ctx); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
}

// TestEpochManagerStop_AlreadyCancelled exercises the timeout
// branch: a pre-cancelled context returns context.Canceled.
func TestEpochManagerStop_AlreadyCancelled(t *testing.T) {
	em := newEpochManager()
	em.Start()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := em.Stop(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Stop: want context.Canceled, got %v", err)
	}
}

// TestEpochManagerStop_WithoutStart verifies that Stop on a
// manager whose goroutine was never started returns nil. The wg
// counter is zero, so wg.Wait returns immediately.
func TestEpochManagerStop_WithoutStart(t *testing.T) {
	em := newEpochManager()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := em.Stop(ctx); err != nil {
		t.Fatalf("Stop on unstarted manager: %v", err)
	}
}

// TestStopGC_GlobalIsIdempotent exercises the package-level StopGC
// wrapper around the global epoch manager.
func TestStopGC_GlobalIsIdempotent(t *testing.T) {
	StopGC()
	StopGC()
}
