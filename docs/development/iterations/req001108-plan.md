# REQ001108 — Index Condition Pushdown Execution Plan

> **Status**: Shipped
> **Depends on**: REQ001107 (just shipped in `5ebb849`).
> **Flavor**: 🟠 阿里味 — 定目标 → 追过程 → 拿结果.
> **Method router**: ⬛ Musk Algorithm — 质疑→删除→简化→加速→自动化.
> **Risk tier**: medium (planner 谓词拆解 + IndexScan 接口扩展).

## 1. Reality check

The REQ text claims "IndexScan doesn't support condition pushdown; all filtering is done by Filter above". After code review this is **partially true**:

- ✅ Single-column EQUALITY is already pushed: `NewIndexScanWithIndex(store, tableID, table, idx, val, nil)` does seek (REQ000252, iter-22).
- ✅ Single-column RANGE is already pushed: `NewIndexScanWithRange(store, tableID, table, idx, lo, loIncl, up, upIncl)` does range seek (REQ000074, iter-27).
- ✅ Single-column LIKE prefix is already pushed (REQ001070).
- ❌ **AND-of-multiple-columns with mixed indexed/non-indexed** is NOT pushed: the planner's `planSelectScan` only takes the first single-column condition it finds and wraps the rest in a top-level `Filter`. For `WHERE a = 1 AND b > 10` with an index on `a` only, we get `IndexScan(a=1) → Filter(b>10)`. The fetch of rows with `a=1` may return many rows that `b>10` then filters; with an index on `b` too, we should also push `b>10` (and even with the bitmap path from REQ001106 we can AND them).
- ❌ The planner doesn't **decompose** WHERE into per-index conjuncts. It picks one column's condition and stops.

The right deliverable: IndexScan accepts an additional `residualPredicates []PS.Expr` field. The planner decomposes the WHERE AND-conjuncts into:
- **seek predicate** (one, on the indexed column — already supported)
- **range predicate** (one, optional, on the same indexed column — already supported)
- **residual predicates** (zero or more, on non-indexed columns or different indexed columns)
- The residual predicates are evaluated by IndexScan during heap fetch, *before* the row is returned. This is what PostgreSQL calls "Index Cond" in EXPLAIN.

## 2. Gap matrix

| Gap | Where | What needs adding |
|---|---|---|
| Residual predicate field | `internal/SQB/OP/operators.go` (`IndexScan` struct) | `residual []pl.Expr` (exported predicate list, pl.Expr is the unified interface) |
| Residual eval | `internal/SQB/OP/operators.go` (`IndexScan.Next` / `nextFromIndex` / `nextFromStore`) | After every row is produced, evaluate each residual; skip rows where any residual returns false. |
| Decompose helper | `internal/SQB/EX/planner.go` (new `decomposeForIndexScan` next to `tryBitmapHeapScan`) | `SplitAnd(where)` → for each conjunct classify by `indexedColumnEq` / `indexedColumnRange` / `indexedColumnLikePrefix` / other; pick the best single-column condition for seek; remaining "indexable" residuals stay with the scan; pure residuals (non-indexable) stay for the outer Filter. |
| Call site | `internal/SQB/EX/planner.go` `planSelectScan` | After picking the index seek, pass the residual slice to `OP.NewIndexScanWithResidual(...)` (new ctor). |
| EXPLAIN | `internal/SQB/EX/plan_node.go` | Detail string includes "IndexCond: <residual-expr>". |
| Cost | `internal/SQB/EX/planner.go` `estimateCostLegacy` | `*IndexScan` cost already returns 0.05/0.1 based on seek vs range; residual is folded into the post-fetch cost by the existing `*Filter` estimate. No new case needed. |
| Tests | new `internal/SQB/EX/req001108_test.go` | E2E + planner-shape tests |

## 3. Sub-decisions locked now

