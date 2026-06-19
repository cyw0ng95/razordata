package EX

import (
	"context"
	"testing"
)

func TestSubqueryPlanner_SeesStoreTables(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	RegisterTable("t1", []Row{
		{Cols: []string{"id", "val"}, Data: []any{int64(1), "a"}},
		{Cols: []string{"id", "val"}, Data: []any{int64(2), "b"}},
		{Cols: []string{"id", "val"}, Data: []any{int64(3), "c"}},
	})

	e := NewExecutor()
	e.RegisterTable("t1", []string{"id", "val"})
	ctx := context.Background()

	tests := []struct {
		sql   string
		want  int
		label string
	}{
		{"SELECT * FROM t1 WHERE id > (SELECT avg(id) FROM t1)", 1, "scalar subquery with tables (avg=2, id>2 → id=3)"},
		{"SELECT EXISTS(SELECT 1 FROM t1 WHERE id = 3)", 1, "exists subquery with tables"},
		{"SELECT 3 IN (SELECT id FROM t1)", 1, "IN subquery with tables"},
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

	RegisterTable("t1", []Row{
		{Cols: []string{"id", "val"}, Data: []any{int64(1), "x"}},
		{Cols: []string{"id", "val"}, Data: []any{int64(2), "y"}},
	})
	RegisterTable("t2", []Row{
		{Cols: []string{"ref", "name"}, Data: []any{int64(1), "alice"}},
		{Cols: []string{"ref", "name"}, Data: []any{int64(1), "bob"}},
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
	src := Row{Cols: []string{"id"}, Data: []any{int64(1)}}
	src.planner = pl
	cloned := cloneRow(src)
	if cloned.planner != pl {
		t.Error("cloneRow should preserve planner")
	}
	if cloned.Outer != src.Outer {
		t.Error("cloneRow should preserve Outer")
	}
}
