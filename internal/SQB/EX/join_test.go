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

// TestNLJ_SelfJoin_DataIndependence verifies that a self-join cross product
// produces 9 distinct rows with correct data (REQ000961).
func TestNLJ_SelfJoin_DataIndependence(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	e := NewExecutor()
	ctx := context.Background()

	for _, s := range []string{
		"CREATE TABLE t (id INTEGER, val INTEGER)",
		"INSERT INTO t VALUES (1, 10), (2, 20), (3, 30)",
	} {
		if _, err := e.Exec(ctx, s); err != nil {
			t.Fatalf("setup %q: %v", s, err)
		}
	}

	rows, err := e.QueryAll(ctx, "SELECT a.id, a.val, b.id, b.val FROM t a, t b ORDER BY a.id, b.id")
	if err != nil {
		t.Fatalf("self-join: %v", err)
	}
	if len(rows) != 9 {
		t.Fatalf("expected 9 rows (3×3), got %d", len(rows))
	}

	// Verify all 9 combinations are correct.
	expected := []struct {
		aID, aVal, bID, bVal int64
	}{
		{1, 10, 1, 10}, {1, 10, 2, 20}, {1, 10, 3, 30},
		{2, 20, 1, 10}, {2, 20, 2, 20}, {2, 20, 3, 30},
		{3, 30, 1, 10}, {3, 30, 2, 20}, {3, 30, 3, 30},
	}
	for i, exp := range expected {
		r := rows[i]
		if len(r.Data) != 4 {
			t.Fatalf("row %d: expected 4 cols, got %d", i, len(r.Data))
		}
		got := []int64{
			r.Data[0].ToAny().(int64),
			r.Data[1].ToAny().(int64),
			r.Data[2].ToAny().(int64),
			r.Data[3].ToAny().(int64),
		}
		want := []int64{exp.aID, exp.aVal, exp.bID, exp.bVal}
		if got[0] != want[0] || got[1] != want[1] || got[2] != want[2] || got[3] != want[3] {
			t.Fatalf("row %d: got %v, want %v", i, got, want)
		}
	}
}
