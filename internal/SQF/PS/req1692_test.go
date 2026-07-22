package PS

import (
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// REQ001692: IN / NOT IN / GLOB / NOT GLOB / NOT LIKE must bind at
// comparison precedence (6), the same as BETWEEN — NOT at the unary
// level. When they were dispatched from parsePostfix (unary level),
// `-a NOT IN (b)` mis-parsed as `-(NOT(a IN (b)))`: the trailing
// `NOT IN (...)` was consumed as a postfix of `a` before unary minus
// wrapped the result, yielding `-(BoolValue)` which evaluates to NULL
// and filtered out every row.
//
// These tests pin the AST shape so the operator attaches to the full
// arithmetic LHS, producing `NOT((<lhs>) IN (b))`.

func whereExpr(t *testing.T, sql string) Expr {
	t.Helper()
	stmt, err := NewParser(sql).Parse()
	if err != nil {
		t.Fatalf("parse %q: %v", sql, err)
	}
	sel, ok := stmt.(*Select)
	if !ok {
		t.Fatalf("expected *Select, got %T", stmt)
	}
	if sel.Where == nil {
		t.Fatal("expected WHERE clause")
	}
	return sel.Where
}

// isNotIn asserts e is `NOT (lhs IN (rhs))` and returns the InExpr.
func isNotIn(t *testing.T, e Expr, lhsDesc string) *InExpr {
	t.Helper()
	u, ok := e.(*UnaryExpr)
	if !ok || u.Op != LX.T_NOT {
		t.Fatalf("expected NOT wrapper, got %T %+v", e, e)
	}
	in, ok := u.Operand.(*InExpr)
	if !ok {
		t.Fatalf("expected InExpr under NOT, got %T", u.Operand)
	}
	if in.Subquery != nil || len(in.List) != 1 {
		t.Fatalf("expected 1-element list InExpr, got subquery=%v list=%d", in.Subquery, len(in.List))
	}
	_ = lhsDesc
	return in
}

func TestREQ1692_UnaryMinusLHS_NotIn(t *testing.T) {
	// `-a NOT IN (b)` must parse as `NOT((-a) IN (b))`,
	// NOT as `-(NOT(a IN (b)))`.
	where := whereExpr(t, "SELECT * FROM t WHERE - a NOT IN (b)")
	in := isNotIn(t, where, "-a")

	// LHS of IN must be the unary-minus expression `-a`.
	lhs, ok := in.Expr.(*UnaryExpr)
	if !ok || lhs.Op != LX.T_MINUS {
		t.Fatalf("expected InExpr.Expr = UnaryExpr{MINUS}, got %T %+v", in.Expr, in.Expr)
	}
	id, ok := lhs.Operand.(*Ident)
	if !ok || id.Name != "a" {
		t.Fatalf("expected -a operand Ident{a}, got %T %+v", lhs.Operand, lhs.Operand)
	}

	// List element must be Ident{b}.
	rhs, ok := in.List[0].(*Ident)
	if !ok || rhs.Name != "b" {
		t.Fatalf("expected list Ident{b}, got %T %+v", in.List[0], in.List[0])
	}
}

func TestREQ1692_ArithmeticLHS_NotIn(t *testing.T) {
	// `a + 1 NOT IN (b)` must parse as `NOT((a+1) IN (b))`.
	where := whereExpr(t, "SELECT * FROM t WHERE a + 1 NOT IN (b)")
	in := isNotIn(t, where, "a+1")

	lhs, ok := in.Expr.(*BinaryExpr)
	if !ok || lhs.Op != LX.T_PLUS {
		t.Fatalf("expected InExpr.Expr = BinaryExpr{PLUS}, got %T %+v", in.Expr, in.Expr)
	}
}

func TestREQ1692_PlainNotIn_Unchanged(t *testing.T) {
	// `a NOT IN (b)` still parses as `NOT(a IN (b))`.
	where := whereExpr(t, "SELECT * FROM t WHERE a NOT IN (b)")
	in := isNotIn(t, where, "a")
	id, ok := in.Expr.(*Ident)
	if !ok || id.Name != "a" {
		t.Fatalf("expected InExpr.Expr Ident{a}, got %T %+v", in.Expr, in.Expr)
	}
}

func TestREQ1692_NotLike_BindsAfterArithmetic(t *testing.T) {
	// `a + 1 NOT LIKE 'x'` must parse as `NOT((a+1) LIKE 'x')`.
	where := whereExpr(t, "SELECT * FROM t WHERE a + 1 NOT LIKE 'x'")
	u, ok := where.(*UnaryExpr)
	if !ok || u.Op != LX.T_NOT {
		t.Fatalf("expected NOT wrapper, got %T", where)
	}
	like, ok := u.Operand.(*BinaryExpr)
	if !ok || like.Op != LX.T_LIKE {
		t.Fatalf("expected LIKE under NOT, got %T %+v", u.Operand, u.Operand)
	}
	if _, ok := like.Left.(*BinaryExpr); !ok {
		t.Fatalf("expected LIKE.Left = BinaryExpr (a+1), got %T", like.Left)
	}
}

func TestREQ1692_NotGlob_BindsAfterArithmetic(t *testing.T) {
	// `a + 1 NOT GLOB 'x*'` must parse as `NOT((a+1) GLOB 'x*')`.
	where := whereExpr(t, "SELECT * FROM t WHERE a + 1 NOT GLOB 'x*'")
	u, ok := where.(*UnaryExpr)
	if !ok || u.Op != LX.T_NOT {
		t.Fatalf("expected NOT wrapper, got %T", where)
	}
	glob, ok := u.Operand.(*BinaryExpr)
	if !ok || glob.Op != LX.T_GLOB {
		t.Fatalf("expected GLOB under NOT, got %T %+v", u.Operand, u.Operand)
	}
	if _, ok := glob.Left.(*BinaryExpr); !ok {
		t.Fatalf("expected GLOB.Left = BinaryExpr (a+1), got %T", glob.Left)
	}
}

func TestREQ1692_PlainGlob_StillBinary(t *testing.T) {
	// `a GLOB 'x*'` continues to parse as a binary GLOB.
	where := whereExpr(t, "SELECT * FROM t WHERE a GLOB 'x*'")
	glob, ok := where.(*BinaryExpr)
	if !ok || glob.Op != LX.T_GLOB {
		t.Fatalf("expected BinaryExpr{GLOB}, got %T %+v", where, where)
	}
}

func TestREQ1692_NotIn_PrecedenceVsAnd(t *testing.T) {
	// `a NOT IN (b) AND c` → `(a NOT IN (b)) AND c`.
	// NOT IN (prec 6) binds tighter than AND (prec 2).
	stmt, err := NewParser("SELECT * FROM t WHERE a NOT IN (b) AND c").Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	sel := stmt.(*Select)
	and, ok := sel.Where.(*BinaryExpr)
	if !ok || and.Op != LX.T_AND {
		t.Fatalf("expected top-level AND, got %T %+v", sel.Where, sel.Where)
	}
	if _, ok := and.Left.(*UnaryExpr); !ok {
		t.Fatalf("expected AND.Left = NOT(...) wrapper, got %T", and.Left)
	}
}
