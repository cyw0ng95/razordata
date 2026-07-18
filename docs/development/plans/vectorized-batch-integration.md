# Vectorized/Batch Execution Integration Plan

> **Date**: 2026-07-18
> **Goal**: Eliminate `BatchToRowAdapter` degradation for SELECT; add vectorized path for UPDATE/DELETE.
> **Starting REQ number**: REQ001582 (REQ001581 = executorStoreAdapter wiring, shipped)

## Current State

### SELECT — vectorized path exists but degrades at boundary
- `tryVectorizePlan()` in `EX/vec_transform.go` successfully transforms eligible SELECT trees into `BatchProducer` chains (VecSeqScan → VecFilter → VecProject → VecAggregate/VecHashJoin).
- **But** the root is always wrapped in `BatchToRowAdapter` (`UT/batch_adapter.go`) which drains `NextBatch()` → `ToRows()` → per-row `pl.Row`, then exposes `Next()` → `pl.Row`.
- **Root cause**: `Executor.QueryAll()`, `QueryStream()`, `QueryStreamCompiled()`, and `ExecCompiled()` all call `op.Next(ctx)` on a `pl.Operator`. The `BatchProducer` interface (`NextBatch`) has no consumer.
- **Result**: The vectorized operators process 1024 rows columnar internally, but the adapter materializes them row-by-row, losing the columnar advantage at the boundary.

### UPDATE/DELETE — chunk-level batch at storage layer only
- REQ001555/1556 added `updateChunkSize`/`deleteChunkSize` (256) to `nextFromStore` paths.
- SET expression evaluation, RETURNING evaluation, validation, and trigger firing remain **per-row**.
- `isEligible()` rejects UPDATE/DELETE entirely — only SELECT operator trees are considered.
- `ExecCompiled` DML path has no vectorization step.

### What's ready
- `EV/eval_vec.go` has `EvalBatch` (predicate → selection vector) and `EvalBatchExpr` (expression → column). These can evaluate SET expressions and RETURNING expressions over batches.
- `UT.RowOperatorAdapter` wraps `pl.Operator` → `BatchProducer` (row → batch). Already exists as a compatibility shim.
- `TableHandle.UpdateRowBatch` / `DeleteRowBatch` exist and are wired through `executorStoreAdapter` (REQ001581).
- All vectorized operators (VecSeqScan, VecFilter, VecProject, VecAggregate, VecHashJoin, VecNestedLoopJoin, VecCompoundOp, VecSort, VecLimit, VecDistinct, WindowFunc) implement `BatchProducer.NextBatch()`.

---

## REQ Plan

### Phase 1: SELECT — expose `NextBatch` at executor level (eliminate BatchToRowAdapter)

#### REQ001582: Add `NextBatch()` to `pl.Operator` interface (optional, backward-compatible)
**Subsystem**: SQF/PL + SQB/EX
**Priority**: high
**Effort**: small
**Deps**: none
**Touches**: SQF/PL/types.go, SQB/EX/ex.go

**Problem**: `pl.Operator.Next()` is the universal contract. `BatchProducer.NextBatch()` is a parallel interface. There's no unified path.

**Fix**:
1. Add `NextBatch(ctx context.Context) (*UT.Batch, error)` as an **optional** method on `pl.Operator` via type assertion pattern (same as `Resettable`).
2. Every `BatchProducer` already satisfies this — just type-assert `op.(interface{ NextBatch(context.Context) (*UT.Batch, error) })`.
3. No interface change needed — use duck typing.

**Alternative**: Skip interface change entirely. In `QueryAll`/`ExecCompiled`, type-assert `BatchProducer` directly. Cleaner, less invasive.

**Decision**: Skip `pl.Operator` interface change. Use direct type-assertion in executor paths. This avoids touching the core `pl.Operator` interface.

#### REQ001583: `Executor.QueryAll` — detect `BatchProducer` and use `NextBatch` path
**Subsystem**: SQB/EX
**Priority**: high
**Effort**: medium
**Deps**: REQ001582 (conceptually, not as a code dep)
**Touches**: SQB/EX/ex.go

