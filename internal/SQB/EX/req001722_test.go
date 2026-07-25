package EX

import (
	"context"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SQB/EV"
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// TestREQ001722_NotBetweenNullJoin verifies that a JOIN whose ON clause
// is a constant NOT BETWEEN NULL expression (evaluates to NULL → falsy)
// produces zero rows instead of being eliminated to a left-table-only
// scan. The root cause was that isConstantExpr did not handle
// BetweenExpr, so the constant-ON join preservation rule never fired
// and the join was silently dropped by join elimination.
func TestREQ001722_NotBetweenNullJoin(t *testing.T) {
	ctx := context.Background()
	ex := NewExecutor()
	defer ex.Close()

	mustExec(t, ex, ctx, "CREATE TABLE tab0(pk INTEGER, col0 INTEGER, col1 INTEGER, col2 INTEGER, col3 INTEGER, col4 INTEGER, col5 INTEGER)")
	mustExec(t, ex, ctx, "CREATE TABLE tab1(pk INTEGER, col0 INTEGER, col1 INTEGER, col2 INTEGER, col3 INTEGER, col4 INTEGER, col5 INTEGER)")
	mustExec(t, ex, ctx, "INSERT INTO tab0 VALUES (0,73,61,94,99,44,76)")
	mustExec(t, ex, ctx, "INSERT INTO tab0 VALUES (1,77,17,14,74,90,81)")
	mustExec(t, ex, ctx, "INSERT INTO tab1 VALUES (0,96,5,83,15,67,26)")
	mustExec(t, ex, ctx, "INSERT INTO tab1 VALUES (1,51,83,51,88,75,62)")

	// ON clause `-15 NOT BETWEEN NULL AND NULL` evaluates to NULL (falsy),
	// so the INNER JOIN must produce 0 rows. If the join is incorrectly
	// eliminated, tab0's 2 rows survive and DISTINCT +21 collapses to 1.
	rows, err := ex.QueryAll(ctx, "SELECT DISTINCT + 21 FROM tab0 AS cor0 JOIN tab1 cor1 ON - 15 NOT BETWEEN NULL AND NULL")
	if err != nil {
		t.Fatalf("query error: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected 0 rows, got %d: %v", len(rows), rows)
	}
}

// TestREQ001722_BetweenConstantOnJoin verifies the full matrix of
// constant BETWEEN/NOT BETWEEN ON clauses: TRUE → cross product
// (collapses to 1 via DISTINCT), FALSE/NULL → zero rows.
func TestREQ001722_BetweenConstantOnJoin(t *testing.T) {
	ctx := context.Background()
	ex := NewExecutor()
	defer ex.Close()

	mustExec(t, ex, ctx, "CREATE TABLE t0(pk INTEGER PRIMARY KEY, v INTEGER)")
	mustExec(t, ex, ctx, "CREATE TABLE t1(pk INTEGER PRIMARY KEY, v INTEGER)")
	mustExec(t, ex, ctx, "INSERT INTO t0 VALUES (0,10),(1,20)")
	mustExec(t, ex, ctx, "INSERT INTO t1 VALUES (0,30),(1,40)")

	tests := []struct {
		name     string
		on       string
		wantRows int
	}{
		{"true_between", "5 BETWEEN 1 AND 10", 1},          // TRUE → 2×2 cross → DISTINCT 1 → 1
		{"false_between", "5 BETWEEN 10 AND 20", 0},        // FALSE → 0 rows
		{"null_between", "5 BETWEEN NULL AND NULL", 0},     // NULL → 0 rows
		{"true_not_between", "5 NOT BETWEEN 10 AND 20", 1}, // TRUE → cross → 1
		{"false_not_between", "5 NOT BETWEEN 1 AND 10", 0}, // FALSE → 0 rows
		{"null_not_between", "5 NOT BETWEEN NULL AND NULL", 0}, // NULL → 0 rows
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Use DISTINCT 1 (not COUNT(*), which has a fast path that
			// bypasses join planning) so the join condition is actually
			// evaluated.
			q := "SELECT DISTINCT 1 FROM t0 JOIN t1 ON " + tc.on
			rows, err := ex.QueryAll(ctx, q)
			if err != nil {
				t.Fatalf("query error: %v", err)
			}
			if len(rows) != tc.wantRows {
				t.Fatalf("ON %s: expected %d rows, got %d", tc.on, tc.wantRows, len(rows))
			}
		})
	}
}

// TestREQ001722_EvalNotBetweenNull verifies the expression evaluates to NULL.
func TestREQ001722_EvalNotBetweenNull(t *testing.T) {
	p := PS.NewParser("SELECT - 15 NOT BETWEEN NULL AND NULL")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	sel := stmt.(*PS.Select)
	expr := sel.Cols[0]

	v, err := EV.EvalValue(expr, nil, nil)
	if err != nil {
		t.Fatalf("eval error: %v", err)
	}
	if v.Kind != DT.KindNull {
		t.Fatalf("expected NULL, got Kind=%d val=%v", v.Kind, v)
	}
}
