package EX

import (
	"context"
	"testing"
)

// TestPlanner_PredicatePushdownSubquery verifies REQ001072: predicates
// from the outer WHERE are pushed into subquery's WHERE clause.
func TestPlanner_PredicatePushdownSubquery(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ctx := context.Background()

	ex.RegisterTable("t", []string{"id", "x", "y"})

	for _, s := range []string{
		"INSERT INTO t VALUES (1, 10, 100), (2, 20, 200), (3, 10, 300), (4, 30, 100)",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatalf("setup %q: %v", s, err)
		}
	}

	// Subquery filters x > 10, outer filters y < 200.
	// With pushdown: the combined filter inside subquery is x > 10 AND y < 200.
	// Matching rows: (4, 30, 100) — x=30>10, y=100<200.
	rows, err := ex.QueryAll(ctx,
		"SELECT * FROM (SELECT * FROM t WHERE x > 10) sq WHERE y < 200 ORDER BY id")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d: %v", len(rows), rows)
	}
	if rows[0].Data[0].ToAny().(int64) != 4 {
		t.Errorf("expected id=4, got %v", rows[0].Data[0].ToAny())
	}
}

// TestPlanner_PredicatePushdownSubquery_NonFlattenable verifies that
// predicates are NOT pushed into subqueries with aggregation (would change semantics).
func TestPlanner_PredicatePushdownSubquery_NonFlattenable(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ctx := context.Background()

	ex.RegisterTable("t", []string{"id", "grp", "val"})

	for _, s := range []string{
		"INSERT INTO t VALUES (1, 1, 10), (2, 1, 20), (3, 2, 30), (4, 2, 40)",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatalf("setup %q: %v", s, err)
		}
	}

	// Subquery has GROUP BY — predicates should NOT be pushed in.
	rows, err := ex.QueryAll(ctx,
		"SELECT * FROM (SELECT grp, SUM(val) AS total FROM t GROUP BY grp) sq WHERE total > 30 ORDER BY grp")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d: %v", len(rows), rows)
	}
	if rows[0].Data[1].ToAny().(int64) != 70 {
		t.Errorf("expected total=70, got %v", rows[0].Data[1].ToAny())
	}
}
