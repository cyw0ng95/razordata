package EX

import (
	"context"
	"testing"
)

// TestREQ000859_NestedScalarSubquery verifies that
// `SELECT (SELECT MAX(v) FROM (SELECT v FROM t WHERE v < 30))`
// returns exactly 1 row with the correct max value, not 2 rows.
//
// REQ000859: The subquery pipeline was returning 2 rows instead of 1
// because the parser's `pendingSubquery` was leaking from nested
// subqueries. When the inner subquery `SELECT MAX(v) FROM (SELECT v FROM t WHERE v < 30)`
// set `pendingSubquery` for its own `FROM (SELECT ...)` clause,
// it leaked into the outer SELECT's `SubqueryFrom` field, causing
// the planner to treat the outer SELECT as having a FROM subquery
// and producing 2 rows.
//
// Fix: Save and restore `pendingSubquery` around nested subquery
// parsing in `parseExpr`.
func TestREQ000859_NestedScalarSubquery(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	ex := NewExecutor()
	ctx := context.Background()

	for _, s := range []string{
		"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
		"INSERT INTO t VALUES (1, 10), (2, 20), (3, 30)",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatalf("setup %q: %v", s, err)
		}
	}

	// Test the full nested scalar subquery
	rows, err := ex.QueryAll(ctx, "SELECT (SELECT MAX(v) FROM (SELECT v FROM t WHERE v < 30))")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}

	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d: %v", len(rows), rows)
	}
	if len(rows[0].Data) != 1 {
		t.Fatalf("expected 1 column, got %d", len(rows[0].Data))
	}
	val := rows[0].Data[0].ToAny()
	if val != int64(20) {
		t.Fatalf("expected 20, got %v", val)
	}
}

// TestREQ000962_OuterScalarSubquerySingleRow verifies that
// `SELECT (subquery)` with no FROM clause returns exactly 1 row.
//
// REQ000962: The outer scalar subquery was returning 2 rows instead of 1.
// Root cause: the parser's `pendingSubquery` leaked from nested subqueries
// (same root cause as REQ000859).
func TestREQ000962_OuterScalarSubquerySingleRow(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	ex := NewExecutor()
	ctx := context.Background()

	for _, s := range []string{
		"CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)",
		"INSERT INTO t VALUES (1, 10), (2, 20), (3, 30)",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatalf("setup %q: %v", s, err)
		}
	}

	// Test: SELECT (SELECT 1) should return exactly 1 row
	rows, err := ex.QueryAll(ctx, "SELECT (SELECT 1)")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}

	// Test: SELECT (SELECT MAX(v) FROM t) should return exactly 1 row
	rows, err = ex.QueryAll(ctx, "SELECT (SELECT MAX(v) FROM t)")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Data[0].ToAny() != int64(30) {
		t.Fatalf("expected 30, got %v", rows[0].Data[0].ToAny())
	}
}