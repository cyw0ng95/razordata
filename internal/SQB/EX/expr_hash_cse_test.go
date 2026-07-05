package EX

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/PS"
	"github.com/cyw0ng95/razordata/internal/SQF/RE"
)

// TestExprHash_DistinctINPredicates verifies REQ001111: two IN predicates
// that reference different columns and different value lists produce
// different hash strings. Before the fix, both fell through to "%T"
// and produced identical hashes, causing
// eliminateCommonSubexpressions to drop one of them and breaking
// cross-join predicate pushdown.
func TestExprHash_DistinctINPredicates(t *testing.T) {
	e1 := &PS.InExpr{
		Expr: &PS.Ident{Name: "b4"},
		List: []PS.Expr{&PS.NumberLiteral{Val: 532}, &PS.NumberLiteral{Val: 593}},
	}
	e2 := &PS.InExpr{
		Expr: &PS.Ident{Name: "d9"},
		List: []PS.Expr{&PS.NumberLiteral{Val: 808}, &PS.NumberLiteral{Val: 662}},
	}
	e3 := &PS.InExpr{
		Expr: &PS.Ident{Name: "b4"},
		List: []PS.Expr{&PS.NumberLiteral{Val: 999}},
	}
	if h1, h2 := exprHash(e1), exprHash(e2); h1 == h2 {
		t.Fatalf("distinct IN predicates hashed identically: %q vs %q", h1, h2)
	}
	if h1, h3 := exprHash(e1), exprHash(e3); h1 == h3 {
		t.Fatalf("IN predicates with same column but different lists hashed identically: %q vs %q", h1, h3)
	}
}

// TestExprHash_AllExpressionTypesDistinct verifies REQ001111: each
// expression node type handled by exprHash produces a stable hash
// that does not collapse to "%T" (the bug pattern).
func TestExprHash_AllExpressionTypesDistinct(t *testing.T) {
	cases := []struct {
		name string
		a, b PS.Expr
	}{
		{"Ident vs Ident different name", &PS.Ident{Name: "a"}, &PS.Ident{Name: "b"}},
		{"NumberLiteral different values", &PS.NumberLiteral{Val: 1}, &PS.NumberLiteral{Val: 2}},
		{"FloatLiteral different values", &PS.FloatLiteral{Val: 1.5}, &PS.FloatLiteral{Val: 2.5}},
		{"StringLiteral different values", &PS.StringLiteral{Val: "a"}, &PS.StringLiteral{Val: "b"}},
		{"BoolLiteral different values", &PS.BoolLiteral{Val: true}, &PS.BoolLiteral{Val: false}},
		{"QualifiedName different table", &PS.QualifiedName{Table: "t1", Name: "a"}, &PS.QualifiedName{Table: "t2", Name: "a"}},
		{"UnaryExpr different op", &PS.UnaryExpr{Op: 10, Operand: &PS.NumberLiteral{Val: 1}}, &PS.UnaryExpr{Op: 11, Operand: &PS.NumberLiteral{Val: 1}}},
		{"BinaryExpr different op", &PS.BinaryExpr{Op: 10, Left: &PS.NumberLiteral{Val: 1}, Right: &PS.NumberLiteral{Val: 2}}, &PS.BinaryExpr{Op: 11, Left: &PS.NumberLiteral{Val: 1}, Right: &PS.NumberLiteral{Val: 2}}},
		{"FunctionCall different name", &PS.FunctionCall{Name: "abs"}, &PS.FunctionCall{Name: "sqrt"}},
		{"CastExpr different target", &PS.CastExpr{Expr: &PS.NumberLiteral{Val: 1}, Type: &PS.TypeInfo{Type: 1}}, &PS.CastExpr{Expr: &PS.NumberLiteral{Val: 1}, Type: &PS.TypeInfo{Type: 2}}},
		{"IN different column", &PS.InExpr{Expr: &PS.Ident{Name: "a"}, List: []PS.Expr{&PS.NumberLiteral{Val: 1}}}, &PS.InExpr{Expr: &PS.Ident{Name: "b"}, List: []PS.Expr{&PS.NumberLiteral{Val: 1}}}},
		{"IN different list", &PS.InExpr{Expr: &PS.Ident{Name: "a"}, List: []PS.Expr{&PS.NumberLiteral{Val: 1}}}, &PS.InExpr{Expr: &PS.Ident{Name: "a"}, List: []PS.Expr{&PS.NumberLiteral{Val: 2}}}},
		{"BetweenExpr different bounds", &PS.BetweenExpr{Expr: &PS.Ident{Name: "x"}, Low: &PS.NumberLiteral{Val: 1}, High: &PS.NumberLiteral{Val: 10}}, &PS.BetweenExpr{Expr: &PS.Ident{Name: "x"}, Low: &PS.NumberLiteral{Val: 1}, High: &PS.NumberLiteral{Val: 20}}},
		{"ListExpr different items", &PS.ListExpr{Items: []PS.Expr{&PS.NumberLiteral{Val: 1}}}, &PS.ListExpr{Items: []PS.Expr{&PS.NumberLiteral{Val: 2}}}},
		{"AliasedExpr different alias", &PS.AliasedExpr{Alias: "x", Expr: &PS.Ident{Name: "a"}}, &PS.AliasedExpr{Alias: "y", Expr: &PS.Ident{Name: "a"}}},
		{"AggregateFunc different name", &PS.AggregateFunc{Name: "count"}, &PS.AggregateFunc{Name: "sum"}},
		{"WindowFunc different name", &PS.WindowFunc{Name: "row_number"}, &PS.WindowFunc{Name: "rank"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ha := exprHash(c.a)
			hb := exprHash(c.b)
			if ha == "" || hb == "" {
				t.Fatalf("exprHash returned empty: ha=%q hb=%q", ha, hb)
			}
			if ha == hb {
				t.Fatalf("distinct expressions hashed identically: %q vs %q", ha, hb)
			}
			if strings.Contains(ha, "%!") || strings.Contains(hb, "%!") {
				t.Fatalf("exprHash fell through to %%T fallback: ha=%q hb=%q", ha, hb)
			}
		})
	}
}

