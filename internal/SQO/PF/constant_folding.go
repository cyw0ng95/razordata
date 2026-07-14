package PF

import (
	"errors"

	OC "github.com/cyw0ng95/razordata/internal/SQO/OC"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	RE "github.com/cyw0ng95/razordata/internal/SQF/RE"
)

// ConstantFoldingPass folds constant expressions in operator tree
// nodes. For filters whose predicate evaluates to a constant TRUE or
// FALSE, the filter is eliminated (TRUE) or replaced with an empty
// scan (FALSE).
type ConstantFoldingPass struct{}

func (p *ConstantFoldingPass) Name() string {
	return "constant_folding"
}

func (p *ConstantFoldingPass) Apply(plan *OC.Plan, ctx *OC.Context) (*OC.Plan, error) {
	if plan == nil || plan.Root == nil {
		return plan, nil
	}
	if ctx == nil || ctx.Factory == nil {
		return plan, errors.New("ConstantFoldingPass: ctx.Factory is nil")
	}

	plan.Root = WalkOp(plan.Root, func(op pl.Operator) pl.Operator {
		filter, ok := op.(pl.PredicateCarrier)
		if !ok || !filter.HasPredicate() {
			return op
		}
		pred := filter.Predicate()
		expr, ok := pred.(PS.Expr)
		if !ok {
			return op
		}
		folded := RE.RewriteExpr(expr)
		if boolLit, ok := folded.(*PS.BoolLiteral); ok {
			if boolLit.Val {
				if parent, ok := op.(pl.Parent); ok {
					return parent.Child()
				}
				return op
			}
			return ctx.Factory.NewValues(nil)
		}
		if folded != expr {
			filter.SetPredicate(folded)
		}
		return op
	})

	return plan, nil
}
