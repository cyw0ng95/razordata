//go:build debug

package cd

import (
	"testing"
)

func TestGlobalTracer(t *testing.T) {
	SetCTETracer(nil)
	if GetCTETracer() != nil {
		t.Error("expected nil tracer")
	}

	bt := NewBufferedTracer(4, LevelSummary)
	SetCTETracer(bt)
	if GetCTETracer() != bt {
		t.Error("tracer not set correctly")
	}
}

func TestVerbosity(t *testing.T) {
	SetVerbosity(LevelOff)
	if GetVerbosity() != LevelOff {
		t.Errorf("verbosity = %d, want %d", GetVerbosity(), LevelOff)
	}

	SetVerbosity(LevelDetailed)
	if GetVerbosity() != LevelDetailed {
		t.Errorf("verbosity = %d, want %d", GetVerbosity(), LevelDetailed)
	}
}

func TestIsEnabled(t *testing.T) {
	SetCTETracer(nil)
	SetVerbosity(LevelOff)
	if IsEnabled() {
		t.Error("expected disabled when no tracer and level=off")
	}

	bt := NewBufferedTracer(4, LevelSummary)
	SetCTETracer(bt)
	SetVerbosity(LevelOff)
	if IsEnabled() {
		t.Error("expected disabled when level=off")
	}

	SetVerbosity(LevelSummary)
	if !IsEnabled() {
		t.Error("expected enabled")
	}
}

func TestBufferedTracerSeed(t *testing.T) {
	bt := NewBufferedTracer(4, LevelSummary)
	bt.Seed("my_cte", 10)

	events := bt.Flush()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != EventSeed {
		t.Errorf("type = %v, want %v", events[0].Type, EventSeed)
	}
	if events[0].Name != "my_cte" {
		t.Errorf("name = %q, want %q", events[0].Name, "my_cte")
	}
	if events[0].NumRows != 10 {
		t.Errorf("numRows = %d, want 10", events[0].NumRows)
	}
}

func TestBufferedTracerIteration(t *testing.T) {
	bt := NewBufferedTracer(4, LevelSummary)
	bt.Iteration("my_cte", 5, 10, 3, []string{"a", "b"})

	events := bt.Flush()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != EventIteration {
		t.Errorf("type = %v, want %v", events[0].Type, EventIteration)
	}
	if events[0].Iter != 5 {
		t.Errorf("iter = %d, want 5", events[0].Iter)
	}
	if events[0].RowsIn != 10 {
		t.Errorf("rowsIn = %d, want 10", events[0].RowsIn)
	}
	if events[0].RowsOut != 3 {
		t.Errorf("rowsOut = %d, want 3", events[0].RowsOut)
	}
}

func TestBufferedTracerMaxIterations(t *testing.T) {
	bt := NewBufferedTracer(4, LevelSummary)
	bt.MaxIterationsReached("my_cte")

	events := bt.Flush()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != EventMaxIterations {
		t.Errorf("type = %v, want %v", events[0].Type, EventMaxIterations)
	}
	if events[0].Name != "my_cte" {
		t.Errorf("name = %q, want %q", events[0].Name, "my_cte")
	}
}

func TestBufferedTracerVerbosityFilter(t *testing.T) {
	bt := NewBufferedTracer(4, LevelOff)
	bt.Seed("my_cte", 10)
	bt.Iteration("my_cte", 1, 5, 3, nil)
	bt.MaxIterationsReached("my_cte")

	events := bt.Flush()
	if len(events) != 0 {
		t.Errorf("expected 0 events at LevelOff, got %d", len(events))
	}
}