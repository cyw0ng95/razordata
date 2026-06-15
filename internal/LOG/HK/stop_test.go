package hk

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"
)

// noopHook is a Hook implementation that does nothing and reports
// its Close calls.
type noopHook struct {
	closeCount int
}

func (h *noopHook) OnLog(level slog.Level, msg string, args []any) {}
func (h *noopHook) Close() error {
	h.closeCount++
	return nil
}

// TestStop_Graceful verifies the dispatcher goroutine exits when
// Stop is called.
func TestStop_Graceful(t *testing.T) {
	r := New(16)
	r.Register("test", &noopHook{})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := r.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	// Idempotent: a second call returns nil.
	if err := r.Stop(ctx); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
}

// TestStop_AlreadyCancelled exercises the timeout branch.
func TestStop_AlreadyCancelled(t *testing.T) {
	r := New(16)
	r.Register("test", &noopHook{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := r.Stop(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Stop: want context.Canceled, got %v", err)
	}
}

// TestStop_GoroutineExited pins that loopDone is closed after
// Stop returns nil.
func TestStop_GoroutineExited(t *testing.T) {
	r := New(16)
	r.Register("test", &noopHook{})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := r.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	select {
	case <-r.loopDone:
		// good
	default:
		t.Fatalf("loopDone not closed after Stop returned nil")
	}
}

// TestClose_AfterStop verifies that Close is still safe to call
// after Stop. The split between Stop (dispatcher) and Close
// (channel + hooks) is by design.
func TestClose_AfterStop(t *testing.T) {
	r := New(16)
	h := &noopHook{}
	r.Register("test", h)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := r.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if h.closeCount != 1 {
		t.Errorf("hook Close calls: want 1, got %d", h.closeCount)
	}
	// And again — Close is idempotent.
	if err := r.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if h.closeCount != 1 {
		t.Errorf("hook Close calls: want still 1, got %d", h.closeCount)
	}
}