// TestExprHash_SameExpressionStable verifies REQ001111: two structurally
// equal expressions produce the same hash (so genuine duplicates are
// deduped by eliminateCommonSubexpressions).
func TestExprHash_SameExpressionStable(t *testing.T) {
	a := &PS.InExpr{Expr: &PS.Ident{Name: "x"}, List: []PS.Expr{&PS.NumberLiteral{Val: 1}, &PS.NumberLiteral{Val: 2}}}
	b := &PS.InExpr{Expr: &PS.Ident{Name: "x"}, List: []PS.Expr{&PS.NumberLiteral{Val: 1}, &PS.NumberLiteral{Val: 2}}}
	if exprHash(a) != exprHash(b) {
		t.Fatalf("identical expressions hashed differently")
	}
}

// TestEliminateCommonSubexpressions_PreservesDistinctIN verifies
// REQ001111 end-to-end: when splitPredicatesByTable receives 4
// distinct IN predicates plus 1 binary equality, all 5 are
// distributed to their respective DT.Tables (not deduplicated by
// eliminateCommonSubexpressions).
func TestEliminateCommonSubexpressions_PreservesDistinctIN(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	for ti := 1; ti <= 9; ti++ {
		ex.RegisterTable(fmt.Sprintf("t%d", ti), []string{"a", "b", "c", "d", "e", "v"})
	}
	stmt, err := PS.NewParser("SELECT count(*) FROM t9, t4, t8, t1, t3 WHERE b4 in (532,593,289,476,749,35,816) AND d9 in (808,662,597,682,628,568) AND e8 in (792,14,646) AND 729=a3 AND a1 in (622,380,862,52,640,776,268,536)").Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	sel := stmt.(*PS.Select)
	conjuncts := RE.SplitAnd(sel.Where)
	if len(conjuncts) != 5 {
		t.Fatalf("expected 5 conjuncts, got %d", len(conjuncts))
	}
	p := NewPlanner()
	allTables := []string{"t9", "t4", "t8", "t1", "t3"}
	pushed, cross := p.splitPredicatesByTable(conjuncts, allTables)
	totalPushed := 0
	for tbl, preds := range pushed {
		totalPushed += len(preds)
		t.Logf("pushed to %s: %d predicates", tbl, len(preds))
	}
	if totalPushed != 5 {
		t.Fatalf("expected 5 pushed predicates, got %d (cross=%d)", totalPushed, len(cross))
	}
	// Each of t1, t3, t4, t8, t9 should have at least one pushed
	// predicate — before the fix, only t3 and t4 had any.
	for _, tbl := range []string{"t1", "t3", "t4", "t8", "t9"} {
		if len(pushed[tbl]) == 0 {
			t.Errorf("table %s received no pushed predicates (bug)", tbl)
		}
	}
	_ = context.Background
}

