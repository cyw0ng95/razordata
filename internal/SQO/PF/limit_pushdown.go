package PF

import (
	OC "github.com/cyw0ng95/razordata/internal/SQO/OC"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

type LimitPushdownPass struct{}

func (p *LimitPushdownPass) Name() string { return "limit_pushdown" }

func (p *LimitPushdownPass) Apply(plan *OC.Plan, ctx *OC.Context) (*OC.Plan, error) {
	if plan == nil || plan.Root == nil {
		return plan, nil
	}
	plan.Root = pushLimit(plan.Root)
	return plan, nil
}

func pushLimit(op pl.Operator) pl.Operator {
	if op == nil {
		return nil
	}

	if p, ok := op.(pl.Parent); ok {
		child := pushLimit(p.Child())
		p.SetChild(child)
	}

		if li, ok := op.(pl.LimitInfo); ok && !li.IsTopN() {
			if child := singleChild(op); child != nil {
				if _, ok := child.(pl.SortInfo); ok {
					if li2, ok := child.(pl.LimitInfo); ok {
						li2.SetTopN(true)
						li.SetTopN(true)
						return child
					}
				}
			}
		}

	return op
}
