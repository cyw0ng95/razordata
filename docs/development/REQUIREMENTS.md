## TBD

| ID | Subsystem | Requirement | Priority | Effort | Deps | Touches |
| --- | --- | --- | --- | --- | --- | --- |
| REQ001508 | SQB/UT | **Incremental ANALYZE trigger.** **Fix:** (1) Add `RowChangeTracker`. (2) Increment on INSERT/UPDATE/DELETE. (3) Check threshold periodically. (4) Add `PRAGMA auto_analyze_threshold`. (5) Add test. Estimate: ~120 LoC, 1 file new + 2 modified, 1 day. | medium | medium | REQ001507 | SQB/UT/analyze.go, SQB/EX/ex.go, SQB/EX/planner.go |
| REQ001509 | SQB/OP | **Memoize node for parameterized NestedLoopJoin.** **Fix:** (1) Add `Memoize` operator. (2) Cache on outer parameter hash. (3) Detect correlated subqueries in planner. (4) Add test. (5) Opt-in via cost threshold. Estimate: ~200 LoC, 1 file new + 1 modified + 1 test, 1-2 days. | medium-high | medium | REQ001497 | SQB/OP/memoize.go (new), SQO/CO/select_plan.go |
| REQ001513 | SQB/OP | **pruneRowCols zero-alloc fast path for all-columns-used.** `pruneRowCols` allocates `newCols`, `newTypes`, `newData`, and `newIndex` on every call where `allUsed` is false (~8MB in SLT profile). The `allUsed` early return already covers full-select cases. Most callers with column pruning (e.g. `SELECT a, b FROM wide_table`) still allocate on every row. **Fix:** (1) Hoist the column-prune decision above `nextFromStore` so the RowArena allocates only the pruned column count from the start, eliminating per-row `pruneRowCols` calls entirely. (2) Fall back to the existing per-row path for dynamic pruning (e.g. subqueries). Estimate: ~60 LoC, 1 file modified + 1 new test, 0.5 day. | low | small | none | SQB/OP/seq_scan.go |

## Deferred

| ID | Reason |
| --- | --- |
| REQ001497 | Planner to SQO/CO migration blocked by EX ↔ CO import cycle. Current PlanBuilder callback in CO.Optimizer provides equivalent functionality. Requires a shared types package to break the cycle. |