package QP

import (
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	RE "github.com/cyw0ng95/razordata/internal/SQF/RE"
)

// ConstantFoldingPass folds constant expressions in filter predicates.
// If a predicate folds to FALSE, the filter is removed (no rows match).
// If it folds to TRUE, the filter is removed (all rows pass).
// Otherwise, the folded expression replaces the original predicate.
// REQ002258.
type ConstantFoldingPass struct{}

func (p *ConstantFoldingPass) Name() string { return "constant_folding" }

func (p *ConstantFoldingPass) Apply(qp *QueryPlan) error {
	if qp == nil || qp.RootIdx < 0 {
		return nil
	}
	qp.walkBottomUp(func(idx int, node *PlanNode) bool {
		if node.Type != NodeFilter || len(node.Exprs) == 0 {
			return true
		}
		pred := node.Exprs[0]
		folded, _ := RE.RewriteExpr(pred)
		if boolLit, ok := folded.(*PS.BoolLiteral); ok {
			if boolLit.Val {
				// TRUE → remove filter, replace with child
				qp.Nodes[idx] = *qp.Node(node.Children[0])
				rewireParent(qp, idx, node.Children[0])
			} else {
				// FALSE → remove filter entirely (no rows match)
				removeNode(qp, idx)
			}
			return true
		}
		if folded != pred {
			node.Exprs[0] = folded
		}
		return true
	})
	return nil
}

// rewireParent replaces references to oldIdx with newIdx in all parent nodes.
func rewireParent(qp *QueryPlan, oldIdx, newIdx int) {
	for i := range qp.Nodes {
		for j, c := range qp.Nodes[i].Children {
			if c == oldIdx {
				qp.Nodes[i].Children[j] = newIdx
			}
		}
	}
	if qp.RootIdx == oldIdx {
		qp.RootIdx = newIdx
	}
}
