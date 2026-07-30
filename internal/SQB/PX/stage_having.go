package PX

import (
	"context"

	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// HavingStageSpec creates HavingStage instances. A HavingStage applies
// a HAVING predicate to post-aggregation batches. It is functionally
// identical to FilterStage but operates on the output of aggregate
// stages, applying group-level filters.
type HavingStageSpec struct {
	Pred PS.Expr // HAVING predicate expression
}

// NewRuntime creates a HavingStage from this spec.
func (s *HavingStageSpec) NewRuntime() Stage {
	return &HavingStage{
		pred: s.Pred,
	}
}

// Category returns CatTransform.
func (s *HavingStageSpec) Category() StageCategory { return CatTransform }

// HavingStage is a Transform-stage that applies a HAVING filter to
// post-aggregation result batches. Same logic as FilterStage but
// semantically distinct: it operates on grouped/aggregate output.
type HavingStage struct {
	child  Stage
	pred   PS.Expr
	params []any
}

// SetChild sets the child stage (implements ChildSetter).
func (h *HavingStage) SetChild(_ ChildSide, child Stage) {
	h.child = child
}

// PropagateParams receives parameter values for ? placeholders
// (implements ParamPropagator).
func (h *HavingStage) PropagateParams(args []any, buf *[]any) {
	// REQ002161: copy into own buffer — the incoming buf is shared
	// across all stages and subsequent stages would overwrite it.
	h.params = append(h.params[:0], args...)
}

// NextBatch pulls a batch from the child (the aggregate stage) and
// applies the HAVING predicate. Batches with no matching groups are
// discarded. Returns (nil, nil) at EOF.
func (h *HavingStage) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for {
		batch, err := h.child.NextBatch(ctx)
		if err != nil {
			return nil, err
		}
		if batch == nil {
			return nil, nil
		}

		sel := EV.EvalBatch(h.pred, batch, h.params)
		if sel != nil && len(sel) == 0 {
			batch.Put()
			continue
		}
		// If all rows match, pass batch through without Sel.
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

// Reset resets the having stage to pre-execution state.
func (h *HavingStage) Reset(_ context.Context) error {
	h.params = nil
	return nil
}

// Close releases the child stage.
func (h *HavingStage) Close() error {
	if h.child != nil {
		return h.child.Close()
	}
	return nil
}
