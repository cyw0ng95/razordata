package QP

import (
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// ColumnPruningPass traverses the plan bottom-up, collecting needed
// column names from projection and filter expressions, then annotates
// scan nodes with the columns they need to read. REQ002259.
type ColumnPruningPass struct{}

func (p *ColumnPruningPass) Name() string { return "column_pruning" }

func (p *ColumnPruningPass) Apply(qp *QueryPlan) error {
	if qp == nil || qp.RootIdx < 0 {
		return nil
	}
	needed := collectRootNeeded(qp)
	pruneNode(qp, qp.RootIdx, needed)
	return nil
}

func collectRootNeeded(qp *QueryPlan) map[string]bool {
	root := qp.Node(qp.RootIdx)
	out := make(map[string]bool)
	switch root.Type {
	case NodeProject:
		for _, e := range root.Exprs {
			collectRefsFromExpr(e, out)
		}
	case NodeFilter, NodeFilterProject:
		for _, e := range root.Exprs {
			collectRefsFromExpr(e, out)
		}
	case NodeSeqScan, NodeIndexScan, NodeFusedScan, NodeBitmapHeapScan:
		if root.Schema != nil {
			for _, c := range root.Schema.Cols {
				out[c] = true
			}
		}
	}
	return out
}

func collectRefsFromExpr(e PS.Expr, out map[string]bool) {
	if e == nil || out == nil {
		return
	}
	switch v := e.(type) {
	case *PS.Ident:
		out[v.Name] = true
	case *PS.QualifiedName:
		out[v.Name] = true
	case *PS.BinaryExpr:
		collectRefsFromExpr(v.Left, out)
		collectRefsFromExpr(v.Right, out)
		if v.Escape != nil {
			collectRefsFromExpr(v.Escape, out)
		}
	case *PS.UnaryExpr:
		collectRefsFromExpr(v.Operand, out)
	case *PS.FunctionCall:
		for _, a := range v.Args {
			collectRefsFromExpr(a, out)
		}
	case *PS.AggregateFunc:
		if v.Arg != nil {
			collectRefsFromExpr(v.Arg, out)
		}
		if v.Filter != nil {
			collectRefsFromExpr(v.Filter, out)
		}
	case *PS.CastExpr:
		collectRefsFromExpr(v.Expr, out)
	case *PS.AliasedExpr:
		collectRefsFromExpr(v.Expr, out)
	case *PS.BetweenExpr:
		collectRefsFromExpr(v.Expr, out)
		collectRefsFromExpr(v.Low, out)
		collectRefsFromExpr(v.High, out)
	case *PS.InExpr:
		collectRefsFromExpr(v.Expr, out)
		for _, e := range v.List {
			collectRefsFromExpr(e, out)
		}
	case *PS.CaseExpr:
		if v.Expr != nil {
			collectRefsFromExpr(v.Expr, out)
		}
		for _, wh := range v.WhenList {
			collectRefsFromExpr(wh.Cond, out)
			collectRefsFromExpr(wh.Then, out)
		}
		if v.Else != nil {
			collectRefsFromExpr(v.Else, out)
		}
	}
}

func pruneNode(qp *QueryPlan, idx int, needed map[string]bool) {
	node := qp.Node(idx)
	if node == nil {
		return
	}

	// Leaf scan nodes: annotate with needed columns via schema pruning.
	if isScanNode(node.Type) {
		if len(needed) > 0 && node.Schema != nil {
			neededSet := make(map[string]bool, len(needed))
			for c := range needed {
				neededSet[c] = true
			}
			cols := make([]string, 0, len(needed))
			types := make([]LX.TokenType, 0, len(needed))
			for i, c := range node.Schema.Cols {
				if neededSet[c] {
					cols = append(cols, c)
					if i < len(node.Schema.ColTypes) {
						types = append(types, node.Schema.ColTypes[i])
					}
				}
			}
			node.Schema.Cols = cols
			node.Schema.ColTypes = types
		}
		return
	}

	// Collect columns needed from predicates
	for _, e := range node.Exprs {
		collectRefsFromExpr(e, needed)
	}

	// Distribute needed columns to children
	childNeeded := copySet(needed)
	switch node.Type {
	case NodeProject:
		// Project defines its own output; children need columns referenced by project exprs
		childNeeded = make(map[string]bool)
		for _, e := range node.Exprs {
			collectRefsFromExpr(e, childNeeded)
		}
	case NodeFilter, NodeFilterProject:
		// Filter/FilterProject pass through; children need what filter references
		// (already collected above)
	case NodeHashJoin, NodeHashCrossJoin, NodeNestedLoopJoin:
		// Both children need the shared columns
		if len(node.Children) >= 2 {
			leftNeeded := filterBySchema(childNeeded, qp, node.Children[0])
			rightNeeded := filterBySchema(childNeeded, qp, node.Children[1])
			pruneNode(qp, node.Children[0], leftNeeded)
			pruneNode(qp, node.Children[1], rightNeeded)
			return
		}
	case NodeAggregate, NodeWindow:
		// Children need columns referenced by group/agg/window exprs
		// (already in childNeeded from predicate collection)
	}

	if len(node.Children) == 1 {
		pruneNode(qp, node.Children[0], childNeeded)
	}
}

func isScanNode(t NodeType) bool {
	switch t {
	case NodeSeqScan, NodeIndexScan, NodeFusedScan, NodeBitmapHeapScan,
		NodeConstRow, NodeValues, NodeValuesRows:
		return true
	}
	return false
}

func copySet(s map[string]bool) map[string]bool {
	if s == nil {
		return nil
	}
	out := make(map[string]bool, len(s))
	for k, v := range s {
		out[k] = v
	}
	return out
}

func filterBySchema(needed map[string]bool, qp *QueryPlan, childIdx int) map[string]bool {
	if childIdx < 0 || childIdx >= len(qp.Nodes) || needed == nil {
		return copySet(needed)
	}
	child := qp.Node(childIdx)
	if child.Schema == nil {
		return copySet(needed)
	}
	schemaSet := make(map[string]bool, len(child.Schema.Cols))
	for _, c := range child.Schema.Cols {
		schemaSet[c] = true
	}
	out := make(map[string]bool)
	for c := range needed {
		if schemaSet[c] {
			out[c] = true
		}
	}
	return out
}
