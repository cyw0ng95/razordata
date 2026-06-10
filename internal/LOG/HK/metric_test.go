package hk

import (
	"log/slog"
	"testing"
)

// TestMetricHook_QueryCount verifies query counting (REQ000193).
func TestMetricHook_QueryCount(t *testing.T) {
	m := &metricHook{}
	
	// Initial count should be 0
	if got := m.QueryCount(); got != 0 {
		t.Errorf("QueryCount: got %d, want 0", got)
	}
	
	// Simulate query events
	m.OnLog(slog.LevelInfo, "SQL query", nil)
	m.OnLog(slog.LevelInfo, "SQL query", nil)
	m.OnLog(slog.LevelInfo, "SQL query", nil)
	
	if got := m.QueryCount(); got != 3 {
		t.Errorf("QueryCount: got %d, want 3", got)
	}
}

// TestMetricHook_RowsReturned verifies rows counting (REQ000193).
func TestMetricHook_RowsReturned(t *testing.T) {
	m := &metricHook{}
	
	// No rows initially
	if got := m.RowsReturned(); got != 0 {
		t.Errorf("RowsReturned: got %d, want 0", got)
	}
	
	// Simulate rows returned events
	m.OnLog(slog.LevelInfo, "rows returned", []any{"rows", int64(10)})
	m.OnLog(slog.LevelInfo, "rows returned", []any{"rows", int64(20)})
	m.OnLog(slog.LevelInfo, "rows returned", []any{"rows", int(5)})
	
	if got := m.RowsReturned(); got != 35 {
		t.Errorf("RowsReturned: got %d, want 35", got)
	}
}

// TestMetricHook_BytesRead verifies bytes read counting (REQ000193).
func TestMetricHook_BytesRead(t *testing.T) {
	m := &metricHook{}
	
	m.OnLog(slog.LevelInfo, "bytes read", []any{"bytes", int64(1024)})
	m.OnLog(slog.LevelInfo, "bytes read", []any{"bytes", int64(2048)})
	
	if got := m.BytesRead(); got != 3072 {
		t.Errorf("BytesRead: got %d, want 3072", got)
	}
}

// TestMetricHook_BytesWritten verifies bytes written counting (REQ000193).
func TestMetricHook_BytesWritten(t *testing.T) {
	m := &metricHook{}
	
	m.OnLog(slog.LevelInfo, "bytes written", []any{"bytes", int64(512)})
	m.OnLog(slog.LevelInfo, "bytes written", []any{"bytes", int64(256)})
	
	if got := m.BytesWritten(); got != 768 {
		t.Errorf("BytesWritten: got %d, want 768", got)
	}
}

// TestMetricHook_UnknownEvent verifies unknown events don't affect counters.
func TestMetricHook_UnknownEvent(t *testing.T) {
	m := &metricHook{}
	
	m.OnLog(slog.LevelInfo, "unknown event", []any{"key", "value"})
	m.OnLog(slog.LevelWarn, "warning", nil)
	
	if got := m.QueryCount(); got != 0 {
		t.Errorf("QueryCount should be 0 for unknown events, got %d", got)
	}
	if got := m.RowsReturned(); got != 0 {
		t.Errorf("RowsReturned should be 0 for unknown events, got %d", got)
	}
}

// TestMetricHook_MissingArgs verifies missing args returns 0.
func TestMetricHook_MissingArgs(t *testing.T) {
	m := &metricHook{}
	
	// Missing the "rows" key
	m.OnLog(slog.LevelInfo, "rows returned", []any{"other", int64(100)})
	
	if got := m.RowsReturned(); got != 0 {
		t.Errorf("RowsReturned with missing key: got %d, want 0", got)
	}
}

// TestMetricHook_WrongType verifies wrong type returns 0.
func TestMetricHook_WrongType(t *testing.T) {
	m := &metricHook{}
	
	// String value instead of int
	m.OnLog(slog.LevelInfo, "rows returned", []any{"rows", "not a number"})
	
	if got := m.RowsReturned(); got != 0 {
		t.Errorf("RowsReturned with wrong type: got %d, want 0", got)
	}
}

// TestMetricHook_AllCounters verifies all counters work together.
func TestMetricHook_AllCounters(t *testing.T) {
	m := &metricHook{}
	
	// Simulate a query with results
	m.OnLog(slog.LevelInfo, "SQL query", nil)
	m.OnLog(slog.LevelInfo, "rows returned", []any{"rows", int64(100)})
	m.OnLog(slog.LevelInfo, "bytes read", []any{"bytes", int64(4096)})
	m.OnLog(slog.LevelInfo, "bytes written", []any{"bytes", int64(2048)})
	
	if got := m.QueryCount(); got != 1 {
		t.Errorf("QueryCount: got %d, want 1", got)
	}
	if got := m.RowsReturned(); got != 100 {
		t.Errorf("RowsReturned: got %d, want 100", got)
	}
	if got := m.BytesRead(); got != 4096 {
		t.Errorf("BytesRead: got %d, want 4096", got)
	}
	if got := m.BytesWritten(); got != 2048 {
		t.Errorf("BytesWritten: got %d, want 2048", got)
	}
}

// TestExtractInt64 verifies the helper function.
func TestExtractInt64(t *testing.T) {
	tests := []struct {
		name	string
		args	[]any
		key	string
		want	int64
	}{
		{"int64", []any{"rows", int64(42)}, "rows", 42},
		{"int", []any{"rows", int(42)}, "rows", 42},
		{"int32", []any{"rows", int32(42)}, "rows", 42},
		{"missing key", []any{"other", int64(42)}, "rows", 0},
		{"wrong type", []any{"rows", "string"}, "rows", 0},
		{"odd args", []any{"rows"}, "rows", 0},
		{"empty", []any{}, "rows", 0},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractInt64(tt.args, tt.key); got != tt.want {
				t.Errorf("extractInt64: got %d, want %d", got, tt.want)
			}
		})
	}
}
