//go:build !debug

package hk

import (
	"context"
	"time"
)

// TraceStats holds trace sink statistics.
type TraceStats struct {
	Capacity int
	Used     int
	Dropped  int64
}

// EventSink is the interface for structured event emission.
type EventSink interface {
	Emit(ctx context.Context, class string, fields map[string]any)
	Stats() TraceStats
}

// MetricSink is the interface for metric observation.
// Named MetricSink (not MetricHook) to avoid collision with the existing
// MetricHook() function in metric.go.
type MetricSink interface {
	Observe(counter string, value int64)
	ObserveLatency(histogram string, d time.Duration)
	Snapshot() MetricSnapshot
}

// MetricSnapshot holds a point-in-time view of metrics.
type MetricSnapshot struct {
	Counters   map[string]int64
	Histograms map[string]LatencyHistSnapshot
}

// LatencyHistSnapshot holds a latency histogram snapshot.
type LatencyHistSnapshot struct {
	Buckets []int64
	Count   int64
	Sum     int64
}

// ProfileHook is the interface for profile dumping.
type ProfileHook interface {
	DumpProfile(kind string, dur time.Duration) (string, error)
}

// noopSink is a no-op EventSink (zero-allocation, inlined by compiler).
type noopSink struct{}

func (noopSink) Emit(_ context.Context, _ string, _ map[string]any) {}
func (noopSink) Stats() TraceStats                                  { return TraceStats{} }

// noopMetricSink is a no-op MetricSink.
type noopMetricSink struct{}

func (noopMetricSink) Observe(_ string, _ int64)                {}
func (noopMetricSink) ObserveLatency(_ string, _ time.Duration) {}
func (noopMetricSink) Snapshot() MetricSnapshot {
	return MetricSnapshot{
		Counters:   make(map[string]int64),
		Histograms: make(map[string]LatencyHistSnapshot),
	}
}

// noopProfile is a no-op ProfileHook.
type noopProfile struct{}

func (noopProfile) DumpProfile(_ string, _ time.Duration) (string, error) {
	return "", nil
}

// DefaultEventSink is the default no-op event sink.
var DefaultEventSink EventSink = noopSink{}

// DefaultMetricSink is the default no-op metric sink.
var DefaultMetricSink MetricSink = noopMetricSink{}

// DefaultProfileHook is the default no-op profile hook.
var DefaultProfileHook ProfileHook = noopProfile{}
