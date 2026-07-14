package PF

import (
	OC "github.com/cyw0ng95/razordata/internal/SQO/OC"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// SubqueryDecorrelationPass converts EXISTS / IN (subq) Filters into
// semi-joins when the subquery is uncorrelated. REQ001448.
//
// Safety: when the subquery is correlated (references outer columns)
// OR the factory refuses to materialize the subquery as an operator,
// the Filter is left untouched and the per-row evaluator handles it.
type SubqueryDecorrelationPass struct{}

func (p *SubqueryDecorrelationPass) Name() string { return "subquery_decorrelation" }

func (p *SubqueryDecorrelationPass) Apply(plan *OC.Plan, ctx *OC.Context) (*OC.Plan, error) {
	if plan == nil || plan.Root == nil || ctx.SubPlanner == nil {
		return plan, nil
	}
	plan.Root = WalkOp(plan.Root, func(op pl.Operator) pl.Operator {
		return tryDecorrelate(op, ctx)
	})
	return plan, nil
}

func tryDecorrelate(op pl.Operator, ctx *OC.Context) pl.Operator {
	pc, ok := op.(pl.PredicateCarrier)
	if !ok || !pc.HasPredicate() {
		return op
	}
	exists, ok := pc.Predicate().(*PS.ExistsExpr)
	if !ok {
		return op
	}
	if isCorrelated(exists.Subquery) {
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
	subOp, err := ctx.SubPlanner.PlanSubquery(exists.Subquery, nil)
	if err != nil || subOp == nil {
		return op
	}
	newOp := ctx.Factory.NewHashJoin(child, subOp, "", "", pl.SemiJoin)
	if newOp == nil {
		return op
	}
	return newOp
}

// isCorrelated reports whether stmt references any outer-query column.
// Conservative: a subquery with any QualifiedName referencing a table
// other than its own FROM is considered correlated. Bare Idents in
// the subquery are treated as inner columns (per the SQL convention
// that bare names resolve to the innermost FROM).
func isCorrelated(stmt PS.Stmt) bool {
	sel, ok := stmt.(*PS.Select)
	if !ok {
		return true
	}
	inner := sel.From
	if inner == "" {
		return true
	}
	res := false
	v := &corrScan{inner: inner, hit: &res}
	PS.AcceptExpr(sel.Where, v)
	if res {
		return true
	}
	PS.AcceptExpr(sel.Having, v)
	if res {
		return true
	}
	for _, c := range sel.Cols {
		expr := c
		if a, ok := c.(*PS.AliasedExpr); ok {
			expr = a.Expr
		}
		res = false
		v = &corrScan{inner: inner, hit: &res}
		PS.AcceptExpr(expr, v)
		if res {
			return true
		}
	}
	return false
}

// corrScan walks an expression via the default AcceptExpr dispatch
// and sets *hit to true if any QualifiedName with Table != inner is
// seen. BaseVisitor default Visit methods recurse, so only the leaves
// need overriding.
type corrScan struct {
	PS.BaseVisitor
	inner string
	hit   *bool
}

func (c *corrScan) VisitQualifiedName(n *PS.QualifiedName) bool {
	if n.Table != "" && n.Table != c.inner {
		*c.hit = true
		return false
	}
	return true
}