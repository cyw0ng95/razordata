package QP

import (
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// PredicatePushdownPass pushes filter predicates down to child scan
// nodes when the predicate references only that scan's table. The
// predicate is stored in the scan node's Exprs slot and cleared
// from the filter. Depends on ColumnPruning (REQ002259). REQ002260.
type PredicatePushdownPass struct{}

func (p *PredicatePushdownPass) Name() string { return "predicate_pushdown" }

func (p *PredicatePushdownPass) Apply(qp *QueryPlan) error {
	if qp == nil || qp.RootIdx < 0 {
		return nil
	}
	qp.walkBottomUp(func(idx int, node *PlanNode) bool {
		if node.Type != NodeFilter && node.Type != NodeFilterProject {
			return true
		}
		if len(node.Exprs) == 0 {
			return true
		}
		if len(node.Children) == 0 {
			return true
		}
		child := qp.Node(node.Children[0])
		if child == nil {
			return true
		}
		// Only push if child is a scan node
		if !isScanNode(child.Type) {
			return true
		}
		// Push predicate to child
		for _, e := range node.Exprs {
			if isExpr, ok := e.(PS.Expr); ok {
				child.Exprs = append(child.Exprs, isExpr)
				child.PushedPred = true
			}
		}
		// Remove predicate from filter (it's now on the child)
		node.Exprs = nil
		return true
	})
	return nil
}
