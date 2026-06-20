package EX

import (
	"context"
	"strconv"
	"testing"
)

// REQ000714: DELETE with correlated subquery in WHERE that references the
// table being deleted from. The subquery should see the pre-mutation
// snapshot, not the rows being deleted.
//
// Setup: t contains (1, 10), (2, 20), (3, 30), (4, 40).
// AVG(v) = 25.  Rows with v > 25 should be deleted: ids 3, 4.
// Expected remaining: (1, 10), (2, 20).
func TestREQ000714_DeleteWithSelfSubquery(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"id", "v"})

	ctx := context.Background()
	for _, pair := range [][2]any{{1, 10}, {2, 20}, {3, 30}, {4, 40}} {
		if _, err := ex.Exec(ctx, "INSERT INTO t VALUES ("+strconv.Itoa(pair[0].(int))+", "+strconv.Itoa(pair[1].(int))+")"); err != nil {
			t.Fatalf("insert %v: %v", pair, err)
		}
	}

	res, err := ex.Exec(ctx, "DELETE FROM t WHERE v > (SELECT AVG(v) FROM t)")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	t.Logf("rows affected: %d", res.RowsAffected)

	rows, err := ex.QueryAll(ctx, "SELECT id, v FROM t ORDER BY id")
	if err != nil {
		t.Fatalf("query after: %v", err)
	}
	t.Logf("remaining: %v", rows)

	if res.RowsAffected != 2 {
		t.Errorf("expected 2 rows deleted, got %d", res.RowsAffected)
	}
	if len(rows) != 2 {
		t.Errorf("expected 2 rows remaining, got %d", len(rows))
	}
}


