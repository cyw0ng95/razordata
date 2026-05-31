package hk

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// Hook is the event hook interface.
// Hooks receive log events asynchronously via a background goroutine.
type Hook interface {
	OnLog(level slog.Level, msg string, args []any)
	Close() error
}

// LogEvent represents a single log event to be dispatched to hooks.
type LogEvent struct {
	Level slog.Level
	Msg   string
	Args  []any
	TS    time.Time
}

// HookRegistry manages registered hooks.
// Hooks receive events asynchronously via a bounded channel.
// Events are non-blocking — if the channel is full, events are dropped.
type HookRegistry struct {
	hooks   map[string]Hook
	ch      chan LogEvent
	mu      sync.RWMutex
	done    chan struct{}
	closed  bool
	muClose sync.Mutex // protects closing

	// Statistics
	dropped     atomic.Int64
	dispatched  atomic.Int64
}

// New creates a HookRegistry with the given buffer size.
func New(bufferSize int) *HookRegistry {
	if bufferSize <= 0 {
		bufferSize = 1024
	}

	r := &HookRegistry{
		hooks: make(map[string]Hook),
		ch:    make(chan LogEvent, bufferSize),
		done:  make(chan struct{}),
	}

	// Start the dispatch goroutine.
	go r.dispatch()

	return r
}

// dispatch loops over the channel and sends events to all registered hooks.
// Runs in a background goroutine — never blocks the logging path.
func (r *HookRegistry) dispatch() {
	for {
		select {
		case event, ok := <-r.ch:
			if !ok {
				// Channel closed, exit dispatch loop.
				return
			}
			r.dispatched.Add(1)
			r.mu.RLock()
			for name, hook := range r.hooks {
				go func(name string, hook Hook) {
					hook.OnLog(event.Level, event.Msg, event.Args)
				}(name, hook)
			}
			r.mu.RUnlock()
		case <-r.done:
			return
		}
	}
}

// Register adds a hook identified by name.
// Duplicate names overwrite the previous hook.
func (r *HookRegistry) Register(name string, hook Hook) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hooks[name] = hook
}

// Unregister removes a hook by name.
func (r *HookRegistry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.hooks, name)
}

// Emit sends a log event to the dispatch goroutine.
// Non-blocking: if the channel is full, the event is dropped.
// This ensures the logging hot path is never blocked.
func (r *HookRegistry) Emit(level slog.Level, msg string, args []any) {
	event := LogEvent{
		Level: level,
		Msg:   msg,
		Args:  args,
		TS:    time.Now(),
	}

	select {
	case r.ch <- event:
		// Sent successfully.
	default:
		// Channel full, drop event.
		r.dropped.Add(1)
	}
}

// Close shuts down the dispatch goroutine and closes all hooks.
// Safe to call multiple times.
func (r *HookRegistry) Close() error {
	r.muClose.Lock()
	defer r.muClose.Unlock()

	if r.closed {
		return nil
	}
	r.closed = true

	close(r.done)
	close(r.ch)

	r.mu.Lock()
	defer r.mu.Unlock()

	var err error
	for name, hook := range r.hooks {
		if closeErr := hook.Close(); closeErr != nil {
			err = closeErr
		}
		delete(r.hooks, name)
	}

	return err
}

// Stats holds hook registry statistics.
type Stats struct {
	Registered int
	ChannelCap int
	Dropped    int64
	Dispatched int64
}

// Stats returns current registry statistics.
func (r *HookRegistry) Stats() Stats {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return Stats{
		Registered: len(r.hooks),
		ChannelCap: cap(r.ch),
		Dropped:    r.dropped.Load(),
		Dispatched: r.dispatched.Load(),
	}
}
