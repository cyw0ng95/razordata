// Package hk: profile.go implements ProfileHook, which dumps CPU and
// heap profiles when Error-level log events fire. This is the runtime
// tracing export target for the LOG subsystem (REQ000322).
//
// Real eBPF integration requires Linux with CAP_BPF and libbpf headers.
// Until then, this implementation uses Go's pprof runtime package to
// capture CPU and heap profiles on Error events.
package hk

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/pprof"
	"sync"
	"sync/atomic"
	"time"
)

// profileHook dumps CPU and heap profiles on Error-level events.
// The hook is opt-in: callers must construct it explicitly with
// NewProfileHook(dir). Once enabled, the first Error event after
// the last dump triggers a new profile capture.
type profileHook struct {
	dir       string // output directory for profile files
	enabled   atomic.Bool
	mu        sync.Mutex
	lastDump  time.Time
	dumpCount atomic.Int64
	// cpuProfile holds an active CPU profile between start/stop.
	// Only used if WithCPU(true) was specified.
	cpuActive atomic.Bool
}

// ProfileHookOpt configures the profileHook.
type ProfileHookOpt func(*profileHook)

// WithCPU enables CPU profiling (slower, more overhead).
// Default: CPU profiling disabled (heap-only on Error events).
func WithCPU(enable bool) ProfileHookOpt {
	return func(p *profileHook) { p.cpuActive.Store(enable) }
}

// NewProfileHook creates a profile hook that dumps profiles to dir.
// If dir is empty, defaults to "profiles/" relative to working dir.
// The hook is registered with the hook manager and runs alongside
// other hooks.
// REQ000322.
func NewProfileHook(dir string, opts ...ProfileHookOpt) Hook {
	if dir == "" {
		dir = "profiles"
	}
	_ = os.MkdirAll(dir, 0755)
	p := &profileHook{dir: dir}
	for _, opt := range opts {
		opt(p)
	}
	p.enabled.Store(true)
	return p
}

var _ Hook = (*profileHook)(nil)

// OnLog implements Hook. Triggers a profile dump on Error events.
// To avoid runaway disk usage, dumps are rate-limited to one per 5s.
// REQ000322.
func (p *profileHook) OnLog(level slog.Level, msg string, args []any) {
	if !p.enabled.Load() {
		return
	}
	if level < slog.LevelError {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	// Rate limit: at most one dump per 5s
	if time.Since(p.lastDump) < 5*time.Second {
		return
	}
	p.lastDump = time.Now()

	if err := p.dump(); err != nil {
		// Log to stderr; we don't have a logger here to avoid recursion
		fmt.Fprintf(os.Stderr, "profileHook: dump failed: %v\n", err)
	}
}

// dump captures a heap profile (and CPU profile if enabled) and
// writes to files in p.dir.
func (p *profileHook) dump() error {
	ts := time.Now().UnixNano()
	heapFile := filepath.Join(p.dir, fmt.Sprintf("heap-%d.pprof", ts))

	f, err := os.Create(heapFile)
	if err != nil {
		return fmt.Errorf("create heap file: %w", err)
	}
	defer f.Close()

	if err := pprof.Lookup("heap").WriteTo(f, 0); err != nil {
		return fmt.Errorf("write heap profile: %w", err)
	}

	if p.cpuActive.Load() {
		cpuFile := filepath.Join(p.dir, fmt.Sprintf("cpu-%d.pprof", ts))
		cf, err := os.Create(cpuFile)
		if err != nil {
			return fmt.Errorf("create cpu file: %w", err)
		}
		// CPU profile is started/stopped via runtime; we only do a
		// short (1s) snapshot here. For continuous CPU profiling,
		// use net/http/pprof separately.
		if err := pprof.StartCPUProfile(cf); err != nil {
			cf.Close()
			return fmt.Errorf("start cpu profile: %w", err)
		}
		time.Sleep(1 * time.Second)
		pprof.StopCPUProfile()
		cf.Close()
	}

	p.dumpCount.Add(1)
	return nil
}

// DumpNow triggers an immediate profile dump, bypassing the
// rate limiter. Returns the path of the heap profile written.
// REQ000322.
func (p *profileHook) DumpNow() (string, error) {
	if p == nil {
		return "", fmt.Errorf("profileHook is nil")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	ts := time.Now().UnixNano()
	heapFile := filepath.Join(p.dir, fmt.Sprintf("heap-%d.pprof", ts))
	f, err := os.Create(heapFile)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if err := pprof.Lookup("heap").WriteTo(f, 0); err != nil {
		return "", err
	}
	p.dumpCount.Add(1)
	return heapFile, nil
}

// Close implements Hook. Flushes any pending profile state.
func (p *profileHook) Close() error {
	if p.cpuActive.Load() {
		pprof.StopCPUProfile()
	}
	p.enabled.Store(false)
	return nil
}

// DumpCount returns the number of profile dumps performed.
func (p *profileHook) DumpCount() int64 {
	if p == nil {
		return 0
	}
	return p.dumpCount.Load()
}
