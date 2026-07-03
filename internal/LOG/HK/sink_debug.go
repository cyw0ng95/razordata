//go:build debug

package hk

// DefaultSink is the global trace event sink.
// Initialized by DBG/DI.NewDebugger at startup.
var DefaultSink EventSink

// DefaultMetricSink is the global metric accumulator.
// Initialized by DBG/DI.NewDebugger at startup.
var DefaultMetricSink MetricSink

// DefaultProfileHook is the global profile trigger.
// Initialized by DBG/DI.NewDebugger at startup.
var DefaultProfileHook ProfileHook
