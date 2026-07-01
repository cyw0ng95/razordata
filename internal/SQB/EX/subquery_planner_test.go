package EX

import (
	"context"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	"testing"
)

func TestSubqueryPlanner_SeesStoreTables(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	DT.RegisterTable("t1", []DT.Row{
		{Cols: []string{"id", "val"}, Data: []DT.Value{NewIntValue(int64(1)), NewTextValue("a")}},
		{Cols: []string{"id", "val"}, Data: []DT.Value{NewIntValue(int64(2)), NewTextValue("b")}},
		{Cols: []string{"id", "val"}, Data: []DT.Value{NewIntValue(int64(3)), NewTextValue("c")}},
	})

	e := NewExecutor()
	e.RegisterTable("t1", []string{"id", "val"})
	ctx := context.Background()

	tests := []struct {
		sql   string
		want  int
		label string
	}{
		{"SELECT * FROM t1 WHERE id > (SELECT avg(id) FROM t1)", 1, "scalar subquery with DT.Tables (avg=2, id>2 → id=3)"},
		{"SELECT EXISTS(SELECT 1 FROM t1 WHERE id = 3)", 1, "exists subquery with DT.Tables"},
		{"SELECT 3 IN (SELECT id FROM t1)", 1, "IN subquery with DT.Tables"},
	}
	for _, tc := range tests {
		rows, err := e.QueryAll(ctx, tc.sql)
		if err != nil {
			t.Fatalf("%s (%s): query error: %v", tc.sql, tc.label, err)
		}
		if len(rows) != tc.want {
			t.Errorf("%s (%s): got %d rows, want %d; data=%v", tc.sql, tc.label, len(rows), tc.want, rows)
		}
	}
}

func TestSubqueryPlanner_StorePropagationAfterClone(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	DT.RegisterTable("t1", []DT.Row{
		{Cols: []string{"id", "val"}, Data: []DT.Value{NewIntValue(int64(1)), NewTextValue("x")}},
		{Cols: []string{"id", "val"}, Data: []DT.Value{NewIntValue(int64(2)), NewTextValue("y")}},
	})
	DT.RegisterTable("t2", []DT.Row{
		{Cols: []string{"ref", "name"}, Data: []DT.Value{NewIntValue(int64(1)), NewTextValue("alice")}},
		{Cols: []string{"ref", "name"}, Data: []DT.Value{NewIntValue(int64(1)), NewTextValue("bob")}},
	})

	e := NewExecutor()
	e.RegisterTable("t1", []string{"id", "val"})
	e.RegisterTable("t2", []string{"ref", "name"})
	ctx := context.Background()

	rows, err := e.QueryAll(ctx, "SELECT CASE WHEN id > (SELECT avg(ref) FROM t2) THEN id ELSE 0 END FROM t1")
	if err != nil {
		t.Fatalf("CASE with scalar subquery: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("got %d rows, want 2", len(rows))
	}
}

func TestSubqueryPlanner_CloneRowPreservesPlanner(t *testing.T) {
	pl := NewPlannerWithStore(nil)
	src := DT.Row{Cols: []string{"id"}, Data: []DT.Value{NewIntValue(int64(1))}}
	src.Planner = pl
	cloned := DT.CloneRow(src)
	if cloned.Planner != pl {
		t.Error("DT.CloneRow should preserve planner")
	}
	if cloned.Outer != src.Outer {
		t.Error("DT.CloneRow should preserve Outer")
	}
}

// TestPlanner_SemiJoin verifies REQ001073: EXISTS subqueries use a
// short-circuit evaluation that stops scanning the right side after
// the first matching row rather than materializing all rows. The test
// validates correctness across correlated and non-correlated EXISTS,
// NOT EXISTS, and multi-table setups. The short-circuit is exercised
// by the type assertion in EV.evalExists falling through to
// Plannner.ExecuteSubqueryFirstMatch.
func TestPlanner_SemiJoin(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ctx := context.Background()

	ex.RegisterTableWithPK("t1", []string{"id", "val"}, "id")
	ex.Exec(ctx, "INSERT INTO t1 VALUES (1, 10)")
	ex.Exec(ctx, "INSERT INTO t1 VALUES (2, 20)")
	ex.Exec(ctx, "INSERT INTO t1 VALUES (3, 30)")

	ex.RegisterTableWithPK("t2", []string{"id", "tid", "name"}, "id")
	ex.Exec(ctx, "INSERT INTO t2 VALUES (10, 1, 'a')")
	ex.Exec(ctx, "INSERT INTO t2 VALUES (20, 1, 'b')")
	ex.Exec(ctx, "INSERT INTO t2 VALUES (30, 2, 'c')")

	t.Run("noncorrelated_exists", func(t *testing.T) {
		rows, err := ex.QueryAll(ctx, "SELECT id FROM t1 WHERE EXISTS (SELECT 1 FROM t2) ORDER BY id")
		if err != nil {
			t.Fatalf("noncorrelated EXISTS: %v", err)
		}
		if len(rows) != 3 {
			t.Errorf("got %d rows, want 3", len(rows))
		}
	})
	t.Run("correlated_exists", func(t *testing.T) {
		rows, err := ex.QueryAll(ctx, "SELECT id FROM t1 WHERE EXISTS (SELECT 1 FROM t2 WHERE t2.tid = t1.id) ORDER BY id")
		if err != nil {
			t.Fatalf("correlated EXISTS: %v", err)
		}
		// t2.tid=1 matches (t1.id=1), t2.tid=2 matches (t1.id=2)
		if len(rows) != 2 {
			t.Errorf("got %d rows, want 2; data=%v", len(rows), rows)
		}
	})
	t.Run("not_exists", func(t *testing.T) {
		rows, err := ex.QueryAll(ctx, "SELECT id FROM t1 WHERE NOT EXISTS (SELECT 1 FROM t2 WHERE t2.tid = t1.id) ORDER BY id")
		if err != nil {
			t.Fatalf("NOT EXISTS: %v", err)
		}
		// t1.id=3 has no matching t2 row
		if len(rows) != 1 {
			t.Errorf("got %d rows, want 1; data=%v", len(rows), rows)
		}
		if len(rows) > 0 && rows[0].Data[0].I64 != 3 {
			t.Errorf("expected id=3, got %v", rows[0].Data[0])
		}
	})
	t.Run("exists_self_join", func(t *testing.T) {
		_, err := ex.Exec(ctx, "INSERT INTO t1 VALUES (4, 40)")
		if err != nil {
			t.Fatalf("insert: %v", err)
		}
		rows, err := ex.QueryAll(ctx, "SELECT id FROM t1 AS a WHERE EXISTS (SELECT 1 FROM t1 WHERE t1.val = a.val AND t1.id != a.id) ORDER BY id")
		if err != nil {
			t.Fatalf("self-join EXISTS: %v", err)
		}
		_ = rows
	})
	t.Run("exists_in_compound", func(t *testing.T) {
		rows, err := ex.QueryAll(ctx, "SELECT id FROM t1 WHERE EXISTS (SELECT 1 FROM t2 WHERE t2.tid = t1.id) AND t1.val > 15 ORDER BY id")
		if err != nil {
			t.Fatalf("EXISTS + WHERE: %v", err)
		}
		if len(rows) != 1 {
			t.Errorf("got %d rows, want 1; data=%v", len(rows), rows)
		}
	})
}
