//go:build debug

package dc

import (
	"log/slog"
	"testing"
)

func TestControl_LogLevel(t *testing.T) {
	c := NewControl()
	c.SetLogLevel("ENG", slog.LevelDebug)
	if c.GetLogLevel("ENG") != slog.LevelDebug {
		t.Errorf("expected Debug, got %v", c.GetLogLevel("ENG"))
	}
	c.SetLogLevel("ENG", slog.LevelWarn)
	if c.GetLogLevel("ENG") != slog.LevelWarn {
		t.Errorf("expected Warn, got %v", c.GetLogLevel("ENG"))
	}
}

func TestControl_TraceClass(t *testing.T) {
	c := NewControl()
	if c.IsTraceEnabled("sql") {
		t.Error("expected trace disabled by default")
	}
	c.EnableTrace("sql", true)
	if !c.IsTraceEnabled("sql") {
		t.Error("expected trace enabled")
	}
	c.EnableTrace("sql", false)
	if c.IsTraceEnabled("sql") {
		t.Error("expected trace disabled")
	}
}

func TestControl_SlowThreshold(t *testing.T) {
	c := NewControl()
	if c.GetSlowThreshold() != 0 {
		t.Error("expected 0 default")
	}
	c.SetSlowThreshold(1_000_000)
	if c.GetSlowThreshold() != 1_000_000 {
		t.Errorf("expected 1ms, got %d", c.GetSlowThreshold())
	}
}
