package EX

import (
	"context"
	"testing"
)

// TestTextPlanCache_HitOnRepeatedQuery verifies REQ001464: the
// textPlanCache serves cached PlanResult for identical SQL text,
// skipping re-parse and re-plan.
func TestTextPlanCache_HitOnRepeatedQuery(t *testing.T) {
	e := NewExecutorWithEngine(nil)
	ctx := context.Background()

	sql := "SELECT 1 AS n"
	rows, err := e.QueryAll(ctx, sql)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Data[0].I64 != 1 {
		t.Fatalf("first call: expected [{n: 1}], got %v", rows)
	}

	// Clear stmtCache to force a textPlanCache-only hit on second call.
	e.clearStmtCache()

	rows, err = e.QueryAll(ctx, sql)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Data[0].I64 != 1 {
		t.Fatalf("second call: expected [{n: 1}], got %v (cache miss?)", rows)
	}
}

// TestTextPlanCache_LRUEviction verifies REQ001464: LRU eviction
// drops oldest entries when maxSize is exceeded.
func TestTextPlanCache_LRUEviction(t *testing.T) {
	e := NewExecutorWithEngine(nil)
	e.initTextPlanCache(2)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		sql := "SELECT 1 AS n"
		if i == 0 {
			sql = "SELECT 1 AS a"
		} else if i == 1 {
			sql = "SELECT 2 AS b"
		}
		_, err := e.QueryAll(ctx, sql)
		if err != nil {
			t.Fatal(err)
		}
	}

	// Entry "SELECT 1 AS a" should have been evicted (oldest).
	// "SELECT 2 AS b" and "SELECT 1 AS n" should remain.
	e.clearStmtCache()

	_, err := e.QueryAll(ctx, "SELECT 2 AS b")
	if err != nil {
		t.Fatal(err)
	}
	// Should still be in textPlanCache — no parse needed.
	ptp := e.textPlanCache
	ptp.mu.Lock()
	aHit := ptp.entries["SELECT 1 AS a"] != nil
	bHit := ptp.entries["SELECT 2 AS b"] != nil
	nHit := ptp.entries["SELECT 1 AS n"] != nil
	ptp.mu.Unlock()
	if aHit {
		t.Error("expected 'SELECT 1 AS a' to be evicted from textPlanCache")
	}
	if !bHit {
		t.Error("expected 'SELECT 2 AS b' to remain in textPlanCache")
	}
	if !nHit {
		t.Error("expected 'SELECT 1 AS n' to remain in textPlanCache")
	}
}

// TestTextPlanCache_ClearPooledState verifies that a reused plan
// from textPlanCache produces correct results after its operator
// tree has been drained on the first call (REQ001464 safety check).
func TestTextPlanCache_ClearPooledState(t *testing.T) {
	e := NewExecutorWithEngine(nil)
	// Use a query that creates a SeqScan-based plan (stateful).
	sql := "SELECT 2+2 AS x"
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		e.clearStmtCache() // force textPlanCache path
		rows, err := e.QueryAll(ctx, sql)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || rows[0].Data[0].I64 != 4 {
			t.Fatalf("call %d: expected [{x: 4}], got %v", i, rows)
		}
	}
}
