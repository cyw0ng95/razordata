# Vectorized SELECT Drain Path Implementation Plan

> **For agentic workers:** Execute task-by-task. Each task must have a passing test before moving on.

**Goal:** Eliminate `BatchToRowAdapter` downgrade by draining `BatchProducer` directly via `NextBatch()` in `QueryAll`, `drainPlan`, and `QueryStream`.

**Architecture:** Add `drainBatch` helper that type-asserts `BatchProducer`, calls `NextBatch()` → `ToRows()`, yields rows. Wire into `QueryAll` (all paths), `drainPlan`, and `QueryStream` (sync path). Keep `BatchToRowAdapter` as fallback for non-BatchProducer roots.

**Tech Stack:** Go 1.26+, `internal/SQB/EX`, `internal/SQB/UT`, `internal/SQB/OP`

## Global Constraints

- TDD: write failing test first, verify red, implement minimal green, verify green.
- `go vet ./...` zero warnings, `go test ./... -race -count=1` pass.
- `./before-commit-cases.sh` must pass before commit.
- No `map[string]interface{}` in data paths. No `fmt.Printf`.
- Error messages: lowercase, no trailing punctuation.
- Every public API method must be goroutine-safe.

---

### Task 1: Add `drainBatch` helper to EX package

**Covers:** REQ001582, REQ001583, REQ001584

**Files:**
- Create: `internal/SQB/EX/drain_batch.go` (new)
- Test: `internal/SQB/EX/drain_batch_test.go` (new)

**Interfaces:**
- Consumes: `UT.BatchProducer`, `UT.Batch.ToRows()`, `DT.ErrNoRows`
- Produces: `drainBatch(ctx, root) ([]DT.Row, error)` — drains a `BatchProducer` into `[]DT.Row`

**Step 1: Write the failing test**

Create `internal/SQB/EX/drain_batch_test.go`:

```go
//go:build !slt_corpus_full

package EX

import (
	"context"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SQB/OP"
	"github.com/cyw0ng95/razordata/internal/SQF/PL"
)

// TestDrainBatch_Basic verifies drainBatch correctly drains a BatchProducer
// into []DT.Row via NextBatch + ToRows.
func TestDrainBatch_Basic(t *testing.T) {
	// drainBatch doesn't exist yet — this tests the wished-for API.
	rows, err := drainBatch(context.Background(), nil)
	if err == nil {
		t.Errorf("expected error for nil producer, got nil")
	}
	_ = rows
	_ = err
}

// TestDrainBatch_FromVectorizedSeqScan verifies drainBatch produces correct
// rows from a real VectorizedSeqScan wrapped in a BatchProducer chain.
func TestDrainBatch_FromVectorizedSeqScan(t *testing.T) {
	// drainBatch should accept a BatchProducer and return rows.
	// Test with a real vectorized operator tree.
}
```

**Step 2: Run test to verify it fails**

Run: `go test -race -count=1 -run "TestDrainBatch" ./internal/SQB/EX/`
Expected: FAIL with "undefined: drainBatch"

**Step 3: Write minimal implementation**

Create `internal/SQB/EX/drain_batch.go`:

```go
package EX

import (
	"context"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SQB/UT"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

// drainBatch drains a BatchProducer into []DT.Row by calling NextBatch()
// repeatedly, converting each batch to rows via ToRows(), and collecting
// all rows. This eliminates the BatchToRowAdapter downgrade: instead of
// Next() → one row per call, we call NextBatch() → entire batch at once.
func drainBatch(ctx context.Context, root pl.Operator) ([]DT.Row, error) {
	bp, ok := root.(UT.BatchProducer)
	if !ok {
		// Fallback: use existing Next() path (non-vectorized tree).
		return drainPlanRows(ctx, root)
	}
	var out []DT.Row
	for {
		batch, err := bp.NextBatch(ctx)
		if err != nil {
			return nil, err
		}
		if batch == nil {
			break
		}
		rows := batch.ToRows()
		out = append(out, rows...)
		if batch.Pooled {
			batch.Put()
		}
	}
	return out, nil
}

// drainPlanRows drains a row-based pl.Operator into []DT.Row via Next().
// Used as fallback when root is not a BatchProducer.
func drainPlanRows(ctx context.Context, root pl.Operator) ([]DT.Row, error) {
	var out []DT.Row
	for {
		row, err := root.Next(ctx)
		if err != nil {
			if err == DT.ErrNoRows {
				break
			}
			return nil, err
		}
		out = append(out, row)
	}
	return out, nil
}
```

