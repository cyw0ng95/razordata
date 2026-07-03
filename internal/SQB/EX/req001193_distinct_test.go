package EX

import (
	"context"
	"testing"
)

func TestReq001193_DistinctExactQueries(t *testing.T) {
	ResetForTest(t)
	ctx := context.Background()
	ex := NewExecutor()

	mustExec(t, ex, ctx, "CREATE TABLE tab1 (a INTEGER)")
	mustExec(t, ex, ctx, "INSERT INTO tab1 VALUES (1), (2), (3)")

	// Query from REQ: SELECT DISTINCT * FROM tab1 AS cor0
	rows, err := ex.QueryAll(ctx, "SELECT DISTINCT * FROM tab1 AS cor0")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 3 {
		t.Errorf("DISTINCT * FROM tab1 AS cor0: expected 3 rows, got %d", len(rows))
	}

	// Query from REQ: SELECT DISTINCT + 77 AS col1, COUNT( * ) - 11
	// This requires a GROUP BY or it's just selecting constants with aggregate
	// Let's try the exact form
	mustExec(t, ex, ctx, "CREATE TABLE tab2 (x INTEGER)")
	mustExec(t, ex, ctx, "INSERT INTO tab2 VALUES (1), (2), (3)")
	rows2, err := ex.QueryAll(ctx, "SELECT DISTINCT +77 AS col1, COUNT(*) - 11 FROM tab2")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	// Without GROUP BY, COUNT(*) = 3, so result should be 1 row: (77, -8)
	t.Logf("DISTINCT +77, COUNT-11: %d rows", len(rows2))
	for _, r := range rows2 {
		t.Logf("  %v", r.Data)
	}
	if len(rows2) != 1 {
		t.Errorf("expected 1 row, got %d", len(rows2))
	}
}
