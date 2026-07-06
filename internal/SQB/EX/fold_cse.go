package EX

import (
	"fmt"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	RE "github.com/cyw0ng95/razordata/internal/SQF/RE"
)

func splitSelectCols(cols []PS.Expr) (aggs, groupCols, other []PS.Expr) {
	if !hasAnyAggregate(cols) {
		return nil, nil, cols
	}
	for _, c := range cols {
		if DT.ContainsAggregate(c) {
			aggs = append(aggs, c)
			continue
		}
		if _, ok := c.(*PS.StarExpr); ok {
			continue
		}
		groupCols = append(groupCols, c)
	}
	return aggs, groupCols, nil
}

// isConstantExpr reports whether e is a constant expression (no column
// references). Used by constant folding (REQ001074).
func isConstantExpr(e PS.Expr) bool {
	if e == nil {
		return true
	}
	switch v := e.(type) {
	case *PS.NumberLiteral, *PS.FloatLiteral, *PS.StringLiteral,
		*PS.BoolLiteral, *PS.NullLiteral, *PS.Param:
		return true
	case *PS.Ident, *PS.QualifiedName:
		return false
	case *PS.UnaryExpr:
		return isConstantExpr(v.Operand)
	case *PS.BinaryExpr:
		return isConstantExpr(v.Left) && isConstantExpr(v.Right)
	case *PS.FunctionCall:
		for _, a := range v.Args {
			if !isConstantExpr(a) {
				return false
			}
		}
		return true
	case *PS.CastExpr:
		return isConstantExpr(v.Expr)
	}
	return false
}

// foldConstants simplifies constant expressions in a WHERE clause.
// It handles:
//   - `const = const` → evaluated to BoolLiteral (removes tautologies)
//   - `col + 0` → col
//   - `col * 1` → col
//   - `col - 0` → col
//   - `col / 1` → col
//
// Returns the simplified expression, or nil if the entire expression
// is a tautology (always true). REQ001074.
func foldConstants(e PS.Expr) PS.Expr {
	if e == nil {
		return nil
	}
	if b, ok := e.(*PS.BinaryExpr); ok {
		l := foldConstants(b.Left)
		r := foldConstants(b.Right)

		// col + 0 → col
		if LX.T_PLUS == b.Op && isSameColumn(l, r) && isZero(r) {
			return l
		}
		if LX.T_PLUS == b.Op && isSameColumn(l, r) && isZero(l) {
			return r
		}
		// col * 1 → col
		if LX.T_STAR == b.Op && isSameColumn(l, r) && isOne(r) {
			return l
		}
		if LX.T_STAR == b.Op && isSameColumn(l, r) && isOne(l) {
			return r
		}
		// col - 0 → col
		if LX.T_MINUS == b.Op && isSameColumn(l, r) && isZero(r) {
			return l
		}
		// col / 1 → col
		if LX.T_SLASH == b.Op && isSameColumn(l, r) && isOne(r) {
			return l
		}

		// If both sides are constant, eval at plan time.
		if isConstantExpr(l) && isConstantExpr(r) {
			v, err := EV.EvalValue(&PS.BinaryExpr{Left: l, Right: r, Op: b.Op}, nil, nil)
			if err == nil {
				return valueToLiteral(v)
			}
		}

		// Short-circuit: const AND FALSE → FALSE, const OR TRUE → TRUE
		if b.Op == LX.T_AND {
			// FALSE AND anything → FALSE
			if isFalse(l) || isFalse(r) {
				return &PS.BoolLiteral{Val: false}
			}
			// TRUE AND x → x
			if isTrue(l) && isConstantExpr(l) {
				return r
			}
			if isTrue(r) && isConstantExpr(r) {
				return l
			}
		}
		if b.Op == LX.T_OR {
			// TRUE OR anything → TRUE
			if isTrue(l) || isTrue(r) {
				return &PS.BoolLiteral{Val: true}
			}
			// FALSE OR x → x
			if isFalse(l) && isConstantExpr(l) {
				return r
			}
			if isFalse(r) && isConstantExpr(r) {
				return l
			}
		}

		// Rebuild with folded children.
		if l != b.Left || r != b.Right {
			cp := *b
			cp.Left = l
			cp.Right = r
			return &cp
		}
	}
	return e
}

// isSameColumn checks if l and r are the same column reference.
func isSameColumn(l, r PS.Expr) bool {
	if lid, ok := l.(*PS.Ident); ok {
		if rid, ok := r.(*PS.Ident); ok {
			return lid.Name == rid.Name
		}
	}
	if lq, ok := l.(*PS.QualifiedName); ok {
		if rq, ok := r.(*PS.QualifiedName); ok {
			return lq.Table == rq.Table && lq.Name == rq.Name
		}
	}
	return false
}

// isZero checks if an expression is the numeric literal 0.
func isZero(e PS.Expr) bool {
	if n, ok := e.(*PS.NumberLiteral); ok {
		return n.Val == 0
	}
	return false
}

// isOne checks if an expression is the numeric literal 1.
func isOne(e PS.Expr) bool {
	if n, ok := e.(*PS.NumberLiteral); ok {
		return n.Val == 1
	}
	return false
}

// isTrue checks if an expression is the boolean literal TRUE.
func isTrue(e PS.Expr) bool {
	if b, ok := e.(*PS.BoolLiteral); ok {
		return b.Val
	}
	// 1 can also be truthy in comparisons
	if n, ok := e.(*PS.NumberLiteral); ok {
		return n.Val != 0
	}
	return false
}

