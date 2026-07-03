//go:build !debug

package hk

import (
	"context"
	"time"
)

type noopSink struct{}

func (noopSink) Emit(_ context.Context, _ string, _ map[string]any) {}
func (noopSink) Stats() TraceStats                                  { return TraceStats{} }

type noopMetricSink struct{}

func (noopMetricSink) Observe(_ string, _ int64)                {}
func (noopMetricSink) ObserveLatency(_ string, _ time.Duration) {}
func (noopMetricSink) Snapshot() MetricSnapshot {
	return MetricSnapshot{
		Counters:   make(map[string]int64),
		Histograms: make(map[string]LatencyHistSnapshot),
	}
}

type noopProfile struct{}

func (noopProfile) DumpProfile(_ string, _ time.Duration) (string, error) {
	return "", nil
}

// DefaultSink is the default no-op event sink.
var DefaultSink EventSink = noopSink{}

// DefaultMetricSink is the default no-op metric sink.
var DefaultMetricSink MetricSink = noopMetricSink{}

// DefaultProfileHook is the default no-op profile hook.
var DefaultProfileHook ProfileHook = noopProfile{}
