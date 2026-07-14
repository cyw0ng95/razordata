package PF

import (
	OC "github.com/cyw0ng95/razordata/internal/SQO/OC"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

type PredicatePushdownPass struct{}

func (p *PredicatePushdownPass) Name() string { return "predicate_pushdown" }

func (p *PredicatePushdownPass) Apply(plan *OC.Plan, ctx *OC.Context) (*OC.Plan, error) {
	if plan == nil || plan.Root == nil {
		return plan, nil
	}
	plan.Root = pushdownNode(plan.Root, ctx)
	return plan, nil
}

func pushdownNode(op pl.Operator, ctx *OC.Context) pl.Operator {
	if op == nil {
		return nil
	}

	if c2, ok := op.(pl.Children2); ok {
		left := c2.Left()
		right := c2.Right()
		if left != nil && right != nil {
			left = pushdownNode(left, ctx)
			right = pushdownNode(right, ctx)
			c2.SetLeft(left)
			c2.SetRight(right)
			return op
		}
	}

	if p, ok := op.(pl.Parent); ok {
		child := pushdownNode(p.Child(), ctx)
		p.SetChild(child)
	}

	if pc, ok := op.(pl.PredicateCarrier); ok && pc.HasPredicate() {
		child := singleChild(op)
		if child != nil {
			if childPc, ok := child.(pl.PredicateCarrier); ok && !childPc.HasPredicate() &&
				isSingleTablePredicate(pc.Predicate(), child) {
				childPc.SetPredicate(pc.Predicate())
				pc.SetPredicate(nil)
				return child
			}
		}
	}

	return op
}

func singleChild(op pl.Operator) pl.Operator {
	if c2, ok := op.(pl.Children2); ok {
		l, r := c2.Left(), c2.Right()
		if l != nil && r != nil {
			return nil
		}
	}
	if p, ok := op.(pl.Parent); ok {
		return p.Child()
	}
	return nil
}

func isSingleTablePredicate(pred interface{}, op pl.Operator) bool {
	expr, ok := pred.(PS.Expr)
	if !ok {
		return false
	}
	rs, ok := op.(pl.RelationSource)
	if !ok {
		return false
	}
	tableName := rs.Table()
	if tableName == "" {
		return false
	}
	return referencesOnlyTable(expr, tableName)
}

func referencesOnlyTable(e PS.Expr, table string) bool {
	refsOther := false
	walkPredRefs(e, func(col, tbl string) {
		if tbl != "" && tbl != table {
			refsOther = true
		}
	})
	return !refsOther
}

func walkPredRefs(e PS.Expr, fn func(col, table string)) {
	if e == nil {
		return
	}
	switch v := e.(type) {
	case *PS.Ident:
		fn(v.Name, "")
	case *PS.QualifiedName:
		fn(v.Name, v.Table)
	case *PS.BinaryExpr:
		walkPredRefs(v.Left, fn)
		walkPredRefs(v.Right, fn)
		if v.Escape != nil {
			walkPredRefs(v.Escape, fn)
		}
	case *PS.UnaryExpr:
		walkPredRefs(v.Operand, fn)
	case *PS.FunctionCall:
		for _, a := range v.Args {
			walkPredRefs(a, fn)
		}
	case *PS.AggregateFunc:
		if v.Arg != nil {
			walkPredRefs(v.Arg, fn)
		}
		if v.Filter != nil {
			walkPredRefs(v.Filter, fn)
		}
	case *PS.CastExpr:
		walkPredRefs(v.Expr, fn)
	case *PS.AliasedExpr:
		walkPredRefs(v.Expr, fn)
	case *PS.BetweenExpr:
		walkPredRefs(v.Expr, fn)
		walkPredRefs(v.Low, fn)
		walkPredRefs(v.High, fn)
	case *PS.InExpr:
		walkPredRefs(v.Expr, fn)
		for _, e := range v.List {
			walkPredRefs(e, fn)
		}
	case *PS.CaseExpr:
		if v.Expr != nil {
			walkPredRefs(v.Expr, fn)
		}
		for _, wh := range v.WhenList {
			walkPredRefs(wh.Cond, fn)
			walkPredRefs(wh.Then, fn)
		}
		if v.Else != nil {
			walkPredRefs(v.Else, fn)
		}
	}
}
