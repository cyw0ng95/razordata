package PX

import (
	"context"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
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

		// If the child batch has a selection vector and the column
		// contains more data than the logical size, compact it.
		if childBatch.Sel != nil && childBatch.Size < childBatch.LogicalSize() {
			if childBatch.Size > 0 {
				physicalSize := 0
				for j := 0; j < childBatch.Size && j < len(childBatch.Sel); j++ {
					if int(childBatch.Sel[j])+1 > physicalSize {
						physicalSize = int(childBatch.Sel[j]) + 1
					}
				}
				col = compactColumn(col, childBatch.Sel[:childBatch.Size], physicalSize)
			}
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

// compactColumn creates a new Column containing only the rows
// identified by sel from the source column. Used when the child
// batch has a selection vector and the output must contain only
// the logically valid rows.
func compactColumn(col UT.Column, sel []uint16, physicalSize int) UT.Column {
	n := len(sel)
	if n == 0 {
		return col
	}
	out := UT.Column{Name: col.Name, Type: col.Type}
	switch col.Type {
	case LX.T_INT_KW, LX.T_BIGINT:
		out.Data.Ints = make([]int64, n)
		for j, idx := range sel {
			if int(idx) < len(col.Data.Ints) {
				out.Data.Ints[j] = col.Data.Ints[idx]
			}
		}
	case LX.T_FLOAT_KW:
		out.Data.Floats = make([]float64, n)
		for j, idx := range sel {
			if int(idx) < len(col.Data.Floats) {
				out.Data.Floats[j] = col.Data.Floats[idx]
			}
		}
	case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
		out.Data.Strs = make([]string, n)
		for j, idx := range sel {
			if int(idx) < len(col.Data.Strs) {
				out.Data.Strs[j] = col.Data.Strs[idx]
			}
		}
	case LX.T_BOOL:
		out.Data.Bools = make([]bool, n)
		for j, idx := range sel {
			if int(idx) < len(col.Data.Bools) {
				out.Data.Bools[j] = col.Data.Bools[idx]
			}
		}
	default:
		return col
	}
	if col.Nulls != nil {
		out.Nulls = make([]bool, n)
		for j, idx := range sel {
			if int(idx) < physicalSize && int(idx) < len(col.Nulls) {
				out.Nulls[j] = col.Nulls[idx]
			}
		}
	}
	return out
}
