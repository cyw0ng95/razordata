//go:build debug

package JD

import "testing"

func TestNoOpTracer(t *testing.T) {
	n := noopTracer{}
	n.RowFlow("Join", "t1", 1, true)
	n.Predicate("Join", "a=b", 1, 2, true)
	n.ColumnOffset("Join", 0, 1, "c1")
	n.Strategy("Join", "nested-loop", "low cardinality", 1.5)
	n.Correlation(0, []string{"t1", "t2"}, 100)
}

func TestGlobalTracer(t *testing.T) {
	orig := GetJoinTracer()
	defer SetJoinTracer(orig)

	SetJoinTracer(nil)
	if GetJoinTracer() != nil {
		t.Fatal("expected nil tracer after SetJoinTracer(nil)")
	}

	SetJoinTracer(noopTracer{})
	if GetJoinTracer() == nil {
		t.Fatal("expected non-nil tracer after SetJoinTracer(noopTracer{})")
	}
}

func TestVerbosity(t *testing.T) {
	orig := GetVerbosity()
	defer SetVerbosity(orig)

	SetVerbosity(LevelFull)
	if got := GetVerbosity(); got != LevelFull {
		t.Fatalf("expected verbosity %d, got %d", LevelFull, got)
	}

	SetVerbosity(LevelOff)
	if got := GetVerbosity(); got != LevelOff {
		t.Fatalf("expected verbosity %d, got %d", LevelOff, got)
	}
}

func TestIsEnabled(t *testing.T) {
	origTracer := GetJoinTracer()
	origVerb := GetVerbosity()
	defer func() {
		SetJoinTracer(origTracer)
		SetVerbosity(origVerb)
	}()

	// No tracer set → disabled
	SetJoinTracer(nil)
	SetVerbosity(LevelDetailed)
	if IsEnabled() {
		t.Fatal("should be disabled when tracer is nil")
	}

	// Tracer set but verbosity off → disabled
	SetJoinTracer(noopTracer{})
	SetVerbosity(LevelOff)
	if IsEnabled() {
		t.Fatal("should be disabled when verbosity is 0")
	}

	// Both set → enabled
	SetVerbosity(LevelSummary)
	if !IsEnabled() {
		t.Fatal("should be enabled when tracer is set and verbosity > 0")
	}
}

func TestBufferedTracer_RowFlow(t *testing.T) {
	tr := NewBufferedTracer(64, LevelSummary)
	tr.RowFlow("HashJoin", "t1", 1, true)
	tr.RowFlow("HashJoin", "t1", 1, false)
	events := tr.Flush()
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Type != EventRowFlow || !events[0].Entering {
		t.Errorf("unexpected first event: %+v", events[0])
	}
	if events[1].Entering {
		t.Errorf("expected leaving event")
	}
}

func TestBufferedTracer_Predicate(t *testing.T) {
	tr := NewBufferedTracer(64, LevelDetailed)
	tr.Predicate("HashJoin", "t1.id = t2.id", 1, 2, true)
	tr.Predicate("HashJoin", "t1.val > 10", 1, 3, false)
	events := tr.Flush()
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if !events[0].Passed {
		t.Error("expected first predicate to pass")
	}
	if events[1].Passed {
		t.Error("expected second predicate to fail")
	}
}

func TestBufferedTracer_VerbosityFiltering(t *testing.T) {
	tr := NewBufferedTracer(64, LevelOff)
	tr.RowFlow("HashJoin", "t1", 1, true)
	tr.Predicate("HashJoin", "expr", 1, 2, true)
	tr.Strategy("HashJoin", "hash", "reason", 100)
	tr.Correlation(1, []string{"t1"}, 10)
	events := tr.Flush()
	if len(events) != 0 {
		t.Fatalf("expected 0 events at LevelOff, got %d", len(events))
	}
}
