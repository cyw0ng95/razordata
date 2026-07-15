package PF

import (
	OC "github.com/cyw0ng95/razordata/internal/SQO/OC"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

// SortEliminationPass removes redundant Sort operators when the input
// is already sorted. Conservative heuristic: only eliminates a Sort
// when its child is another Sort with an identical OrderBy spec
// (outer Sort is redundant).
//
// REQ001492.
type SortEliminationPass struct{}

func (p *SortEliminationPass) Name() string {
	return "sort_elimination"
}

func (p *SortEliminationPass) Apply(plan *OC.Plan, ctx *OC.Context) (*OC.Plan, error) {
	if plan == nil || plan.Root == nil {
		return plan, nil
	}

	plan.Root = WalkOp(plan.Root, func(op pl.Operator) pl.Operator {
		sort, ok := op.(pl.SortInfo)
		if !ok {
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
		if childSort, ok := child.(pl.SortInfo); ok {
			if sortOrdersMatch(sort.OrderBy(), childSort.OrderBy()) {
				return child
			}
		}
		return op
	})

	return plan, nil
}

// sortOrdersMatch reports whether two order-by specs describe the same
// ordering. Compares column name (case-insensitive) and direction.
func sortOrdersMatch(a, b []pl.OrderSpec) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Col != b[i].Col {
			return false
		}
		if a[i].Desc != b[i].Desc {
			return false
		}
	}
	return true
}
