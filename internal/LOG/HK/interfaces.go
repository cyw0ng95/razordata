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
