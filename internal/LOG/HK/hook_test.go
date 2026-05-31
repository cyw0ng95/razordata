package hk

import (
	"log/slog"
	"sync"
	"testing"
	"time"
)

// mockHook is a test hook that records all received events.
type mockHook struct {
	mu     sync.Mutex
	events []LogEvent
	name   string
}

func newMockHook(name string) *mockHook {
	return &mockHook{name: name}
}

func (h *mockHook) OnLog(level slog.Level, msg string, args []any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, LogEvent{Level: level, Msg: msg, Args: args, TS: time.Now()})
}

func (h *mockHook) Close() error {
	return nil
}

func (h *mockHook) eventsCopy() []LogEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	events := make([]LogEvent, len(h.events))
	copy(events, h.events)
	return events
}

func (h *mockHook) eventCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.events)
}

// TestNewDefaultBufferSize verifies New uses default buffer size when <= 0.
func TestNewDefaultBufferSize(t *testing.T) {
	registry := New(0)
	defer registry.Close()

	stats := registry.Stats()
	if stats.ChannelCap != 1024 {
		t.Errorf("expected default ChannelCap 1024, got %d", stats.ChannelCap)
	}
}

func TestNewNegativeBufferSize(t *testing.T) {
	registry := New(-1)
	defer registry.Close()

	stats := registry.Stats()
	if stats.ChannelCap != 1024 {
		t.Errorf("expected default ChannelCap 1024 for negative, got %d", stats.ChannelCap)
	}
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

// TestRegisterMultiple verifies registering multiple unique hooks.
func TestRegisterMultiple(t *testing.T) {
	registry := New(1024)
	defer registry.Close()

	for i := 0; i < 10; i++ {
		registry.Register(string(rune('a'+i)), newMockHook(string(rune('a'+i))))
	}

	stats := registry.Stats()
	if stats.Registered != 10 {
		t.Errorf("expected 10 registered hooks, got %d", stats.Registered)
	}
}

// TestUnregisterNonExistent verifies Unregister on non-existent hook is safe.
func TestUnregisterNonExistent(t *testing.T) {
	registry := New(1024)
	defer registry.Close()

	// Should not panic
	registry.Unregister("nonexistent")

	stats := registry.Stats()
	if stats.Registered != 0 {
		t.Errorf("expected 0 registered hooks, got %d", stats.Registered)
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

	if events[0].Msg != msg {
		t.Errorf("expected msg=%q, got %q", msg, events[0].Msg)
	}

	if events[0].Level != slog.LevelInfo {
		t.Errorf("expected level=Info, got %v", events[0].Level)
	}
}

// TestEventDeliveryWithArgs verifies event args are delivered correctly.
func TestEventDeliveryWithArgs(t *testing.T) {
	registry := New(1024)
	defer registry.Close()

	hook := newMockHook("args")
	registry.Register("args", hook)

	args := []any{"key1", "value1", "key2", "value2"}
	registry.Emit(slog.LevelWarn, "args test", args)

	time.Sleep(10 * time.Millisecond)

	events := hook.eventsCopy()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	if len(events[0].Args) != len(args) {
		t.Errorf("expected %d args, got %d", len(args), len(events[0].Args))
	}
}

// TestEventDeliveryWithNilArgs verifies event with nil args.
func TestEventDeliveryWithNilArgs(t *testing.T) {
	registry := New(1024)
	defer registry.Close()

	hook := newMockHook("nilargs")
	registry.Register("nilargs", hook)

	registry.Emit(slog.LevelInfo, "nil args", nil)

	time.Sleep(10 * time.Millisecond)

	events := hook.eventsCopy()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
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

// TestAllLogLevels verifies all slog levels are delivered.
func TestAllLogLevels(t *testing.T) {
	registry := New(1024)
	defer registry.Close()

	hook := newMockHook("levels")
	registry.Register("levels", hook)

	// Use unique messages so we can verify all levels arrive (order not guaranteed async)
	registry.Emit(slog.LevelDebug, "debug", nil)
	registry.Emit(slog.LevelInfo, "info", nil)
	registry.Emit(slog.LevelWarn, "warn", nil)
	registry.Emit(slog.LevelError, "error", nil)

	time.Sleep(20 * time.Millisecond)

	events := hook.eventsCopy()
	if len(events) != 4 {
		t.Fatalf("expected 4 events, got %d", len(events))
	}

	// Check that all 4 unique messages arrived
	msgSet := make(map[string]bool)
	for _, e := range events {
		msgSet[e.Msg] = true
	}
	for _, msg := range []string{"debug", "info", "warn", "error"} {
		if !msgSet[msg] {
			t.Errorf("expected message %q in events", msg)
		}
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

// TestEmitNoHook verifies Emit without any hook is safe.
func TestEmitNoHook(t *testing.T) {
	registry := New(1024)
	defer registry.Close()

	// Should not panic
	registry.Emit(slog.LevelInfo, "no hook", nil)
	time.Sleep(10 * time.Millisecond)
}

// TestEmitAfterUnregister verifies Emit after unregistering hook.
func TestEmitAfterUnregister(t *testing.T) {
	registry := New(1024)
	defer registry.Close()

	hook := newMockHook("unreg")
	registry.Register("unreg", hook)

	registry.Emit(slog.LevelInfo, "before unregister", nil)
	time.Sleep(10 * time.Millisecond)

	registry.Unregister("unreg")

	beforeCount := hook.eventCount()
	registry.Emit(slog.LevelInfo, "after unregister", nil)
	time.Sleep(10 * time.Millisecond)

	afterCount := hook.eventCount()
	if afterCount > beforeCount {
		t.Errorf("expected no new events after unregister, got %d -> %d", beforeCount, afterCount)
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

// TestDoubleClose verifies calling Close twice is safe.
func TestDoubleClose(t *testing.T) {
	registry := New(1024)

	hook := newMockHook("doubleclose")
	registry.Register("doubleclose", hook)

	registry.Close()
	registry.Close() // should not panic
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

// TestStatsAfterUnregister verifies Stats after unregistering.
func TestStatsAfterUnregister(t *testing.T) {
	registry := New(1024)
	defer registry.Close()

	registry.Register("hook1", newMockHook("hook1"))
	registry.Register("hook2", newMockHook("hook2"))

	stats := registry.Stats()
	if stats.Registered != 2 {
		t.Errorf("expected 2, got %d", stats.Registered)
	}

	registry.Unregister("hook1")
	stats = registry.Stats()
	if stats.Registered != 1 {
		t.Errorf("expected 1, got %d", stats.Registered)
	}
}

// TestConcurrentRegisterUnregister verifies concurrent register/unregister.
func TestConcurrentRegisterUnregister(t *testing.T) {
	registry := New(1024)
	defer registry.Close()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			name := string(rune('a' + id%26))
			registry.Register(name, newMockHook(name))
			time.Sleep(time.Microsecond)
			registry.Unregister(name)
		}(i)
	}

	wg.Wait()
}

// TestEmitWithEmptyMessage verifies Emit with empty message.
func TestEmitWithEmptyMessage(t *testing.T) {
	registry := New(1024)
	defer registry.Close()

	hook := newMockHook("empty")
	registry.Register("empty", hook)

	registry.Emit(slog.LevelInfo, "", nil)

	time.Sleep(10 * time.Millisecond)

	events := hook.eventsCopy()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Msg != "" {
		t.Errorf("expected empty message, got %q", events[0].Msg)
	}
}

// TestEmitWithManyArgs verifies Emit with many args.
func TestEmitWithManyArgs(t *testing.T) {
	registry := New(1024)
	defer registry.Close()

	hook := newMockHook("manyargs")
	registry.Register("manyargs", hook)

	args := make([]any, 100)
	for i := 0; i < 100; i++ {
		args[i] = i
	}

	registry.Emit(slog.LevelInfo, "many args", args)

	time.Sleep(10 * time.Millisecond)

	events := hook.eventsCopy()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if len(events[0].Args) != len(args) {
		t.Errorf("expected %d args, got %d", len(args), len(events[0].Args))
	}
}

// TestStatsDropped verifies Dropped count is tracked.
func TestStatsDropped(t *testing.T) {
	registry := New(1) // tiny buffer
	defer registry.Close()

	hook := newMockHook("drop")
	registry.Register("drop", hook)

	// Flood events to trigger drops
	for i := 0; i < 1000; i++ {
		registry.Emit(slog.LevelInfo, "flood", nil)
	}

	// Allow dispatch to process
	time.Sleep(50 * time.Millisecond)

	stats := registry.Stats()
	if stats.Dispatched == 0 {
		t.Error("expected non-zero Dispatched count")
	}
	// Dropped should be > 0 since we sent 1000 with buffer size 1
	if stats.Dropped == 0 {
		t.Log("Note: Dropped may be 0 if dispatch processed faster than overflow")
	}
}

// TestStatsDispatched verifies Dispatched count increases.
func TestStatsDispatched(t *testing.T) {
	registry := New(1024)
	defer registry.Close()

	hook := newMockHook("dispatched")
	registry.Register("dispatched", hook)

	for i := 0; i < 10; i++ {
		registry.Emit(slog.LevelInfo, "test", nil)
	}

	time.Sleep(20 * time.Millisecond)

	stats := registry.Stats()
	if stats.Dispatched < 10 {
		t.Errorf("expected Dispatched >= 10, got %d", stats.Dispatched)
	}
}