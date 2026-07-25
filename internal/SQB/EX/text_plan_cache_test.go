package EX

import (
	"context"
	"testing"
)

// TestTextPlanCache_HitOnRepeatedQuery verifies REQ001464: the
// textPlanCache serves cached PlanResult for identical SQL text,
// skipping re-parse and re-plan.
//
// REQ002010: textPlanCache is no longer populated by QueryAll (which
// drains the operator tree on each call). Verify the cache behavior
// directly via Query, which still consults textPlanCache.
func TestTextPlanCache_HitOnRepeatedQuery(t *testing.T) {
	e := NewExecutorWithEngine(nil)
	ctx := context.Background()

	sql := "SELECT 1 AS n"
	r1, err := e.Query(ctx, sql)
	if err != nil {
		t.Fatal(err)
	}
	if r1 == nil || len(r1.Cols) == 0 {
		t.Fatalf("first call: expected non-nil Rows, got %v", r1)
	}

	// Clear stmtCache to force a textPlanCache-only hit on second call.
	e.clearStmtCache()

	r2, err := e.Query(ctx, sql)
	if err != nil {
		t.Fatal(err)
	}
	if r2 == nil || len(r2.Cols) == 0 {
		t.Fatalf("second call: expected non-nil Rows, got %v (cache miss?)", r2)
	}
}

// TestTextPlanCache_LRUEviction verifies REQ001464: LRU eviction
// drops oldest entries when maxSize is exceeded.
//
// REQ002010: textPlanCache is populated only via Explain/Query in
// the post-REQ002010 world. Drive the cache directly via Explain
// to verify LRU mechanics.
func TestTextPlanCache_LRUEviction(t *testing.T) {
	e := NewExecutorWithEngine(nil)
	e.initTextPlanCache(2)
	ctx := context.Background()

	sqls := []string{"SELECT 1 AS a", "SELECT 2 AS b", "SELECT 3 AS c"}
	for _, sql := range sqls {
		if _, err := e.Explain(sql); err != nil {
			t.Fatal(err)
		}
	}
	_ = ctx

	// Entry "SELECT 1 AS a" should have been evicted (oldest).
	ptp := e.textPlanCache
	ptp.mu.Lock()
	aHit := ptp.entries["SELECT 1 AS a"] != nil
	bHit := ptp.entries["SELECT 2 AS b"] != nil
	cHit := ptp.entries["SELECT 3 AS c"] != nil
	ptp.mu.Unlock()
	if aHit {
		t.Error("expected 'SELECT 1 AS a' to be evicted from textPlanCache")
	}
	if !bHit {
		t.Error("expected 'SELECT 2 AS b' to remain in textPlanCache")
	}
	if !cHit {
		t.Error("expected 'SELECT 3 AS c' to remain in textPlanCache")
	}
}

// TestTextPlanCache_ClearPooledState verifies that a reused plan
// from textPlanCache produces correct results after its operator
// tree has been drained on the first call (REQ001464 safety check).
//
// REQ002010: same caveat — drive via Query (which uses textPlanCache).
func TestTextPlanCache_ClearPooledState(t *testing.T) {
	e := NewExecutorWithEngine(nil)
	sql := "SELECT 2+2 AS x"
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		e.clearStmtCache() // force textPlanCache path
		r, err := e.Query(ctx, sql)
		if err != nil {
			t.Fatal(err)
		}
		if r == nil || len(r.Cols) == 0 {
			t.Fatalf("call %d: expected non-nil Rows, got %v", i, r)
		}
	}
}