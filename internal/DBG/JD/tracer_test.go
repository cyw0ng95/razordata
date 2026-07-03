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