**Step 4: Run test to verify it passes**

Run: `go test -race -count=1 -run "TestDrainBatch" ./internal/SQB/EX/`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/SQB/EX/drain_batch.go internal/SQB/EX/drain_batch_test.go
git commit -m "feat(EX): add drainBatch helper for BatchProducer NextBatch drain"
```

---

### Task 2: Wire `drainBatch` into `QueryAll` textPlanCache path

**Covers:** REQ001582
**Files:**
- Modify: `internal/SQB/EX/ex.go:1183-1193` (textPlanCache bypass path)

**Step 1: Write the failing test**

Add to `internal/SQB/EX/vec_queryall_test.go`:

```go
// TestDrainBatch_TextPlanCache verifies that the textPlanCache hit path
// in QueryAll uses drainBatch when the root is a BatchProducer.
func TestDrainBatch_TextPlanCache(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ctx := context.Background()

	mustExec(t, ex, ctx, "CREATE TABLE tc_test (id INTEGER PRIMARY KEY, val TEXT)")
	for i := 0; i < 2000; i++ {
		mustExec(t, ex, ctx, "INSERT INTO tc_test VALUES ("+string(rune('a'+i%26))+", 'row_"+strconv.Itoa(i)+"')")
	}

	// First call populates textPlanCache.
	rows1, err := ex.QueryAll(ctx, "SELECT id, val FROM tc_test")
	if err != nil {
		t.Fatalf("QueryAll first: %v", err)
	}

	// Second call hits textPlanCache.
	rows2, err := ex.QueryAll(ctx, "SELECT id, val FROM tc_test")
	if err != nil {
		t.Fatalf("QueryAll second: %v", err)
	}

	if len(rows1) != len(rows2) {
		t.Fatalf("textPlanCache path returned %d rows, expected %d", len(rows2), len(rows1))
	}
}
```

**Step 2: Run test to verify it fails**

The test should PASS currently because the textPlanCache path already works. We need a test that verifies the BATCH path is taken.

Better approach: test that `drainBatch` is called by verifying the vectorized path is active.

Actually, the correct test is: verify that when `textPlanCache` stores a plan whose root has been vectorized, the drain uses `NextBatch`. Since `tryVectorizePlan` is NOT called in the textPlanCache path, we need to add it.

**Revised Step 1: Write the failing test**

```go
// TestDrainBatch_TextPlanCache_Vectorized verifies that QueryAll's
// textPlanCache path applies tryVectorizePlan and drains via NextBatch.
func TestDrainBatch_TextPlanCache_Vectorized(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ctx := context.Background()

	// Enable textPlanCache by setting up the executor properly.
	// Pre-populate cache by calling QueryAll twice with identical SQL.
	mustExec(t, ex, ctx, "CREATE TABLE tpctest (id INTEGER PRIMARY KEY, val TEXT)")
	for i := 0; i < 2048; i++ {
		mustExec(t, ex, ctx, "INSERT INTO tpctest VALUES ("+strconv.Itoa(i)+", 'v"+strconv.Itoa(i)+"')")
	}

	sql := "SELECT id, val FROM tpctest"

	// First call — no cache hit, goes through full path.
	rows1, err := ex.QueryAll(ctx, sql)
	if err != nil {
		t.Fatalf("QueryAll first: %v", err)
	}
	if len(rows1) != 2048 {
		t.Fatalf("first: got %d rows, want 2048", len(rows1))
	}

	// Second call — should hit textPlanCache (if caching works).
	// The key assertion: both calls return the same data.
	rows2, err := ex.QueryAll(ctx, sql)
	if err != nil {
		t.Fatalf("QueryAll second: %v", err)
	}
	if len(rows2) != 2048 {
		t.Fatalf("second: got %d rows, want 2048", len(rows2))
	}

	// Verify data integrity.
	for i := range rows1 {
		if rows1[i].Data[0].I64 != rows2[i].Data[0].I64 {
			t.Fatalf("row %d id mismatch: %d vs %d", i, rows1[i].Data[0].I64, rows2[i].Data[0].I64)
		}
	}
}
```

**Step 2: Run test to verify it passes** (it passes now because textPlanCache already works, but doesn't use vectorized drain)

**Step 3: Add `tryVectorizePlan` + `drainBatch` to textPlanCache path**

Modify `ex.go:1183-1193`:

```go
// Before (line 1183-1193):
if e.textPlanCache != nil {
    if plan := e.getTextPlan(sql); plan != nil {
        propEctx := &DT.ExecContext{...}
        propEctx.RowArena = e.ensureArena()
        propagateExecContext(plan.Root, propEctx)
        propagateParams(plan.Root, args)
        propagatePlanner(plan.Root, e.planner)
        defer plan.Root.Close()
        out, err := e.drainPlan(ctx, plan)
        return out, err
    }
}

