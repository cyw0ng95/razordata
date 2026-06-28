# SQB — SQL Backend Execution Layer

## Overview

Consumes an operator tree (produced by `SQF/PL`) and executes it against the storage engine to return rows. The executor is a direct tree traverser with no virtual machine or bytecode layer. Implements the `Operator` interface (defined in `SQB/EX/ex.go`), the `Row` data type, and all execution operators. Depends on `TXN`, `ENG`, `LOG`, and `SQF` (for AST types consumed by ANALYZE).

**Dependency direction:** `SQB → SQF` (soft split). The `Operator` interface stays in `SQB/EX`; `SQF/PL` imports it. A future iteration may move Operator/Row to a shared `SQF/PL` to break the import cycle and enable extraction of `SQB/ST` (Statistics) and `SQB/QC` (Query Cache) clusters.

## Dependencies

- Required: `TXN`, `ENG`, `LOG`, `SQF` (PS, PL)
- Consumed interfaces: `Tx`, `Store`, `Iterator`, `Logger`

## Exposed Interfaces

```go
// Operator is a node in the operator tree
type Operator interface {
    Next(ctx context.Context) (Row, error)
    Close() error
}

// Row is a single row of data
type Row struct {
    Cols  []string
    Types []int
    Data  []Value
    Outer *Row
    planner  *Planner
    colIndex map[string]int
    storeKey []byte
    execCtx  *ExecContext
    tableName string
}

// Value is the typed cell value (alias for AP.Value)
type Value = AP.Value
```

## Function Clusters

| Cluster | Responsibility |
|---|---|
| `EX` | Core types: Operator/Row/Value/ExecContext, executor factory, streaming operator tree glue |
| `OP` | Operators: SeqScan, IndexScan, Filter, Project, Sort, Limit, Insert, Update, Delete, HashJoin, NestedLoopJoin, HashCrossJoin, Compound (UNION/INTERSECT/EXCEPT) |
| `EV` | Evaluation: eval.go, eval_vec.go, all scalar functions (aggregate, string, math, conditional, system), NULL propagation |
| `AG` | Aggregation: aggregate.go, aggregate_vec.go, hashagg.go, window.go, window functions |
| `AD` | ADQC/Planning: adqc*.go, planner.go, memo.go, selectivity estimation, plan cache |
| `WT` | Write operators: writers.go, source.go, store.go, ALTER TABLE executor, FK validation, CTE/recursive CTE executor, views, triggers |
| `UT` | Utilities: coerce.go, pragma.go, integrity.go, decimal.go, datetime.go, json.go, EXPLAIN, ANALYZE, parallel sort, pipeline parallelism, SIMD-dispatched scalars |

### EX — Core Types

**Responsibility:** Core operator types, Row/Value data types, ExecContext, executor factory.

**Key behaviors:**
- `Operator` interface: `Next(ctx) (Row, error)`, `Close() error`.
- `Row` struct: `Cols`, `Types`, `Data`, `Outer`, `planner`, `colIndex`, `storeKey`, `execCtx`, `tableName`.
- `Value` alias: `type Value = AP.Value`.
- `ExecContext`: per-execution state threading (cancellation, trace, stats).
- `Executor`: `Exec()`, `Query()` — builds operator tree from plan, calls `root.Next()` to pull rows.

### OP — Operators

**Responsibility:** All execution operators: leaf, intermediate, writer, join, and compound.

**Key behaviors:**

#### Operator Tree

```go
type Operator interface {
    Next(ctx context.Context) (Row, error)
    Close() error
}

// Leaf operators
type SeqScan   struct{ table string; filter Expr; schema *TableSchema; iter Iterator }
type IndexScan struct{ table string; idx string; rangeStart, rangeEnd []byte; schema *TableSchema }

// Intermediate operators
type Filter    struct{ child Operator; predicate Expr }
type Project   struct{ child Operator; cols []string }
type Sort      struct{ child Operator; keys []Expr; asc []bool }
type Limit     struct{ child Operator; n int64 }

// Leaf writer operators
type Insert struct{ table string; values []Row }
type Update struct{ table string; set []Pair; where Expr; iter Operator }
type Delete struct{ table string; where Expr; iter Operator }

// Join operators
type NestedLoopJoin struct{ left, right Operator; cond Expr }
type HashJoin       struct{ left, right Operator; keys []string }
type HashCrossJoin  struct{ left, right Operator; leftTbl, rightTbl, leftKey, rightKey string }
```

- Leaf nodes materialize rows by reading from `ENG` via the schema.
- Intermediate nodes are pull-based streaming. Parent calls `child.Next()`, processes the row, yields to its parent.
- No fully materialized intermediate sets unless `Sort` requires it.
- All operators accept `context.Context` for cancellation support.

#### Join Operators

