package PX

import (
	"context"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// AggregateStageSpec creates AggregateStage instances. An AggregateStage
// is a CatMapReduce stage that drains all child batches through a
// mapper→shuffler→reducer pipeline. It replaces VectorizedHashAggregate,
// Aggregate (scalar streaming), HashAggregate, and ParallelHashAggregate.
type AggregateStageSpec struct {
	Specs     []AccumulatorSpec // aggregate definitions
	GroupCols []int                // GROUP BY column indices; nil = scalar aggregate
	KeyCols   []int                // key extraction column indices for hash grouping
}

// NewRuntime creates an AggregateStage from this spec.
func (s *AggregateStageSpec) NewRuntime() Stage {
	reducer := NewAggregateReducer(s.Specs, s.GroupCols)

	var shuffler Shuffler
	if len(s.GroupCols) == 0 {
		shuffler = NewScalarShuffler(reducer, s.Specs)
	} else {
		extractor := NewSimpleIntKeyExtractor(s.KeyCols[0])
		shuffler = NewHashShuffler(reducer, s.Specs, extractor)
	}

	mapper := NewAggregateMapper(s.GroupCols)

	return &AggregateStage{
		specs:     s.Specs,
		groupCols: s.GroupCols,
		mapper:    mapper,
		shuffler:  shuffler,
	}
}

// Category returns CatMapReduce.
func (s *AggregateStageSpec) Category() StageCategory { return CatMapReduce }

// AggregateStage is a MapReduce-stage that performs grouped or scalar
// aggregation using the MR core (Mapper→Shuffler→Reducer).
//
// Execution model:
//   - First NextBatch() call: drains all child batches through
//     mapper.MapBatch→shuffler.AcceptBatch, then calls
//     shuffler.Finalize to produce result batches.
//   - Subsequent calls: returns cached result batches.
//   - Reset: clears cached results and resets shuffler+mapper,
//     preserving allocated buffers (hash table capacity, scratch space).
type AggregateStage struct {
	child     Stage
	specs     []AccumulatorSpec
	groupCols []int
	mapper    Mapper
	shuffler  Shuffler
	result    []*UT.Batch
	pos       int
	drained   bool
}

// SetChild sets the child stage (implements ChildSetter).
func (a *AggregateStage) SetChild(_ ChildSide, child Stage) {
	a.child = child
}

// PropagateExecContext stores the per-execution context for use in
// expression evaluation within aggregates. REQ002148.
func (a *AggregateStage) PropagateExecContext(ec *DT.ExecContext) {
	// Note: AggregateStage itself may not use execCtx directly, but
	// its underlying MR components might during expression evaluation.
	// This method exists to satisfy the ExecContextPropagator interface.
	_ = ec // placeholder; actual usage may be in child or expressions
}

// NextBatch performs the aggregation and returns result batches.
// On first call, drains the child through the MR pipeline.
// Returns (nil, nil) at EOF.
func (a *AggregateStage) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !a.drained {
		if err := a.drain(ctx); err != nil {
			return nil, err
		}
	}

	if a.pos >= len(a.result) {
		return nil, nil
	}

	batch := a.result[a.pos]
	a.pos++
	return batch, nil
}

// drain reads all child batches through the mapper-shuffler pipeline,
// then calls Finalize to produce result batches.
func (a *AggregateStage) drain(ctx context.Context) error {
	a.drained = true

	for {
		batch, err := a.child.NextBatch(ctx)
		if err != nil {
			return err
		}
		if batch == nil {
			break
		}
		if err := a.mapper.MapBatch(ctx, batch, a.shuffler); err != nil {
			return err
		}
		batch.Put()
	}

	result, err := a.shuffler.Finalize(ctx)
	if err != nil {
		return err
	}
	a.result = result
	return nil
}

// Reset clears the aggregation state for plan cache reuse.
// Preserves allocated hash table capacity and scratch buffers.
func (a *AggregateStage) Reset(_ context.Context) error {
	a.result = nil
	a.pos = 0
	a.drained = false
	a.shuffler.Reset()
	a.mapper.Reset()
	return nil
}

// Close releases all resources held by the aggregate stage.
func (a *AggregateStage) Close() error {
	a.result = nil
	if a.shuffler != nil {
		a.shuffler.Reset()
	}
	if a.mapper != nil {
		a.mapper.Reset()
	}
	if a.child != nil {
		return a.child.Close()
	}
	return nil
}
