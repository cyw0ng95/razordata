package PX

import (
	"context"
	"sync"

	AG "github.com/cyw0ng95/razordata/internal/SQB/AG"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// WindowStageSpec creates WindowStage instances for window function
// execution. It wraps the existing VectorizedWindowOperator.
type WindowStageSpec struct {
	Spec     *PS.WindowSpec
	FuncName string
	Args     []PS.Expr
	Cols     []string
}

// NewRuntime creates a WindowStage from this spec.
func (s *WindowStageSpec) NewRuntime() Stage {
	return &WindowStage{
		spec:     s.Spec,
		funcName: s.FuncName,
		args:     s.Args,
		cols:     s.Cols,
	}
}

// Category returns CatMapReduce — window functions need to materialize
// all input rows, partition, sort, and compute.
func (s *WindowStageSpec) Category() StageCategory { return CatMapReduce }

// WindowStage wraps VectorizedWindowOperator as a Stage.
type WindowStage struct {
	child    Stage
	spec     *PS.WindowSpec
	funcName string
	args     []PS.Expr
	cols     []string

	mu     sync.Mutex
	closed bool
	inner  *AG.VectorizedWindowOperator
	childBP *stageBatchProducer
}

// SetChild sets the child stage.
func (w *WindowStage) SetChild(_ ChildSide, child Stage) {
	w.child = child
}

// PropagateParams forwards parameters to the inner operator.
func (w *WindowStage) PropagateParams(args []any, buf *[]any) {}

// NextBatch returns the next batch from the window function result.
func (w *WindowStage) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil, nil
	}
	if w.inner == nil {
		w.childBP = &stageBatchProducer{stage: w.child}
		w.inner = AG.NewVectorizedWindowOperator(w.childBP, w.funcName, w.args, w.spec, w.cols)
	}
	return w.inner.NextBatch(ctx)
}

// Reset returns to pre-execution state.
func (w *WindowStage) Reset(_ context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.inner = nil
	w.childBP = nil
	return nil
}

// Close releases all resources.
func (w *WindowStage) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	w.inner = nil
	w.childBP = nil
	return nil
}

// stageBatchProducer adapts a Stage as a UT.BatchProducer.
type stageBatchProducer struct {
	stage Stage
	done  bool
}

func (s *stageBatchProducer) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if s.done {
		return nil, nil
	}
	return s.stage.NextBatch(ctx)
}

func (s *stageBatchProducer) Close() error {
	s.done = true
	return nil
}