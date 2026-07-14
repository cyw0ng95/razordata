package PF

import (
	"errors"

	OC "github.com/cyw0ng95/razordata/internal/SQO/OC"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

// FilterProjectFusionPass merges adjacent Filter + Project operators
// into a single FilterProject. Detects both Filter{Project{...}} and
// Project{Filter{...}} patterns.
type FilterProjectFusionPass struct{}

func (p *FilterProjectFusionPass) Name() string {
	return "filter_project_fusion"
}

func (p *FilterProjectFusionPass) Apply(plan *OC.Plan, ctx *OC.Context) (*OC.Plan, error) {
	if plan == nil || plan.Root == nil {
		return plan, nil
	}
	if ctx == nil || ctx.Factory == nil {
		return plan, errors.New("FilterProjectFusionPass: ctx.Factory is nil")
	}

	plan.Root = WalkOp(plan.Root, func(op pl.Operator) pl.Operator {
		filter, ok := op.(pl.PredicateCarrier)
		if !ok || !filter.HasPredicate() {
			return op
		}
		parent, ok := op.(pl.Parent)
		if !ok {
			return op
		}
		child := parent.Child()
		if child == nil {
			return op
		}
		proj, ok := child.(pl.ProjectInfo)
		if !ok {
			return op
		}
		projParent, ok := child.(pl.Parent)
		if !ok {
			return op
		}
		exprs := make([]interface{}, len(proj.Cols()))
		for i, c := range proj.Cols() {
			exprs[i] = c
		}
		fused := ctx.Factory.NewFilterProject(
			projParent.Child(),
			filter.Predicate(),
			nil,
			exprs,
		)
		return fused
	})

	return plan, nil
}