//go:build !slt_corpus_full

package EX

import (
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// REQ001444: PRAGMA vectorized_mode sets the planner's BatchSize knob.
// off → 1, auto → 0 (use heuristic), on → 1024, <int> → explicit.
func TestPragma_VectorizedMode_Off(t *testing.T) {
	p := NewPlanner()
	stmt := &PS.PragmaStmt{Name: "vectorized_mode", Value: "off"}
	_ = p.planPragma(stmt)
	if p.BatchSize() != 1 {
		t.Errorf("off: got %d, want 1", p.BatchSize())
	}
}

func TestPragma_VectorizedMode_Auto(t *testing.T) {
	p := NewPlanner()
	stmt := &PS.PragmaStmt{Name: "vectorized_mode", Value: "auto"}
	_ = p.planPragma(stmt)
	if p.BatchSize() != 0 {
		t.Errorf("auto: got %d, want 0", p.BatchSize())
	}
}

func TestPragma_VectorizedMode_On(t *testing.T) {
	p := NewPlanner()
	stmt := &PS.PragmaStmt{Name: "vectorized_mode", Value: "on"}
	_ = p.planPragma(stmt)
	if p.BatchSize() != 1024 {
		t.Errorf("on: got %d, want 1024", p.BatchSize())
	}
}

func TestPragma_VectorizedMode_Explicit256(t *testing.T) {
	p := NewPlanner()
	stmt := &PS.PragmaStmt{Name: "vectorized_mode", Value: "256"}
	_ = p.planPragma(stmt)
	if p.BatchSize() != 256 {
		t.Errorf("256: got %d, want 256", p.BatchSize())
	}
}

func TestPragma_VectorizedMode_Default(t *testing.T) {
	p := NewPlanner()
	// No PRAGMA issued — default should be 0 (auto).
	stmt := &PS.PragmaStmt{Name: "vectorized_mode"}
	_ = p.planPragma(stmt)
	if p.BatchSize() != 0 {
		t.Errorf("default: got %d, want 0", p.BatchSize())
	}
}
