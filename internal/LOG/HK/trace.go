package hk

import (
	"log/slog"
)

// traceHook is a stub implementation of Hook for SQL query tracing.
// Real implementation will intercept SQL start/end events from SQL/EX.
// For now, it does nothing — events are logged but not traced.
type traceHook struct{}

var _ Hook = (*traceHook)(nil)

// OnLog implements Hook.
// Stub: real implementation will emit structured trace records with execution time.
func (t traceHook) OnLog(level slog.Level, msg string, args []any) {
	// TODO: emit structured trace record with timing information.
	// Real implementation: intercept SQL query start/end events from SQL/EX.
}

// Close implements Hook.
func (t traceHook) Close() error {
	return nil
}
