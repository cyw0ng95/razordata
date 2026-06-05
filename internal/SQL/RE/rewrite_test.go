package RE

import (
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

func mustParse(t *testing.T, sql string) PS.Stmt {
	t.Helper()
	p := PS.NewParser(sql)
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return stmt
}

func mustRewrite(t *testing.T, sql string) PS.Stmt {
	t.Helper()
	stmt := mustParse(t, sql)
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	return out
}

func TestRewriteConstantFoldArithmetic(t *testing.T) {
	cases := []struct {
		sql  string
		want int64
	}{
		{"SELECT 1 + 2 FROM t", 3},
		{"SELECT 10 - 3 FROM t", 7},
		{"SELECT 4 * 5 FROM t", 20},
		{"SELECT 10 / 2 FROM t", 5},
		{"SELECT 1 + 2 * 3 FROM t", 7},
		{"SELECT (1 + 2) * 3 FROM t", 9},
	}
	for _, c := range cases {
		t.Run(c.sql, func(t *testing.T) {
			out := mustRewrite(t, c.sql).(*PS.Select)
			n, ok := out.Cols[0].(*PS.NumberLiteral)
			if !ok {
				t.Fatalf("expected NumberLiteral, got %T", out.Cols[0])
			}
			if n.Val != c.want {
				t.Errorf("got %d, want %d", n.Val, c.want)
			}
		})
	}
}

func TestRewriteConstantFoldCompare(t *testing.T) {
	cases := []struct {
		sql  string
		want bool
	}{
		{"SELECT * FROM t WHERE 1 = 1", true},
		{"SELECT * FROM t WHERE 1 = 2", false},
		{"SELECT * FROM t WHERE 3 > 2", true},
		{"SELECT * FROM t WHERE 3 < 2", false},
		{"SELECT * FROM t WHERE 'a' = 'a'", true},
		{"SELECT * FROM t WHERE 'a' = 'b'", false},
	}
	for _, c := range cases {
		t.Run(c.sql, func(t *testing.T) {
			out := mustRewrite(t, c.sql).(*PS.Select)
			b, ok := out.Where.(*PS.BoolLiteral)
			if !ok {
				t.Fatalf("expected BoolLiteral, got %T", out.Where)
			}
			if b.Val != c.want {
				t.Errorf("got %v, want %v", b.Val, c.want)
			}
		})
	}
}

func TestRewriteBooleanSimplify(t *testing.T) {
	cases := []struct {
		sql  string
		want string
	}{
		{"SELECT * FROM t WHERE TRUE AND a = 1", "*BinaryExpr"},
		{"SELECT * FROM t WHERE FALSE AND a = 1", "*BoolLiteral"},
		{"SELECT * FROM t WHERE a = 1 OR TRUE", "*BoolLiteral"},
		{"SELECT * FROM t WHERE a = 1 OR FALSE", "*BinaryExpr"},
		{"SELECT * FROM t WHERE NOT (NOT TRUE)", "*BoolLiteral"},
		{"SELECT * FROM t WHERE 1 = 1", "*BoolLiteral"},
		{"SELECT * FROM t WHERE 1 != 1", "*BoolLiteral"},
	}
	for _, c := range cases {
		t.Run(c.sql, func(t *testing.T) {
			out := mustRewrite(t, c.sql).(*PS.Select)
			got := typeNameExpr(out.Where)
			if got != c.want {
				t.Errorf("got %s, want %s", got, c.want)
			}
		})
	}
}

func typeNameExpr(e PS.Expr) string {
	if e == nil {
		return "nil"
	}
	switch e.(type) {
	case *PS.NumberLiteral:
		return "*NumberLiteral"
	case *PS.FloatLiteral:
		return "*FloatLiteral"
	case *PS.StringLiteral:
		return "*StringLiteral"
	case *PS.BoolLiteral:
		return "*BoolLiteral"
	case *PS.NullLiteral:
		return "*NullLiteral"
	case *PS.Ident:
		return "*Ident"
	case *PS.BinaryExpr:
		return "*BinaryExpr"
	case *PS.UnaryExpr:
		return "*UnaryExpr"
	}
	return "*other"
}

func TestRewriteFlattenSubquery(t *testing.T) {
	cases := []struct {
		sql       string
		wantListN int
	}{
		{"SELECT * FROM t WHERE id IN (SELECT 1, 2, 3)", 3},
		{"SELECT * FROM t WHERE id IN (SELECT 1)", 1},
		{"SELECT * FROM t WHERE id IN (SELECT 1, 2, 3, 4, 5)", 5},
		{"SELECT * FROM t WHERE id IN (SELECT id FROM t)", 0},
		{"SELECT * FROM t WHERE id IN (SELECT 1 FROM t)", 0},
		{"SELECT * FROM t WHERE id IN (SELECT 1 WHERE a > 5)", 0},
	}
	for _, c := range cases {
		t.Run(c.sql, func(t *testing.T) {
			out := mustRewrite(t, c.sql).(*PS.Select)
			in, ok := out.Where.(*PS.InExpr)
			if !ok {
				t.Fatalf("expected InExpr, got %T", out.Where)
			}
			if len(in.List) != c.wantListN {
				t.Errorf("list len: got %d, want %d", len(in.List), c.wantListN)
			}
			if c.wantListN > 0 && in.Subquery != nil {
				t.Errorf("Subquery should be nil after flatten")
			}
		})
	}
}

func TestSplitAnd(t *testing.T) {
	cases := []struct {
		sql string
		n   int
	}{
		{"SELECT * FROM t", 0},
		{"SELECT * FROM t WHERE a = 1", 1},
		{"SELECT * FROM t WHERE a = 1 AND b = 2", 2},
		{"SELECT * FROM t WHERE a = 1 AND b = 2 AND c = 3", 3},
		{"SELECT * FROM t WHERE (a = 1 OR b = 2) AND c = 3", 2},
	}
	for _, c := range cases {
		t.Run(c.sql, func(t *testing.T) {
			out := mustRewrite(t, c.sql).(*PS.Select)
			parts := SplitAnd(out.Where)
			if len(parts) != c.n {
				t.Errorf("got %d parts, want %d", len(parts), c.n)
			}
		})
	}
}

func TestRewriteNegation(t *testing.T) {
	out := mustRewrite(t, "SELECT * FROM t WHERE NOT (1 = 1)").(*PS.Select)
	b, ok := out.Where.(*PS.BoolLiteral)
	if !ok || b.Val {
		t.Fatalf("expected folded BoolLiteral false, got %T %v", out.Where, out.Where)
	}
}

func TestRewriteUnaryMinus2(t *testing.T) {
	out := mustRewrite(t, "SELECT -5 FROM t").(*PS.Select)
	n, ok := out.Cols[0].(*PS.NumberLiteral)
	if !ok {
		t.Fatalf("expected NumberLiteral, got %T", out.Cols[0])
	}
	if n.Val != -5 {
		t.Errorf("got %d, want -5", n.Val)
	}
}

func TestRewritePreservesOriginal(t *testing.T) {
	orig := mustParse(t, "SELECT 1 + 2 FROM t").(*PS.Select)
	origFirst, _ := orig.Cols[0].(*PS.BinaryExpr)
	if origFirst == nil {
		t.Fatal("expected binary expr in original")
	}
	Rewrite(orig)
	if _, ok := orig.Cols[0].(*PS.BinaryExpr); !ok {
		t.Error("rewrite should not mutate original")
	}
}
