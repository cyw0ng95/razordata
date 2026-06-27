package EX

import (
	"context"
	"testing"
)

// TestREQ000904_RecursiveCTE tests basic recursive CTE with column aliases.
func TestREQ000904_RecursiveCTE(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	e := NewExecutor()
	ctx := context.Background()

	rows, err := e.QueryAll(ctx, "WITH RECURSIVE cnt(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM cnt WHERE x<5) SELECT x FROM cnt")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(rows) != 5 {
		t.Fatalf("got %d rows, want 5", len(rows))
	}
	for i, r := range rows {
		if r.Data[0].I64 != int64(i+1) {
			t.Fatalf("row %d: got %v, want %d", i, r.Data[0], i+1)
		}
	}
}

// TestREQ000904_RecursiveCTE_Empty tests a recursive CTE that produces no rows.
func TestREQ000904_RecursiveCTE_Empty(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	e := NewExecutor()
	ctx := context.Background()

	rows, err := e.QueryAll(ctx, "WITH RECURSIVE cnt(x) AS (SELECT 1 WHERE 1=0 UNION ALL SELECT x+1 FROM cnt WHERE x<5) SELECT x FROM cnt")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("got %d rows, want 0", len(rows))
	}
}

// TestREQ000904_RecursiveCTE_NoRecursion tests that a CTE with RECURSIVE but
// no actual recursion works (the recursive arm produces no rows).
func TestREQ000904_RecursiveCTE_NoRecursion(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	e := NewExecutor()
	ctx := context.Background()

	rows, err := e.QueryAll(ctx, "WITH RECURSIVE cnt(x) AS (SELECT 1 UNION ALL SELECT 2 WHERE 1=0) SELECT x FROM cnt")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].Data[0].I64 != 1 {
		t.Fatalf("row 0: got %v, want 1", rows[0].Data[0])
	}
}

// TestREQ000904_RecursiveCTE_Union tests UNION (with dedup) in recursive CTE.
func TestREQ000904_RecursiveCTE_Union(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	e := NewExecutor()
	ctx := context.Background()

	rows, err := e.QueryAll(ctx, "WITH RECURSIVE cnt(x) AS (SELECT 1 UNION SELECT x+1 FROM cnt WHERE x<3) SELECT x FROM cnt")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3 (got %d)", len(rows), len(rows))
	}
	expected := []int64{1, 2, 3}
	for i, r := range rows {
		if r.Data[0].I64 != expected[i] {
			t.Fatalf("row %d: got %v, want %d", i, r.Data[0], expected[i])
		}
	}
}

// TestREQ000904_RecursiveCTE_Fibonacci tests a fibonacci-style recursive CTE.
func TestREQ000904_RecursiveCTE_Fibonacci(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	e := NewExecutor()
	ctx := context.Background()

	rows, err := e.QueryAll(ctx, "WITH RECURSIVE fib(a, b) AS (SELECT 0, 1 UNION ALL SELECT b, a+b FROM fib WHERE b<50) SELECT a FROM fib")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(rows) < 7 {
		t.Fatalf("got %d rows, want >= 7", len(rows))
	}
	expected := []int64{0, 1, 1, 2, 3, 5, 8}
	for i, v := range expected {
		if rows[i].Data[0].I64 != v {
			t.Fatalf("row %d: got %v, want %d", i, rows[i].Data[0], v)
		}
	}
}
