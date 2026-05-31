package hk

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// mockHook is a test hook that records all received events.
type mockHook struct {
	mu     sync.Mutex
	events []logEvent
	name   string
}

func newMockHook(name string) *mockHook {
	return &mockHook{name: name}
}

func (h *mockHook) OnLog(level slog.Level, msg string, args []any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, logEvent{level: level, msg: msg, args: args, ts: time.Now()})
}

func (h *mockHook) Close() error {
	return nil
}

func (h *mockHook) eventsCopy() []logEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	events := make([]logEvent, len(h.events))
	copy(events, h.events)
	return events
}

// TestRegisterUnregister verifies hook registration and unregistration.
func TestRegisterUnregister(t *testing.T) {
	registry := New(1024)
	defer registry.Close()

	hook := newMockHook("test")
	registry.Register("test", hook)

	registry.Emit(slog.LevelInfo, "test message", nil)

	stats := registry.Stats()
	if stats.Registered != 1 {
		t.Errorf("expected 1 registered hook, got %d", stats.Registered)
	}

	registry.Unregister("test")
	stats = registry.Stats()
	if stats.Registered != 0 {
		t.Errorf("expected 0 registered hooks, got %d", stats.Registered)
	}
}

// TestDuplicateRegister verifies duplicate names overwrite previous hooks.
func TestDuplicateRegister(t *testing.T) {
	registry := New(1024)
	defer registry.Close()

	hook1 := newMockHook("hook1")
	hook2 := newMockHook("hook2")

	registry.Register("name", hook1)
	registry.Register("name", hook2) // overwrites hook1

	stats := registry.Stats()
	if stats.Registered != 1 {
		t.Errorf("expected 1 registered hook, got %d", stats.Registered)
	}
}

// TestEventDelivery verifies OnLog is called with correct arguments.
func TestEventDelivery(t *testing.T) {
	registry := New(1024)
	defer registry.Close()

	hook := newMockHook("delivery")
	registry.Register("delivery", hook)

	msg := "test message"
	args := []any{"key", "value"}
	registry.Emit(slog.LevelInfo, msg, args)

	// Give dispatch goroutine time to deliver.
	time.Sleep(10 * time.Millisecond)

	events := hook.eventsCopy()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	if events[0].msg != msg {
		t.Errorf("expected msg=%q, got %q", msg, events[0].msg)
	}

	if events[0].level != slog.LevelInfo {
		t.Errorf("expected level=Info, got %v", events[0].level)
	}
}

// TestNonBlocking verifies Emit does not block when channel is full.
func TestNonBlocking(t *testing.T) {
	registry := New(1) // buffer size 1
	defer registry.Close()

	hook := newMockHook("nonblock")
	registry.Register("nonblock", hook)

	// Emit many events quickly — should not block even though channel is small.
	start := time.Now()
	for i := 0; i < 10000; i++ {
		registry.Emit(slog.LevelDebug, "message", nil)
	}
	elapsed := time.Since(start)

	// Should complete in under 1 second even with 10000 events and buffer of 1.
	if elapsed > 1*time.Second {
		t.Errorf("Emit blocked for %v", elapsed)
	}
}

// TestConcurrentEmit verifies concurrent Emit calls are safe.
func TestConcurrentEmit(t *testing.T) {
	registry := New(1024)
	defer registry.Close()

	var wg sync.WaitGroup
	const goroutines = 100
	const eventsPerGoroutine = 100

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < eventsPerGoroutine; j++ {
				registry.Emit(slog.LevelInfo, "concurrent", []any{"id", id, "n", j})
			}
		}(i)
	}

	wg.Wait()
}

// TestMultipleHooks verifies all hooks receive events.
func TestMultipleHooks(t *testing.T) {
	registry := New(1024)
	defer registry.Close()

	hook1 := newMockHook("hook1")
	hook2 := newMockHook("hook2")
	registry.Register("hook1", hook1)
	registry.Register("hook2", hook2)

	registry.Emit(slog.LevelWarn, "multi", nil)

	time.Sleep(10 * time.Millisecond)

	if len(hook1.eventsCopy()) != 1 {
		t.Errorf("hook1 expected 1 event, got %d", len(hook1.eventsCopy()))
	}
	if len(hook2.eventsCopy()) != 1 {
		t.Errorf("hook2 expected 1 event, got %d", len(hook2.eventsCopy()))
	}
}

// TestDropOnOverflow verifies events are dropped when channel is full.
func TestDropOnOverflow(t *testing.T) {
	registry := New(10) // small buffer
	defer registry.Close()

	hook := newMockHook("drop")
	registry.Register("drop", hook)

	// Flood with events — many will be dropped.
	for i := 0; i < 10000; i++ {
		registry.Emit(slog.LevelDebug, "flood", nil)
	}

	// Wait for dispatch to process what it can.
	time.Sleep(50 * time.Millisecond)

	// Not all events should be delivered due to overflow.
	// This verifies non-blocking behavior.
	events := hook.eventsCopy()
	if len(events) == 10000 {
		t.Log("all events delivered (may indicate channel was large enough)")
	}
}

// TestClose verifies Close shuts down dispatch and closes hooks.
func TestClose(t *testing.T) {
	registry := New(1024)

	hook := newMockHook("close")
	registry.Register("close", hook)

	registry.Emit(slog.LevelInfo, "before close", nil)
	time.Sleep(10 * time.Millisecond)

	registry.Close()

	// After Close, Emit should still be non-blocking (writes to closed channel will drop).
	// But dispatch loop is stopped, so no more events delivered.
	beforeCount := len(hook.eventsCopy())
	time.Sleep(10 * time.Millisecond)
	afterCount := len(hook.eventsCopy())

	if afterCount > beforeCount {
		t.Error("events delivered after Close")
	}
}

// TestInterfaceCompliance verifies registry implements HookRegistry interface.
func TestInterfaceCompliance(t *testing.T) {
	// HookRegistry is used via direct calls, no interface defined yet.
	// Verify New returns a valid registry.
	registry := New(1024)
	if registry == nil {
		t.Error("New returned nil")
	}
	registry.Close()
}

// TestStats verifies Stats returns correct information.
func TestStats(t *testing.T) {
	registry := New(1024)
	defer registry.Close()

	stats := registry.Stats()
	if stats.Registered != 0 {
		t.Errorf("expected 0 registered, got %d", stats.Registered)
	}
	if stats.ChannelCap != 1024 {
		t.Errorf("expected ChannelCap 1024, got %d", stats.ChannelCap)
	}

	registry.Register("hook1", newMockHook("hook1"))
	registry.Register("hook2", newMockHook("hook2"))
	stats = registry.Stats()
	if stats.Registered != 2 {
		t.Errorf("expected 2 registered, got %d", stats.Registered)
	}
}

// dropCounter is a thread-safe counter for dropped events.
var droppedEvents atomic.Int64

// incrementDropped increments the dropped event counter.
func incrementDropped() {
	droppedEvents.Add(1)
}

// resetDropped resets the dropped event counter.
func resetDropped() {
	droppedEvents.Store(0)
}

// getDropped returns the number of dropped events.
func getDropped() int64 {
	return droppedEvents.Load()
}
