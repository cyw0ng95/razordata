package hk

import (
	"log/slog"
	"strings"
	"testing"
)

// TestPrometheusMetrics verifies the metricHook exports counters
// in Prometheus text format.
// REQ000101.
func TestPrometheusMetrics(t *testing.T) {
	m := &metricHook{}
	// Simulate some events
	m.OnLog(slog.LevelInfo, "SQL query", nil)
	m.OnLog(slog.LevelInfo, "SQL query", nil)
	m.OnLog(slog.LevelInfo, "rows returned", []any{"rows", int64(100)})
	m.OnLog(slog.LevelInfo, "bytes read", []any{"bytes", int64(2048)})
	m.OnLog(slog.LevelInfo, "bytes written", []any{"bytes", int64(4096)})

	out := m.PrometheusMetrics()
	if !strings.Contains(out, "razor_queries_total 2") {
		t.Errorf("queries_total: got %q, want razor_queries_total 2", out)
	}
	if !strings.Contains(out, "razor_rows_returned_total 100") {
		t.Errorf("rows_returned_total: got %q", out)
	}
	if !strings.Contains(out, "razor_bytes_read_total 2048") {
		t.Errorf("bytes_read_total: got %q", out)
	}
	if !strings.Contains(out, "razor_bytes_written_total 4096") {
		t.Errorf("bytes_written_total: got %q", out)
	}
	// Verify all required HELP and TYPE lines
	for _, exp := range []string{
		"# HELP razor_queries_total",
		"# TYPE razor_queries_total counter",
		"# HELP razor_rows_returned_total",
		"# TYPE razor_rows_returned_total counter",
	} {
		if !strings.Contains(out, exp) {
			t.Errorf("missing %q in output", exp)
		}
	}
}

func TestPrometheusMetrics_Noop(t *testing.T) {
	m := MetricHook()
	if m.PrometheusMetrics() != "" {
		t.Errorf("expected empty string from noop, got %q", m.PrometheusMetrics())
	}
}
