package hk

import (
	"log/slog"
	"strconv"
	"strings"
	"sync/atomic"
)

type metricHook struct {
	queryCount   atomic.Int64
	rowsReturned atomic.Int64
	bytesRead    atomic.Int64
	bytesWritten atomic.Int64
}

var _ Hook = (*metricHook)(nil)

// OnLog implements Hook (REQ000193).
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

func (m *metricHook) Close() error {
	return nil
}

func (m *metricHook) QueryCount() int64 {
	return m.queryCount.Load()
}

func (m *metricHook) RowsReturned() int64 {
	return m.rowsReturned.Load()
}

func (m *metricHook) BytesRead() int64 {
	return m.bytesRead.Load()
}

func (m *metricHook) BytesWritten() int64 {
	return m.bytesWritten.Load()
}

// PrometheusMetrics returns the metric counters in Prometheus text format (REQ000101).
func (m *metricHook) PrometheusMetrics() string {
	var b strings.Builder
	b.WriteString("# HELP razor_queries_total Total number of SQL queries.\n")
	b.WriteString("# TYPE razor_queries_total counter\n")
	b.WriteString("razor_queries_total ")
	b.WriteString(strconv.FormatInt(m.queryCount.Load(), 10))
	b.WriteString("\n")

	b.WriteString("# HELP razor_rows_returned_total Total rows returned by queries.\n")
	b.WriteString("# TYPE razor_rows_returned_total counter\n")
	b.WriteString("razor_rows_returned_total ")
	b.WriteString(strconv.FormatInt(m.rowsReturned.Load(), 10))
	b.WriteString("\n")

	b.WriteString("# HELP razor_bytes_read_total Total bytes read from storage.\n")
	b.WriteString("# TYPE razor_bytes_read_total counter\n")
	b.WriteString("razor_bytes_read_total ")
	b.WriteString(strconv.FormatInt(m.bytesRead.Load(), 10))
	b.WriteString("\n")

	b.WriteString("# HELP razor_bytes_written_total Total bytes written to storage.\n")
	b.WriteString("# TYPE razor_bytes_written_total counter\n")
	b.WriteString("razor_bytes_written_total ")
	b.WriteString(strconv.FormatInt(m.bytesWritten.Load(), 10))
	b.WriteString("\n")
	return b.String()
}

// MetricHook returns the singleton metricHook (REQ000101).
func MetricHook() interface {
	PrometheusMetrics() string
	QueryCount() int64
	RowsReturned() int64
	BytesRead() int64
	BytesWritten() int64
} {
	if h := GetMetricHook(); h != nil {
		return h
	}
	return &noopMetric{}
}

type noopMetric struct{}

func (n *noopMetric) PrometheusMetrics() string { return "" }
func (n *noopMetric) QueryCount() int64         { return 0 }
func (n *noopMetric) RowsReturned() int64       { return 0 }
func (n *noopMetric) BytesRead() int64          { return 0 }
func (n *noopMetric) BytesWritten() int64       { return 0 }

var globalMetricHook *metricHook

// SetMetricHook sets the global metricHook (REQ000101).
func SetMetricHook(h *metricHook) { globalMetricHook = h }

func GetMetricHook() *metricHook { return globalMetricHook }
