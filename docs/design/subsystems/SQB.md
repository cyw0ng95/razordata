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
| `EX` | Executor: streaming operator tree, HashJoin, window functions, ALTER TABLE, FK, CTE/recursive CTE, views, triggers, JSON/datetime, PRAGMA, integrity, EXPLAIN, compound SELECT, parallel sort, pipeline parallelism, SIMD-dispatched scalars, decimal, hash agg, coerce, ExecContext threading, adaptive query compilation (ADQC), plan cache, ANALYZE |

### EX — Executor

**Responsibility:** All execution: streaming operator tree, joins, aggregates, eval, writing operators, ANALYZE, ADQC, plan cache.

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

#### Adaptive Query Compilation (ADQC)

- **AdaptiveOp:** wrapper Operator that tracks invocation count. After a threshold, triggers compilation to a specialized codegen path.
- **AdqcCache:** fixed-size LRU cache for compiled plans (256 entries).
- **FallbackOp:** on compilation panic or failure, falls back to the interpreted path.
- Telemetry logged for compile duration, saved cycles, fallback reasons.

#### ANALYZE

- **Analyze** operator: scans a table, collects column statistics (NDV, null count, min/max) using reservoir sampling, builds histograms, persists to the catalog.
- Pipeline: `EXEC ANALYZE table` → `buildWriterOp` → `Analyze.Next()` reads all rows from storage.

#### PRAGMA Support

Configuration modifies engine behavior at runtime — lives in `SQB/EX/pragma_config.go`.

```go
type PragmaConfig struct {
    journalMode string
    syncMode    string
    cacheSize   int
    foreignKeys bool
    userVersion int
    listeners   []PragmaListener
}
```

Implemented: `integrity_check`, `cache_size`, `journal_mode`, `synchronous`, `user_version`.
Critical missing: `table_info`, `foreign_keys`, `index_list`, `index_info`.

#### Eval

All scalar functions (aggregate, string, math, conditional, system) are executed in `SQB/EX/eval.go`. NULL propagation: any NULL argument returns NULL (except `coalesce`/`ifnull`/`nullif`).

## Implementation Plan

1. **`internal/SQB/EX/ex.go`** — Executor: `Exec()`, `Query()`. Operator/Row definitions.
2. **`internal/SQB/EX/execctx.go`** — ExecContext for per-execution state threading.
3. **`internal/SQB/EX/operators.go`** — SeqScan, IndexScan, Filter, Project, Sort, Limit, Insert, Update, Delete.
4. **`internal/SQB/EX/join.go`** / `hashjoin.go` / `hashcrossjoin.go` — all join operators.
5. **`internal/SQB/EX/eval.go`** — all scalar and aggregate eval functions.
6. **`internal/SQB/EX/aggregate.go`** / `aggregate_vec.go` — aggregate execution.
7. **`internal/SQB/EX/writers.go`** — INSERT/UPDATE/DELETE with constraint enforcement.
8. **`internal/SQB/EX/analyze.go`** — ANALYZE TABLE statistics collection.
9. **`internal/SQB/EX/adqc*.go`** — adaptive query compilation.
10. **`internal/SQB/EX/pragma*.go`** — PRAGMA support.
11. **~140 more files** — window, ALTER TABLE, FK, CTE, views, triggers, JSON, datetime, decimal, compound, parallel, sort, SIMD, EXPLAIN, integrity, hash agg, coerce, plan cache, etc.

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