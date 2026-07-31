package QP

import (
	"strings"

	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// SortEliminationPass removes redundant Sort nodes when the input is
// already sorted. Only eliminates when child is another Sort with
// identical order-by spec. REQ002263.
type SortEliminationPass struct{}

func (p *SortEliminationPass) Name() string { return "sort_elimination" }

func (p *SortEliminationPass) Apply(qp *QueryPlan) error {
	if qp == nil || qp.RootIdx < 0 {
		return nil
	}
	qp.walkBottomUp(func(idx int, node *PlanNode) bool {
		if node.Type != NodeSort || len(node.Children) != 1 {
			return true
		}
		child := qp.Node(node.Children[0])
		if child == nil || child.Type != NodeSort {
			return true
		}
		if sortKeysMatch(node.Exprs, child.Exprs) {
			removeNode(qp, idx)
		}
		return true
	})
	return nil
}

func sortKeysMatch(a, b []PS.Expr) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		ai, okA := extractIdentName(a[i])
		bi, okB := extractIdentName(b[i])
		if !okA || !okB {
			return false
		}
		if strings.ToLower(ai) != strings.ToLower(bi) {
			return false
		}
	}
	return true
}

func extractIdentName(e PS.Expr) (string, bool) {
	if e == nil {
		return "", false
	}
	if id, ok := e.(*PS.Ident); ok {
		return id.Name, true
	}
	return "", false
}

