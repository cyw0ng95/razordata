// Package PX (Pipeline eXecution) provides the unified execution model
// for the entire SQL pipeline. It replaces the current scattered compile
// flow (Parser → Rewriter → Planner → ResolvePlanSlots → tryVectorizePlan)
// with a single PipelineBuilder.Build() method that produces a cacheable
// PipelineSpec. The PipelineSpec has NO runtime state; it describes the
// pipeline as a DAG of StageSpec factories. PipelineSpec.NewRuntime()
// creates a live Pipeline of Stages for execution.
package PX

import (
	"context"
	"errors"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// ErrResetNotSupported is returned by Stage.Reset() when the stage
// cannot reset its internal state and must be recreated from its
// StageSpec. The Pipeline handles this by calling spec.NewRuntime()
// for the affected stage.
var ErrResetNotSupported = errors.New("px: reset not supported; recreate from spec")

// Stage is the sole operator type in the pipeline execution model.
// Every operator (scan, filter, join, aggregate, sort, distinct)
// implements this interface. Categories (Source, Transform,
// MapReduce, Join) are distinguished by behavior, not by separate
// interfaces — a Stage simply declares its category via the
// StageSpec that created it.
//
// Lifecycle:
//   - NextBatch: called repeatedly until (nil, nil) EOF
//   - Reset:     return to pre-execution state (preserves buffers)
//   - Close:     release all resources
type Stage interface {
	// NextBatch produces the next columnar batch.
	// Returns (nil, nil) at EOF. The caller takes ownership
	// of the returned batch and must call batch.Put() when done.
	NextBatch(ctx context.Context) (*UT.Batch, error)
	// Reset returns the stage to pre-execution state, preserving
	// allocated buffers (hash tables, scratch space) for plan
	// cache reuse. Returns ErrResetNotSupported if the stage
	// must be recreated from its StageSpec instead.
	Reset(ctx context.Context) error
	// Close releases all resources held by the stage.
	Close() error
}

// StageCategory classifies a stage's execution behavior.
type StageCategory uint8

const (
	// CatSource reads from a table or store (e.g., SeqScan, IndexScan).
	CatSource StageCategory = iota
	// CatTransform processes one batch from its child and produces
	// zero or one output batch (e.g., Filter, Project, Limit).
	CatTransform
	// CatMapReduce drains all child batches through a mapper/shuffler/
	// reducer pipeline, then produces output batches (e.g., Aggregate,
	// Sort, Distinct).
	CatMapReduce
	// CatJoin has two inputs (build + probe) and produces one output
	// (e.g., HashJoin, MergeJoin, CrossJoin).
	CatJoin
)

// String returns a human-readable name for the category.
func (c StageCategory) String() string {
	switch c {
	case CatSource:
		return "source"
	case CatTransform:
		return "transform"
	case CatMapReduce:
		return "mapreduce"
	case CatJoin:
		return "join"
	default:
		return "unknown"
	}
}

// ChildSide identifies which child slot a stage accepts.
type ChildSide uint8

const (
	// SingleChild indicates the stage accepts one child (Source/Transform/MapReduce).
	SingleChild ChildSide = iota
	// LeftChild indicates the left input of a Join stage.
	LeftChild
	// RightChild indicates the right input of a Join stage.
	RightChild
)

// ChildSetter is an optional interface that Stages implement to
// receive child stages during Pipeline wiring. Transform and
// MapReduce stages accept SingleChild; Join stages accept
// LeftChild and RightChild. Source stages do not implement
// ChildSetter.
type ChildSetter interface {
	SetChild(side ChildSide, child Stage)
}

// ParamPropagator is an optional interface that Stages implement
// to receive parameter values for ? placeholders. This is used
// after PipelineSpec.NewRuntime() to inject per-execution
// parameter values without modifying the cached spec.
type ParamPropagator interface {
	PropagateParams(args []any, buf *[]any)
}
