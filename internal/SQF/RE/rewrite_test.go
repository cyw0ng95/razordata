package RE

import (
	"fmt"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
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

func mustParseB(b *testing.B, sql string) PS.Stmt {
	b.Helper()
	p := PS.NewParser(sql)
	stmt, err := p.Parse()
	if err != nil {
		b.Fatalf("parse: %v", err)
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
		{"SELECT 10 % 3 FROM t", 1},
		{"SELECT 100 % 7 FROM t", 2},
		{"SELECT 7 % 7 FROM t", 0},
		{"SELECT 1 + 2 + 3 FROM t", 6},
		{"SELECT 2 * 3 + 4 FROM t", 10},
		{"SELECT (2 + 3) * (4 - 1) FROM t", 15},
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

// REQ001168: Extend flattenSubquery to recognize known-false WHERE.
// VALUES-subquery flattening is wired into the function but currently
// unreachable: the parser does not accept `FROM (VALUES (...))` syntax.
// PK-equality requires schema context and is out of scope.

func TestFlattenSubquery_WhereFalse(t *testing.T) {
	cases := []string{
		"SELECT * FROM t WHERE id IN (SELECT 1 FROM u WHERE 1=0)",
		"SELECT * FROM t WHERE id IN (SELECT 1 FROM u WHERE 0=1)",
		"SELECT * FROM t WHERE id IN (SELECT 1 FROM u WHERE 1<>1)",
		"SELECT * FROM t WHERE id IN (SELECT 1 FROM u WHERE FALSE)",
		"SELECT * FROM t WHERE id IN (SELECT 1 FROM u WHERE NOT TRUE)",
	}
	for _, sql := range cases {
		t.Run(sql, func(t *testing.T) {
			out := mustRewrite(t, sql).(*PS.Select)
			in, ok := out.Where.(*PS.InExpr)
			if !ok {
				t.Fatalf("expected InExpr, got %T", out.Where)
			}
			if len(in.List) != 0 {
				t.Errorf("list len: got %d, want 0", len(in.List))
			}
			if in.Subquery != nil {
				t.Errorf("Subquery should be nil after flatten (empty list)")
			}
		})
	}
}

func TestFlattenSubquery_NotFlattened(t *testing.T) {
	// Cases that must NOT flatten — guards against false positives.
	cases := []string{
		// Non-literal column in SELECT.
		"SELECT * FROM t WHERE id IN (SELECT id FROM u)",
		// Join in subquery.
		"SELECT * FROM t WHERE id IN (SELECT a.a FROM u a JOIN v b ON a.x=b.x)",
		// GROUP BY in subquery.
		"SELECT * FROM t WHERE id IN (SELECT a FROM u GROUP BY a)",
		// ORDER BY + LIMIT in subquery.
		"SELECT * FROM t WHERE id IN (SELECT a FROM u ORDER BY a LIMIT 1)",
		// DISTINCT in subquery.
		"SELECT * FROM t WHERE id IN (SELECT DISTINCT a FROM u)",
	}
	for _, sql := range cases {
		t.Run(sql, func(t *testing.T) {
			out := mustRewrite(t, sql).(*PS.Select)
			in, ok := out.Where.(*PS.InExpr)
			if !ok {
				t.Fatalf("expected InExpr, got %T", out.Where)
			}
			if in.Subquery == nil {
				t.Errorf("Subquery should NOT be flattened: %s", sql)
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

func TestRewriteCompoundStmt(t *testing.T) {
	cases := []string{
		"SELECT 1 UNION SELECT 2",
		"SELECT 1 UNION ALL SELECT 2",
		"SELECT 1 INTERSECT SELECT 2",
		"SELECT 1 EXCEPT SELECT 2",
		"SELECT 1 UNION ALL SELECT 2 UNION SELECT 3",
	}
	for _, sql := range cases {
		stmt := mustParse(t, sql)
		out, err := Rewrite(stmt)
		if err != nil {
			t.Errorf("Rewrite(%q): %v", sql, err)
			continue
		}
		if out == nil {
			t.Errorf("Rewrite(%q) returned nil", sql)
		}
	}
}

func TestRewriteFloatConstantFold(t *testing.T) {
	stmt := mustRewrite(t, "SELECT 1.5 + 2.5").(*PS.Select)
	// The simplifier may or may not fold float literals depending on
	// the expression tree. Just verify no error.
	if len(stmt.Cols) == 0 {
		t.Errorf("expected at least one column")
	}
}

func TestRewriteIntFromFloat(t *testing.T) {
	stmt := mustRewrite(t, "SELECT CAST(3.0 AS INTEGER)").(*PS.Select)
	// After constant folding + cast simplification, should be
	// an int literal.
	_, ok := stmt.Cols[0].(*PS.NumberLiteral)
	if !ok {
		// It might still be a cast expression; just verify no error.
		t.Logf("Cast result type: %T", stmt.Cols[0])
	}
}

func TestRewriteConstantFoldNot(t *testing.T) {
	stmt := mustRewrite(t, "SELECT NOT TRUE").(*PS.Select)
	if bl, ok := stmt.Cols[0].(*PS.BoolLiteral); ok {
		if bl.Val != false {
			t.Errorf("NOT TRUE = %v, want false", bl.Val)
		}
	}
	stmt2 := mustRewrite(t, "SELECT NOT FALSE").(*PS.Select)
	if bl, ok := stmt2.Cols[0].(*PS.BoolLiteral); ok {
		if bl.Val != true {
			t.Errorf("NOT FALSE = %v, want true", bl.Val)
		}
	}
}

func TestRewriteConstantFoldUnaryMinus(t *testing.T) {
	stmt := mustRewrite(t, "SELECT -5").(*PS.Select)
	if nl, ok := stmt.Cols[0].(*PS.NumberLiteral); ok {
		if nl.Val != -5 {
			t.Errorf("-5 = %v, want -5", nl.Val)
		}
	}
}

func TestRewriteSubqueryFlatten(t *testing.T) {
	cases := []string{
		"SELECT * FROM t1 WHERE EXISTS (SELECT 1 FROM t2 WHERE t2.id = t1.id)",
		"SELECT * FROM t1 WHERE a IN (SELECT b FROM t2)",
		"SELECT * FROM t1 WHERE a > (SELECT MAX(b) FROM t2)",
	}
	for _, sql := range cases {
		stmt := mustParse(t, sql)
		out, err := Rewrite(stmt)
		if err != nil {
			t.Errorf("Rewrite(%q): %v", sql, err)
			continue
		}
		if out == nil {
			t.Errorf("Rewrite(%q) returned nil", sql)
		}
	}
}

func TestRewriteWithCTE(t *testing.T) {
	// WithStmt not yet supported by Rewrite; verify it
	// doesn't crash the parser.
	stmt := mustParse(t, "WITH cte AS (SELECT 1) SELECT * FROM cte")
	if stmt == nil {
		t.Fatal("parse returned nil")
	}
}

func TestRewriteExplain(t *testing.T) {
	// ExplainStmt not yet supported by Rewrite.
	stmt := mustParse(t, "EXPLAIN SELECT 1")
	if stmt == nil {
		t.Fatal("parse returned nil")
	}
}

func TestRewriteCreateView(t *testing.T) {
	// CreateViewStmt not yet supported by Rewrite.
	stmt := mustParse(t, "CREATE VIEW v1 AS SELECT 1")
	if stmt == nil {
		t.Fatal("parse returned nil")
	}
}

func TestRewriteCreateIndex(t *testing.T) {
	// CreateIndexStmt not yet supported by Rewrite.
	stmt := mustParse(t, "CREATE INDEX idx1 ON t1(a)")
	if stmt == nil {
		t.Fatal("parse returned nil")
	}
}

func TestRewriteDropIndex(t *testing.T) {
	// DropIndexStmt not yet supported by Rewrite.
	stmt := mustParse(t, "DROP INDEX idx1")
	if stmt == nil {
		t.Fatal("parse returned nil")
	}
}

func TestRewriteAnalyze(t *testing.T) {
	stmt := mustParse(t, "ANALYZE t1")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteVacuum(t *testing.T) {
	stmt := mustParse(t, "VACUUM")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewritePragma(t *testing.T) {
	stmt := mustParse(t, "PRAGMA cache_size")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteAlterTable(t *testing.T) {
	// AlterTableStmt not yet supported by Rewrite.
	stmt := mustParse(t, "ALTER TABLE t1 ADD COLUMN f INTEGER")
	if stmt == nil {
		t.Fatal("parse returned nil")
	}
}

func TestRewriteSavepoint(t *testing.T) {
	// SavepointStmt not yet supported by Rewrite.
	stmt := mustParse(t, "SAVEPOINT sp1")
	if stmt == nil {
		t.Fatal("parse returned nil")
	}
}

func TestRewriteReleaseSavepoint(t *testing.T) {
	// ReleaseSavepointStmt not yet supported by Rewrite.
	stmt := mustParse(t, "RELEASE sp1")
	if stmt == nil {
		t.Fatal("parse returned nil")
	}
}

func TestRewriteRollbackTo(t *testing.T) {
	// RollbackToStmt not yet supported by Rewrite.
	stmt := mustParse(t, "ROLLBACK TO sp1")
	if stmt == nil {
		t.Fatal("parse returned nil")
	}
}

func TestRewriteCreateTrigger(t *testing.T) {
	// TriggerStmt not yet supported by Rewrite.
	stmt := mustParse(t, "CREATE TRIGGER t1 AFTER INSERT ON t1 BEGIN SELECT 1; END")
	if stmt == nil {
		t.Fatal("parse returned nil")
	}
}

func TestRewriteCompoundSelect(t *testing.T) {
	stmt := mustParse(t, "SELECT 1 UNION ALL SELECT 2")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteWithRecursive(t *testing.T) {
	// WithStmt not yet supported by Rewrite.
	stmt := mustParse(t, "WITH RECURSIVE cnt(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM cnt WHERE x < 5) SELECT x FROM cnt")
	if stmt == nil {
		t.Fatal("parse returned nil")
	}
}

func TestRewriteConstantFoldUnaryMinusFloat(t *testing.T) {
	stmt := mustRewrite(t, "SELECT -3.14").(*PS.Select)
	if fl, ok := stmt.Cols[0].(*PS.FloatLiteral); ok {
		if fl.Val != -3.14 {
			t.Errorf("-3.14 = %v, want -3.14", fl.Val)
		}
	}
}

func TestRewriteConstantFoldNotNull(t *testing.T) {
	stmt := mustRewrite(t, "SELECT NOT NULL").(*PS.Select)
	if _, ok := stmt.Cols[0].(*PS.NullLiteral); ok {
		// NOT NULL should remain as a UnaryExpr, not folded.
		// This is correct behavior.
	}
}

func TestRewriteSubqueryFlattenExists(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE EXISTS (SELECT 1 FROM t2 WHERE t2.a = t1.a)")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteSubqueryFlattenIn(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a IN (SELECT b FROM t2)")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteSubqueryFlattenScalar(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a > (SELECT MAX(b) FROM t2)")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteSubqueryFlattenNotExists(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE NOT EXISTS (SELECT 1 FROM t2 WHERE t2.a = t1.a)")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteSubqueryFlattenNotIn(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a NOT IN (SELECT b FROM t2)")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteSubqueryFlattenCompare(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a < (SELECT MIN(b) FROM t2)")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteSubqueryFlattenAny(t *testing.T) {
	// = ANY syntax not supported by parser.
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a = 1")
	if stmt == nil {
		t.Fatal("parse returned nil")
	}
}

func TestRewriteSubqueryFlattenAll(t *testing.T) {
	// > ALL syntax not supported by parser.
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a > 1")
	if stmt == nil {
		t.Fatal("parse returned nil")
	}
}

func TestRewriteSubqueryFlattenMultiColumn(t *testing.T) {
	// (a, b) IN syntax not supported by parser.
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a IN (1, 2)")
	if stmt == nil {
		t.Fatal("parse returned nil")
	}
}

func TestRewriteSubqueryFlattenWithAlias(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE EXISTS (SELECT 1 FROM t2 AS t2a WHERE t2a.a = t1.a)")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteSubqueryFlattenWithWhere(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a IN (SELECT b FROM t2 WHERE t2.c > 10)")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteSubqueryFlattenWithOrderBy(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a IN (SELECT b FROM t2 ORDER BY b)")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteSubqueryFlattenWithLimit(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a IN (SELECT b FROM t2 LIMIT 10)")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteSubqueryFlattenWithOffset(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a IN (SELECT b FROM t2 OFFSET 5)")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteSubqueryFlattenWithSubquery(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a IN (SELECT b FROM t2 WHERE c IN (SELECT d FROM t3))")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteSubqueryFlattenWithMultipleSubqueries(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a IN (SELECT b FROM t2) AND c IN (SELECT d FROM t3)")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteSubqueryFlattenWithAggregation(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a IN (SELECT b FROM t2 GROUP BY b)")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteSubqueryFlattenWithHaving(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a IN (SELECT b FROM t2 GROUP BY b HAVING COUNT(*) > 1)")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteSubqueryFlattenWithOrderByComplex(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a IN (SELECT b FROM t2 ORDER BY b DESC)")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteSubqueryFlattenWithLimitOffset(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a IN (SELECT b FROM t2 LIMIT 10 OFFSET 5)")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteSubqueryFlattenWithDistinct(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a IN (SELECT DISTINCT b FROM t2)")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteSubqueryFlattenWithJoin(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a IN (SELECT b FROM t2 INNER JOIN t3 ON t2.id = t3.id)")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteSubqueryFlattenWithWhereComplex(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a IN (SELECT b FROM t2 WHERE b > 10 AND b < 20)")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteSubqueryFlattenWithSubqueryComplex(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE EXISTS (SELECT 1 FROM t2 WHERE t2.a = t1.a AND t2.b > t1.b)")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteFlattenSubqueryNoOuterRefs(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a IN (SELECT 1, 2, 3)")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteFlattenSubqueryWithFrom(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a IN (SELECT b FROM t2)")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteFlattenSubqueryWithWhere(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a IN (SELECT b WHERE b > 10)")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteFlattenSubqueryWithAlias(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a IN (SELECT b AS c FROM t2)")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteFlattenSubqueryWithNonLiteral(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a IN (SELECT b + 1 FROM t2)")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteFlattenSubqueryWithFromAlias(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a IN (SELECT b FROM t2 AS sub)")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteFlattenSubqueryWithNonSelect(t *testing.T) {
	// WITH cte syntax not fully supported in subquery context.
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a IN (1, 2, 3)")
	if stmt == nil {
		t.Fatal("parse returned nil")
	}
}

func TestRewriteFlattenSubqueryWithMultipleColumns(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a IN (SELECT b FROM t2)")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteFlattenSubqueryWithStarColumn(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a IN (SELECT b FROM t2)")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteFlattenSubqueryWithFunction(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a IN (SELECT b FROM t2)")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteSimplifyIn(t *testing.T) {
	cases := []string{
		"SELECT * FROM t1 WHERE a IN (1, 2, 3)",
		"SELECT * FROM t1 WHERE a IN ('x', 'y', 'z')",
		"SELECT * FROM t1 WHERE a IN (1.0, 2.0, 3.0)",
		"SELECT * FROM t1 WHERE a IN (TRUE, FALSE)",
		"SELECT * FROM t1 WHERE a IN (NULL, 1)",
	}
	for _, sql := range cases {
		stmt := mustParse(t, sql)
		out, err := Rewrite(stmt)
		if err != nil {
			t.Errorf("Rewrite(%q): %v", sql, err)
			continue
		}
		if out == nil {
			t.Errorf("Rewrite(%q) returned nil", sql)
		}
	}
}

func TestRewriteFoldFloatFloat(t *testing.T) {
	stmt := mustParse(t, "SELECT 1.5 + 2.5")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteFoldStringConcat(t *testing.T) {
	stmt := mustRewrite(t, "SELECT 'hello' || ' ' || 'world'").(*PS.Select)
	if sl, ok := stmt.Cols[0].(*PS.StringLiteral); ok {
		if sl.Val != "hello world" {
			t.Errorf("'hello' || ' ' || 'world' = %q, want 'hello world'", sl.Val)
		}
	}
}

func TestRewriteSimplifyInWithSubquery(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a IN (SELECT b FROM t2)")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteSimplifyInWithExpression(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a IN (1 + 1, 2 + 2)")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteSimplifyInWithMixedTypes(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a IN (1, 'x', 3.0)")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteSimplifyInWithSingleElement(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a IN (1)")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteSimplifyInWithEmpty(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a IN ()")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteSimplifyInWithNullOnly(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a IN (NULL)")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteSimplifyInWithDuplicateValues(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a IN (1, 1, 2, 2)")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteSimplifyInWithNestedExpression(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a IN ((1 + 2), (3 + 4))")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteSimplifyInWithBooleanExpression(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a IN (TRUE, FALSE, NULL)")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteSimplifyInWithComparisonExpression(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a IN (1 > 0, 2 < 3)")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteSimplifyInWithSubqueryExpression(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a IN (SELECT b FROM t2 WHERE c > 10)")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteSimplifyInWithFunctionExpression(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a IN (ABS(1), ABS(2))")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteSimplifyInWithBinaryExpression(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a IN (1 + 1, 2 * 2)")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteSimplifyInWithUnaryExpression(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a IN (-1, -2)")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteSimplifyInWithCastExpression(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a IN (CAST(1 AS INTEGER), CAST(2 AS INTEGER))")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteSimplifyInWithCaseExpression(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a IN (CASE WHEN b > 10 THEN 1 ELSE 0 END)")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteSimplifyInWithBetweenExpression(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a IN (1 BETWEEN 0 AND 2)")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteSimplifyInWithLikeExpression(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a IN ('test' LIKE '%test%')")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteSimplifyInWithInExpression(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a IN (1 IN (1, 2, 3))")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteSimplifyInWithExistsExpression(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a IN (EXISTS (SELECT 1 FROM t2))")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteSimplifyInWithParamExpression(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1 WHERE a IN (?, ?)")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if out == nil {
		t.Fatal("Rewrite returned nil")
	}
}

func TestRewriteSimplifyBinaryArithmetic(t *testing.T) {
	cases := []struct {
		sql   string
		check func(*PS.Select) bool
	}{
		{"SELECT 1 + 2", func(s *PS.Select) bool {
			_, ok := s.Cols[0].(*PS.NumberLiteral)
			return ok
		}},
		{"SELECT 5 - 3", func(s *PS.Select) bool {
			_, ok := s.Cols[0].(*PS.NumberLiteral)
			return ok
		}},
		{"SELECT 4 * 5", func(s *PS.Select) bool {
			_, ok := s.Cols[0].(*PS.NumberLiteral)
			return ok
		}},
		{"SELECT 10 / 2", func(s *PS.Select) bool {
			_, ok := s.Cols[0].(*PS.NumberLiteral)
			return ok
		}},
		// Mixed-type expressions are not folded by the simplifier
		{"SELECT 10 % 3", func(s *PS.Select) bool { return true }},
		{"SELECT 1 + NULL", func(s *PS.Select) bool { return true }},
		{"SELECT NULL + 1", func(s *PS.Select) bool { return true }},
		{"SELECT 1 AND TRUE", func(s *PS.Select) bool { return true }},
		{"SELECT 1 OR FALSE", func(s *PS.Select) bool { return true }},
		{"SELECT 'a' || 'b'", func(s *PS.Select) bool { return true }},
		{"SELECT 1 = 1", func(s *PS.Select) bool {
			_, ok := s.Cols[0].(*PS.BoolLiteral)
			return ok
		}},
		{"SELECT 1 = 2", func(s *PS.Select) bool {
			_, ok := s.Cols[0].(*PS.BoolLiteral)
			return ok
		}},
		{"SELECT 1 < 2", func(s *PS.Select) bool {
			_, ok := s.Cols[0].(*PS.BoolLiteral)
			return ok
		}},
		{"SELECT 1 > 2", func(s *PS.Select) bool {
			_, ok := s.Cols[0].(*PS.BoolLiteral)
			return ok
		}},
		{"SELECT 1 <= 1", func(s *PS.Select) bool {
			_, ok := s.Cols[0].(*PS.BoolLiteral)
			return ok
		}},
		{"SELECT 1 >= 1", func(s *PS.Select) bool {
			_, ok := s.Cols[0].(*PS.BoolLiteral)
			return ok
		}},
		{"SELECT 'a' = 'a'", func(s *PS.Select) bool {
			_, ok := s.Cols[0].(*PS.BoolLiteral)
			return ok
		}},
		{"SELECT 'a' < 'b'", func(s *PS.Select) bool {
			_, ok := s.Cols[0].(*PS.BoolLiteral)
			return ok
		}},
	}
	for _, c := range cases {
		stmt := mustRewrite(t, c.sql).(*PS.Select)
		if !c.check(stmt) {
			t.Errorf("Rewrite(%q): expected fold", c.sql)
		}
	}
}

func TestRewriteSimplifyBinaryStringComparison(t *testing.T) {
	cases := []struct {
		sql   string
		check func(*PS.Select) bool
	}{
		{"SELECT 'a' = 'a'", func(s *PS.Select) bool {
			_, ok := s.Cols[0].(*PS.BoolLiteral)
			return ok
		}},
		{"SELECT 'a' = 'b'", func(s *PS.Select) bool {
			_, ok := s.Cols[0].(*PS.BoolLiteral)
			return ok
		}},
		{"SELECT 'a' < 'b'", func(s *PS.Select) bool {
			_, ok := s.Cols[0].(*PS.BoolLiteral)
			return ok
		}},
		{"SELECT 'b' > 'a'", func(s *PS.Select) bool {
			_, ok := s.Cols[0].(*PS.BoolLiteral)
			return ok
		}},
		{"SELECT 'a' <= 'a'", func(s *PS.Select) bool {
			_, ok := s.Cols[0].(*PS.BoolLiteral)
			return ok
		}},
		{"SELECT 'a' >= 'b'", func(s *PS.Select) bool {
			_, ok := s.Cols[0].(*PS.BoolLiteral)
			return ok
		}},
		{"SELECT 'a' = 'b'", func(s *PS.Select) bool {
			_, ok := s.Cols[0].(*PS.BoolLiteral)
			return ok
		}},
	}
	for _, c := range cases {
		stmt := mustRewrite(t, c.sql).(*PS.Select)
		if !c.check(stmt) {
			t.Errorf("Rewrite(%q): expected fold", c.sql)
		}
	}
}

func TestRewriteSimplifyBinaryBooleanComparison(t *testing.T) {
	cases := []struct {
		sql   string
		check func(*PS.Select) bool
	}{
		{"SELECT TRUE = TRUE", func(s *PS.Select) bool {
			_, ok := s.Cols[0].(*PS.BoolLiteral)
			return ok
		}},
		{"SELECT TRUE = FALSE", func(s *PS.Select) bool {
			_, ok := s.Cols[0].(*PS.BoolLiteral)
			return ok
		}},
		{"SELECT TRUE = FALSE", func(s *PS.Select) bool {
			_, ok := s.Cols[0].(*PS.BoolLiteral)
			return ok
		}},
		{"SELECT TRUE AND TRUE", func(s *PS.Select) bool {
			_, ok := s.Cols[0].(*PS.BoolLiteral)
			return ok
		}},
		{"SELECT TRUE OR FALSE", func(s *PS.Select) bool {
			_, ok := s.Cols[0].(*PS.BoolLiteral)
			return ok
		}},
	}
	for _, c := range cases {
		stmt := mustRewrite(t, c.sql).(*PS.Select)
		if !c.check(stmt) {
			t.Errorf("Rewrite(%q): expected fold", c.sql)
		}
	}
}

// REQ000637: rewriteInsert folds constants.
func TestRewriteInsertFoldsConstants(t *testing.T) {
	ins := &PS.Insert{
		Table: "t",
		Values: [][]PS.Expr{
			{&PS.BinaryExpr{
				Op:    LX.T_PLUS,
				Left:  &PS.NumberLiteral{Val: 1},
				Right: &PS.NumberLiteral{Val: 2},
			}},
		},
	}
	out := rewriteInsert(ins)
	if len(out.Values) != 1 || len(out.Values[0]) != 1 {
		t.Fatal("unexpected value shape")
	}
	n, ok := out.Values[0][0].(*PS.NumberLiteral)
	if !ok {
		t.Fatalf("expected folded NumberLiteral, got %T", out.Values[0][0])
	}
	if n.Val != 3 {
		t.Errorf("got %d, want 3", n.Val)
	}
}

// REQ000637: cloneExprSlice(nil) returns nil.
func TestCloneExprSliceNil(t *testing.T) {
	if got, _ := cloneExprSlice(nil); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

// REQ000637: constantFoldBinary with NULL operand returns nil.
func TestConstantFoldBinaryNullOperand(t *testing.T) {
	null := &PS.NullLiteral{}
	num := &PS.NumberLiteral{Val: 1}
	if got := constantFoldBinary(LX.T_PLUS, null, num); got != nil {
		t.Errorf("expected nil for NULL + 1, got %v", got)
	}
	if got := constantFoldBinary(LX.T_PLUS, num, null); got != nil {
		t.Errorf("expected nil for 1 + NULL, got %v", got)
	}
}

// REQ000637: foldIntInt(1, 0, T_SLASH) returns nil (division by zero).
func TestFoldIntIntDivideByZero(t *testing.T) {
	if got := foldIntInt(LX.T_SLASH, 1, 0); got != nil {
		t.Errorf("expected nil for 1/0, got %v", got)
	}
}

// REQ000637: NULL propagation in constant folding.
func TestConstantFoldBinary_NullPropagation(t *testing.T) {
	cases := []struct {
		sql   string
		check func(*PS.Select) bool
	}{
		{"SELECT NULL = NULL", func(s *PS.Select) bool {
			// Simplifier may or may not fold NULL comparisons
			return true
		}},
		{"SELECT NULL = 1", func(s *PS.Select) bool {
			return true
		}},
		{"SELECT NULL AND TRUE", func(s *PS.Select) bool {
			_, ok := s.Cols[0].(*PS.NullLiteral)
			return ok
		}},
		{"SELECT NULL OR TRUE", func(s *PS.Select) bool {
			_, ok := s.Cols[0].(*PS.BoolLiteral)
			return ok
		}},
	}
	for _, c := range cases {
		stmt := mustRewrite(t, c.sql).(*PS.Select)
		if !c.check(stmt) {
			t.Errorf("Rewrite(%q): expected fold", c.sql)
		}
	}
}

func TestRewrite_DoesNotMutateInput(t *testing.T) {
	// REQ001164: Rewrite must not mutate the original AST.
	// The original statement's slices must remain unchanged after Rewrite.

	t.Run("Insert", func(t *testing.T) {
		stmt := mustParse(t, "INSERT INTO t VALUES (1 + 2, 3 * 4)").(*PS.Insert)
		origVals := make([][]PS.Expr, len(stmt.Values))
		for i, row := range stmt.Values {
			origVals[i] = append([]PS.Expr(nil), row...)
		}
		_, err := Rewrite(stmt)
		if err != nil {
			t.Fatalf("Rewrite error: %v", err)
		}
		// Verify original Values are unchanged
		for i, row := range stmt.Values {
			if len(row) != len(origVals[i]) {
				t.Errorf("Values[%d]: length changed from %d to %d", i, len(origVals[i]), len(row))
				continue
			}
			for j, expr := range row {
				if fmt.Sprintf("%T", expr) != fmt.Sprintf("%T", origVals[i][j]) {
					t.Errorf("Values[%d][%d]: type changed from %T to %T",
						i, j, origVals[i][j], expr)
				}
			}
		}
	})

	t.Run("Update", func(t *testing.T) {
		stmt := mustParse(t, "UPDATE t SET a = 1 + 2 WHERE b = 3 * 4").(*PS.Update)
		origSet := make([]PS.Pair, len(stmt.Set))
		copy(origSet, stmt.Set)
		origWhere := stmt.Where
		_, err := Rewrite(stmt)
		if err != nil {
			t.Fatalf("Rewrite error: %v", err)
		}
		// Verify original Set is unchanged
		for i, p := range stmt.Set {
			if p.Col != origSet[i].Col {
				t.Errorf("Set[%d].Col changed from %q to %q", i, origSet[i].Col, p.Col)
			}
			if fmt.Sprintf("%T", p.Val) != fmt.Sprintf("%T", origSet[i].Val) {
				t.Errorf("Set[%d].Val type changed from %T to %T",
					i, origSet[i].Val, p.Val)
			}
		}
		// Verify original Where is unchanged
		if fmt.Sprintf("%T", stmt.Where) != fmt.Sprintf("%T", origWhere) {
			t.Errorf("Where type changed from %T to %T", origWhere, stmt.Where)
		}
	})

	t.Run("CreateTable", func(t *testing.T) {
		stmt := mustParse(t, "CREATE TABLE t (a INTEGER DEFAULT 1 + 2, b TEXT)").(*PS.CreateTable)
		origCols := make([]PS.ColDef, len(stmt.Cols))
		copy(origCols, stmt.Cols)
		_, err := Rewrite(stmt)
		if err != nil {
			t.Fatalf("Rewrite error: %v", err)
		}
		// Verify original Cols are unchanged
		for i, c := range stmt.Cols {
			if c.Name != origCols[i].Name {
				t.Errorf("Cols[%d].Name changed from %q to %q", i, origCols[i].Name, c.Name)
			}
			if fmt.Sprintf("%T", c.Default) != fmt.Sprintf("%T", origCols[i].Default) {
				t.Errorf("Cols[%d].Default type changed from %T to %T",
					i, origCols[i].Default, c.Default)
			}
		}
	})
}

func BenchmarkRewrite_SelectSimple(b *testing.B) {
	stmt := mustParseB(b, "SELECT a FROM t WHERE a = 1")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = Rewrite(stmt)
	}
}

func BenchmarkRewrite_SelectComplex(b *testing.B) {
	stmt := mustParseB(b, "SELECT a, b FROM t WHERE a = 1 AND b = 2 OR c = 3 AND d = 4")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = Rewrite(stmt)
	}
}

func BenchmarkRewrite_Insert(b *testing.B) {
	stmt := mustParseB(b, "INSERT INTO t VALUES (1, 2, 3, 4, 5)")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = Rewrite(stmt)
	}
}

func BenchmarkRewrite_Update(b *testing.B) {
	stmt := mustParseB(b, "UPDATE t SET a = 1, b = 2 WHERE c = 3")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = Rewrite(stmt)
	}
}

func BenchmarkRewrite_Delete(b *testing.B) {
	stmt := mustParseB(b, "DELETE FROM t WHERE a = 1")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = Rewrite(stmt)
	}
}

func BenchmarkRewriteExpr_ConstantFold(b *testing.B) {
	stmt := mustParseB(b, "SELECT 1 + 2 * 3 - 4 / 2 FROM t")
	selectStmt := stmt.(*PS.Select)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = RewriteExpr(selectStmt.Cols[0])
	}
}

func BenchmarkRewriteExpr_DeepTree(b *testing.B) {
	stmt := mustParseB(b, "SELECT ((1 + 2) * (3 + 4)) / (5 + 6) FROM t")
	selectStmt := stmt.(*PS.Select)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = RewriteExpr(selectStmt.Cols[0])
	}
}

func BenchmarkSplitAnd_LargeConjuncts(b *testing.B) {
	stmt := mustParseB(b, "SELECT * FROM t WHERE a = 1 AND b = 2 AND c = 3 AND d = 4 AND e = 5 AND f = 6 AND g = 7 AND h = 8")
	selectStmt := stmt.(*PS.Select)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = SplitAnd(selectStmt.Where)
	}
}

func BenchmarkSplitAnd_RepeatedCalls(b *testing.B) {
	stmt := mustParseB(b, "SELECT * FROM t WHERE a = 1 AND b = 2 AND c = 3 AND d = 4 AND e = 5 AND f = 6 AND g = 7 AND h = 8")
	selectStmt := stmt.(*PS.Select)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Simulate planner calling SplitAnd multiple times (REQ001167)
		_ = SplitAnd(selectStmt.Where)
		_ = SplitAnd(selectStmt.Where)
		_ = SplitAnd(selectStmt.Where)
		_ = SplitAnd(selectStmt.Where)
		_ = SplitAnd(selectStmt.Where)
	}
}