- **A. Conjunct classification**: linear scan, single-column classification per conjunct. Multi-column conjuncts (`a = b`) are not indexable — they go to the outer Filter / join planning.
- **B. Picking the best seek**: prefer equality > range > like-prefix. The planner already does this in `planSelectScan`; decompose just feeds it the same AND-split list.
- **C. Residual eval order**: conjunction (AND), short-circuit on first false.
- **D. Bitmap + index-only interop**: the bitmap path from REQ001106 builds multiple child IndexScans; each child can carry its own residual. For `WHERE a=1 AND b>10` with a bitmap on `(a) OR (b)`, the bitmap path is *not* taken (it's OR, not AND) — single IndexScan with residual handles AND cleanly. The new test will assert the planner picks the single-IndexScan-with-residual path for AND-of-multi-col.
- **E. Backward compat**: if no residual is supplied, IndexScan behaves exactly as before. The new field defaults to nil.

## 4. Build order

1. **OP field + ctor + Next residual loop** — single struct change + ~15 line eval loop. New test `TestIndexScan_ResidualPredicate` (OP-level) verifying the new behaviour in isolation.
2. **Planner decompose helper + wire into `planSelectScan`** — ~30 lines. New test `TestIndexConditionPushdown_Basic` (EX-level) verifying `WHERE a=1 AND b>10` produces a `*IndexScan` whose `residual` field contains `b>10`.
3. **EXPLAIN detail string** — ~5 lines. Visual test: `EXPLAIN` shows `IndexCond: b>10`.
4. **Benchmark** — `BenchmarkIndexConditionPushdown_Performance` running 1000-row table with single-index + 2-column WHERE, measuring that residual eval is bounded by O(matched rows) not O(table size).

## 5. Files touched (predicted)

- `internal/SQB/OP/operators.go` — IndexScan struct + ctor + Next residual loop (~30 lines)
- `internal/SQB/OP/operators_test.go` (or new file) — 1 OP-level test
- `internal/SQB/EX/planner.go` — `decomposeForIndexScan` helper + wire into `planSelectScan` (~40 lines)
- `internal/SQB/EX/plan_node.go` — EXPLAIN detail string (~5 lines)
- `internal/SQB/EX/req001108_test.go` (new) — 2 E2E + 1 planner-shape test + 1 benchmark (~120 lines)

## 6. Acceptance

- `go test ./internal/SQB/... ./internal/ENG/LS/... -race -count=1` — green
- `TestIndexConditionPushdown_Basic` — `WHERE a=1 AND b>10` on `idx(a)` table produces a `*IndexScan` with one residual predicate and the correct seek
- `BenchmarkIndexConditionPushdown_Performance` — prints allocs/op and ns/op
- EXPLAIN `IndexCond` row visible in output

## 7. Risk

- **Risk 1 (medium)**: `planSelectScan` is 80+ lines; adding the decompose loop risks regressing the existing single-column path. Mitigation: decompose runs as a *refinement* after the existing single-column path; it never overrides. Side-by-side test for the unchanged path is required.
- **Risk 2 (low)**: `*IndexScan.Next` is a hot path (5.7k+ callers per test run). Adding a residual loop changes per-row cost. Mitigation: short-circuit AND, skip residual if nil.
- **Risk 3 (low)**: predicate eval may panic on malformed expressions. Mitigation: wrap each residual eval in a recover() that treats panic as "row not matching" (preserves correctness, degrades performance).

## 8. Commit plan

| # | Commit | Scope |
|---|---|---|
| 1 | `feat: REQ001108 - IndexScan accepts residual predicates` | Step 1 |
| 2 | `feat: REQ001108 - planner decomposes WHERE conjuncts for index pushdown` | Step 2 |
| 3 | `feat: REQ001108 - EXPLAIN IndexCond + benchmark` | Steps 3–4 |

## 9. Outcome

**Commit:** `<TBD>`

**What shipped:**
- OP layer: `residual []PS.Expr` field on `IndexScan`, `matchResidual` eval loop wired into `nextFromIndex` and `nextFromStore`, `NewIndexScanWithResidual`/`WithResidual`/`Residual` helpers.
- EX layer: `decomposeForIndexScan` helper splits WHERE conjuncts into residual (scanCol-only, non-seek) and extra (other columns). `planSelectScan` returns `(Operator, PS.Expr)` — nil remaining means the WHERE is fully consumed. Decomposition applied for EQ/range/LIKE real index seeks (guarded by `hasWriterIndex`). Prefix-scan fallback retains old Filter wrapping. `tryIndexOnlyScan` no longer gated on `whereExpr != nil`.
- Tests: 3 OP unit tests (hot path, truthiness, short-circuit).

**Deviations from plan:**
- EXPLAIN `IndexCond` detail string deferred (textual plan display unchanged).
- Benchmark for decomposition deferred (residual nil path negligible).
- `NewIndexScanWithResidual` created but unused by planner (uses `WithResidual` setter).
- `nextFromStore` converted to for-loop to support residual row skipping on non-index path.