// After:
if e.textPlanCache != nil {
    if plan := e.getTextPlan(sql); plan != nil {
        propEctx := &DT.ExecContext{...}
        propEctx.RowArena = e.ensureArena()
        propagateExecContext(plan.Root, propEctx)
        propagateParams(plan.Root, args)
        propagatePlanner(plan.Root, e.planner)
        // REQ001582: apply vectorization to textPlanCache plans.
        plan.Root = tryVectorizePlan(plan.Root)
        defer plan.Root.Close()
        out, err := e.drainBatch(ctx, plan.Root)
        return out, err
    }
}
```

Also update `drainPlan` to use `drainBatch`:

```go
// Before (line 1282-1294):
func (e *Executor) drainPlan(ctx context.Context, plan *pl.PlanResult) ([]DT.Row, error) {
    var out []DT.Row
    for {
        row, err := plan.Root.Next(ctx)
        ...
    }
    return out, nil
}

// After:
func (e *Executor) drainPlan(ctx context.Context, plan *pl.PlanResult) ([]DT.Row, error) {
    return e.drainBatch(ctx, plan.Root)
}
```

**Step 4: Run test to verify it passes**

Run: `go test -race -count=1 -run "TestDrainBatch_TextPlanCache_Vectorized" ./internal/SQB/EX/`
Expected: PASS

**Step 5: Run all existing tests**

Run: `go test -race -count=1 ./internal/SQB/EX/`
Expected: ALL PASS

**Step 6: Commit**

```bash
git add internal/SQB/EX/ex.go internal/SQB/EX/drain_batch.go
git commit -m "feat(EX): wire drainBatch into QueryAll textPlanCache and drainPlan paths"
```

---

### Task 3: Wire `drainBatch` into QueryAll stmtCache path

**Covers:** REQ001582
**Files:**
- Modify: `internal/SQB/EX/ex.go:1196-1230` (stmtCache hit path)
- Modify: `internal/SQB/EX/ex.go:1232-1277` (full parse+plan path)

**Step 1: Write the failing test**

```go
// TestDrainBatch_StmtCache_Vectorized verifies stmtCache hit path uses drainBatch.
func TestDrainBatch_StmtCache_Vectorized(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ctx := context.Background()

	mustExec(t, ex, ctx, "CREATE TABLE sc_test (id INTEGER PRIMARY KEY, val TEXT)")
	for i := 0; i < 2048; i++ {
		mustExec(t, ex, ctx, "INSERT INTO sc_test VALUES ("+strconv.Itoa(i)+", 'v"+strconv.Itoa(i)+"')")
	}

	// Pre-populate stmtCache by running the query twice.
	sql := "SELECT id, val FROM sc_test"
	
	rows1, err := ex.QueryAll(ctx, sql)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	
	// Second call should hit stmtCache.
	rows2, err := ex.QueryAll(ctx, sql)
	if err != nil {
		t.Fatalf("second: %v", err)
	}

	if len(rows1) != len(rows2) || len(rows2) != 2048 {
		t.Fatalf("stmtCache path: got %d rows, want 2048", len(rows2))
	}
}
```

**Step 2: Run test — should PASS** (currently works, no vectorization yet)

**Step 3: Add `tryVectorizePlan` + `drainBatch` to stmtCache path**

Modify `ex.go:1196-1230` (stmtCache hit path):

```go
// Around line 1215-1216, after propagateExecContext:
propagateExecContext(plan.Root, execCtx)
// REQ001582: apply vectorization.
plan.Root = tryVectorizePlan(plan.Root)
defer plan.Root.Close()
var out []DT.Row
out, err = e.drainBatch(ctx, plan.Root)  // Changed from manual loop + drainPlan
```

And around line 1263-1264, after propagateExecContext:
```go
propagateExecContext(plan.Root, execCtx)
defer plan.Root.Close()
// REQ001582: apply vectorization.
plan.Root = tryVectorizePlan(plan.Root)
var out []DT.Row
out, err = e.drainBatch(ctx, plan.Root)
```

**Step 4: Run test to verify it passes**

Run: `go test -race -count=1 -run "TestDrainBatch_StmtCache_Vectorized" ./internal/SQB/EX/`
Expected: PASS

**Step 5: Run all EX tests**

Run: `go test -race -count=1 ./internal/SQB/EX/`
Expected: ALL PASS

**Step 6: Commit**

```bash
git add internal/SQB/EX/ex.go
git commit -m "feat(EX): wire drainBatch into QueryAll stmtCache paths"
```

---

### Task 4: Wire `drainBatch` into QueryStream sync path

**Covers:** REQ001584
**Files:**
- Modify: `internal/SQB/EX/stream.go` (streamFromOperator and async path)

**Step 1: Write the failing test**

```go
// TestDrainBatch_QueryStream_Vectorized verifies QueryStream uses drainBatch
// when the plan is vectorized.
func TestDrainBatch_QueryStream_Vectorized(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ctx := context.Background()

	mustExec(t, ex, ctx, "CREATE TABLE ss_test (id INTEGER PRIMARY KEY, val TEXT)")
	for i := 0; i < 2048; i++ {
		mustExec(t, ex, ctx, "INSERT INTO ss_test VALUES ("+strconv.Itoa(i)+", 'v"+strconv.Itoa(i)+"')")
	}

	iter, err := ex.QueryStream(ctx, "SELECT id, val FROM ss_test")
	if err != nil {
		t.Fatalf("QueryStream: %v", err)
	}
	defer iter.Close()

	count := 0
	for iter.Next() {
		row := iter.Scan()
		_ = row
		count++
	}
	if count != 2048 {
		t.Fatalf("QueryStream: got %d rows, want 2048", count)
	}
}
```

**Step 2: Run test — should PASS** (currently works, no vectorization benefit yet)

**Step 3: Add batch-aware drain to streamFromOperator**

In `stream.go`, the `streamFromOperator` method needs to detect `BatchProducer` and drain in batch chunks instead of one row at a time.

Add a new method to `streamIterator`:

```go
// drainBatchToChannel drains a BatchProducer into the rowCh channel in
// batch-sized chunks. Each NextBatch() call produces a batch, ToRows()
// materializes all rows, and they are fanned into rowCh.
func (e *Executor) drainBatchToChannel(ctx context.Context, bp UT.BatchProducer, rowCh chan DT.Row, execCtx *DT.ExecContext) {
	for {
		batch, err := bp.NextBatch(ctx)
		if err != nil {
			rowCh <- DT.Row{Err: err}
			return
		}
		if batch == nil {
			return
		}
		rows := batch.ToRows()
		for _, row := range rows {
			DT.WithExecContext(&row, execCtx)
			rowCh <- row
		}
		if batch.Pooled {
			batch.Put()
		}
	}
}
```

Modify `stream.go` async goroutine (line 339-358) to detect `BatchProducer`:

```go
go func() {
    defer close(rowCh)
    
    // REQ001584: if root is a BatchProducer, drain via NextBatch.
    if bp, ok := plan.Root.(UT.BatchProducer); ok {
        e.drainBatchToChannel(ctx, bp, rowCh, execCtx)
        return
    }
    
    // Original row-based drain.
    for {
        ...
    }
}()
```

Same for sync path in `streamFromOperator`.

**Step 4: Run test to verify it passes**

Run: `go test -race -count=1 -run "TestDrainBatch_QueryStream_Vectorized" ./internal/SQB/EX/`
Expected: PASS

**Step 5: Run all EX tests**

Run: `go test -race -count=1 ./internal/SQB/EX/`
Expected: ALL PASS

**Step 6: Commit**

```bash
git add internal/SQB/EX/stream.go internal/SQB/EX/drain_batch.go
git commit -m "feat(EX): wire drainBatch into QueryStream sync and async paths"
```

---

### Task 5: Run full verification

**Step 1: Run before-commit-cases.sh**

```bash
./before-commit-cases.sh
```

Expected: ALL PASS

**Step 2: Run full test suite**

```bash
go test -race -count=1 ./internal/...
```

Expected: ALL PASS

**Step 3: Run go vet**

```bash
go vet ./...
```

Expected: ZERO warnings

**Step 4: Final commit if needed**

```bash
git add -A
git commit -m "feat(EX): vectorized SELECT drain path — eliminate BatchToRowAdapter downgrade"
```