// isFalse checks if an expression is the boolean literal FALSE.
func isFalse(e PS.Expr) bool {
	if b, ok := e.(*PS.BoolLiteral); ok {
		return !b.Val
	}
	if n, ok := e.(*PS.NumberLiteral); ok {
		return n.Val == 0
	}
	return false
}

// valueToLiteral converts a Value back to a literal AST node.
func valueToLiteral(v DT.Value) PS.Expr {
	switch v.Kind {
	case KindNull:
		return &PS.NullLiteral{}
	case KindInt:
		return &PS.NumberLiteral{Val: v.I64}
	case KindFloat:
		return &PS.FloatLiteral{Val: v.F64}
	case KindText:
		return &PS.StringLiteral{Val: v.S}
	case KindBool:
		return &PS.BoolLiteral{Val: v.Bo}
	}
	return nil
}

// REQ001075: Common subexpression elimination (CSE).
// exprHash returns a structural hash for an expression to detect
// identical subexpressions in the WHERE clause.
func exprHash(e PS.Expr) string {
	if e == nil {
		return ""
	}
	switch v := e.(type) {
	case *PS.Ident:
		return "id:" + v.Name
	case *PS.QualifiedName:
		return "qn:" + v.Table + "." + v.Name
	case *PS.NumberLiteral:
		return fmt.Sprintf("num:%d", v.Val)
	case *PS.FloatLiteral:
		return fmt.Sprintf("flt:%g", v.Val)
	case *PS.StringLiteral:
		return "str:" + v.Val
	case *PS.BoolLiteral:
		return fmt.Sprintf("bool:%v", v.Val)
	case *PS.NullLiteral:
		return "null"
	case *PS.UnaryExpr:
		return fmt.Sprintf("un:%d:%s", v.Op, exprHash(v.Operand))
	case *PS.BinaryExpr:
		return fmt.Sprintf("bin:%d:%s:%s", v.Op, exprHash(v.Left), exprHash(v.Right))
	case *PS.FunctionCall:
		h := "fn:" + v.Name
		for _, a := range v.Args {
			h += ":" + exprHash(a)
		}
		return h
	case *PS.CastExpr:
		return fmt.Sprintf("cast:%d:%s", v.Type.Type, exprHash(v.Expr))
	case *PS.InExpr:
		// REQ0011XX: hash must include the target column and the
		// full list — without these, two distinct IN-list predicates
		// (e.g. `b4 IN (532,...)` and `d9 IN (808,...)`) hash to the
		// same value, causing eliminateCommonSubexpressions to
		// incorrectly drop one. That destroys cross-join predicate
		// pushdown and turns 5-table SELECTs into 10^10-row
		// Cartesian products that OOM the OP.HashJoin dataBuf.
		h := "in:" + exprHash(v.Expr) + ":["
		for _, it := range v.List {
			h += exprHash(it) + ","
		}
		return h + "]"
	case *PS.BetweenExpr:
		return fmt.Sprintf("btw:%s:%s:%s", exprHash(v.Expr), exprHash(v.Low), exprHash(v.High))
	case *PS.ListExpr:
		h := "list:["
		for _, it := range v.Items {
			h += exprHash(it) + ","
		}
		return h + "]"
	case *PS.AliasedExpr:
		return fmt.Sprintf("alias:%s:%s", v.Alias, exprHash(v.Expr))
	case *PS.AggregateFunc:
		return fmt.Sprintf("agg:%s:%s:%t", v.Name, exprHash(v.Arg), v.Distinct)
	case *PS.WindowFunc:
		return fmt.Sprintf("win:%s", v.Name)
	case *PS.CaseExpr:
		h := "case:" + exprHash(v.Expr) + ":["
		for _, w := range v.WhenList {
			h += "(" + exprHash(w.Cond) + "->" + exprHash(w.Then) + "),"
		}
		if v.Else != nil {
			h += "else=" + exprHash(v.Else)
		}
		return h + "]"
	case *PS.SubqueryExpr, *PS.ExistsExpr:
		// Subqueries have their own scope — hash by a stable tag so
		// they are not deduplicated against each other, but also do
		// not collapse to "%T" which would treat all subqueries as
		// identical.
		return fmt.Sprintf("subq:%T:%p", v, v)
	}
	return fmt.Sprintf("%T", e)
}

// eliminateCommonSubexpressions detects identical expressions in the
// WHERE clause and folds them so each is only evaluated once. For
// expressions that appear multiple times, the first occurrence is kept
// and subsequent occurrences reference the result of the first. REQ001075.
// For the MVP, this removes duplicate conjuncts from AND-connected clauses.
func eliminateCommonSubexpressions(where PS.Expr) PS.Expr {
	if where == nil {
		return nil
	}
	conjuncts := RE.SplitAnd(where)
	if len(conjuncts) <= 1 {
		return where
	}
	seen := make(map[string]bool, len(conjuncts))
	unique := make([]PS.Expr, 0, len(conjuncts))
	for _, c := range conjuncts {
		h := exprHash(c)
		if !seen[h] {
			seen[h] = true
			unique = append(unique, c)
		}
		// Skip duplicate — identical expression already present.
	}
	if len(unique) == 0 {
		return nil
	}
	if len(unique) == 1 {
		return unique[0]
	}
	result := unique[0]
	for _, u := range unique[1:] {
		result = &PS.BinaryExpr{
			Left:  result,
			Op:    LX.T_AND,
			Right: u,
		}
	}
	return result
}

// hasAnyAggregate checks if any column in the select list contains
// an aggregate function.
