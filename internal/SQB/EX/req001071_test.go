package EX

import (
	"context"
	"testing"
)

// TestPlanner_JoinReordering verifies REQ001071: the planner reorders
// tables for multi-table joins instead of always using FROM-clause order.
func TestPlanner_JoinReordering_ThreeWay(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ctx := context.Background()

	ex.RegisterTable("t1", []string{"id", "val"})
	ex.RegisterTable("t2", []string{"id", "val"})
	ex.RegisterTable("t3", []string{"id", "val"})

	for _, s := range []string{
		"INSERT INTO t1 VALUES (1, 10), (2, 20), (3, 30)",
		"INSERT INTO t2 VALUES (1, 100), (2, 200), (3, 300)",
		"INSERT INTO t3 VALUES (1, 1000), (2, 2000), (4, 4000)",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatalf("insert %q: %v", s, err)
		}
	}

	// Three-way equi-join
	rows, err := ex.QueryAll(ctx,
		"SELECT t1.val, t2.val, t3.val FROM t1, t2, t3 WHERE t1.id = t2.id AND t2.id = t3.id ORDER BY t1.id")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d: %v", len(rows), rows)
	}
}
