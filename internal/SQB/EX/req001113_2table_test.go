//go:build !slt_corpus

package EX

import (
	"context"
	"fmt"
	"testing"

	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	RE "github.com/cyw0ng95/razordata/internal/SQF/RE"
)

// TestREQ001113_2TableImplicitJoin_PredicateDistribution reproduces
// the smallest failing case from the REQ: an implicit comma-join of
// two tables with constant equality predicates, where the expected
// cardinality is |t1.a1=281| * |t9.c9=232| (a cross product filtered
// only by the per-table predicates).
//
// This test focuses on the predicate-distribution sub-item of REQ001113:
// splitPredicatesByTable must route `281=a1` to t1 (or any single
// table that owns a1) and `c9=232` to t9 (or any single table that
// owns c9). It does NOT depend on the SLT corpus — we hand-seed
// 3 rows of t1 (a1=281) and 1 row of t9 (c9=232) so the cross
// product is 3 rows.
func TestREQ001113_2TableImplicitJoin_PredicateDistribution(t *testing.T) {
	ResetForTest(t)
	ex := NewExecutor()
	ctx := context.Background()

	// Schema: t1 has columns a1..e1, x1; t9 has columns a9..e9, x9.
	for _, ddl := range []string{
		"CREATE TABLE t1(a1 INTEGER, b1 INTEGER, c1 INTEGER, d1 INTEGER, e1 INTEGER, x1 VARCHAR(30))",
		"CREATE TABLE t9(a9 INTEGER, b9 INTEGER, c9 INTEGER, d9 INTEGER, e9 INTEGER, x9 VARCHAR(30))",
	} {
		if _, err := ex.Exec(ctx, ddl); err != nil {
			t.Fatalf("CREATE TABLE: %v", err)
		}
	}

	// 3 rows in t1 with a1=281.
	for i := 0; i < 3; i++ {
		ex.Exec(ctx, fmt.Sprintf("INSERT INTO t1 VALUES(281,%d,%d,%d,%d,'r1_%d')", i, i*2, i*3, i*4, i))
	}
	// 1 row in t9 with c9=232.
	ex.Exec(ctx, "INSERT INTO t9 VALUES(1,2,232,99,77,'r9_0')")

	// Query: SELECT x1, d9 FROM t1, t9 WHERE 281=a1 AND c9=232.
	// Expected: 3 * 1 = 3 rows.
	rows, err := ex.QueryAll(ctx, `SELECT x1, d9 FROM t1, t9 WHERE 281=a1 AND c9=232`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("REQ001113 under-count: want 3 rows (3 t1 with a1=281 × 1 t9 with c9=232), got %d", len(rows))
	}
}

// TestREQ001113_2TableImplicitJoin_TableOrderPermutation is the
// order-independence variant: the same query with tables swapped
// (FROM t9, t1) must produce the same 3 rows. REQ001113 explicitly
// notes that different orderings produce different wrong results.
func TestREQ001113_2TableImplicitJoin_TableOrderPermutation(t *testing.T) {
	ResetForTest(t)
	ex := NewExecutor()
	ctx := context.Background()

	for _, ddl := range []string{
		"CREATE TABLE t1(a1 INTEGER, b1 INTEGER, c1 INTEGER, d1 INTEGER, e1 INTEGER, x1 VARCHAR(30))",
		"CREATE TABLE t9(a9 INTEGER, b9 INTEGER, c9 INTEGER, d9 INTEGER, e9 INTEGER, x9 VARCHAR(30))",
	} {
		if _, err := ex.Exec(ctx, ddl); err != nil {
			t.Fatalf("CREATE TABLE: %v", err)
		}
	}

	for i := 0; i < 3; i++ {
		ex.Exec(ctx, fmt.Sprintf("INSERT INTO t1 VALUES(281,%d,%d,%d,%d,'r1_%d')", i, i*2, i*3, i*4, i))
	}
	ex.Exec(ctx, "INSERT INTO t9 VALUES(1,2,232,99,77,'r9_0')")

	// Reversed order: t9 first, t1 second.
	rows, err := ex.QueryAll(ctx, `SELECT x1, d9 FROM t9, t1 WHERE c9=232 AND 281=a1`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("REQ001113 order-dep: want 3 rows, got %d (t9,t1 order differs from t1,t9 order)", len(rows))
	}
}

// TestREQ001113_2TableImplicitJoin_SplitDistribution is a unit-level
// test of splitPredicatesByTable itself: it directly inspects the
// per-table map to confirm each constant predicate is routed to the
// correct single table. This pinpoints which sub-item of REQ001113
// is broken when end-to-end cardinality is wrong.
func TestREQ001113_2TableImplicitJoin_SplitDistribution(t *testing.T) {
	ResetForTest(t)
	ex := NewExecutor()
	for ti := 1; ti <= 9; ti++ {
		ex.RegisterTable(fmt.Sprintf("t%d", ti), []string{"a", "b", "c", "d", "e", "v"})
	}

	sql := `SELECT x1, d9 FROM t1, t9 WHERE 281=a1 AND c9=232`
	stmt, err := PS.NewParser(sql).Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	sel := stmt.(*PS.Select)
	conjuncts := RE.SplitAnd(sel.Where)
	if len(conjuncts) != 2 {
		t.Fatalf("expected 2 conjuncts, got %d", len(conjuncts))
	}

	p := NewPlanner()
	tables := []string{"t1", "t9"}
	pushed, cross := p.splitPredicatesByTable(conjuncts, tables)

	t.Logf("pushed: %v", pushed)
	t.Logf("cross: %d predicates", len(cross))

	// REQ001113 sub-item 1: each constant predicate must be pushed
	// to exactly one table. 281=a1 → t1; c9=232 → t9.
	if len(pushed["t1"]) != 1 {
		t.Errorf("t1 should have 1 pushed predicate (281=a1), got %d", len(pushed["t1"]))
	}
	if len(pushed["t9"]) != 1 {
		t.Errorf("t9 should have 1 pushed predicate (c9=232), got %d", len(pushed["t9"]))
	}
	if len(cross) != 0 {
		t.Errorf("expected 0 cross-table predicates (both are constant equality), got %d", len(cross))
	}
}
