package QP

import (
	"strconv"

	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// ImplicitCastEliminationPass rewrites predicates that compare a column
// to a string literal whose content is purely numeric, replacing the
// StringLiteral with a NumberLiteral. REQ002263.
type ImplicitCastEliminationPass struct{}

func (p *ImplicitCastEliminationPass) Name() string { return "implicit_cast_elimination" }

func (p *ImplicitCastEliminationPass) Apply(qp *QueryPlan) error {
	if qp == nil || qp.RootIdx < 0 {
		return nil
	}
	qp.walkBottomUp(func(idx int, node *PlanNode) bool {
		for i, e := range node.Exprs {
			if expr, ok := e.(PS.Expr); ok {
				node.Exprs[i] = rewriteNumericStrings(expr)
			}
		}
		return true
	})
	return nil
}

func looksLikeNumber(s string) bool {
	if len(s) == 0 {
		return false
	}
	start := 0
	if s[0] == '-' {
		if len(s) == 1 {
			return false
		}
		start = 1
	}
	for i := start; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func rewriteNumericStrings(e PS.Expr) PS.Expr {
	return rewriteExpr(e).(PS.Expr)
}

func rewriteExpr(e PS.Expr) PS.Expr {
	switch n := e.(type) {
	case *PS.StringLiteral:
		if looksLikeNumber(n.Val) {
			if num, err := strconv.ParseInt(n.Val, 10, 64); err == nil {
				return &PS.NumberLiteral{Loc: n.Loc, Val: num}
			}
		}
		return n
	case *PS.BinaryExpr:
		l := rewriteExpr(n.Left)
		r := rewriteExpr(n.Right)
		if l != n.Left || r != n.Right {
			return &PS.BinaryExpr{Loc: n.Loc, Op: n.Op, Left: l, Right: r, Escape: n.Escape}
		}
		return n
	case *PS.UnaryExpr:
		op := rewriteExpr(n.Operand)
		if op != n.Operand {
			return &PS.UnaryExpr{Loc: n.Loc, Op: n.Op, Operand: op}
		}
		return n
	case *PS.FunctionCall:
		changed := false
		newArgs := make([]PS.Expr, len(n.Args))
		copy(newArgs, n.Args)
		for i, a := range n.Args {
			newArgs[i] = rewriteExpr(a)
			if newArgs[i] != a {
				changed = true
			}
		}
		if changed {
			return &PS.FunctionCall{Loc: n.Loc, Name: n.Name, Args: newArgs}
		}
		return n
	case *PS.CastExpr:
		expr := rewriteExpr(n.Expr)
		if expr != n.Expr {
			return &PS.CastExpr{Loc: n.Loc, Expr: expr, Type: n.Type}
		}
		return n
	case *PS.BetweenExpr:
		expr := rewriteExpr(n.Expr)
		low := rewriteExpr(n.Low)
		high := rewriteExpr(n.High)
		if expr != n.Expr || low != n.Low || high != n.High {
			return &PS.BetweenExpr{Loc: n.Loc, Expr: expr, Low: low, High: high}
		}
		return n
	case *PS.InExpr:
		expr := rewriteExpr(n.Expr)
		changed := expr != n.Expr
		newList := make([]PS.Expr, len(n.List))
		copy(newList, n.List)
		for i, it := range n.List {
			newList[i] = rewriteExpr(it)
			if newList[i] != it {
				changed = true
			}
		}
		if changed {
			return &PS.InExpr{Loc: n.Loc, Expr: expr, List: newList, Subquery: n.Subquery}
		}
		return n
	}
	return e
}
