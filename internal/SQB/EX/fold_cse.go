package EX

import (
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// splitSelectCols separates a SELECT list into aggregate expressions
// and non-aggregate expressions. Non-aggregate columns that reference
// table columns are routed to groupCols so the Aggregate operator can
// emit one row per distinct group. Columns whose values are constant
// across all rows (no Ident/QualifiedName references) stay in
// `other` so the Aggregate operator can evaluate them once after
// grouping and inject them into the output row. REQ001711.
func splitSelectCols(cols []PS.Expr) (aggs, groupCols, other []PS.Expr) {
	if !hasAnyAggregate(cols) {
		return nil, nil, nil
	}
	for _, c := range cols {
		if DT.ContainsAggregate(c) {
			aggs = append(aggs, c)
			continue
		}
		if _, ok := c.(*PS.StarExpr); ok {
			continue
		}
		if isConstantExpr(c) {
			// Constant projection — emitted alongside aggs after
			// grouping; does not change the group partition.
			other = append(other, c)
			continue
		}
		groupCols = append(groupCols, c)
	}
	return aggs, groupCols, other
}

// isConstantExpr reports whether e is a constant expression (no column
// references). Used by constant folding (REQ001074) and to route
// constant SELECT projections alongside aggregates (REQ001711).
// Aggregates wrapped in an expression are NOT considered constant for
// the purposes of routing — they are routed to the aggregate list
// instead, so they are evaluated against the full input row set.
func isConstantExpr(e PS.Expr) bool {
	if e == nil {
		return true
	}
	switch v := e.(type) {
	case *PS.NumberLiteral, *PS.FloatLiteral, *PS.StringLiteral,
		*PS.BoolLiteral, *PS.NullLiteral, *PS.Param:
		return true
	case *PS.Ident, *PS.QualifiedName, *PS.AggregateFunc:
		return false
	case *PS.UnaryExpr:
		return isConstantExpr(v.Operand)
	case *PS.BinaryExpr:
		return isConstantExpr(v.Left) && isConstantExpr(v.Right)
	case *PS.BetweenExpr:
		// REQ001722: NOT BETWEEN expressions like `-15 NOT BETWEEN NULL AND NULL`
		// are parsed as UnaryExpr(NOT, BetweenExpr(...)). Without this case,
		// isConstantExpr returns false for any BetweenExpr, causing the join
		// elimination pass to drop joins whose ON is a constant BETWEEN/NOT
		// BETWEEN expression (the constant-ON preservation rule at line 273
		// never fires). This silently turns a 0-row INNER JOIN into a
		// left-table-only scan.
		return isConstantExpr(v.Expr) && isConstantExpr(v.Low) && isConstantExpr(v.High)
	case *PS.FunctionCall:
		for _, a := range v.Args {
			if !isConstantExpr(a) {
				return false
			}
		}
		return true
	case *PS.CastExpr:
		return isConstantExpr(v.Expr)
	case *PS.AliasedExpr:
		return isConstantExpr(v.Expr)
	case *PS.CaseExpr:
		if !isConstantExpr(v.Expr) {
			return false
		}
		for _, w := range v.WhenList {
			if !isConstantExpr(w.Cond) || !isConstantExpr(w.Then) {
				return false
			}
		}
		return isConstantExpr(v.Else)
	case *PS.InExpr:
		// InExpr with a subquery is never constant.
		if v.Subquery != nil {
			return false
		}
		if !isConstantExpr(v.Expr) {
			return false
		}
		for _, item := range v.List {
			if !isConstantExpr(item) {
				return false
			}
		}
		return true
	case *PS.ListExpr:
		for _, item := range v.Items {
			if !isConstantExpr(item) {
				return false
			}
		}
		return true
	case *PS.IntervalLiteral:
		return true
	}
	return false
}

// REQ002256: the constant-folding (REQ001074) and common-subexpression
// elimination (REQ001075) passes were removed with the old optimizer.
// Query optimization will be redesigned from scratch on the QueryPlan
// DAG, so no folding/CSE logic is kept here. Only the structural
// SELECT-list splitting helper remains.

// hasAnyAggregate checks if any column in the select list contains
// an aggregate function.
