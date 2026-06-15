package EX

import (
	"context"
	"testing"
)

func TestJOIN_DuplicateRows(t *testing.T) {
	e := NewExecutor()
	ctx := context.Background()

	for _, s := range []string{
		"CREATE TABLE t1 (id INTEGER PRIMARY KEY, val INTEGER)",
		"CREATE TABLE t2 (id INTEGER PRIMARY KEY, val INTEGER)",
		"INSERT INTO t1 VALUES (1, 10), (2, 20), (3, 30)",
		"INSERT INTO t2 VALUES (1, 100), (2, 200)",
	} {
		if _, err := e.Exec(ctx, s); err != nil {
			t.Fatalf("setup %q: %v", s, err)
		}
	}

	rows, err := e.QueryAll(ctx, "SELECT * FROM t1, t2")
	if err != nil {
		t.Fatalf("cross join: %v", err)
	}
	if len(rows) != 6 {
		t.Fatalf("CROSS JOIN: got %d rows, want 6; data=%v", len(rows), rows)
	}

	rows, err = e.QueryAll(ctx, "SELECT * FROM t1 CROSS JOIN t2")
	if err != nil {
		t.Fatalf("cross join: %v", err)
	}
	if len(rows) != 6 {
		t.Fatalf("CROSS JOIN explicit: got %d rows, want 6", len(rows))
	}

	rows, err = e.QueryAll(ctx, "SELECT * FROM t1 INNER JOIN t2 ON t1.id = t2.id")
	if err != nil {
		t.Fatalf("inner join: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("INNER JOIN: got %d rows, want 2; data=%v", len(rows), rows)
	}

	rows, err = e.QueryAll(ctx, "SELECT * FROM t1 LEFT JOIN t2 ON t1.id = t2.id")
	if err != nil {
		t.Fatalf("left join: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("LEFT JOIN: got %d rows, want 3", len(rows))
	}
}