**Problem**: `QueryAll` (lines 1218-1228, 1266-1276) always calls `plan.Root.Next(ctx)` in a for-loop, even when `Root` is a `BatchToRowAdapter` wrapping a `BatchProducer`.

**Fix**:
1. After `tryVectorizePlan()`, check if root is a `BatchProducer` (type-assert).
2. If yes, call `NextBatch()` in a loop, accumulating batches.
3. Convert each batch to rows via `Batch.ToRows()` (already exists, amortized per-batch).
4. Return `[]DT.Row` as before — same API, faster path.
5. If not a `BatchProducer`, fall back to existing `Next()` loop.

**Key insight**: `BatchToRowAdapter.Next()` calls `NextBatch()` → `ToRows()` → yields one row at a time. The new path calls `NextBatch()` → `ToRows()` → yields ALL rows at once. Same output, dramatically fewer function calls and allocations.

**Verification**: `BenchmarkQueryAll_VecVsRow_1KRows` should show ~10x improvement. SLT select1-4 must pass.

#### REQ001584: `Executor.QueryStream` — batch-drain for vectorized stream
**Subsystem**: SQB/EX
**Priority**: high
**Effort**: medium
**Deps**: REQ001583
**Touches**: SQB/EX/stream.go

**Problem**: `QueryStream` (line 295, 348) calls `plan.Root.Next(ctx)` per-row in both the sync and async paths. When vectorized, this defeats the batch.

**Fix**:
1. After `tryVectorizePlan()`, detect `BatchProducer`.
2. In the sync path (`streamFromOperator`), drain batches in chunks: each `NextBatch()` call fills a local buffer, then rows are yielded from the buffer.
3. In the async path (goroutine), same logic — `NextBatch()` → `ToRows()` → fan out rows through `rowCh`.
4. Schema discovery (first row) works the same — `ToRows()[0]` gives column names.

#### REQ001585: `Executor.ExecCompiled` — vectorized SELECT path
**Subsystem**: SQB/EX
**Priority**: high
**Effort**: small
**Deps**: REQ001583
**touches**: SQB/EX/ex.go

**Problem**: `ExecCompiled` SELECT path (line 1443) calls `plan.Root.Next(ctx)` once and discards the result (just checks for errors). This is for non-SELECT statements that still need plan execution (side effects). For pure SELECTs with no RETURNING, this is a waste.

**Fix**:
1. After `tryVectorizePlan()` (if called in CompilePlan), detect `BatchProducer`.
2. Drain via `NextBatch()` loop instead of single `Next()` call.
3. Same semantics — just faster.

**Note**: Need to check if `CompilePlan` calls `tryVectorizePlan`. Looking at line 1375, it calls `ResolvePlanSlots(plan.Root)` but NOT `tryVectorizePlan`. Vectorization happens at `ExecCompiled` time for the SELECT path? No — `ExecCompiled` SELECT path (line 1436-1448) does NOT call `tryVectorizePlan`. It relies on the plan being pre-vectorized at `CompilePlan` time.

**Correction**: `CompilePlan` (line 1359-1386) does NOT call `tryVectorizePlan`. Only `QueryAll`, `QueryStream`, `QueryStreamCompiled` do. So `ExecCompiled` for SELECT uses a pre-planned tree that was NOT vectorized.

**Fix for ExecCompiled**: Add `tryVectorizePlan` call in `CompilePlan` for SELECT statements, before storing the plan. Then `ExecCompiled` just drains via the appropriate path.

#### REQ001586: `CompilePlan` — apply `tryVectorizePlan` to cached plans
**Subsystem**: SQB/EX
**Priority**: high
**Effort**: small
**Deps**: REQ001585
**Touches**: SQB/EX/ex.go

**Fix**:
1. In `CompilePlan` (line 1368), after `planWithCache`, call `plan.Root = tryVectorizePlan(plan.Root)`.
2. This ensures the memoized plan already has the vectorized tree.
3. `ExecCompiled` then drains via `NextBatch` for batch producers.

#### REQ001587: SLT runner — add `batchToSltValues` conversion
**Subsystem**: tests/sqlcmp/slt
**Priority**: high
**Effort**: small
**Deps**: REQ001583
**Touches**: tests/sqlcmp/slt/razor_driver.go

