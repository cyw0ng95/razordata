package EX

import (
	"context"
	"strconv"
	"testing"
)

// REQ000714: store-path DELETE with self-referencing subquery. The
// subquery's SeqScan runs against the same table being deleted from;
// it must observe a pre-mutation snapshot, not the in-progress mutation.
func TestREQ000714_StoreDeleteSelfSubquery(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex, eng := newEngineExecutor(t)
	defer eng.Close()

	ctx := context.Background()
	if _, err := ex.Exec(ctx, "CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, pair := range [][2]int{{1, 10}, {2, 20}, {3, 30}, {4, 40}} {
		if _, err := ex.Exec(ctx, "INSERT INTO t VALUES ("+strconv.Itoa(pair[0])+", "+strconv.Itoa(pair[1])+")"); err != nil {
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
