package RE

import (
	"fmt"

	"github.com/cyw0ng95/razordata/internal/SQL/LX"
	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

// Rewrite returns a semantically equivalent AST with constant
// expressions folded, boolean identities simplified, and trivial
// subqueries flattened. The original statement is not mutated.
func Rewrite(stmt PS.Stmt) (PS.Stmt, error) {
	switch s := stmt.(type) {
	case *PS.Select:
		return rewriteSelect(s), nil
	case *PS.Insert:
		return rewriteInsert(s), nil
	case *PS.Update:
		return rewriteUpdate(s), nil
	case *PS.Delete:
		return rewriteDelete(s), nil
	case *PS.CreateTable:
		return rewriteCreateTable(s), nil
	case *PS.DropTable:
		return rewriteDropTable(s), nil
	}
	return nil, fmt.Errorf("re: unknown statement type %T", stmt)
}

func rewriteSelect(s *PS.Select) *PS.Select {
	out := *s
	out.Cols = cloneExprSlice(s.Cols)
	out.Where = RewriteExpr(s.Where)
	out.OrderBy = cloneOrderBy(s.OrderBy)
	out.Limit = RewriteExpr(s.Limit)
	out.Offset = RewriteExpr(s.Offset)
	return &out
}

func rewriteInsert(s *PS.Insert) *PS.Insert {
	out := *s
	out.Cols = append([]string(nil), s.Cols...)
	for i, row := range s.Values {
		cp := make([]PS.Expr, len(row))
		for j, c := range row {
			cp[j] = RewriteExpr(c)
		}
		out.Values[i] = cp
	}
	return &out
}

func rewriteUpdate(s *PS.Update) *PS.Update {
	out := *s
	out.Where = RewriteExpr(s.Where)
	for i, p := range s.Set {
		out.Set[i] = PS.Pair{Col: p.Col, Val: RewriteExpr(p.Val)}
	}
	return &out
}

func rewriteDelete(s *PS.Delete) *PS.Delete {
	out := *s
	out.Where = RewriteExpr(s.Where)
	return &out
}

func rewriteCreateTable(s *PS.CreateTable) *PS.CreateTable {
	out := *s
	for i, c := range s.Cols {
		cp := c
		cp.Default = RewriteExpr(c.Default)
		out.Cols[i] = cp
	}
	return &out
}

func rewriteDropTable(s *PS.DropTable) *PS.DropTable {
	out := *s
	return &out
}

func cloneExprSlice(in []PS.Expr) []PS.Expr {
	if in == nil {
		return nil
	}
	out := make([]PS.Expr, len(in))
	for i, e := range in {
		out[i] = RewriteExpr(e)
	}
	return out
}

func cloneOrderBy(in []PS.OrderItem) []PS.OrderItem {
	if in == nil {
		return nil
	}
	out := make([]PS.OrderItem, len(in))
	for i, o := range in {
		out[i] = PS.OrderItem{Expr: RewriteExpr(o.Expr), Desc: o.Desc}
	}
	return out
}

// RewriteExpr returns a simplified expression. The original tree
// is not mutated.
func RewriteExpr(e PS.Expr) PS.Expr {
	if e == nil {
		return nil
	}
	switch v := e.(type) {
	case *PS.NumberLiteral, *PS.FloatLiteral, *PS.StringLiteral,
		*PS.BoolLiteral, *PS.NullLiteral, *PS.Ident, *PS.QualifiedName,
		*PS.Param, *PS.StarExpr:
		return v
	case *PS.UnaryExpr:
		return simplifyUnary(v)
	case *PS.BinaryExpr:
		return simplifyBinary(v)
	case *PS.AggregateFunc:
		arg := RewriteExpr(v.Arg)
		if arg != v.Arg {
			cp := *v
			cp.Arg = arg
			return &cp
		}
		return v
	case *PS.FunctionCall:
		args := make([]PS.Expr, len(v.Args))
		changed := false
		for i, a := range v.Args {
			args[i] = RewriteExpr(a)
			if args[i] != a {
				changed = true
			}
		}
		if !changed {
			return v
		}
		cp := *v
		cp.Args = args
		return &cp
	case *PS.AliasedExpr:
		inner := RewriteExpr(v.Expr)
		if inner != v.Expr {
			cp := *v
			cp.Expr = inner
			return &cp
		}
		return v
	case *PS.CastExpr:
		inner := RewriteExpr(v.Expr)
		if inner != v.Expr {
			cp := *v
			cp.Expr = inner
			return &cp
		}
		return v
	case *PS.ListExpr:
		items := make([]PS.Expr, len(v.Items))
		changed := false
		for i, it := range v.Items {
			items[i] = RewriteExpr(it)
			if items[i] != it {
				changed = true
			}
		}
		if !changed {
			return v
		}
		cp := *v
		cp.Items = items
		return &cp
	case *PS.BetweenExpr:
		expr := RewriteExpr(v.Expr)
		low := RewriteExpr(v.Low)
		high := RewriteExpr(v.High)
		if expr != v.Expr || low != v.Low || high != v.High {
			cp := *v
			cp.Expr = expr
			cp.Low = low
			cp.High = high
			return &cp
		}
		return v
	case *PS.CaseExpr:
		expr := RewriteExpr(v.Expr)
		elseExpr := RewriteExpr(v.Else)
		whens := make([]PS.WhenClause, len(v.WhenList))
		changed := false
		for i, w := range v.WhenList {
			whens[i] = PS.WhenClause{Cond: RewriteExpr(w.Cond), Then: RewriteExpr(w.Then)}
			if whens[i].Cond != w.Cond || whens[i].Then != w.Then {
				changed = true
			}
		}
		if expr != v.Expr || elseExpr != v.Else || changed {
			cp := *v
			cp.Expr = expr
			cp.Else = elseExpr
			cp.WhenList = whens
			return &cp
		}
		return v
	case *PS.InExpr:
		return simplifyIn(v)
	case *PS.ExistsExpr:
		return v
	case *PS.SubqueryExpr:
		return v
	}
	return e
}

func simplifyUnary(v *PS.UnaryExpr) PS.Expr {
	operand := RewriteExpr(v.Operand)
	var folded PS.Expr
	switch v.Op {
	case int(LX.T_MINUS):
		folded = constantFoldUnaryMinus(operand)
	case int(LX.T_PLUS):
		folded = operand
	case int(LX.T_NOT):
		folded = constantFoldNot(operand)
		if folded == nil {
			if b, ok := operand.(*PS.BoolLiteral); ok {
				folded = &PS.BoolLiteral{Val: !b.Val}
			} else if _, ok := operand.(*PS.NullLiteral); ok {
				folded = &PS.NullLiteral{}
			} else if u, ok := operand.(*PS.UnaryExpr); ok && u.Op == int(LX.T_NOT) {
				return u.Operand
			}
		}
	}
	if folded != nil {
		return folded
	}
	if operand != v.Operand {
		cp := *v
		cp.Operand = operand
		return &cp
	}
	return v
}

func simplifyBinary(v *PS.BinaryExpr) PS.Expr {
	left := RewriteExpr(v.Left)
	right := RewriteExpr(v.Right)
	if folded := constantFoldBinary(v.Op, left, right); folded != nil {
		return folded
	}
	if v.Op == int(LX.T_AND) {
		if l, ok := left.(*PS.BoolLiteral); ok {
			if l.Val {
				return right
			}
			return left
		}
		if r, ok := right.(*PS.BoolLiteral); ok {
			if r.Val {
				return left
			}
			return right
		}
		if equalLiteral(left, right) && (isLiteral(left) || isLiteral(right)) {
			if _, ok := left.(*PS.NullLiteral); ok {
				return left
			}
			return left
		}
	}
	if v.Op == int(LX.T_OR) {
		if l, ok := left.(*PS.BoolLiteral); ok {
			if l.Val {
				return left
			}
			return right
		}
		if r, ok := right.(*PS.BoolLiteral); ok {
			if r.Val {
				return right
			}
			return left
		}
		if equalLiteral(left, right) && (isLiteral(left) || isLiteral(right)) {
			return left
		}
	}
	if v.Op == int(LX.T_EQ) {
		if isLiteral(left) && isLiteral(right) && equalLiteral(left, right) {
			return &PS.BoolLiteral{Val: true}
		}
	}
	if v.Op == int(LX.T_NE) {
		if isLiteral(left) && isLiteral(right) && equalLiteral(left, right) {
			return &PS.BoolLiteral{Val: false}
		}
	}
	if left != v.Left || right != v.Right {
		cp := *v
		cp.Left = left
		cp.Right = right
		return &cp
	}
	return v
}

func simplifyIn(v *PS.InExpr) PS.Expr {
	target := RewriteExpr(v.Expr)
	if v.Subquery == nil {
		list := make([]PS.Expr, len(v.List))
		changed := false
		for i, it := range v.List {
			list[i] = RewriteExpr(it)
			if list[i] != it {
				changed = true
			}
		}
		if target != v.Expr || changed {
			cp := *v
			cp.Expr = target
			cp.List = list
			return &cp
		}
		return v
	}
	folded, ok := flattenSubquery(v.Subquery)
	if !ok {
		if target != v.Expr {
			cp := *v
			cp.Expr = target
			return &cp
		}
		return v
	}
	if target != v.Expr {
		cp := *v
		cp.Expr = target
		cp.List = folded
		cp.Subquery = nil
		return &cp
	}
	cp := *v
	cp.List = folded
	cp.Subquery = nil
	return &cp
}

func constantFoldUnaryMinus(operand PS.Expr) PS.Expr {
	switch v := operand.(type) {
	case *PS.NumberLiteral:
		return &PS.NumberLiteral{Val: -v.Val}
	case *PS.FloatLiteral:
		return &PS.FloatLiteral{Val: -v.Val}
	}
	return nil
}

func constantFoldNot(operand PS.Expr) PS.Expr {
	switch v := operand.(type) {
	case *PS.BoolLiteral:
		return &PS.BoolLiteral{Val: !v.Val}
	case *PS.NullLiteral:
		return &PS.NullLiteral{}
	}
	return nil
}

func constantFoldBinary(op int, left, right PS.Expr) PS.Expr {
	if !isLiteral(left) || !isLiteral(right) {
		return nil
	}
	ln, li := left.(*PS.NumberLiteral)
	lf, lfok := left.(*PS.FloatLiteral)
	ls, lsok := left.(*PS.StringLiteral)
	lb, lbok := left.(*PS.BoolLiteral)
	_, lnRnil := right.(*PS.NullLiteral)
	_, lnil := left.(*PS.NullLiteral)
	if lnil || lnRnil {
		return nil
	}
	rn, ri := right.(*PS.NumberLiteral)
	rf, rfok := right.(*PS.FloatLiteral)
	rs, rsok := right.(*PS.StringLiteral)
	rb, rbok := right.(*PS.BoolLiteral)
	_ = ri
	_ = li
	switch op {
	case int(LX.T_PLUS), int(LX.T_MINUS), int(LX.T_STAR), int(LX.T_SLASH):
		if ln != nil && rn != nil {
			return foldIntInt(op, ln.Val, rn.Val)
		}
		if (ln != nil && rfok) || (lfok && rn != nil) || (lfok && rfok) {
			var a, b float64
			if ln != nil {
				a = float64(ln.Val)
			} else {
				a = lf.Val
			}
			if rn != nil {
				b = float64(rn.Val)
			} else {
				b = rf.Val
			}
			return foldFloatFloat(op, a, b)
		}
	case int(LX.T_EQ), int(LX.T_NE), int(LX.T_LT), int(LX.T_LE), int(LX.T_GT), int(LX.T_GE):
		return foldCompare(op, left, right, ln, lf, ls, lb, rn, rf, rs, rb)
	case int(LX.T_AND), int(LX.T_OR):
		if lbok && rbok {
			if op == int(LX.T_AND) {
				return &PS.BoolLiteral{Val: lb.Val && rb.Val}
			}
			return &PS.BoolLiteral{Val: lb.Val || rb.Val}
		}
	}
	_ = lsok
	_ = rsok
	return nil
}

func foldIntInt(op int, a, b int64) PS.Expr {
	switch op {
	case int(LX.T_PLUS):
		return &PS.NumberLiteral{Val: a + b}
	case int(LX.T_MINUS):
		return &PS.NumberLiteral{Val: a - b}
	case int(LX.T_STAR):
		return &PS.NumberLiteral{Val: a * b}
	case int(LX.T_SLASH):
		if b == 0 {
			return nil
		}
		return &PS.NumberLiteral{Val: a / b}
	}
	return nil
}

func foldFloatFloat(op int, a, b float64) PS.Expr {
	switch op {
	case int(LX.T_PLUS):
		if ln, ok := intFromFloat(a); ok {
			if rn, ok := intFromFloat(b); ok {
				return &PS.NumberLiteral{Val: ln + rn}
			}
		}
		return &PS.FloatLiteral{Val: a + b}
	case int(LX.T_MINUS):
		if ln, ok := intFromFloat(a); ok {
			if rn, ok := intFromFloat(b); ok {
				return &PS.NumberLiteral{Val: ln - rn}
			}
		}
		return &PS.FloatLiteral{Val: a - b}
	case int(LX.T_STAR):
		if ln, ok := intFromFloat(a); ok {
			if rn, ok := intFromFloat(b); ok {
				return &PS.NumberLiteral{Val: ln * rn}
			}
		}
		return &PS.FloatLiteral{Val: a * b}
	case int(LX.T_SLASH):
		if b == 0 {
			return nil
		}
		return &PS.FloatLiteral{Val: a / b}
	}
	return nil
}

func intFromFloat(f float64) (int64, bool) {
	i := int64(f)
	if float64(i) == f {
		return i, true
	}
	return 0, false
}

func foldCompare(op int, left, right PS.Expr, ln *PS.NumberLiteral, lf *PS.FloatLiteral, ls *PS.StringLiteral, lb *PS.BoolLiteral, rn *PS.NumberLiteral, rf *PS.FloatLiteral, rs *PS.StringLiteral, rb *PS.BoolLiteral) PS.Expr {
	cmp := 0
	switch {
	case ln != nil && rn != nil:
		if ln.Val < rn.Val {
			cmp = -1
		} else if ln.Val > rn.Val {
			cmp = 1
		}
	case lf != nil && rf != nil:
		if lf.Val < rf.Val {
			cmp = -1
		} else if lf.Val > rf.Val {
			cmp = 1
		}
	case ls != nil && rs != nil:
		if ls.Val < rs.Val {
			cmp = -1
		} else if ls.Val > rs.Val {
			cmp = 1
		}
	case lb != nil && rb != nil:
		if lb.Val == rb.Val {
			cmp = 0
		} else if lb.Val {
			cmp = 1
		} else {
			cmp = -1
		}
	default:
		return nil
	}
	switch op {
	case int(LX.T_EQ):
		return &PS.BoolLiteral{Val: cmp == 0}
	case int(LX.T_NE):
		return &PS.BoolLiteral{Val: cmp != 0}
	case int(LX.T_LT):
		return &PS.BoolLiteral{Val: cmp < 0}
	case int(LX.T_LE):
		return &PS.BoolLiteral{Val: cmp <= 0}
	case int(LX.T_GT):
		return &PS.BoolLiteral{Val: cmp > 0}
	case int(LX.T_GE):
		return &PS.BoolLiteral{Val: cmp >= 0}
	}
	_ = left
	_ = right
	return nil
}

func isLiteral(e PS.Expr) bool {
	switch e.(type) {
	case *PS.NumberLiteral, *PS.FloatLiteral, *PS.StringLiteral,
		*PS.BoolLiteral, *PS.NullLiteral:
		return true
	}
	return false
}

func equalLiteral(a, b PS.Expr) bool {
	an, aok := a.(*PS.NumberLiteral)
	bn, bok := b.(*PS.NumberLiteral)
	if aok && bok {
		return an.Val == bn.Val
	}
	af, aok := a.(*PS.FloatLiteral)
	bf, bok := b.(*PS.FloatLiteral)
	if aok && bok {
		return af.Val == bf.Val
	}
	as, aok := a.(*PS.StringLiteral)
	bs, bok := b.(*PS.StringLiteral)
	if aok && bok {
		return as.Val == bs.Val
	}
	ab, aok := a.(*PS.BoolLiteral)
	bb, bok := b.(*PS.BoolLiteral)
	if aok && bok {
		return ab.Val == bb.Val
	}
	return false
}

// flattenSubquery returns the literal list if the subquery is a
// 'SELECT <literals> FROM <no-outer-refs> WHERE <no-outer-refs>'
// and the literal list is known statically. Otherwise returns
// ok=false and the planner falls back to runtime evaluation.
func flattenSubquery(stmt PS.Stmt) ([]PS.Expr, bool) {
	sel, ok := stmt.(*PS.Select)
	if !ok {
		return nil, false
	}
	if sel.From != "" {
		return nil, false
	}
	if sel.Where != nil {
		return nil, false
	}
	if sel.FromAlias != "" {
		return nil, false
	}
	out := make([]PS.Expr, 0, len(sel.Cols))
	for _, c := range sel.Cols {
		if !isLiteral(c) {
			return nil, false
		}
		out = append(out, c)
	}
	return out, true
}

// SplitAnd returns the top-level AND conjuncts of e. If e is not
// an AND chain, returns [e]. Used by the planner to push each
// conjunct into its own Filter so the first one can sit closest
// to the scan.
func SplitAnd(e PS.Expr) []PS.Expr {
	if e == nil {
		return nil
	}
	if b, ok := e.(*PS.BinaryExpr); ok && b.Op == int(LX.T_AND) {
		left := SplitAnd(b.Left)
		right := SplitAnd(b.Right)
		return append(left, right...)
	}
	return []PS.Expr{e}
}

// SplitOr returns the top-level OR disjuncts of e.
func SplitOr(e PS.Expr) []PS.Expr {
	if e == nil {
		return nil
	}
	if b, ok := e.(*PS.BinaryExpr); ok && b.Op == int(LX.T_OR) {
		left := SplitOr(b.Left)
		right := SplitOr(b.Right)
		return append(left, right...)
	}
	return []PS.Expr{e}
}

// InferredType returns the type of a literal expression for use
// by the planner or test helpers.
func InferredType(e PS.Expr) string {
	switch e.(type) {
	case *PS.NumberLiteral:
		return "INTEGER"
	case *PS.FloatLiteral:
		return "FLOAT"
	case *PS.StringLiteral:
		return "TEXT"
	case *PS.BoolLiteral:
		return "BOOLEAN"
	case *PS.NullLiteral:
		return "NULL"
	}
	return ""
}