- **HashJoin:** radix-partitioned hash join for INNER equi-joins. Single key column.
- **HashCrossJoin:** hash-probe equi-join for cross-join materialization. Full O(N+M) hash fallback when either side exceeds materialization limit.
- **NestedLoopJoin:** outer join, non-equi join, cross join with block-mode batching (batch size 32, right-side cache for ≤256 rows).

#### Compound SELECT

- `UNION`, `INTERSECT`, `EXCEPT` — set operations on result sets.
- `compound.go` handles the merge/dedup logic.

### EV — Evaluation

**Responsibility:** All scalar function evaluation, vectorized eval, NULL propagation.

**Key behaviors:**

All scalar functions (aggregate, string, math, conditional, system) are executed in `SQB/EV/eval.go`. NULL propagation: any NULL argument returns NULL (except `coalesce`/`ifnull`/`nullif`).

- `evalBinaryValue` — all binary ops (EQ, NE, LT, LE, GT, GE, PLUS, MINUS, STAR, SLASH, MOD, BITAND, BITOR, BITXOR, LSHIFT, RSHIFT, CONCAT, LIKE, GLOB, DIV, IS).
- `evalUnaryValue` — all unary ops (NOT, NEG, etc.).
- `evalBetween`, `evalCast`, `evalCase`, `evalInValue`, `evalInHashValue`, `evalBinaryShortCircuit`, `evalAggregate`, `evalFunction`, `evalWindowFunc`, `evalRaise`.
- 33 scalar functions via `valueFromAnyWrap` bridges.
- `eval_vec.go` — vectorized batch evaluation for SIMD-dispatched paths.

### AG — Aggregation

**Responsibility:** Aggregate execution, hash aggregation, window functions.

**Key behaviors:**

- `aggregate.go` — aggregate execution: `computeAggregate`, `evalAggregateOver`.
- `aggregate_vec.go` — vectorized aggregate execution.
- `hashagg.go` — hash-based aggregation for GROUP BY queries (1000-row threshold).
- `hashagg_parallel.go` — parallel hash aggregation.
- `window.go` — window functions: `ROW_NUMBER`, `RANK`, `LAG`, `LEAD`, partitioning, frame specification.
- Aggregate registry: `aggregate_registry.go` maps function names to aggregate implementations.

### AD — ADQC/Planning

**Responsibility:** Adaptive query compilation, planner, memo, selectivity estimation, plan cache.

**Key behaviors:**

#### Adaptive Query Compilation (ADQC)

- `AdaptiveOp`: wrapper Operator that tracks invocation count. After a threshold, triggers compilation to a specialized codegen path.
- `AdqcCache`: fixed-size LRU cache for compiled plans (256 entries).
- `FallbackOp`: on compilation panic or failure, falls back to the interpreted path.
- Telemetry logged for compile duration, saved cycles, fallback reasons.

#### Planner

- `planner.go` — query planning from AST to operator tree.
- `memo.go` — plan memoization: SHA256-based plan fingerprinting.
- `selectivity.go` — NDV-based selectivity estimation for equi-joins (`1/max(ndv_left, ndv_right)`), range predicates use `(1 - null_frac) / 3`. Falls back to hardcoded constants (0.1/0.3/0.5) when stats unavailable.
- `shape_specialize.go` — shape-based plan specialization.

#### Plan Cache

- `cache_stats.go` — plan cache statistics and management.

### WT — Write Operators

**Responsibility:** Write operators, source operators, store operations, DDL executors, FK, CTE, views, triggers.

**Key behaviors:**

- `writers.go` — `INSERT`, `UPDATE`, `DELETE` with `RETURNING` clause, UPSERT (`ON CONFLICT`).
- `source.go` — `VALUES` rows, `buildInsertRow`, `applyUpdate`, `evalTriggerWhen`.
- `store.go` — store operations for write operators.
- `alter_table.go` — `ALTER TABLE` executor: `ADD COLUMN`, `DROP COLUMN`, `RENAME TO`, `RENAME COLUMN`.
- `fk.go` — foreign key validation during writes.
- `subq.go` — CTE/recursive CTE executor.
- `view.go` — view materialization.
- `trigger_test.go` — trigger execution.
- `constraints.go` — constraint enforcement during writes.

### UT — Utilities

**Responsibility:** Coercion, PRAGMA, integrity checks, decimal, datetime, JSON, EXPLAIN, ANALYZE, parallel sort, pipeline, SIMD.

**Key behaviors:**

#### Coercion

- `coerce.go` — type coercion: Go `int` to `BIGINT`, Go `string` to `INT` (error), etc.

#### PRAGMA Support

- `pragma.go` — configuration modifies engine behavior at runtime.
- Implemented: `integrity_check`, `cache_size`, `journal_mode`, `synchronous`, `user_version`.
- Critical missing: `table_info`, `foreign_keys`, `index_list`, `index_info`.

#### Integrity

- `integrity.go` — `IntegrityTable` verifies catalog consistency, row counts, and data corruption.

#### Decimal

- `decimal.go` — decimal support for precise numeric calculations.

