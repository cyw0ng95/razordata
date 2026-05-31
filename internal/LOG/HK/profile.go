package hk

import (
	"log/slog"
)

// profileHook is a stub implementation of Hook for CPU/memory profiling.
// Real implementation will dump CPU and heap profiles on Error level events.
// For now, it does nothing.
type profileHook struct{}

var _ Hook = (*profileHook)(nil)

// OnLog implements Hook.
// Stub: real implementation will trigger pprof dump on Error events.
func (p *profileHook) OnLog(level slog.Level, msg string, args []any) {
	// TODO: trigger profile dump on Error level events.
	// Real implementation: pprof.Lookup("heap").WriteTo(...) and pprof.StartCPUProfile(...).
	// Should be opt-in (not enabled by default).
}

// Close implements Hook.
func (p *profileHook) Close() error {
	return nil
}
