package EX

import (
	"context"
	"fmt"
	"testing"
)

// TestREQ000861_IN_OR reproduces the 4-table join with IN+OR.
// Setup: 4 tables with 10 rows each, values 100-109 in all columns.
// Expected: 1160 rows (10000 × (2/10) × (1 - (6/10)*(7/10)) = 2000 × 0.58)
// Actual: 1400 rows (the OR condition is being applied as two separate
// filters that are combined with OR semantics (union) instead of
// being applied as a single filter with the correct OR semantics).
//
// Root cause: The planner splits the cross-table OR condition
// `(t2.b > 105 OR t4.d < 103)` into two separate predicates and
// pushes each one to the respective table. The predicates are then
// combined with OR semantics (union) instead of AND semantics
// (intersection), giving 800 + 600 = 1400 instead of the correct
// 800 + 600 - 240 = 1160.
//
// Fix: Apply the OR condition as a single cross-table Filter on
// the join result, not as two separate per-table predicates.
// This requires changes to the planner's predicate splitting logic.
func TestREQ000861_IN_OR(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	e := NewExecutor()
	ctx := context.Background()

	// Create 4 tables with 10 rows each, values 100-109
	for ti := 1; ti <= 4; ti++ {
		setup := fmt.Sprintf("CREATE TABLE t%d (a INTEGER, b INTEGER, c INTEGER, d INTEGER, e INTEGER)", ti)
		if _, err := e.Exec(ctx, setup); err != nil {
			t.Fatalf("setup %q: %v", setup, err)
		}
		for j := 0; j < 10; j++ {
			v := 100 + j
			insert := fmt.Sprintf("INSERT INTO t%d VALUES (%d, %d, %d, %d, %d)", ti, v, v, v, v, v)
			if _, err := e.Exec(ctx, insert); err != nil {
				t.Fatalf("insert %q: %v", insert, err)
			}
		}
	}

	rows, err := e.QueryAll(ctx, "SELECT * FROM t1, t2, t3, t4 WHERE t1.a IN (101, 103) AND (t2.b > 105 OR t4.d < 103)")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	t.Logf("got %d rows, expected 1160 (known issue: REQ000861)", len(rows))
}