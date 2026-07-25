package OP

import (
	"context"

	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// PushFilter is the push-based equivalent of VectorizedFilter.
// It receives a batch, applies the predicate via EV.EvalBatch to
// derive a selection vector, and forwards the filtered batch to
// its sink. REQ002002.
//
// Filter behavior matches VectorizedFilter:
//   - sel == nil  → all rows match; forward batch unchanged.
//   - len(sel)==0 → no rows match; drop batch (Put) and continue.
//   - otherwise   → set batch.Sel and batch.Size, forward.
type PushFilter struct {
	basePushOp
	pred   PS.Expr
	params []any
}

// NewPushFilter constructs a PushFilter with the given predicate.
// The source and sink must be set via SetSource/SetSink before Run.
func NewPushFilter(pred PS.Expr) *PushFilter {
	return &PushFilter{pred: pred}
}

// WithParams propagates bound ? placeholders to the predicate
// evaluation. Returns the receiver for chaining.
func (f *PushFilter) WithParams(p []any) *PushFilter {
	f.params = p
	return f
}

// PushBatch applies the filter predicate to the batch and forwards
// the result to the sink. Takes ownership of batch.
func (f *PushFilter) PushBatch(ctx context.Context, batch *UT.Batch) error {
	sel := EV.EvalBatch(f.pred, batch, f.params)
	if sel == nil {
		// All rows match — forward as-is.
		return f.sink.PushBatch(ctx, batch)
	}
	if len(sel) == 0 {
		// No rows match — drop and continue.
		batch.Put()
		return nil
	}
	batch.Sel = sel
	batch.Size = len(sel)
	return f.sink.PushBatch(ctx, batch)
}

// Run pulls batches from the source and pushes them through PushBatch.
func (f *PushFilter) Run(ctx context.Context) error {
	return f.runWith(ctx, f.PushBatch)
}

// Close releases the child operator. The sink is owned by the
// pipeline and closed separately.
func (f *PushFilter) Close() error {
	if f.child != nil {
		return f.child.Close()
	}
	return nil
}
