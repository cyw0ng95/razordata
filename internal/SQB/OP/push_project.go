package OP

import (
	"context"

	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// PushProject is the push-based equivalent of VectorizedProject.
// It receives a batch, allocates one output batch, evaluates each
// projection expression into a column, and forwards the result.
// REQ002002.
//
// Project behavior matches VectorizedProject:
//   - Allocates one *UT.Batch with len(exprs) columns per input batch.
//   - Uses compileProjectExpr fast-path evaluators when available,
//     falling back to EV.EvalBatchExpr for complex expressions.
//   - Compacts Sel when the input batch has a selection vector.
type PushProject struct {
	basePushOp
	exprs         []PS.Expr
	names         []string
	compiledEvals []batchEvalFunc
	execCtx       interface{} // placeholder; subquery support deferred
}

// NewPushProject constructs a PushProject. Compiles fast-path
// evaluators on construction (mirrors NewVectorizedProject).
func NewPushProject(exprs []PS.Expr, names []string) *PushProject {
	p := &PushProject{
		exprs: exprs,
		names: names,
	}
	p.compiledEvals = make([]batchEvalFunc, len(exprs))
	for i, expr := range exprs {
		p.compiledEvals[i] = compileProjectExpr(expr)
	}
	return p
}

// PushBatch projects the input batch into a new output batch and
// forwards it. Takes ownership of the input batch (Put after eval).
//
// When the input batch has a selection vector (Filter upstream),
// each output column is compacted to only the selected logical
// rows so the output batch is densely packed (no Sel needed).
func (p *PushProject) PushBatch(ctx context.Context, batch *UT.Batch) error {
	defer batch.Put()

	n := batch.LogicalSize()
	output := UT.GetBatch(len(p.exprs))
	output.Size = n
	for i, expr := range p.exprs {
		var col UT.Column
		if i < len(p.compiledEvals) && p.compiledEvals[i] != nil {
			col = p.compiledEvals[i](batch)
		} else {
			col = EV.EvalBatchExpr(expr, batch, nil)
		}
		col.Name = p.names[i]
		// REQ002002: when the upstream Filter set a selection vector,
		// the column returned by ExtractColumnRef / EvalBatchExpr has
		// the FULL physical row count — we must compact to the
		// selected logical rows so the output batch is densely packed.
		if batch.Sel != nil {
			col = compactColumn(col, batch.Sel, batch.Size)
		}
		output.Cols[i] = col
	}
	return p.sink.PushBatch(ctx, output)
}

// Run pulls batches from the source and pushes them through PushBatch.
func (p *PushProject) Run(ctx context.Context) error {
	return p.runWith(ctx, p.PushBatch)
}

// Close releases the child operator.
func (p *PushProject) Close() error {
	if p.child != nil {
		return p.child.Close()
	}
	return nil
}