#### Datetime

- `datetime.go` — datetime functions.

#### JSON

- `json.go` — JSON functions.

#### EXPLAIN

- `explain.go` — query plan output.

#### ANALYZE

- `analyze.go` — `Analyze` operator: scans a table, collects column statistics (NDV, null count, min/max) using reservoir sampling, builds histograms, persists to the catalog.
- Pipeline: `EXEC ANALYZE table` → `buildWriterOp` → `Analyze.Next()` reads all rows from storage.

#### Parallel Sort

- `sort_parallel.go` — parallel sort with top-k and external merge.

#### Pipeline Parallelism

- `pipeline.go` — fan-out/fan-in pipeline parallelism for query execution.

#### SIMD Dispatch

- `simd_dispatch.go` — SIMD-dispatched scalar functions.

#### Virtual Tables

- `virtual.go` — virtual table support.

#### Batch

- `batch.go` — batch processing utilities.

## Implementation Plan

1. **`internal/SQB/EX/ex.go`** — Core types: `Operator`, `Row`, `Value`, `ExecContext`. Executor: `Exec()`, `Query()`.
2. **`internal/SQB/EX/execctx.go`** — ExecContext for per-execution state threading.
3. **`internal/SQB/OP/operators.go`** — SeqScan, IndexScan, Filter, Project, Sort, Limit.
4. **`internal/SQB/OP/join.go`** / `hashjoin.go` / `hashcrossjoin.go` — all join operators.
5. **`internal/SQB/OP/compound.go`** — UNION, INTERSECT, EXCEPT.
6. **`internal/SQB/EV/eval.go`** — all scalar eval functions.
7. **`internal/SQB/EV/eval_vec.go`** — vectorized batch evaluation.
8. **`internal/SQB/AG/aggregate.go`** / `aggregate_vec.go` — aggregate execution.
9. **`internal/SQB/AG/hashagg.go`** — hash-based aggregation.
10. **`internal/SQB/AG/window.go`** — window functions.
11. **`internal/SQB/AD/adqc*.go`** — adaptive query compilation.
12. **`internal/SQB/AD/planner.go`** — query planning from AST to operator tree.
13. **`internal/SQB/AD/memo.go`** — plan memoization.
14. **`internal/SQB/AD/selectivity.go`** — NDV-based selectivity estimation.
15. **`internal/SQB/WT/writers.go`** — INSERT/UPDATE/DELETE with constraint enforcement.
16. **`internal/SQB/WT/source.go`** — VALUES rows, trigger evaluation.
17. **`internal/SQB/WT/store.go`** — store operations for write operators.
18. **`internal/SQB/WT/alter_table.go`** — ALTER TABLE executor.
19. **`internal/SQB/WT/fk.go`** — foreign key validation.
20. **`internal/SQB/WT/subq.go`** — CTE/recursive CTE executor.
21. **`internal/SQB/WT/view.go`** — view materialization.
22. **`internal/SQB/WT/constraints.go`** — constraint enforcement.
23. **`internal/SQB/UT/coerce.go`** — type coercion.
24. **`internal/SQB/UT/pragma.go`** — PRAGMA support.
25. **`internal/SQB/UT/integrity.go`** — integrity checks.
26. **`internal/SQB/UT/decimal.go`** — decimal support.
27. **`internal/SQB/UT/datetime.go`** — datetime functions.
28. **`internal/SQB/UT/json.go`** — JSON functions.
29. **`internal/SQB/UT/explain.go`** — query plan output.
30. **`internal/SQB/UT/analyze.go`** — ANALYZE TABLE statistics collection.
31. **`internal/SQB/UT/sort_parallel.go`** — parallel sort with top-k and external merge.
32. **`internal/SQB/UT/pipeline.go`** — fan-out/fan-in pipeline parallelism.
33. **`internal/SQB/UT/simd_dispatch.go`** — SIMD-dispatched scalar functions.
34. **`internal/SQB/UT/virtual.go`** — virtual table support.
35. **`internal/SQB/UT/batch.go`** — batch processing utilities.

## Open Issues

- **SQB/ST (Statistics) and SQB/QC (Query Cache) clusters are deferred.**
  Blocked on import cycle: `SQB/EX` calls `SQB/QC.NewAdaptiveOp`, while
  `SQB/QC` implements `SQB/EX.Operator`. Breaking the cycle requires moving
  `Operator`/`Row` to a package both can import (e.g., `SQF/PL`). Tracked
  as REQ000959/960.
- **RANGE window frame spec:** ROWS implemented; RANGE deferred.
- **Multi-column hash join keys:** Single key column only. Composite keys
  deferred (REQ000684).
- **LEFT OUTER JOIN:** INNER via HashJoin, OUTER via NestedLoopJoin.
- **Parallel query execution:** opt-in via query hint. Auto-parallelism
  deferred.
- **In-memory tables** do not support transaction rollback (REQ000641).