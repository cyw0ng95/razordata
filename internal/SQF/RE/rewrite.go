package RE

import (
	"fmt"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func Rewrite(stmt PS.Stmt) (PS.Stmt, error) {
	switch s := stmt.(type) {
	case *PS.Select:
		return rewriteSelect(s), nil
	case *PS.CompoundStmt:
		return rewriteCompound(s)
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
	case *PS.AnalyzeStmt, *PS.VacuumStmt, *PS.PragmaStmt, *PS.ExplainStmt, *PS.TruncateStmt, *PS.ReindexStmt, *PS.DropViewStmt, *PS.DropTriggerStmt, *PS.DropIndexStmt, *PS.CreateIndexStmt, *PS.CreateViewStmt, *PS.CreateMatViewStmt, *PS.DropMatViewStmt, *PS.RefreshMatViewStmt, *PS.TriggerStmt, *PS.AlterTableStmt, *PS.WithStmt, *PS.AttachStmt, *PS.DetachStmt, *PS.CreateVirtualTableStmt:
		return s, nil
	}
	return nil, fmt.Errorf("re: unknown statement type %T", stmt)
}

func rewriteSelect(s *PS.Select) *PS.Select {
	cols, colsChanged := cloneExprSlice(s.Cols)
	where, whereChanged := RewriteExpr(s.Where)
	orderBy, obChanged := cloneOrderBy(s.OrderBy)
	limit, limitChanged := RewriteExpr(s.Limit)
	offset, offsetChanged := RewriteExpr(s.Offset)
	having, havingChanged := RewriteExpr(s.Having)
	groupBy, gbChanged := cloneExprSlice(s.GroupBy)

	if !colsChanged && !whereChanged && !obChanged && !limitChanged && !offsetChanged && !havingChanged && !gbChanged {
		return s
	}
	out := *s
	out.Cols = cols
	out.Where = where
	out.OrderBy = orderBy
	out.Limit = limit
	out.Offset = offset
	out.Having = having
	out.GroupBy = groupBy
	return &out
}

func rewriteCompound(s *PS.CompoundStmt) (PS.Stmt, error) {
	out := *s
	left, err := Rewrite(s.Left)
	if err != nil {
		return nil, err
	}
	out.Left = left
	right, err := Rewrite(s.Right)
	if err != nil {
		return nil, err
	}
	out.Right = right
	out.OrderBy, _ = cloneOrderBy(s.OrderBy)
	out.Limit, _ = RewriteExpr(s.Limit)
	out.Offset, _ = RewriteExpr(s.Offset)
	return &out, nil
}

func rewriteInsert(s *PS.Insert) *PS.Insert {
	out := *s
	out.Cols = append([]string(nil), s.Cols...)
	out.Values = make([][]PS.Expr, len(s.Values))
	for i, row := range s.Values {
		cp := make([]PS.Expr, len(row))
		for j, c := range row {
			cp[j], _ = RewriteExpr(c)
		}
		out.Values[i] = cp
	}
	return &out
}

func rewriteUpdate(s *PS.Update) *PS.Update {
	out := *s
	out.Where, _ = RewriteExpr(s.Where)
	out.Set = make([]PS.Pair, len(s.Set))
	for i, p := range s.Set {
		out.Set[i] = PS.Pair{Col: p.Col, Val: RewriteExprUnchanged(p.Val)}
	}
	return &out
}

func rewriteDelete(s *PS.Delete) *PS.Delete {
	out := *s
	out.Where, _ = RewriteExpr(s.Where)
	return &out
}

func rewriteCreateTable(s *PS.CreateTable) *PS.CreateTable {
	out := *s
	out.Cols = make([]PS.ColDef, len(s.Cols))
	for i, c := range s.Cols {
		cp := c
		cp.Default, _ = RewriteExpr(c.Default)
		out.Cols[i] = cp
	}
	return &out
}

func rewriteDropTable(s *PS.DropTable) *PS.DropTable {
	out := *s
	return &out
}

func cloneExprSlice(in []PS.Expr) ([]PS.Expr, bool) {
	if in == nil {
		return nil, false
	}
	changed := false
	out := make([]PS.Expr, len(in))
	for i, e := range in {
		rewritten, rc := RewriteExpr(e)
		out[i] = rewritten
		if rc {
			changed = true
		}
	}
	if !changed {
		return in, false
	}
	return out, true
}

func cloneOrderBy(in []PS.OrderItem) ([]PS.OrderItem, bool) {
	if in == nil {
		return nil, false
	}
	changed := false
	out := make([]PS.OrderItem, len(in))
	for i, o := range in {
		expr, ec := RewriteExpr(o.Expr)
		out[i] = PS.OrderItem{Expr: expr, Desc: o.Desc, Collation: o.Collation, NullsOrder: o.NullsOrder}
		if ec {
			changed = true
		}
	}
	if !changed {
		return in, false
	}
	return out, true
}

// RewriteExpr returns a simplified expression. The original tree is not mutated.
// REQ002109: returns (newExpr, changed) where changed=true if the expression was modified.
func RewriteExpr(e PS.Expr) (PS.Expr, bool) {
	if e == nil {
		return nil, false
	}
	switch v := e.(type) {
	case *PS.NumberLiteral, *PS.FloatLiteral, *PS.StringLiteral,
		*PS.BoolLiteral, *PS.NullLiteral, *PS.Ident, *PS.QualifiedName,
		*PS.Param, *PS.StarExpr:
		return v, false
	case *PS.UnaryExpr:
		return simplifyUnary(v)
	case *PS.BinaryExpr:
		return simplifyBinary(v)
	case *PS.AggregateFunc:
		arg, argChanged := RewriteExpr(v.Arg)
		if argChanged {
			cp := *v
			cp.Arg = arg
			return &cp, true
		}
		return v, false
	case *PS.FunctionCall:
		args := make([]PS.Expr, len(v.Args))
		changed := false
		for i, a := range v.Args {
			ra, rc := RewriteExpr(a)
			args[i] = ra
			if rc {
				changed = true
			}
		}
		if !changed {
			return v, false
		}
		cp := *v
		cp.Args = args
		return &cp, true
	case *PS.AliasedExpr:
		inner, innerChanged := RewriteExpr(v.Expr)
		if innerChanged {
			cp := *v
			cp.Expr = inner
			return &cp, true
		}
		return v, false
	case *PS.CastExpr:
		inner, innerChanged := RewriteExpr(v.Expr)
		if innerChanged {
			cp := *v
			cp.Expr = inner
			return &cp, true
		}
		return v, false
	case *PS.ListExpr:
		items := make([]PS.Expr, len(v.Items))
		changed := false
		for i, it := range v.Items {
			ri, rc := RewriteExpr(it)
			items[i] = ri
			if rc {
				changed = true
			}
		}
		if !changed {
			return v, false
		}
		cp := *v
		cp.Items = items
		return &cp, true
	case *PS.BetweenExpr:
		expr, ec := RewriteExpr(v.Expr)
		low, lc := RewriteExpr(v.Low)
		high, hc := RewriteExpr(v.High)
		if ec || lc || hc {
			cp := *v
			cp.Expr = expr
			cp.Low = low
			cp.High = high
			return &cp, true
		}
		return v, false
	case *PS.CaseExpr:
		expr, ec := RewriteExpr(v.Expr)
		elseExpr, elc := RewriteExpr(v.Else)
		whens := make([]PS.WhenClause, len(v.WhenList))
		wc := false
		for i, w := range v.WhenList {
			cond, cc := RewriteExpr(w.Cond)
			then, tc := RewriteExpr(w.Then)
			whens[i] = PS.WhenClause{Cond: cond, Then: then}
			if cc || tc {
				wc = true
			}
		}
		if ec || elc || wc {
			cp := *v
			cp.Expr = expr
			cp.Else = elseExpr
			cp.WhenList = whens
			return &cp, true
		}
		return v, false
	case *PS.InExpr:
		return simplifyIn(v)
	case *PS.ExistsExpr:
		return v, false
	case *PS.SubqueryExpr:
		return v, false
	}
	return e, false
}

func simplifyUnary(v *PS.UnaryExpr) (PS.Expr, bool) {
	operand, operandChanged := RewriteExpr(v.Operand)
	var folded PS.Expr
	switch v.Op {
	case LX.T_MINUS:
		folded = constantFoldUnaryMinus(operand)
	case LX.T_PLUS:
		folded = operand
	case LX.T_NOT:
		if _, isNull := operand.(*PS.NullLiteral); isNull {
			if operandChanged {
				cp := *v
				cp.Operand = operand
				return &cp, true
			}
			return v, false
		}
		folded = constantFoldNot(operand)
		if folded == nil {
			if b, ok := operand.(*PS.BoolLiteral); ok {
				folded = &PS.BoolLiteral{Val: !b.Val}
			} else if u, ok := operand.(*PS.UnaryExpr); ok && u.Op == LX.T_NOT {
				return u.Operand, true
			}
		}
	}
	if folded != nil {
		return folded, true
	}
	if operandChanged {
		cp := *v
		cp.Operand = operand
		return &cp, true
	}
	return v, false
}

func simplifyBinary(v *PS.BinaryExpr) (PS.Expr, bool) {
	left, leftChanged := RewriteExpr(v.Left)
	right, rightChanged := RewriteExpr(v.Right)
	if folded := constantFoldBinary(v.Op, left, right); folded != nil {
		return folded, true
	}
	if v.Op == LX.T_AND {
		if l, ok := left.(*PS.BoolLiteral); ok {
			if l.Val {
				return right, true
			}
			return left, true
		}
		if r, ok := right.(*PS.BoolLiteral); ok {
			if r.Val {
				return left, true
			}
			return right, true
		}
		if equalLiteral(left, right) && (isLiteral(left) || isLiteral(right)) {
			if _, ok := left.(*PS.NullLiteral); ok {
				return left, true
			}
			return left, true
		}
	}
	if v.Op == LX.T_OR {
		if l, ok := left.(*PS.BoolLiteral); ok {
			if l.Val {
				return left, true
			}
			return right, true
		}
		if r, ok := right.(*PS.BoolLiteral); ok {
			if r.Val {
				return right, true
			}
			return left, true
		}
		if equalLiteral(left, right) && (isLiteral(left) || isLiteral(right)) {
			return left, true
		}
	}
	if v.Op == LX.T_EQ {
		if isLiteral(left) && isLiteral(right) && equalLiteral(left, right) {
			return &PS.BoolLiteral{Val: true}, true
		}
	}
	if v.Op == LX.T_NE {
		if isLiteral(left) && isLiteral(right) && equalLiteral(left, right) {
			return &PS.BoolLiteral{Val: false}, true
		}
	}
	if leftChanged || rightChanged {
		cp := *v
		cp.Left = left
		cp.Right = right
		return &cp, true
	}
	return v, false
}

func simplifyIn(v *PS.InExpr) (PS.Expr, bool) {
	target, targetChanged := RewriteExpr(v.Expr)
	if v.Subquery == nil {
		list := make([]PS.Expr, len(v.List))
		changed := false
		for i, it := range v.List {
			ri, rc := RewriteExpr(it)
			list[i] = ri
			if rc {
				changed = true
			}
		}
		if !targetChanged && !changed {
			return v, false
		}
		cp := *v
		cp.Expr = target
		cp.List = list
		return &cp, true
	}
	folded, ok := flattenSubquery(v.Subquery)
	if ok {
		cp := *v
		cp.Expr = target
		cp.List = folded
		cp.Subquery = nil
		return &cp, true
	}
	if targetChanged {
		cp := *v
		cp.Expr = target
		return &cp, true
	}
	return v, false
}

func RewriteExprUnchanged(e PS.Expr) PS.Expr {
	result, _ := RewriteExpr(e)
	return result
}

func cloneExprSliceUnchanged(in []PS.Expr) []PS.Expr {
	if in == nil {
		return nil
	}
	out := make([]PS.Expr, len(in))
	for i, e := range in {
		out[i] = RewriteExprUnchanged(e)
	}
	return out
}

func cloneOrderByUnchanged(in []PS.OrderItem) []PS.OrderItem {
	if in == nil {
		return nil
	}
	out := make([]PS.OrderItem, len(in))
	for i, o := range in {
		out[i] = PS.OrderItem{Expr: RewriteExprUnchanged(o.Expr), Desc: o.Desc, Collation: o.Collation, NullsOrder: o.NullsOrder}
	}
	return out
}

var _ = cloneExprSliceUnchanged
var _ = cloneOrderByUnchanged

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

func constantFoldBinary(op LX.TokenType, left, right PS.Expr) PS.Expr {
	if !isLiteral(left) || !isLiteral(right) {
		return nil
	}
	// Single type switch on left, then nested switch on right.
	// Reduces type assertions from 8 to 2. REQ001166.
	switch l := left.(type) {
	case *PS.NullLiteral:
		return nil
	case *PS.NumberLiteral:
		switch r := right.(type) {
		case *PS.NullLiteral:
			return nil
		case *PS.NumberLiteral:
			switch op {
			case LX.T_PLUS, LX.T_MINUS, LX.T_STAR, LX.T_SLASH:
				return foldIntInt(op, l.Val, r.Val)
			case LX.T_MOD:
				if r.Val == 0 {
					return nil
				}
				return &PS.NumberLiteral{Val: l.Val % r.Val}
			case LX.T_EQ, LX.T_NE, LX.T_LT, LX.T_LE, LX.T_GT, LX.T_GE:
				return foldCompare(op, left, right, l, nil, nil, nil, r, nil, nil, nil)
			}
		case *PS.FloatLiteral:
			switch op {
			case LX.T_PLUS, LX.T_MINUS, LX.T_STAR, LX.T_SLASH:
				return foldFloatFloat(op, float64(l.Val), r.Val)
			case LX.T_EQ, LX.T_NE, LX.T_LT, LX.T_LE, LX.T_GT, LX.T_GE:
				return foldCompare(op, left, right, l, nil, nil, nil, nil, r, nil, nil)
			}
		}
	case *PS.FloatLiteral:
		switch right.(type) {
		case *PS.NullLiteral:
			return nil
		}
	case *PS.StringLiteral:
		switch r := right.(type) {
		case *PS.NullLiteral:
			return nil
		case *PS.StringLiteral:
			switch op {
			case LX.T_EQ, LX.T_NE, LX.T_LT, LX.T_LE, LX.T_GT, LX.T_GE:
				return foldCompare(op, left, right, nil, nil, l, nil, nil, nil, r, nil)
			}
		}
	case *PS.BoolLiteral:
		switch r := right.(type) {
		case *PS.NullLiteral:
			return nil
		case *PS.BoolLiteral:
			switch op {
			case LX.T_AND:
				return &PS.BoolLiteral{Val: l.Val && r.Val}
			case LX.T_OR:
				return &PS.BoolLiteral{Val: l.Val || r.Val}
			case LX.T_EQ, LX.T_NE, LX.T_LT, LX.T_LE, LX.T_GT, LX.T_GE:
				return foldCompare(op, left, right, nil, nil, nil, l, nil, nil, nil, r)
			}
		}
	}
	return nil
}
	func foldIntInt(op LX.TokenType, a, b int64) PS.Expr {
	switch op {
	case LX.T_PLUS:
		return &PS.NumberLiteral{Val: a + b}
	case LX.T_MINUS:
		return &PS.NumberLiteral{Val: a - b}
	case LX.T_STAR:
		return &PS.NumberLiteral{Val: a * b}
	case LX.T_SLASH:
		if b == 0 {
			return nil
		}
		return &PS.NumberLiteral{Val: a / b}
	}
	return nil
}

func foldFloatFloat(op LX.TokenType, a, b float64) PS.Expr {
	switch op {
	case LX.T_PLUS:
		if ln, ok := intFromFloat(a); ok {
			if rn, ok := intFromFloat(b); ok {
				return &PS.NumberLiteral{Val: ln + rn}
			}
		}
		return &PS.FloatLiteral{Val: a + b}
	case LX.T_MINUS:
		if ln, ok := intFromFloat(a); ok {
			if rn, ok := intFromFloat(b); ok {
				return &PS.NumberLiteral{Val: ln - rn}
			}
		}
		return &PS.FloatLiteral{Val: a - b}
	case LX.T_STAR:
		if ln, ok := intFromFloat(a); ok {
			if rn, ok := intFromFloat(b); ok {
				return &PS.NumberLiteral{Val: ln * rn}
			}
		}
		return &PS.FloatLiteral{Val: a * b}
	case LX.T_SLASH:
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

func foldCompare(op LX.TokenType, left, right PS.Expr, ln *PS.NumberLiteral, lf *PS.FloatLiteral, ls *PS.StringLiteral, lb *PS.BoolLiteral, rn *PS.NumberLiteral, rf *PS.FloatLiteral, rs *PS.StringLiteral, rb *PS.BoolLiteral) PS.Expr {
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
	case LX.T_EQ:
		return &PS.BoolLiteral{Val: cmp == 0}
	case LX.T_NE:
		return &PS.BoolLiteral{Val: cmp != 0}
	case LX.T_LT:
		return &PS.BoolLiteral{Val: cmp < 0}
	case LX.T_LE:
		return &PS.BoolLiteral{Val: cmp <= 0}
	case LX.T_GT:
		return &PS.BoolLiteral{Val: cmp > 0}
	case LX.T_GE:
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

// flattenSubquery returns the literal list if the subquery is known to
// evaluate to a static literal set. Otherwise returns ok=false and the
// planner falls back to runtime evaluation.
//
// Recognized shapes (REQ001168):
//   - SELECT <literals> with no FROM and no WHERE          (returns the literal list)
//   - SELECT <literals> FROM (VALUES <row>, <row>, ...)     (returns flattened VALUES rows —
//     currently dead code: parser does not yet accept VALUES as a FROM subquery.
//     Retained for the day the parser is extended; the SubqueryFrom field is wired.)
//   - SELECT <literals> FROM <t> WHERE <known-false>       (returns empty list, ok=true)
//
// Not recognized (returns ok=false; falls back to runtime):
//   - SELECT with non-literal projections
//   - SELECT with joins, GROUP BY, HAVING, ORDER BY, LIMIT, OFFSET
//   - PK-equality flattening (would require schema context; not in scope here).
func flattenSubquery(stmt PS.Stmt) ([]PS.Expr, bool) {
	sel, ok := stmt.(*PS.Select)
	if !ok {
		return nil, false
	}
	if hasNonTrivialClauses(sel) {
		return nil, false
	}
	if sel.From == "" && sel.Where == nil {
		return flattenLiterals(sel.Cols)
	}
	if vs, isValues := sel.SubqueryFrom.(*PS.ValuesStmt); isValues && sel.From == "" {
		return flattenValues(vs)
	}
	if sel.From != "" && sel.Where != nil && isKnownFalse(sel.Where) {
		return []PS.Expr{}, true
	}
	return nil, false
}

// hasNonTrivialClauses reports whether the SELECT has clauses that prevent
// literal flattening (joins, grouping, ordering, limits, distinct).
func hasNonTrivialClauses(sel *PS.Select) bool {
	if sel.FromAlias != "" {
		return true
	}
	if len(sel.Joins) > 0 {
		return true
	}
	if sel.Distinct {
		return true
	}
	if len(sel.GroupBy) > 0 || sel.Having != nil {
		return true
	}
	if len(sel.OrderBy) > 0 || sel.Limit != nil || sel.Offset != nil {
		return true
	}
	return false
}

// flattenLiterals returns the literal projection list, or ok=false if any
// column is not a literal.
func flattenLiterals(cols []PS.Expr) ([]PS.Expr, bool) {
	out := make([]PS.Expr, 0, len(cols))
	for _, c := range cols {
		if !isLiteral(c) {
			return nil, false
		}
		out = append(out, c)
	}
	return out, true
}

// flattenValues extracts a flat literal list from a `FROM (VALUES ...)` clause.
// Each VALUES row must produce exactly one literal; multi-column VALUES rows
// are rejected to avoid silently reshaping the projection arity.
func flattenValues(vs *PS.ValuesStmt) ([]PS.Expr, bool) {
	if len(vs.Rows) == 0 {
		return []PS.Expr{}, true
	}
	out := make([]PS.Expr, 0, len(vs.Rows))
	for _, row := range vs.Rows {
		if len(row) != 1 || !isLiteral(row[0]) {
			return nil, false
		}
		out = append(out, row[0])
	}
	return out, true
}

// isKnownFalse reports whether the WHERE expression is statically false
// after constant folding. Recognized shapes: `1=0`, `0=1`, `1<>1`, `0<>0`,
// `FALSE`, `NOT TRUE`. Anything else (including unknown identifiers) is
// treated as not-known-false to avoid false positives.
func isKnownFalse(e PS.Expr) bool {
	switch v := e.(type) {
	case *PS.BoolLiteral:
		return !v.Val
	case *PS.UnaryExpr:
		if v.Op == LX.T_NOT {
			if inner, ok := v.Operand.(*PS.BoolLiteral); ok {
				return inner.Val
			}
		}
	case *PS.BinaryExpr:
		if v.Op != LX.T_EQ && v.Op != LX.T_NE {
			return false
		}
		ln, lok := v.Left.(*PS.NumberLiteral)
		rn, rok := v.Right.(*PS.NumberLiteral)
		if lok && rok {
			if v.Op == LX.T_EQ {
				return ln.Val != rn.Val
			}
			return ln.Val == rn.Val
		}
	}
	return false
}

// SplitAnd returns the top-level AND conjuncts of e. If e is not
// an AND chain, returns [e]. Used by the planner to push each
// conjunct into its own Filter so the first one can sit closest
// to the scan.
func SplitAnd(e PS.Expr) []PS.Expr {
	if e == nil {
		return nil
	}
	if b, ok := e.(*PS.BinaryExpr); ok && b.Op == LX.T_AND {
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
	if b, ok := e.(*PS.BinaryExpr); ok && b.Op == LX.T_OR {
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
