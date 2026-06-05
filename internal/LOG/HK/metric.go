package hk

import (
	"log/slog"
	"sync/atomic"
)

// metricHook is a stub implementation of Hook for collecting metrics.
// Real implementation will collect counters (queries, rows, bytes) and
// histograms (latency) in a lock-free way using sync/atomic.
// For now, it does nothing.
type metricHook struct {
	// counters (lock-free via atomic)
	queryCount   atomic.Int64
	rowsReturned atomic.Int64
	bytesRead    atomic.Int64
	bytesWritten atomic.Int64
}

var _ Hook = (*metricHook)(nil)

// OnLog implements Hook.
// Stub: real implementation will update counters/histograms based on log events.
func (m *metricHook) OnLog(level slog.Level, msg string, args []any) {
	// TODO: parse log events and update metrics.
	// Real implementation: lock-free counters and histograms.
}

// Close implements Hook.
func (m *metricHook) Close() error {
	return nil
}

// QueryCount returns the total number of queries.
func (m *metricHook) QueryCount() int64 {
	return m.queryCount.Load()
}

// RowsReturned returns the total number of rows returned.
func (m *metricHook) RowsReturned() int64 {
	return m.rowsReturned.Load()
}

// BytesRead returns the total bytes read.
func (m *metricHook) BytesRead() int64 {
	return m.bytesRead.Load()
}

// BytesWritten returns the total bytes written.
func (m *metricHook) BytesWritten() int64 {
	return m.bytesWritten.Load()
}
