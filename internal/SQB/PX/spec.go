package PX

import (
	"fmt"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// StageSpec is a factory for creating Stage instances. It holds
// configuration (expressions, schemas, column indices) but NO
// runtime state (no iterators, no hash tables, no open cursors).
// This makes PipelineSpec safe to cache and share across concurrent
// executions. Each call to NewRuntime() produces a fresh Stage
// with independent runtime state.
type StageSpec interface {
	// NewRuntime creates a fresh Stage from this specification.
	// Called on each execution; must be fast (~5µs target).
	NewRuntime() Stage
	// Category returns the stage's execution category.
	Category() StageCategory
}

// EdgeSpec describes a parent→child connection between stages
// in the pipeline DAG. The From and To fields are indices into
// the PipelineSpec.Stages slice.
type EdgeSpec struct {
	From int       // parent stage index
	To   int       // child stage index
	Side ChildSide // which child slot (SingleChild, LeftChild, RightChild)
}

// PipelineSpec is a cacheable, serializable pipeline description.
// It contains NO runtime state and is safe to share across
// concurrent executions. Use NewRuntime() to create a live Pipeline.
type PipelineSpec struct {
	Stages      []StageSpec    // ordered stage factory objects
	Edges       []EdgeSpec     // parent→child wiring
	RootIdx     int            // index of root/output stage
	OutputCols  []string       // output column names
	OutputTypes []LX.TokenType // output column types
	Cost        float64        // estimated execution cost
	MemoKey     string         // parameterized cache key
	SQLText     string         // original SQL for exact-text cache key
}

// NewRuntime creates a live Pipeline from this spec. All stages
// are instantiated via StageSpec.NewRuntime() and wired together
// per the edge list. The resulting Pipeline is ready for Execute().
func (s *PipelineSpec) NewRuntime() (*Pipeline, error) {
	if len(s.Stages) == 0 {
		return nil, fmt.Errorf("px: PipelineSpec has no stages")
	}
	if s.RootIdx < 0 || s.RootIdx >= len(s.Stages) {
		return nil, fmt.Errorf("px: invalid RootIdx %d (stages: %d)", s.RootIdx, len(s.Stages))
	}

	// Create all stage instances
	stages := make([]Stage, len(s.Stages))
	for i, spec := range s.Stages {
		stages[i] = spec.NewRuntime()
	}

	// Wire children per edge list
	for _, edge := range s.Edges {
		if edge.From < 0 || edge.From >= len(stages) {
			return nil, fmt.Errorf("px: edge From %d out of range", edge.From)
		}
		if edge.To < 0 || edge.To >= len(stages) {
			return nil, fmt.Errorf("px: edge To %d out of range", edge.To)
		}
		parent, ok := stages[edge.From].(ChildSetter)
		if !ok {
			return nil, fmt.Errorf("px: stage %d (%T) does not implement ChildSetter", edge.From, stages[edge.From])
		}
		parent.SetChild(edge.Side, stages[edge.To])
	}

	return &Pipeline{
		spec:   s,
		stages: stages,
		root:   stages[s.RootIdx],
	}, nil
}
