package PF

import (
	OC "github.com/cyw0ng95/razordata/internal/SQO/OC"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

type ColumnPruningPass struct{}

func (p *ColumnPruningPass) Name() string { return "column_pruning" }

func (p *ColumnPruningPass) Apply(plan *OC.Plan, ctx *OC.Context) (*OC.Plan, error) {
	if plan == nil || plan.Root == nil {
		return plan, nil
	}
	needed := collectRootNeeded(plan.Root)
	pruneNode(plan.Root, needed)
	return plan, nil
}

func collectRootNeeded(op pl.Operator) map[string]bool {
	if pi, ok := op.(pl.ProjectInfo); ok {
		out := make(map[string]bool)
		for _, e := range pi.Cols() {
			collectRefsFromExpr(e, out)
		}
		return out
	}
	if cs, ok := op.(pl.ColumnSchema); ok {
		cols := cs.Columns()
		out := make(map[string]bool, len(cols))
		for _, c := range cols {
			out[c] = true
		}
		return out
	}
	return nil
}

func collectRefsFromExpr(e PS.Expr, out map[string]bool) {
	if e == nil {
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
	case *PS.WindowFunc:
		for _, a := range v.Args {
			collectRefsFromExpr(a, out)
		}
	case *PS.CastExpr:
		collectRefsFromExpr(v.Expr, out)
	case *PS.AliasedExpr:
		collectRefsFromExpr(v.Expr, out)
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
	case *PS.BetweenExpr:
		collectRefsFromExpr(v.Expr, out)
		collectRefsFromExpr(v.Low, out)
		collectRefsFromExpr(v.High, out)
	case *PS.InExpr:
		collectRefsFromExpr(v.Expr, out)
		for _, e := range v.List {
			collectRefsFromExpr(e, out)
		}
	case *PS.IntervalLiteral:
	case *PS.SubqueryExpr:
	case *PS.ExistsExpr:
	}
}

func pruneNode(op pl.Operator, needed map[string]bool) {
	if op == nil {
		return
	}

	_, isParent := op.(pl.Parent)
	_, isC2 := op.(pl.Children2)
	isLeaf := !isParent && !isC2

	if cp, ok := op.(pl.ColPrunable); ok && isLeaf {
		if len(needed) > 0 {
			cols := make([]string, 0, len(needed))
			for c := range needed {
				cols = append(cols, c)
			}
			cp.SetUsedCols(cols)
		}
	}

	if pc, ok := op.(pl.PredicateCarrier); ok && pc.HasPredicate() {
		pred := pc.Predicate()
		if expr, ok := pred.(PS.Expr); ok {
			collectRefsFromExpr(expr, needed)
		}
	}

	if pi, ok := op.(pl.ProjectInfo); ok {
		childNeeded := make(map[string]bool)
		for k, v := range needed {
			childNeeded[k] = v
		}
		for _, e := range pi.Cols() {
			collectRefsFromExpr(e, childNeeded)
		}
		needed = childNeeded
	}

	if c2, ok := op.(pl.Children2); ok {
		left := c2.Left()
		right := c2.Right()
		if left != nil && right != nil {
			leftNeeded := filterBySchema(needed, left)
			if pl, ok := left.(pl.PredicateCarrier); ok && pl.HasPredicate() {
				collectRefsFromPred(pl.Predicate(), leftNeeded)
			}
			pruneNode(left, leftNeeded)

			rightNeeded := filterBySchema(needed, right)
			if pr, ok := right.(pl.PredicateCarrier); ok && pr.HasPredicate() {
				collectRefsFromPred(pr.Predicate(), rightNeeded)
			}
			pruneNode(right, rightNeeded)
			return
		}
	}

	if p, ok := op.(pl.Parent); ok {
		if child := p.Child(); child != nil {
			pruneNode(child, needed)
		}
	}
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

func filterBySchema(needed map[string]bool, op pl.Operator) map[string]bool {
	cs, ok := op.(pl.ColumnSchema)
	if !ok || needed == nil {
		return copySet(needed)
	}
	schema := cs.Columns()
	schemaSet := make(map[string]bool, len(schema))
	for _, c := range schema {
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

func collectRefsFromPred(pred interface{}, out map[string]bool) {
	if expr, ok := pred.(PS.Expr); ok {
		collectRefsFromExpr(expr, out)
	}
}
