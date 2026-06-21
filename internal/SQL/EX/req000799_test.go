package EX

import (
	"context"
	"testing"
)

// TestJoinElimination_UnreferencedTable verifies REQ000799:
// tables not referenced in any part of the query are eliminated.
func TestJoinElimination_UnreferencedTable(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	e := NewExecutor()
	ctx := context.Background()

	for _, s := range []string{
		"CREATE TABLE t1(a INTEGER)",
		"CREATE TABLE t2(b INTEGER)",
		"CREATE TABLE t3(c INTEGER)",
		"INSERT INTO t1 VALUES (1), (2), (3)",
		"INSERT INTO t2 VALUES (10), (20), (30)",
		"INSERT INTO t3 VALUES (100), (200), (300)",
	} {
		if _, err := e.Exec(ctx, s); err != nil {
			t.Fatalf("setup %q: %v", s, err)
		}
	}

	// Query with unqualified column — can't eliminate (safety)
	rows, err := e.QueryAll(ctx, "SELECT a FROM t1, t2, t3")
	if err != nil {
		t.Fatalf("query unqualified: %v", err)
	}
	// Unqualified `a` can't prove t2/t3 are unused — keep all
	if len(rows) != 27 {
		t.Errorf("unqualified: expected 27 rows, got %d", len(rows))
	}

	// Query with qualified column — can eliminate
	rows, err = e.QueryAll(ctx, "SELECT t1.a FROM t1, t2, t3")
	if err != nil {
		t.Fatalf("query qualified: %v", err)
	}
	// t1.a is the only reference — t2, t3 eliminated → 3 rows
	if len(rows) != 3 {
		t.Errorf("qualified: expected 3 rows after elimination, got %d", len(rows))
	}

	// Query referencing t2 in WHERE — t3 eliminated
	rows, err = e.QueryAll(ctx, "SELECT t1.a FROM t1, t2, t3 WHERE t2.b > 15")
	if err != nil {
		t.Fatalf("query where: %v", err)
	}
	// t1 referenced in SELECT, t2 referenced in WHERE, t3 unreferenced
	// Without elimination: 3*3*3=27 rows, WHERE filters to 18.
	// With elimination: 3*3=9 rows, WHERE filters to 6.
	if len(rows) != 6 {
		t.Errorf("where clause: expected 6 rows after elimination, got %d", len(rows))
	}

	// Query with * — can't eliminate anything
	rows, err = e.QueryAll(ctx, "SELECT * FROM t1, t2, t3")
	if err != nil {
		t.Fatalf("query star: %v", err)
	}
	if len(rows) != 27 {
		t.Errorf("star: expected 27 rows, got %d", len(rows))
	}
}

// TestHashCrossJoin_SmallTables verifies REQ000800:
// hash-based cross join for small materialized tables.
func TestHashCrossJoin_SmallTables(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	e := NewExecutor()
	ctx := context.Background()

	for _, s := range []string{
		"CREATE TABLE t1(a INTEGER, b INTEGER)",
		"CREATE TABLE t2(c INTEGER, d INTEGER)",
		"INSERT INTO t1 VALUES (1,10), (2,20), (3,30)",
		"INSERT INTO t2 VALUES (1,100), (2,200)",
	} {
		if _, err := e.Exec(ctx, s); err != nil {
			t.Fatalf("setup %q: %v", s, err)
		}
	}

	// Cross join — 3*2 = 6 rows
	rows, err := e.QueryAll(ctx, "SELECT t1.a, t2.c FROM t1, t2")
	if err != nil {
		t.Fatalf("cross join: %v", err)
	}
	if len(rows) != 6 {
		t.Errorf("cross join: expected 6 rows, got %d", len(rows))
	}

	// Verify all combinations exist
	seen := map[[2]int64]bool{}
	for _, r := range rows {
		key := [2]int64{r.Data[0].(int64), r.Data[1].(int64)}
		seen[key] = true
	}
	for _, a := range []int64{1, 2, 3} {
		for _, c := range []int64{1, 2} {
			if !seen[[2]int64{a, c}] {
				t.Errorf("missing combination (%d, %d)", a, c)
			}
		}
	}
}

// TestHashCrossJoin_ThreeTables verifies REQ000800 with 3 tables.
func TestHashCrossJoin_ThreeTables(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	e := NewExecutor()
	ctx := context.Background()

	for _, s := range []string{
		"CREATE TABLE t1(a INTEGER)",
		"CREATE TABLE t2(b INTEGER)",
		"CREATE TABLE t3(c INTEGER)",
		"INSERT INTO t1 VALUES (1), (2)",
		"INSERT INTO t2 VALUES (10), (20)",
		"INSERT INTO t3 VALUES (100)",
	} {
		if _, err := e.Exec(ctx, s); err != nil {
			t.Fatalf("setup %q: %v", s, err)
		}
	}

	// 3 tables: 2*2*1 = 4 rows
	rows, err := e.QueryAll(ctx, "SELECT t1.a, t2.b, t3.c FROM t1, t2, t3")
	if err != nil {
		t.Fatalf("3-table cross: %v", err)
	}
	if len(rows) != 4 {
		t.Errorf("3-table cross: expected 4 rows, got %d", len(rows))
	}
}

// TestHashCrossJoin_LeftJoinFallsBack verifies REQ000800:
// LEFT JOINs don't use hash mode (needs NULL padding logic).
func TestHashCrossJoin_LeftJoinFallsBack(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	e := NewExecutor()
	ctx := context.Background()

	for _, s := range []string{
		"CREATE TABLE t1(a INTEGER)",
		"CREATE TABLE t2(b INTEGER)",
		"INSERT INTO t1 VALUES (1), (2), (3)",
		"INSERT INTO t2 VALUES (10), (20)",
	} {
		if _, err := e.Exec(ctx, s); err != nil {
			t.Fatalf("setup %q: %v", s, err)
		}
	}

	// LEFT JOIN — all left rows present (3 rows with matching right + NULL)
	rows, err := e.QueryAll(ctx, "SELECT t1.a, t2.b FROM t1 LEFT JOIN t2 ON t1.a = t2.b")
	if err != nil {
		t.Fatalf("left join: %v", err)
	}
	// Only t1.a=1 matches t2.b=10. t1.a=2,3 get NULL right.
	if len(rows) != 3 {
		t.Errorf("left join: expected 3 rows, got %d", len(rows))
	}
}
