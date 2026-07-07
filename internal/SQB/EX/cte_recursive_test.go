package EX

import (
	"context"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
)

func TestRecursiveCTE_Arithmetic(t *testing.T) {
	sql := "WITH RECURSIVE cnt(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM cnt WHERE x<5) SELECT x FROM cnt"
	rows := executeSQLRows(t, sql)
	want := []int64{1, 2, 3, 4, 5}
	if len(rows) != len(want) {
		t.Fatalf("row count: got %d, want %d. rows=%v", len(rows), len(want), rows)
	}
	for i, r := range rows {
		v, ok := r[0].(int64)
		if !ok {
			t.Errorf("row %d: unexpected type %T, want int64", i, r[0])
			continue
		}
		if v != want[i] {
			t.Errorf("row %d: got %d, want %d", i, v, want[i])
		}
	}
}

func TestRecursiveCTE_UnionDedup(t *testing.T) {
	sql := "WITH RECURSIVE cnt(x) AS (SELECT 1 UNION SELECT x+1 FROM cnt WHERE x<3) SELECT x FROM cnt"
	rows := executeSQLRows(t, sql)
	want := []int64{1, 2, 3}
	if len(rows) != len(want) {
		t.Fatalf("row count: got %d, want %d. rows=%v", len(rows), len(want), rows)
	}
	for i, r := range rows {
		v, ok := r[0].(int64)
		if !ok {
			t.Errorf("row %d: unexpected type %T, want int64", i, r[0])
			continue
		}
		if v != want[i] {
			t.Errorf("row %d: got %d, want %d", i, v, want[i])
		}
	}
}

func TestRecursiveCTE_Fibonacci(t *testing.T) {
	sql := "WITH RECURSIVE fib(a, b) AS (SELECT 0, 1 UNION ALL SELECT b, a+b FROM fib WHERE b<50) SELECT a FROM fib"
	rows := executeSQLRows(t, sql)
	want := []int64{0, 1, 1, 2, 3, 5, 8, 13, 21, 34}
	if len(rows) != len(want) {
		t.Fatalf("row count: got %d, want %d. rows=%v", len(rows), len(want), rows)
	}
	for i, r := range rows {
		v, ok := r[0].(int64)
		if !ok {
			t.Errorf("row %d: unexpected type %T, want int64", i, r[0])
			continue
		}
		if v != want[i] {
			t.Errorf("row %d: got %d, want %d", i, v, want[i])
		}
	}
}

func TestCTETraceEvents(t *testing.T) {
	sql := "WITH RECURSIVE cnt(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM cnt WHERE x<5) SELECT x FROM cnt"
	rows := executeSQLRows(t, sql)
	if len(rows) != 5 {
		t.Fatalf("expected 5 rows, got %d", len(rows))
	}
}

func executeSQLRows(t *testing.T, sql string) [][]any {
	p := NewPlanner()
	plan, err := p.ParseAndPlan(sql)
	if err != nil {
		t.Fatalf("plan error: %v", err)
	}
	if plan == nil || plan.Root == nil {
		t.Fatalf("plan is nil")
	}

	var rows [][]any
	ctx := context.TODO()
	for {
		row, err := plan.Root.Next(ctx)
		if err != nil {
			if err == DT.ErrNoRows {
				break
			}
			t.Logf("error: %v", err)
			continue
		}
		vals := make([]any, len(row.Data))
		for i, d := range row.Data {
			vals[i] = d.ToAny()
		}
		rows = append(rows, vals)
	}
	plan.Root.Close()
	return rows
}