**Problem**: `QueryRaw` currently calls `exe.QueryAll()` which returns `[]DT.Row`. If we change `QueryAll` to use `NextBatch` internally, the output is still `[]DT.Row` — no change needed at the driver level.

**Actually**: REQ001583 keeps the `QueryAll` API the same (`[]DT.Row`). The vectorization is an internal optimization. No SLT runner changes needed for Phase 1.

**However**: For `QueryStream` path, the SLT runner uses `database/sql` which goes through `Rows.Next() → driver.Value()`. This path is unaffected by vectorization since `QueryStream` is not used by the SLT runner directly.

**Conclusion**: No SLT runner changes needed for Phase 1. Phase 2 (DML vectorization) may need changes if RETURNING output goes through a batch path.

---

### Phase 2: UPDATE/DELETE — vectorized SET and RETURNING evaluation

#### REQ001588: Vectorized SET expression evaluation in `Update.nextFromStore`
**Subsystem**: SQB/WT + SQB/EV
**Priority**: high
**Effort**: large
**Deps**: REQ001583 (BatchProducer drain in executor), REQ001555 (chunk-level batching exists)
**Touches**: SQB/WT/writers_dml.go, SQB/EV/eval_vec.go

**Problem**: `Update.nextFromStore` (line 971-1019) calls `ApplyUpdate(&row, u.set, u.params)` per row. `ApplyUpdate` iterates SET expressions and calls `EV.EvalExpr` per expression per row.

**Fix**:
1. Add a `vectorizedSet` field to `Update` struct: `[]PS.Pair` → pre-compiled batch expressions.
2. In the chunk loop, before flushing, collect all old rows and new rows in the chunk.
3. For each SET pair `(col, expr)`, call `EV.EvalBatchExpr(expr, batch, params)` once per chunk, producing a `UT.Column`.
4. Write the column values into each row's Data slice at the correct index.
5. This replaces `O(chunkSize * numSetExprs)` individual `EvalExpr` calls with `O(numSetExprs)` batch evaluations.

**Complexity**:
- Need to resolve column indices from SET targets to batch column positions.
- Need to handle DEFAULT expressions (constant across batch).
- Need to handle NULL results (null bitmap from batch).
- Validation (FillDefaults, ValidateRow, ValidateCheck, CheckUnique) remains per-row (correctness).
- Only the SET expression evaluation is vectorized.

**Expected win**: 3-5x faster for UPDATE with multiple SET columns on large result sets.

#### REQ001589: Vectorized RETURNING expression evaluation in Update/Delete
**Subsystem**: SQB/WT
**Priority**: medium
**Effort**: medium
**Deps**: REQ001588
**Touches**: SQB/WT/writers_dml.go

**Problem**: `evalReturning` (line 1418-1470) allocates per RETURNING row and evaluates each expression via `EvalExpr`.

**Fix**:
1. For each RETURNING expression, call `EV.EvalBatchExpr` once per chunk.
2. Collect results into pre-allocated buffers (`rColsBuf`, `rTypesBuf`, `rDataBuf`).
3. Same approach as REQ001557 (pre-allocating buffers) but extended to batch evaluation.

#### REQ001590: Vectorized Filter in UPDATE/DELETE delete path
**Subsystem**: SQB/WT
**Priority**: medium
**Effort**: small
**Deps**: REQ001588
**Touches**: SQB/WT/writers_dml.go

**Problem**: The `iter` in Update/Delete is typically `OP.Filter(OP.SeqScan(...))`. The filter is evaluated per-row via `EvalExpr`.

**Fix**:
1. When the iterator is a Filter+SeqScan, use `VectorizedFilter` instead (already exists).
2. The `BatchToRowAdapter` wrapping already converts it, but the executor drains via `Next()` row-by-row.
3. Once REQ001583-1587 are done (executor drains via `NextBatch`), the filter automatically benefits.

**This is a free win from Phase 1.**

---

### Phase 3: END-TO-END — wire everything together

