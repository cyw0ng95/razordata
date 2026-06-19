// Package hk implements profile hooks for CPU/heap dumps (REQ000322).
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
type profileHook struct {
	dir       string // output directory for profile files
	enabled   atomic.Bool
	mu        sync.Mutex
	lastDump  time.Time
	dumpCount atomic.Int64
	rateLimit time.Duration // minimum interval between dumps
	cpuActive atomic.Bool
}

type ProfileHookOpt func(*profileHook)

// WithCPU enables CPU profiling.
func WithCPU(enable bool) ProfileHookOpt {
	return func(p *profileHook) { p.cpuActive.Store(enable) }
}

// WithRateLimit sets the minimum interval between dumps.
func WithRateLimit(d time.Duration) ProfileHookOpt {
	return func(p *profileHook) { p.rateLimit = d }
}

// NewProfileHook creates a profile hook that dumps profiles to dir (REQ000322).
func NewProfileHook(dir string, opts ...ProfileHookOpt) Hook {
	if dir == "" {
		dir = "profiles"
	}
	_ = os.MkdirAll(dir, 0755)
	p := &profileHook{dir: dir, rateLimit: 5 * time.Second}
	for _, opt := range opts {
		opt(p)
	}
	p.enabled.Store(true)
	return p
}

var _ Hook = (*profileHook)(nil)

// OnLog implements Hook. Triggers a profile dump on Error events.
func (p *profileHook) OnLog(level slog.Level, msg string, args []any) {
	if !p.enabled.Load() {
		return
	}
	if level < slog.LevelError {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	if time.Since(p.lastDump) < p.rateLimit {
		return
	}
	p.lastDump = time.Now()

	if err := p.dump(); err != nil {
		fmt.Fprintf(os.Stderr, "profileHook: dump failed: %v\n", err)
	}
}

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

// DumpNow triggers an immediate profile dump (REQ000322).
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

func (p *profileHook) Close() error {
	if p.cpuActive.Load() {
		pprof.StopCPUProfile()
	}
	p.enabled.Store(false)
	return nil
}

func (p *profileHook) DumpCount() int64 {
	if p == nil {
		return 0
	}
	return p.dumpCount.Load()
}
