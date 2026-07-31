package QP

import (
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	CO "github.com/cyw0ng95/razordata/internal/SQO/CO"
)

// IndexSelectionPass examines predicates on scan nodes and decides
// SeqScan vs IndexScan. When a predicate references an indexed column
// with an equality comparison, the node type is changed to NodeIndexScan
// and the index name is stored in TableName. Depends on PredicatePushdown
// (REQ002260). REQ002262.
type IndexSelectionPass struct{}

func (p *IndexSelectionPass) Name() string { return "index_selection" }

func (p *IndexSelectionPass) Apply(qp *QueryPlan) error {
	if qp == nil || qp.RootIdx < 0 {
		return nil
	}
	qp.walkBottomUp(func(idx int, node *PlanNode) bool {
		if node.Type != NodeSeqScan && node.Type != NodeFusedScan {
			return true
		}
		if len(node.Exprs) == 0 {
			return true
		}
		table := node.TableName
		if table == "" {
			return true
		}
		// Check registered indexes
		DT.TablesMu.RLock()
		idxs, ok := DT.RegisteredIndexes[table]
		DT.TablesMu.RUnlock()
		if !ok || len(idxs) == 0 {
			return true
		}
		for _, idx := range idxs {
			// Try each predicate to find a matching index
			for _, pred := range node.Exprs {
				if expr, ok := pred.(PS.Expr); ok && predicateMatchesIndex(expr, idx.Name, table) {
					node.Type = NodeIndexScan
					node.TableName = idx.Name
					node.Cost = CO.EstimateSelectivity(expr)
					return true
				}
			}
		}
		return true
	})
	return nil
}

func predicateMatchesIndex(pred PS.Expr, indexName, table string) bool {
	found := false
	CO.WalkExpr(pred, func(e PS.Expr) {
		if found {
			return
		}
		switch n := e.(type) {
		case *PS.Ident:
			if n.Name == indexName {
				found = true
			}
		case *PS.QualifiedName:
			if n.Name == indexName && (table == "" || n.Table == table) {
				found = true
			}
		}
	})
	return found
}
