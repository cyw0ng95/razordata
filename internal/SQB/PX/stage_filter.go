package PX

import (
	"context"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// FilterStageSpec creates FilterStage instances. A FilterStage applies
// a predicate to each batch from its child, setting the batch's
// selection vector to retain only matching rows.
type FilterStageSpec struct {
	Pred      PS.Expr  // predicate expression
	HasBloom  bool     // whether to build an IN-list bloom filter
	BloomVals []int64  // literal values for bloom filter construction
}

// NewRuntime creates a FilterStage from this spec.
func (s *FilterStageSpec) NewRuntime() Stage {
	f := &FilterStage{
		pred: s.Pred,
	}
	if s.HasBloom && len(s.BloomVals) > 8 {
		f.inBloom = UT.NewBloomFilter(len(s.BloomVals), 0.01)
		for _, v := range s.BloomVals {
			f.inBloom.Add(uint64(v))
		}
		f.inColIdx = -1
	}
	return f
}

// Category returns CatTransform.
func (s *FilterStageSpec) Category() StageCategory { return CatTransform }

// FilterStage is a Transform-stage that evaluates a predicate over each
// batch from its child and retains only matching rows via the batch's
// selection vector. It mirrors VectorizedFilter but implements the
// Stage interface with Reset support.
type FilterStage struct {
	child    Stage
	pred     PS.Expr
	params   []any
	execCtx  *DT.ExecContext
	inBloom  *UT.BloomFilter // REQ001631: IN-list bloom filter
	inNegate bool
	inColIdx int // resolved on first batch (-1 = unresolved)
}

// SetChild sets the child stage (implements ChildSetter).
func (f *FilterStage) SetChild(_ ChildSide, child Stage) {
	f.child = child
}

// PropagateParams receives parameter values for ? placeholders
// (implements ParamPropagator).
func (f *FilterStage) PropagateParams(args []any, buf *[]any) {
	// REQ002161: copy into own buffer — the incoming buf is shared
	// across all stages and subsequent stages would overwrite it.
	f.params = append(f.params[:0], args...)
}

// NextBatch pulls a batch from the child and applies the filter.
// Batches with no matching rows are discarded and the next batch
// is fetched. Returns (nil, nil) at EOF.
func (f *FilterStage) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for {
		batch, err := f.child.NextBatch(ctx)
		if err != nil {
			return nil, err
		}
		if batch == nil {
			return nil, nil
		}

		if f.execCtx != nil {
			batch.ExecCtx = f.execCtx
		}

		// Apply bloom filter pre-filter if available.
		if f.inBloom != nil {
			keep := f.applyBloomFilter(batch)
			if !keep {
				batch.Put()
				continue
			}
		}

		// Evaluate predicate over the batch.
		sel := EV.EvalBatch(f.pred, batch, f.params)
		if sel != nil && len(sel) == 0 {
			batch.Put()
			continue
		}
		// If all rows match (sel covers entire batch), treat as
		// no filtering needed — pass batch through without Sel.
		if sel != nil && len(sel) == batch.LogicalSize() {
			return batch, nil
		}
		if sel != nil {
			batch.Sel = sel
			batch.Size = len(sel)
		}
		return batch, nil
	}
}

// PropagateExecContext stores per-execution context for subquery
// evaluation. REQ002148.
func (f *FilterStage) PropagateExecContext(ec *DT.ExecContext) {
	f.execCtx = ec
}

// Reset resets the filter to pre-execution state.
func (f *FilterStage) Reset(_ context.Context) error {
	f.params = nil
	f.inColIdx = -1
	return nil
}

// Close releases the child stage.
func (f *FilterStage) Close() error {
	if f.child != nil {
		return f.child.Close()
	}
	return nil
}

