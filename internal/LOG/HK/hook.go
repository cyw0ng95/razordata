package hk

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// Hook is the event hook interface.
type Hook interface {
	OnLog(level slog.Level, msg string, args []any)
	Close() error
}

// LogEvent represents a single log event.
type LogEvent struct {
	Level slog.Level
	Msg   string
	Args  []any
	TS    time.Time
}

// HookRegistry manages registered hooks.
type HookRegistry struct {
	hooks    map[string]Hook
	ch       chan LogEvent
	mu       sync.RWMutex
	done     chan struct{}
	loopDone chan struct{}
	stopOnce sync.Once
	closed   bool
	muClose  sync.Mutex // protects closing

	dropped    atomic.Int64
	dispatched atomic.Int64
}

// New creates a HookRegistry with the given buffer size.
func New(bufferSize int) *HookRegistry {
	if bufferSize <= 0 {
		bufferSize = 1024
	}

	r := &HookRegistry{
		hooks:    make(map[string]Hook),
		ch:       make(chan LogEvent, bufferSize),
		done:     make(chan struct{}),
		loopDone: make(chan struct{}),
	}

	go r.dispatch()

	return r
}

func (r *HookRegistry) dispatch() {
	defer close(r.loopDone)
	for {
		select {
		case event, ok := <-r.ch:
			if !ok {
				return
			}
			r.dispatched.Add(1)
			r.mu.RLock()
			for _, hook := range r.hooks {
				hook.OnLog(event.Level, event.Msg, event.Args)
			}
			r.mu.RUnlock()
		case <-r.done:
			return
		}
	}
}

// Register adds a hook identified by name.
func (r *HookRegistry) Register(name string, hook Hook) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hooks[name] = hook
}

func (r *HookRegistry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.hooks, name)
}

// Emit sends a log event to the dispatch goroutine. Non-blocking.
func (r *HookRegistry) Emit(level slog.Level, msg string, args []any) {
	event := LogEvent{
		Level: level,
		Msg:   msg,
		Args:  args,
		TS:    time.Now(),
	}

	select {
	case r.ch <- event:
	default:
		r.dropped.Add(1)
	}
}

// Close shuts down the dispatch goroutine and closes all hooks.
func (r *HookRegistry) Close() error {
	r.muClose.Lock()
	defer r.muClose.Unlock()

	if r.closed {
		return nil
	}
	r.closed = true

	if err := r.Stop(context.Background()); err != nil {
		return err
	}
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

// Stop signals the dispatch goroutine to exit and waits for it.
func (r *HookRegistry) Stop(ctx context.Context) error {
	r.stopOnce.Do(func() {
		close(r.done)
	})
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-r.loopDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Stats holds hook registry statistics.
type Stats struct {
	Registered int
	ChannelCap int
	Dropped    int64
	Dispatched int64
}

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
