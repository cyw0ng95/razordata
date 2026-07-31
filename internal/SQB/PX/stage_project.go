package PX

import (
	"context"

	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// batchEvalFunc is a compiled fast-path evaluator for simple expressions.
// Returns a Column of results for all logical rows in the batch.
type batchEvalFunc func(batch *UT.Batch) UT.Column

// ProjectStageSpec creates ProjectStage instances. A ProjectStage
// evaluates projection expressions over each batch from its child,
// producing an output batch with the computed columns.
type ProjectStageSpec struct {
	Exprs         []PS.Expr       // projection expressions
	Names         []string        // output column names
	CompiledEvals []batchEvalFunc // compiled fast-path evaluators (nil = fallback to EvalBatchExpr)
}

// NewRuntime creates a ProjectStage from this spec.
func (s *ProjectStageSpec) NewRuntime() Stage {
	return &ProjectStage{
		exprs:         s.Exprs,
		names:         s.Names,
		compiledEvals: s.CompiledEvals,
	}
}

// Category returns CatTransform.
func (s *ProjectStageSpec) Category() StageCategory { return CatTransform }

// ProjectStage is a Transform-stage that evaluates projection
// expressions over each child batch, producing an output batch with
// the computed columns. It mirrors VectorizedProject but implements
// the Stage interface with Reset support.
type ProjectStage struct {
	child         Stage
	exprs         []PS.Expr
	names         []string
	compiledEvals []batchEvalFunc
	execCtx       *DT.ExecContext
	done          bool
}

// SetChild sets the child stage (implements ChildSetter).
func (p *ProjectStage) SetChild(_ ChildSide, child Stage) {
	p.child = child
}

// NextBatch pulls a batch from the child, evaluates projection
// expressions, and returns a new batch with the computed columns.
// The child batch is always returned to the pool. Returns (nil, nil)
// at EOF.
func (p *ProjectStage) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.done {
		return nil, nil
	}
	childBatch, err := p.child.NextBatch(ctx)
	if err != nil {
		return nil, err
	}
	if childBatch == nil {
		p.done = true
		return nil, nil
	}
	defer childBatch.Put()

	if p.execCtx != nil {
		childBatch.ExecCtx = p.execCtx
	}

	output := UT.GetBatch(len(p.exprs))
	output.Size = childBatch.LogicalSize()

	for i, expr := range p.exprs {
		var col UT.Column
		if i < len(p.compiledEvals) && p.compiledEvals[i] != nil {
			col = p.compiledEvals[i](childBatch)
		} else {
			col = EV.EvalBatchExpr(expr, childBatch, nil)
		}
		col.Name = p.names[i]

		// If the child batch has a selection vector, the shallow-copied
		// column still contains unselected physical rows. Compact so the
		// output is fully materialized. REQ002220.
		if childBatch.Sel != nil && childBatch.Size > 0 {
			physicalSize := 0
			for j := 0; j < childBatch.Size && j < len(childBatch.Sel); j++ {
				if int(childBatch.Sel[j])+1 > physicalSize {
					physicalSize = int(childBatch.Sel[j]) + 1
				}
			}
			col = UT.CompactColumn(col, childBatch.Sel[:childBatch.Size], physicalSize)
		}
		output.Cols[i] = col
	}

	return output, nil
}

// PropagateExecContext stores per-execution context for subquery
// evaluation. REQ002148.
func (p *ProjectStage) PropagateExecContext(ec *DT.ExecContext) {
	p.execCtx = ec
}

// Reset resets the project stage to pre-execution state.
func (p *ProjectStage) Reset(_ context.Context) error {
	p.done = false
	return nil
}

// Close releases the child stage.
func (p *ProjectStage) Close() error {
	if p.child != nil {
		return p.child.Close()
	}
	return nil
}