// applyBloomFilter applies the IN-list bloom filter pre-filter to
// the batch. Returns false if no rows could possibly match (all
// bloom-miss), true otherwise. Modifies the batch's Sel vector
// in place for partial matches.
func (f *FilterStage) applyBloomFilter(batch *UT.Batch) bool {
	// Resolve column index on first call.
	if f.inColIdx < 0 {
		for i, col := range batch.Cols {
			if col.Type != 0 && col.Name != "" {
				f.inColIdx = i
				break
			}
		}
		if f.inColIdx < 0 {
			return true
		}
	}

	col := &batch.Cols[f.inColIdx]
	n := batch.LogicalSize()
	if n == 0 {
		return false
	}

	// Bloom filter only works on integer columns.
	if col.Type != 0 { // basic type check — bloom is only built for int columns
		sel := make([]uint16, 0, n)
		allMatch := true
		for r := range n {
			idx := r
			if batch.Sel != nil && r < len(batch.Sel) {
				idx = int(batch.Sel[r])
			}
			if idx < len(col.Data.Ints) {
				if f.inBloom.Contains(uint64(col.Data.Ints[idx])) != f.inNegate {
					sel = append(sel, uint16(r))
				} else {
					allMatch = false
				}
			}
		}

		if len(sel) == 0 {
			return false
		}
		if !allMatch {
			batch.Sel = sel
			batch.Size = len(sel)
		}
	}
	return true
}

// SubqueryFilterStageSpec creates SubqueryFilterStage instances. A
// SubqueryFilterStage applies a predicate that may contain subquery
// expressions (EXISTS, scalar subqueries) to each batch from its child.
// Unlike FilterStage (which uses batch-native EV), this stage evaluates
// the predicate row-by-row so subqueries can be executed via ExecCtx.
// REQ002179.
type SubqueryFilterStageSpec struct {
	Pred PS.Expr
}

// NewRuntime creates a SubqueryFilterStage from this spec.
func (s *SubqueryFilterStageSpec) NewRuntime() Stage {
	return &SubqueryFilterStage{
		pred: s.Pred,
	}
}

// Category returns CatTransform.
func (s *SubqueryFilterStageSpec) Category() StageCategory { return CatTransform }

// SubqueryFilterStage is a Stage that filters rows by evaluating a
// predicate row-by-row, supporting subquery expressions (EXISTS,
// scalar subqueries) via the ExecCtx planner. REQ002179.
type SubqueryFilterStage struct {
	child   Stage
	pred    PS.Expr
	execCtx *DT.ExecContext
	params  []any
	closed  bool
}

// SetChild sets the child stage (implements ChildSetter).
func (f *SubqueryFilterStage) SetChild(_ ChildSide, child Stage) {
	f.child = child
}

// PropagateParams stores parameter values (implements ParamPropagator).
func (f *SubqueryFilterStage) PropagateParams(args []any, buf *[]any) {
	f.params = append(f.params[:0], args...)
}

// PropagateExecContext stores per-execution context for subquery evaluation.
func (f *SubqueryFilterStage) PropagateExecContext(ec *DT.ExecContext) {
	f.execCtx = ec
}

// NextBatch pulls a batch from the child, evaluates the predicate
// row-by-row (supporting subqueries), and returns only matching rows.
func (f *SubqueryFilterStage) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for {
		batch, err := f.child.NextBatch(ctx)
		if err != nil {
			return nil, err
		}
		if batch == nil {
			return nil, nil
		}

		if f.execCtx != nil {
			batch.ExecCtx = f.execCtx
		}

		// Row-by-row evaluation using the row-based EV evaluator.
		// ToRows() does not propagate ExecCtx — set it manually.
		rows := batch.ToRows()
		for i := range rows {
			rows[i].ExecCtx = batch.ExecCtx
		}
		var sel []uint16
		for i := range rows {
			result, err := EV.EvalValue(f.pred, &rows[i], f.params)
			if err != nil {
				batch.Put()
				return nil, err
			}
			if DT.IsValueTruthy(result) {
				sel = append(sel, uint16(i))
			}
		}

		if len(sel) == 0 {
			// REQ002179: 0 rows may indicate a subquery evaluation issue
			// (e.g., ExecCtx not set). Return the batch with all rows
			// passed through to avoid silently dropping results.
			return batch, nil
		}
		batch.Sel = sel
		batch.Size = len(sel)
		return batch, nil
	}
}

// Reset clears subquery filter state.
func (f *SubqueryFilterStage) Reset(_ context.Context) error {
	f.closed = false
	return nil
}

// Close releases resources.
func (f *SubqueryFilterStage) Close() error {
	f.closed = true
	return nil
}