#### REQ001591: End-to-end benchmark — vectorized SELECT through SLT
**Subsystem**: SQB/EX + tests/sqlcmp
**Priority**: medium
**Effort**: small
**Deps**: REQ001583, REQ001584, REQ001586
**Touches**: SQB/EX/vec_queryall_bench_test.go (new), tests/sqlcmp/

**Add**:
1. `BenchmarkQueryAll_Vectorized_10K` — 10K rows, SELECT with filter + project + aggregate.
2. Compare before/after Phase 1: `BatchToRowAdapter.Next()` loop vs `NextBatch()` loop.
3. Expected: 5-10x improvement on simple projections, 2-3x on complex queries (join + aggregate).

#### REQ001592: End-to-end benchmark — vectorized UPDATE/DELETE through SLT
**Subsystem**: SQB/WT + tests/sqlcmp
**Priority**: medium
**Effort**: small
**Deps**: REQ001588, REQ001589
**Touches**: SQB/WT/writers_dml_test.go (new benchmarks)

**Add**:
1. `BenchmarkUpdate_VectorizedSET_10K` — 10K rows, UPDATE with 3 SET columns.
2. `BenchmarkDelete_VectorizedFilter_10K` — 10K rows, DELETE with WHERE filter.
3. Expected: UPDATE 3-5x, DELETE 2-3x improvement.

---

### Phase 4: Advanced — MicroOp fusion (optional, future)

#### REQ001593: MicroOp fusion — SeqScan+Filter+Project → single vectorized operator
**Subsystem**: SQB/EX + SQB/OP
**Priority**: low
**Effort**: large
**Deps**: Phase 1-3 stable
**Touches**: EX/vec_transform.go, OP/operators_vec.go

**Concept**: Fuse the common SeqScan→Filter→Project pattern into a single `VectorizedMicroOp` that:
1. Reads from store in batches
2. Evaluates filter predicate
3. Projects columns
4. All in one pass, no intermediate batch structures

**This is the DuckDB `ExecutorOperator` model.** Not needed for Phase 1-2 wins.

---

## Implementation Order (recommended)

| Iteration | REQs | Description | Expected Win |
|-----------|------|-------------|--------------|
| **iter-VEC-EXEC-1** | 1583, 1586 | QueryAll/ExecCompiled NextBatch drain + CompilePlan vectorization | 5-10x SELECT on vectorizable queries |
| **iter-VEC-EXEC-2** | 1584 | QueryStream NextBatch drain | Same for streaming path |
| **iter-VEC-UPDATE-1** | 1588 | Vectorized SET evaluation in Update.nextFromStore | 3-5x UPDATE |
| **iter-VEC-UPDATE-2** | 1589 | Vectorized RETURNING evaluation | Additional UPDATE/DELETE win |
| **iter-VEC-BENCH** | 1591, 1592 | Benchmarks + SLT verification | Evidence of improvement |
| **iter-VEC-MICRO** | 1593 | MicroOp fusion (future) | Further optimization |

---

## Risk Assessment

1. **Backward compatibility**: Phase 1 changes are internal to `QueryAll`/`QueryStream`. Same API, same `[]DT.Row` output. No breaking changes.
2. **BatchToRowAdapter still works**: The adapter is kept as a fallback for non-vectorized paths. No deletion needed.
3. **SET expression coverage**: `EvalBatchExpr` supports most expression types but may not cover all SET expressions (e.g., correlated subqueries). Fallback to per-row `ApplyUpdate` for unsupported expressions.
4. **Validation remains per-row**: FillDefaults, ValidateRow, ValidateCheck, CheckUnique, FK validation — all remain per-row. This is correct (each row must be validated independently) and not on the critical path for the biggest wins.

---

## Out of Scope (tracked separately)

- **Vectorized IndexScan in UPDATE/DELETE**: Currently UPDATE/DELETE use SeqScan iterator. Index-based updates would need vectorized IndexScan support.
- **Vectorized trigger firing**: Triggers fire per-row (correctness). Could batch trigger event collection (already done in REQ001578) but execution remains per-trigger.
- **Parallel vectorized execution**: Multiple batch producers running in parallel. Requires significant infrastructure changes.
- **SIMD intrinsics**: `UT/simd_dispatch.go` has a stub but no real SIMD. This is orthogonal to the batch path.