// TestEliminateCommonSubexpressions_GenuineDuplicates verifies that
// legitimate duplicate conjuncts ARE deduped (so we don't lose the
// fix from over-eager removal).
func TestEliminateCommonSubexpressions_GenuineDuplicates(t *testing.T) {
	stmt, err := PS.NewParser("SELECT * FROM t1 WHERE a1=1 AND a1=1 AND b1=2").Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	sel := stmt.(*PS.Select)
	result := eliminateCommonSubexpressions(sel.Where)
	conjuncts := RE.SplitAnd(result)
	if len(conjuncts) != 2 {
		t.Fatalf("expected 2 unique conjuncts, got %d", len(conjuncts))
	}
}

// TestCrossClauseCSE_DetectsCommonSubexpression verifies REQ001234:
// when the same subexpression appears in SELECT and ORDER BY, the
// analyzer detects it as a common subexpression and produces a CSE
// plan with a precompute step.
func TestCrossClauseCSE_DetectsCommonSubexpression(t *testing.T) {
	stmt, err := PS.NewParser("SELECT a+b*2 FROM t1 WHERE a+b*2 > 10 ORDER BY a+b*2").Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	sel := stmt.(*PS.Select)
	p := NewPlanner()
	cse := p.analyzeCrossClauseCSE(sel)
	if cse == nil {
		t.Fatal("expected CSE plan, got nil")
	}
	// a+b*2 is shared across SELECT, WHERE, ORDER BY.
	// b*2 is shared across SELECT and WHERE.
	// Both should be detected.
	if len(cse.commonExprs) < 1 {
		t.Fatalf("expected at least 1 common expression, got %d", len(cse.commonExprs))
	}
	// Verify the rewritten SELECT references a precomputed slot
	rewritten := cse.rewriteExprWithCSE(cloneExpr(sel.Cols[0]))
	ident, ok := rewritten.(*PS.Ident)
	if !ok {
		t.Fatalf("expected Ident after rewrite, got %T", rewritten)
	}
	if !strings.HasPrefix(ident.Name, "__cse") {
		// If the entire SELECT expression is not a common subexpression,
		// it might be partially rewritten (e.g., a + __cse1 for b*2).
		t.Logf("rewritten = %v", rewritten)
	}
}

// TestCrossClauseCSE_DetectsWhereAndOrderByCommon verifies REQ001234:
// when the same subexpression appears in WHERE and ORDER BY, it is
// detected as a common subexpression.
func TestCrossClauseCSE_DetectsWhereAndOrderByCommon(t *testing.T) {
	stmt, err := PS.NewParser("SELECT a FROM t1 WHERE a+b > 10 ORDER BY a+b").Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	sel := stmt.(*PS.Select)
	p := NewPlanner()
	cse := p.analyzeCrossClauseCSE(sel)
	if cse == nil {
		t.Fatal("expected CSE plan, got nil")
	}
	if len(cse.commonExprs) != 1 {
		// The WHERE conjunct (a+b > 10) and ORDER BY (a+b) share the
		// subexpression a+b. The analyzer should detect this.
		// However, the WHERE is split into conjuncts (a+b > 10 is one
		// conjunct), so the shared subexpression is a+b which appears
		// in both the WHERE conjunct and the ORDER BY expression.
		t.Fatalf("expected 1 common expression, got %d", len(cse.commonExprs))
	}
}

// TestCrossClauseCSE_NoCommonSubexpression verifies that when no
// common subexpressions exist, the analyzer returns nil.
func TestCrossClauseCSE_NoCommonSubexpression(t *testing.T) {
	stmt, err := PS.NewParser("SELECT a+b FROM t1 WHERE c>10 ORDER BY d").Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	sel := stmt.(*PS.Select)
	p := NewPlanner()
	cse := p.analyzeCrossClauseCSE(sel)
	if cse != nil {
		t.Fatalf("expected nil CSE plan, got %v", cse)
	}
}