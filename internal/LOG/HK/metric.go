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
// Parses log events and updates metrics (REQ000193).
// Expected events:
// - "SQL query": increment queryCount
// - "rows returned": add N to rowsReturned (args: "rows", N)
// - "bytes read": add N to bytesRead (args: "bytes", N)
// - "bytes written": add N to bytesWritten (args: "bytes", N)
func (m *metricHook) OnLog(level slog.Level, msg string, args []any) {
	switch msg {
	case "SQL query":
		m.queryCount.Add(1)
	case "rows returned":
		m.rowsReturned.Add(extractInt64(args, "rows"))
	case "bytes read":
		m.bytesRead.Add(extractInt64(args, "bytes"))
	case "bytes written":
		m.bytesWritten.Add(extractInt64(args, "bytes"))
	}
}

// extractInt64 extracts an int64 value from args by key.
// Returns 0 if not found or wrong type.
func extractInt64(args []any, key string) int64 {
	for i := 0; i < len(args)-1; i += 2 {
		if k, ok := args[i].(string); ok && k == key {
			switch v := args[i+1].(type) {
			case int64:
				return v
			case int:
				return int64(v)
			case int32:
				return int64(v)
			}
		}
	}
	return 0
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
