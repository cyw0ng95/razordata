package PF

import (
	"strconv"

	OC "github.com/cyw0ng95/razordata/internal/SQO/OC"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// ImplicitCastEliminationPass rewrites predicates that compare a column
// to a string literal whose content is purely numeric.  For example
//
//	WHERE int_col = '42'
//
// becomes
//
//	WHERE int_col = 42
//
// This lets the executor do the comparison without per-row string parsing.
// Because PF passes lack catalog type info, the pass is conservative: it
// only rewrites when the string value is unambiguously numeric.
type ImplicitCastEliminationPass struct{}

// Name implements OC.Pass.
func (p *ImplicitCastEliminationPass) Name() string {
	return "implicit_cast_elimination"
}

// Apply implements OC.Pass.
func (p *ImplicitCastEliminationPass) Apply(plan *OC.Plan, ctx *OC.Context) (*OC.Plan, error) {
	if plan == nil {
		return plan, nil
	}
	if plan.Root == nil {
		return plan, nil
	}

	plan.Root = WalkOp(plan.Root, func(op pl.Operator) pl.Operator {
		filter, ok := op.(pl.PredicateCarrier)
		if !ok || !filter.HasPredicate() {
			return op
		}
		pred, ok := filter.Predicate().(PS.Expr)
		if !ok {
			return op
		}
		newPred := rewriteNumericStrings(pred)
		if newPred != pred {
			filter.SetPredicate(newPred)
		}
		return op
	})

	return plan, nil
}

// looksLikeNumber reports whether s is a valid integer literal (optional
// leading minus, then one or more digits).
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

// rewriteNumericStrings walks the expression tree and replaces
// StringLiterals whose value is purely numeric with NumberLiterals.
// Returns the (possibly identical) expression root.
func rewriteNumericStrings(e PS.Expr) PS.Expr {
	if e == nil {
		return nil
	}
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
	case *PS.NumberLiteral:
		return n
	case *PS.FloatLiteral:
		return n
	case *PS.BoolLiteral:
		return n
	case *PS.NullLiteral:
		return n
	case *PS.Ident:
		return n
	case *PS.QualifiedName:
		return n
	case *PS.Param:
		return n
	case *PS.StarExpr:
		return n
	case *PS.BinaryExpr:
		l := rewriteExpr(n.Left)
		r := rewriteExpr(n.Right)
		esc := rewriteExpr(n.Escape)
		if l != n.Left || r != n.Right || esc != n.Escape {
			return &PS.BinaryExpr{
				Loc:    n.Loc,
				Op:     n.Op,
				Left:   l,
				Right:  r,
				Escape: esc,
			}
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
	case *PS.AggregateFunc:
		arg := rewriteExpr(n.Arg)
		sep := rewriteExpr(n.Separator)
		filt := rewriteExpr(n.Filter)
		if arg != n.Arg || sep != n.Separator || filt != n.Filter {
			return &PS.AggregateFunc{
				Loc:       n.Loc,
				Name:      n.Name,
				Arg:       arg,
				Distinct:  n.Distinct,
				Separator: sep,
				Filter:    filt,
			}
		}
		return n
	case *PS.WindowFunc:
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
			return &PS.WindowFunc{Loc: n.Loc, Name: n.Name, Args: newArgs, Over: n.Over}
		}
		return n
	case *PS.ListExpr:
		changed := false
		newItems := make([]PS.Expr, len(n.Items))
		copy(newItems, n.Items)
		for i, it := range n.Items {
			newItems[i] = rewriteExpr(it)
			if newItems[i] != it {
				changed = true
			}
		}
		if changed {
			return &PS.ListExpr{Loc: n.Loc, Items: newItems}
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
	case *PS.BetweenExpr:
		expr := rewriteExpr(n.Expr)
		low := rewriteExpr(n.Low)
		high := rewriteExpr(n.High)
		if expr != n.Expr || low != n.Low || high != n.High {
			return &PS.BetweenExpr{Loc: n.Loc, Expr: expr, Low: low, High: high}
		}
		return n
	case *PS.CastExpr:
		expr := rewriteExpr(n.Expr)
		if expr != n.Expr {
			return &PS.CastExpr{Loc: n.Loc, Expr: expr, Type: n.Type}
		}
		return n
	case *PS.AliasedExpr:
		expr := rewriteExpr(n.Expr)
		if expr != n.Expr {
			return &PS.AliasedExpr{Loc: n.Loc, Expr: expr, Alias: n.Alias}
		}
		return n
	case *PS.CaseExpr:
		expr := rewriteExpr(n.Expr)
		elseExpr := rewriteExpr(n.Else)
		changed := expr != n.Expr || elseExpr != n.Else
		newWhenList := make([]PS.WhenClause, len(n.WhenList))
		copy(newWhenList, n.WhenList)
		for i, w := range n.WhenList {
			c := rewriteExpr(w.Cond)
			t := rewriteExpr(w.Then)
			if c != w.Cond || t != w.Then {
				newWhenList[i] = PS.WhenClause{Loc: w.Loc, Cond: c, Then: t}
				changed = true
			}
		}
		if changed {
			return &PS.CaseExpr{Loc: n.Loc, Expr: expr, WhenList: newWhenList, Else: elseExpr}
		}
		return n
	case *PS.ExistsExpr:
		return n
	case *PS.SubqueryExpr:
		return n
	case *PS.IntervalLiteral:
		return n
	case *PS.RaiseFunc:
		msg := rewriteExpr(n.Message)
		if msg != n.Message {
			return &PS.RaiseFunc{Loc: n.Loc, Action: n.Action, Message: msg}
		}
		return n
	}
	return e
}
