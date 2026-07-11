# SQB — SQL Backend Execution Layer

## Overview

Consumes an operator tree (produced by `SQF/PL`) and executes it against the storage engine to return rows. The executor is a direct tree traverser with no virtual machine or bytecode layer. Implements the `Operator` interface (defined in `SQB/DT/types.go`), the `Row` data type, and all execution operators. Depends on `TXN`, `ENG`, `LOG`, and `SQF` (for AST types consumed by ANALYZE).

**Dependency direction:** SQB internal layout. The `Operator` interface and `Row` type now live in `SQB/DT`, shared by all SQB clusters. `SQF/PL` imports `SQB/DT` (not `SQB/EX`). Within SQB the cluster graph is DAG-shaped:

```
DT ← EX, EV, AG, AD, OP, UT (terminal)
        ↑
EV ← AG (aggregate needs EvalValue)
OP ← AD (planner constructs OP operators), AG (planner constructs AG operators), EX (executor owns planner and operators)
EX ← AD (planner lives in EX; AD owns plan_node/explain; SQO/CO injection seam wired but production still uses EX.Planner)

No cycles; clusters are extracted one at a time.

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
| `DT` | **Shared data types.** Row/Value/Operator/ExecContext/Store/StatsCatalog types + aliases; schema registry (Tables/Schemas/StoreSchemas/InMemSchemas/TableIDs/TablePKs/RegisteredIndexes); view/matview/catalog registries (ViewRegistry/MatViewRegistry/CurrentCatalog); session counters (SessionCounterAccessor interface, CurrentSessionID atomic); AST traversal helpers (ContainsAggregate, ContainsWindowFunc); value conversion utilities (ValueFromAny, ValueToString, ValueFromAnySlice, ValueSliceToAny, Compare, ToInt64, EqualValueAny, IsValueTruthy). Imported by every other SQB cluster; does not import EX/EV/AG. |
| `EX` | Executor factory: `Executor.Exec`/`Query`/`QueryStream`, statement cache, session lifecycle, subq.go (injectOuter + runSubqueryPlan helpers used by `(*Planner).ExecuteSubquery`), `dispatch.go`, `stream.go`, `executor_factory.go`. Re-exports `SessionCounterAccessor`, `SetSessionCounterAccessor`, `SetCatalog`, `RegisterFromCatalog`, `RestoreInMemoryTables`, and the `ErrEval`/`ErrDivByZero`/`ErrTypeMismatch`/`ErrSubquery`/`ErrTriggerAbort` error sentinels from EV for SYS callers. Houses the **active planner** (`planner.go`, 917 lines, plus `cost.go` 822 lines, `join_order.go` 813 lines, `predicate.go`, `view_subquery.go`, `index_plan.go`) — production path until SQO migration completes. |
| `OP` | Operators. Fully extracted library: `seq_scan.go`, `index_scan.go`, `index_only_scan.go`, `bitmap_scan.go`, `filter.go`, `project.go`, `sort_parallel.go`, `topn_sort.go`, `nljoin.go`, `hashjoin.go`, `mergejoin.go`, `parallel_hashjoin.go`, `hashcrossjoin.go`, `distinct.go`, `pragma.go` (PragmaResult), `values.go`, `virtual.go` (SqliteMaster, SqliteTempMaster, SqliteSequence), `rowpool.go`. Vectorized variants: `VectorizedSeqScan`, `VectorizedFilter`, `VectorizedProject`, `VectorizedDistinct`, `VectorizedCompoundOp`, `VectorizedHashJoin`, `VectorizedNestedLoopJoin`. Parallel scans (ParallelSeqScan, ParallelIndexScan, ParallelIndexRangeScan, ParallelHashJoin, ParallelUnionAll) ship here. |
| `EV` | Expression evaluation. `eval.go` (`EvalValue` entry point, all eval functions: evalBinaryValue, evalUnaryValue, evalBetween, evalCast, evalCase, evalInValue, evalInHashValue, evalBinaryShortCircuit, evalAggregate, evalFunction, evalWindowFunc, evalRaise, 33 scalar functions via bridges), `eval_vec.go` (`EvalBatch` vectorized batch predicate, hoisted operators `CompareFloat64ColLit`/`CompareInt64ColLit`/`CompareInt64Cols`/`CompareStringColLit`, `SwapOp`, `InvertSelection`, `BatchValueAt`), `function_registry.go` (`ScalarFuncRegistry` map of native scalar functions: LENGTH, UPPER, LOWER, IFNULL, COALESCE, NULLIF, NOW). Public exports `EvalValue`/`EvalForTest`/`EvalBatch`/`ClearSubqueryCaches`/`ScalarFuncRegistry`; error sentinels `ErrEval`, `ErrDivByZero`, `ErrTypeMismatch`, `ErrSubquery`, `ErrIgnoreRow`, `ErrTriggerAbort`. |
| `AG` | Aggregation. `aggregate.go` (`Aggregate` operator, `EvalAggregateOver`), `aggregate_vec.go`, `hashagg.go` (`HashAggregate` + `Child()`/`GroupCols()` accessors), `hashagg_parallel.go` (ParallelHashAggregate), `window.go` (`WindowOperator` + `Input()`/`FuncName()` accessors), `aggregate_registry.go` (`AggregateFuncRegistry` map: sumImpl, avgImpl, minImpl, maxImpl, countImpl, plus four `*Distinct` variants). AG depends on `EV.EvalValue`. |
| `AD` | ADQC and cache. `adqc.go` (`AdaptiveOp`), `adqc_cache.go` (`AdqcCache` LRU, 256 entries), `adqc_fallback.go` (`FallbackOp` safe-fallback wrapper), `adqc_telemetry.go` (telemetry), `cache_stats.go` (cache statistics), `index_usage.go` (index-skip tracking), `plan_node.go` (PlanNode tree, 721 lines — migrated from EX), `explain.go` (migrated from EX). The production planner **remains in EX** (`SQB/EX/planner.go` + `cost.go` + `join_order.go`) until the SQO migration wires an `SQO/CO` optimizer through the `Executor.RegisterOptimizer` seam. |
| `WT` | Write operators and DDL. **Shipped.** `internal/SQB/WT/` hosts 18 files: `writers_admin.go` (PRAGMA dispatch — `journal_mode`, `temp_store`, `auto_compact`, `quick_check`, `incremental_vacuum`, `wal_checkpoint`, `busy_timeout`, `foreign_keys`, `cell_size_check`, `database_list`, `table_list`, `index_list`, `foreign_key_list`, `table_info`), `writers_ddl.go` (CREATE/DROP/ALTER/TRUNCATE/REINDEX/ATTACH-DETACH), `writers_dml.go` (INSERT/UPDATE/DELETE/UPSERT/RETURNING/TRIGGER), `alter_table.go`, `constraints.go`, `view.go`, `matview.go`, `virtual_init.go`, `source.go`. WT depends on DT, EV, OP, and UT. It does NOT depend on EX. |
| `UT` | Utilities. `coerce.go`, `decimal.go`, `datetime.go`, `json.go`, `parallel.go` (`WorkerPool`), `batch.go` (`Batch` + `Column` for columnar evaluation), `pipeline.go`, `simd_dispatch.go`, `string_column.go`, `txn_debug.go`, `pragma.go` (`PragmaListener` interface — `OnPragmaChange`, `RegisterPragmaListener`, `UnregisterAllPragmaListeners`, `NotifyPragmaChange`), `fk.go` (FK validation incl. MATCH FULL/PARTIAL/SIMPLE), `analyze.go` (ANALYZE with reservoir sampling + histogram persistence), `integrity.go`. |

### DT — Shared Data Types

**Responsibility:** All SQB clusters depend on DT. Provides:
- `Operator` interface (alias of `pl.Operator`): `Next(ctx) (Row, error)`, `Close() error`.
- `Row` struct: `Cols`, `Types`, `Data`, `Outer`, `planner`, `colIndex`, `storeKey`, `execCtx`, `tableName`.
- `Value` alias: `type Value = pl.Value` (= `AP.Value`).
- `ExecContext`: per-execution state threading.
- `Store`, `StatsCatalog` interfaces for engine integration.
- Schema state: `Tables`, `Schemas`, `StoreSchemas`, `InMemSchemas`, `TableIDs`, `TablePKs`, `RegisteredIndexes`, `ViewRegistry`, `MatViewRegistry`, `CurrentCatalog` — all guarded by `TablesMu`/`StoreMu`. Registration helpers: `RegisterTable`, `RegisterTableSchema`, `RegisterInMemorySchema`, `RegisterStoreSchema`, `RegisterStoreSchemaWithConstraints`, `RegisterStoreSchemaFull`, `RegisterStoreSchemaWithFK`, `NextTableID`, `TableIDFor`, `AllTableNames`, `UnregisterTable`, `Schema`, `SchemaFor`, `ReplaceBySnapshot`, `CloneRow`, `RowIndex`, `RowEqual`. View/matview lifecycle: `RegisterView`, `LookupView`, `UnregisterView`, `RegisterMatView`, `LookupMatView`, `UnregisterMatView`. Index registration: `RegisterIndexWithID`, `GetRegisteredIndexes`. Catalog rehydration: `SetCatalog`, `Catalog`, `RegisterFromCatalog`, `RegisterStoreSchemaWithFKLocked`. In-memory rollback: `SnapshotInMemoryTable`, `RestoreInMemoryTables`. Row helpers: `CloneRow`, `RowIndex`, `RowEqual`, `ReplaceBySnapshot`. Value helpers: `NewIntValue`, `NewFloatValue`, `NewTextValue`, `NewBlobValue`, `NewBoolValue`, `NullValue`, `ValueFromAny`, `ValueFromAnySlice`, `ValueSliceToAny`, `ValueToString`, `EqualValueAny`, `Compare`, `ToInt64`, `IsValueTruthy`. AST helpers: `ContainsAggregate`, `ContainsWindowFunc`. Session support: `SessionCounterAccessor` interface, `SetSessionCounterAccessor`, `GetSessionCounterAccessor`, `GetCurrentSessionID`, `CurrentSessionID` (atomic). Errors: `ErrNotImplemented`, `ErrNoRows`, `ErrClosed`. Re-export type aliases for `Result`, `Rows`.

**Locking invariant:** `UnregisterAll` and registration helpers always acquire `TablesMu` before `StoreMu`. This ordering is verified by `TestLockOrder_TablesMuBeforeStoreMu` (in `internal/SQB/EX/coverage_test.go`). Any future registration helper that violates this ordering will deadlock under concurrent schema operations.

**Public surface tests rely on:** package-level type aliases `Value`, `ValueKind`, `Operator`, `Row`, `ExecContext`, `ColInfo`, `TxWriter`, `InMemoryTxWriter`, plus the constructor functions and the error sentinels.

### EX — Executor Factory

**Responsibility:** `Executor.Exec` / `Query` / `QueryStream`, statement cache, session lifecycle, integration with SYS.

**Key behaviors:**
- `Executor`: `Exec()`, `Query()`, `QueryAll()`, `QueryStream()`. Owns `*Planner` and `Store`. Threads `ExecContext` (with `Planner`, `SessionID`, `TxWriter`, `LastChanges`, `TotalChanges`, `SubqueryCache`) through plan roots via `propagateExecContext`, and the planner through row outer-chains via `propagatePlanner` (REQ000366).
- In-flight hooks: `SetSessionCounterAccessor(acc)`, `SetSessionID(id)`, `SetTxWriter(w)`, `SetForeignKeysEnabled(v)`, `GetCurrentSessionID()`, `CurrentTxWriter()`, `IsForeignKeysEnabled()`. Re-exports `SetCatalog`, `RegisterFromCatalog`, `RestoreInMemoryTables` for SYS callers.
- Re-exports `SessionCounterAccessor` interface and `ErrEval`, `ErrDivByZero`, `ErrTypeMismatch`, `ErrSubquery`, `ErrTriggerAbort` for backward compatibility with `SYS/SE` and `SYS/SY`.
- `UnregisterAll()` (test reset) clears `Tables`/`Schemas`/`StoreSchemas`/`TableIDs`/`InMemSchemas`/`TablePKs`/`RegisteredIndexes`/`ViewRegistry`/`MatViewRegistry`, all view/matview/trigger/scalar function/subquery caches. Lock order: `TablesMu` → `StoreMu` → `triggerMu` → `TablesMu.Unlock` → `tableSchemaMu` → subquery cache clear.
- `Executor.RegisterTableWithPK`, `RegisterTable`, `RegisterIndex` — convenience wrappers around `DT` registration helpers.
- `subq.go`: `injectOuter` (walks operator trees wrapping `SeqScan`/`IndexScan`/`Filter`/`Project`/`Sort`/`Limit`/`Offset`/`NestedLoopJoin`/`Aggregate` in `outerInjector` for correlated subqueries) and `runSubqueryPlan` (drives a `*pl.PlanResult` and collects rows). Used by `(*Planner).ExecuteSubquery`.
- `plan_node.go`: PlanNode tree used by EXPLAIN output. Recurses through `*Filter`/`*Project`/`*Sort`/`*Limit`/`*NestedLoopJoin`/`*SeqScan`/`*IndexScan`/`*Value`/`*Aggregate`/`*HashAggregate`/`*WindowOperator`/`*HashJoin`/`*HashCrossJoin`/`*CompoundOp`/etc. References `AG.Aggregate.Child()`, `AG.WindowOperator.Input()`/`FuncName()`, `AG.HashAggregate.Child()`/`GroupCols()` via accessor methods — replaced direct field access so cross-package callers cannot reach `unexported` fields.
- `shape_specialize.go`: pattern detection (e.g. `ShapeHashAggInt64`); uses `AG.HashAggregate.GroupCols()` accessor.
- `matview.go`: `CreateMatViewOperator`, `RefreshMatViewOperator`, `DropMatViewOperator` (moved to `WT/matview.go` for the canonical location; EX retains a thin re-export).

**EX residual after the SQB/WT extraction and SQB/OP finalization (moved out):**
- Operators (`seq_scan.go`, `index_scan.go`, `filter.go`, `project.go`, `nljoin.go`, `hashjoin.go`, `mergejoin.go`, `parallel_hashjoin.go`, `hashcrossjoin.go`, `distinct.go`, `pragma.go`, `values.go`, `virtual.go`, `sort_parallel.go`, `topn_sort.go`) → `OP/`.
- Writers (`writers_admin.go`, `writers_ddl.go`, `writers_dml.go`, `alter_table.go`, `constraints.go`, `view.go`, `virtual_init.go`) → `WT/`.
- `matview.go` → `WT/matview.go`.
- `explain.go` (EX's), `plan_node.go` (canonical PlanNode tree) → `AD/`.
- `analyze.go`, `integrity.go`, `fk.go` → `UT/`.
- `indexscan_strategy.go` → `OP/`.

**Remaining in EX (intentionally):**
- `ex.go`, `dispatch.go`, `stream.go`, `executor_factory.go` — executor glue.
- `planner.go`, `cost.go`, `join_order.go`, `predicate.go`, `view_subquery.go`, `index_plan.go`, `resolve_slots.go`, `expr_util.go`, `fold_cse.go`, `plan_subquery_explain.go`, `plan_node.go` (dispatcher subtree), `vec_transform.go`, `function_registry.go` (re-export) — active planner.
- `subq.go` (correlated-subquery injection).
- `source.go` (re-export).
- `constraint_error.go` (re-export).

### OP — Operators

**Responsibility:** All execution operators: leaf, intermediate, writer, join, and compound.

**Key behaviors:**

#### Operator Tree

```go
type Operator interface {
    Next(ctx context.Context) (Row, error)
    Close() error
}

// Leaf operators currently in OP
type Distinct       struct{ ... }
type HashJoin       struct{ left, right Operator; keys []string }
type HashCrossJoin  struct{ left, right Operator; leftTbl, rightTbl, leftKey, rightKey string }
type PragmaResult   struct{ name, value string; done bool }
type SqliteMaster   struct{ rows []Row; idx int }

// Leaf operators (now in OP)
type SeqScan   struct{ table string; filter Expr; schema *TableSchema; iter Iterator }
type IndexScan struct{ ... }

// Intermediate operators (now in OP)
type Filter    struct{ child Operator; predicate Expr }
type Project   struct{ child Operator; cols []string }
type Sort      struct{ child Operator; keys []Expr; asc []bool }
type Limit     struct{ child Operator; n int64 }
type Offset    struct{ child Operator; n int64 }

// Join operators (now in OP)
type NestedLoopJoin struct{ left, right Operator; cond Expr }

// Compound + parallel + vectorized (now in OP)
type CompoundOp struct{ ... }
type ParallelSeqScan struct{ ... }
type ParallelIndexScan struct{ ... }
type ParallelUnionAll struct{ ... }
type VectorizedSeqScan struct{ ... }
type VectorizedFilter struct{ ... }
```

- Leaf nodes materialize rows by reading from `ENG` via the schema or from in-memory tables.
- Intermediate nodes are pull-based streaming. Parent calls `child.Next()`, processes the row, yields to its parent.
- No fully materialized intermediate sets unless `Sort` requires it.
- All operators accept `context.Context` for cancellation support.

#### Join Operators

- **HashJoin:** radix-partitioned hash join for INNER equi-joins. Single key column.
- **HashCrossJoin:** hash-probe equi-join for cross-join materialization. Full O(N+M) hash fallback when either side exceeds materialization limit.
- **NestedLoopJoin:** outer join, non-equi join, cross join with block-mode batching (batch size 32, right-side cache for ≤256 rows). Note: NestedLoopJoin is currently in EX/join.go and will move to OP in SQB finalization along with `join_strategy.go`.

#### Compound SELECT

- `UNION`, `INTERSECT`, `EXCEPT` — set operations on result sets.
- `compound.go` handles the merge/dedup logic; still in EX pending SQB finalization.

### EV — Evaluation

**Responsibility:** All scalar function evaluation, vectorized batch evaluation, NULL propagation, scalar function dispatch.

**Public surface (replaces lowercase names):**
- `EvalValue(e PS.Expr, row *Row, params []any) (Value, error)` — entry point used by Filter, Project, Insert builders, planner, exec test harness. Dispatches to `evalLiteral`, `evalColumnRef`, `evalUnaryValue`, `evalBinaryValue`, `evalBinaryShortCircuit`, `evalCast`, `evalCase`, `evalBetween`, `evalInSubquery`, `evalInValue`, `evalInHashValue`, `evalAggregate`, `evalWindowFunc`, `evalFunction`, `evalRaise`.
- `EvalForTest(e, row, params) (any, error)` — convenience wrapper returning `Value.ToAny()`.
- `EvalBatch(expr, batch, params) []uint16` — vectorized predicate evaluation over a columnar batch; returns selection vector (nil = all match, empty []uint16 = none, non-empty = indices of matching rows).
- `ClearSubqueryCaches()` — resets global and correlated subquery caches for test isolation.
- Scalar function registry: `ScalarFuncRegistry` (map, exported) — formerly `scalarFuncRegistry`. 33 native scalar functions: LENGTH, UPPER, LOWER, IFNULL, COALESCE, NULLIF, NOW, CHANGES, LAST_INSERT_ROWID, TOTAL_CHANGES, SUBSTR, etc. Registered via `init()` calls in `function_registry.go`.
- Error sentinels: `ErrEval`, `ErrDivByZero`, `ErrTypeMismatch`, `ErrSubquery`, `ErrIgnoreRow`, `ErrTriggerAbort`. (EX re-exports the subset used by SYS.)
- Vectorized helpers (used by tests / future optimizations): `CompareInt64ColLit`, `CompareInt64Cols`, `CompareFloat64ColLit`, `CompareStringColLit`, `SwapOp`, `InvertSelection`, `BatchValueAt`, `MatchLike`, `GlobValue`, `ConcatValue`, `NumericArithValue`, `EvalAbs`, `EvalInHashValue`, `EvalInValue`, `EvalAggregateOver` (re-exported from AG through `AG.EvalAggregateOver`).

**Internal helpers (kept lowercase):** `evalBinaryValue`, `evalUnaryValue`, `evalBetween`, `evalCast`, `evalCase`, `evalInValue`, `evalInHashValue`, `evalBinaryShortCircuit`, `evalAggregate`, `evalFunction`, `evalWindowFunc`, `evalRaise`, `evalInHash`, `evalInHashValue`, `evalScalarSubquery`, `evalExists`, `evalInSubquery`, `evalStringIndex`, `evalLength`, `evalSubstr`, `evalUpper`, `evalLower`. Reserved internal names; package-level lowercase state is `InHashCacheMap` (map of `*PS.InExpr` → `*InHashCache`, exported to allow EX tests to assert cache hits and bypass state during reset).

**Eval helpers re-exported by DT:** `ValueFromAny`, `ValueToString`, `Compare`, `ToInt64`, `EqualValueAny`, `IsValueTruthy`. EV uses these directly; consumers outside EV should import DT, not EV, for these.

**Subquery execution model:** `EvalValue` for an `*PS.ExistsExpr`/`*PS.SubqueryExpr`/IN-subquery reads `ec.Planner` from `ExecContext` (via `OP.ExecContextFromRow`) and falls back to `outer.GetPlanner()`, then calls `planner.ExecuteSubquery(ctx, subquery, outer, params)`. The concrete `ExecuteSubquery` method is implemented on `*EX.Planner` (lives in `EX/planner.go` today, migrates to `AD/planner.go` in SQB finalization) and exercises the subquery plan via `injectOuter` + `runSubqueryPlan` (both in `EX/subq.go`). The interface is added to `pl.QueryPlanner` in `SQF/PL/types.go` so eval.go never depends on `*Planner` directly.

### AG — Aggregation

**Responsibility:** Aggregate execution, hash aggregation, parallel hash aggregation, window functions.

**Public surface:**
- `NewAggregate(child Operator, groupCols, aggs []PS.Expr) *Aggregate` — streaming GROUP BY aggregator.
- `(*Aggregate).Next(ctx) (Row, error)`, `(*Aggregate).Close() error`, `(*Aggregate).WithParams(p []any) Operator`, `(*Aggregate).SetExpandStar()`, `(*Aggregate).Child() Operator` (accessor for cross-package use), `(*Aggregate).AppendRow(row)`.
- `EvalAggregateOver(e PS.Expr, rows []Row, params []any) (any, error)` (formerly lowercase `evalAggregateOver`) — exported because cross-cluster callers (function_registry tests, hash agg reuse) need it.
- `NewHashAggregate(child Operator, groupCols, aggs []PS.Expr) *HashAggregate`.
- `(*HashAggregate).Child() Operator`, `(*HashAggregate).GroupCols() []PS.Expr` accessors (replacing direct field access from `EX/plan_node.go` and `EX/shape_specialize.go`).
- `NewWindowOperator(input Operator, funcName string, args []PS.Expr, spec *PS.WindowSpec, cols []string) *WindowOperator`.
- `(*WindowOperator).Input() Operator`, `(*WindowOperator).FuncName() string` accessors.
- `AggregateFuncRegistry` (map, exported) — registry consulted by `EvalAggregateOver`. Implementations: `sumImpl`, `avgImpl`, `minImpl`, `maxImpl`, `countImpl`, plus the four `*Distinct` variants (`sumDistinctImpl`, `avgDistinctImpl`, `minDistinctImpl`, `maxDistinctImpl`).

**Cross-package dependency:** `AG/aggregate.go` calls `EV.EvalValue` to evaluate inner expressions inside aggregate arguments. `AG/EvalAggregateOver` is invoked by `EV/eval.go` for placeholder/partial aggregates. `EX/planner.go` constructs `AG.NewAggregate` / `AG.NewHashAggregate` / `AG.NewWindowOperator` with `EV.EvalValue`-compatible children.

**Implementation plan notes:**
- `aggregate.go` — streaming `Aggregate` operator: `computeAggregate`, `evalAggregateOver`. Resolves aggregate arguments via `EV.EvalValue` when row context available (per `REQB000753` fast paths).
- `aggregate_vec.go` — vectorized aggregate execution.
- `hashagg.go` — hash-based `HashAggregate` for GROUP BY queries; threshold ~1000 rows before the planner switches to it.
- `hashagg_parallel.go` — `ParallelHashAggregate` partitions by group-key hash, builds N partial hash tables in parallel, merges.
- `window.go` — `WindowOperator`: `ROW_NUMBER`, `RANK`, `LAG`, `LEAD` with partitioning and frame specification. RANGE deferred (open issue).
- `aggregate_registry.go` — function-name → aggregate implementation map.

**Removed from this cluster during the SQB/WT extraction split:** `evalWindowFunc` (the placeholder, returns the "requires WindowOperator execution" error) was moved to `EV/eval.go` to keep `EV → AG → EV` from cycling.

### AD — ADQC/Planning

**Responsibility:** Adaptive query compilation, ADQC cache, plan cache (future), selectivity estimation (future).

**Currently in AD:**
- `adqc.go`, `adqc_cache.go`, `adqc_fallback.go`, `adqc_telemetry.go`: ADQC wrappers (`AdaptiveOp`, `AdqcCache`, `FallbackOp`), telemetry.
- `cache_stats.go`: cache statistics. Reserved home for the plan cache once the planner migrates in SQB finalization.
- `index_usage.go`: index-skip tracking (used by SeqScan to record when an available index was bypassed).

**Still in EX (pending SQB finalization):**
- `planner.go` (917 lines): `Planner`, `plan`, `joinPlan`, `tableInfo`, `joinTableInfo`, `planInsert`, `planUpdate`, `planDelete`, `planCreateTable`, `planDropTable`, `planSelect`, `planSelectScan`, `planSelectJoins`, `planSelectSubquery`, `planSelectSqliteMaster`, `planCompound`, `planAggregation`, `planOrdering`, `planLimitOffset`, `*Planner.Plan`, `*Planner.ExecuteSubquery`, `*Planner.SetPool`, `*Planner.SetStatsCatalog`, `*Planner.InvalidateCache`, `*Planner.SetJoinBufferSize`, `*Planner.SetMaxMemoryPerQuery`, `NewPlanner`, `ParallelThreshold`, `estimateCost`, `estimatePredicateSelectivity`, `estimateRowCount`, `findTableForColumn`, `extractTablesFromExpr`, `walkExprForTables`, `canPushDown`, `splitPredicatesByTable`, `equiJoinKey`, `extractEquiJoinKeys`, `extractSingleOnEquiKey`, `selectIndex`, `n3JoinOrdering`, `n3JoinOrderingMultiStart`, `exhaustiveJoinOrder`, `estimateJoinOrderCost`, `findPredicatesForPair`, `findPredicatesForSet`, `hasIndexOnTable`, `joinResultRows`, `populatePlan`, `propagatePlanner`, `propagateExecContext`, `hasAnyWindowFunc`, `isStarExpr`. Plan-node and explain-output code has migrated to `AD/plan_node.go` (721 lines) and `AD/explain.go`. The production planner stays in EX until the SQO/CO optimizer is wired via `Executor.RegisterOptimizer` (REQ001473-1481).
- `shape_specialize.go`: `DetectShape` and `isInt64GroupBy` use `AG.HashAggregate.GroupCols()` accessor; this file is the smallest of the three and could move independently once `_` references to EX types are eliminated.
- `plan_node.go`: `PlanNode` tree (in `EX`, will move to AD with the planner) — currently in EX because its `buildNode` recursively walks `*SeqScan`/`*Filter`/`*Aggregate`/etc.

#### Adaptive Query Compilation (ADQC)

- `AdaptiveOp`: wrapper Operator that tracks invocation count. After a threshold, triggers compilation to a specialized codegen path.
- `AdqcCache`: fixed-size LRU cache for compiled plans (256 entries).
- `FallbackOp`: on compilation panic or failure, falls back to the interpreted path.
- Telemetry logged for compile duration, saved cycles, fallback reasons.

#### Plan Cache

- `cache_stats.go` — plan cache statistics and management.

### WT — Write Operators

**Responsibility:** Write operators, source operators, store operations, DDL executors, FK, CTE, views, triggers.

**Status:** **Shipped.** Directory `internal/SQB/WT/` hosts 18 files:

- `writers_admin.go` (~80 funcs/types): PRAGMA dispatch — `journal_mode`, `temp_store`, `auto_compact`, `quick_check`, `incremental_vacuum`, `wal_checkpoint`, `wal_autocheckpoint`, `busy_timeout`, `busy_handler`, `batch_size`, `foreign_keys`, `foreign_key_check`, `cell_size_check`, `database_list`, `table_list`, `index_list`, `foreign_key_list`, `table_info`, plus debug_*.
- `writers_ddl.go`: `CREATE TABLE`/`DROP TABLE`/`CREATE INDEX`/`DROP INDEX`/`CREATE VIEW`/`DROP VIEW`/`CREATE MATERIALIZED VIEW`/`DROP MATERIALIZED VIEW`/`REFRESH MATERIALIZED VIEW [CONCURRENTLY]`/`CREATE TRIGGER [TEMP]`/`DROP TRIGGER`/`ALTER TABLE`/`TRUNCATE TABLE`/`REINDEX`/`ATTACH`/`DETACH`/`CREATE VIRTUAL TABLE`.
- `writers_dml.go`: `Insert`, `Update`, `Delete`, `Upsert` (all 5 conflict actions), `RETURNING`, TRIGGER fire logic.
- `alter_table.go`: `AlterTable` operator (`ADD COLUMN`, `DROP COLUMN` with cascade rules — REQ001384, `RENAME TO`, `RENAME COLUMN`, `ALTER SET/DROP DEFAULT`).
- `constraints.go`: `memLookup`, `memLookupAdapter`, `uniqueLookupWithApply`, `validateCheck`, `fillDefaults`.
- `view.go`: `CreateViewOperator`.
- `matview.go`: `CreateMatViewOperator`, `RefreshMatViewOperator`, `DropMatViewOperator`.
- `virtual_init.go`: virtual-table initialization.
- `source.go`: `buildInsertRow`, `applyUpdate`, `evalTriggerWhen`, `UnregisterAll` (test reset).

**Cross-package dependencies:** WT depends on DT (Row/Value/Schema registry), EV (`EV.EvalValue` for DEFAULT expressions), OP (constructs `OP.NewSqliteMaster` and consumes other OP operators), and UT (FK validation, PragmaListener). It does NOT depend on EX.

### UT — Utilities

**Responsibility:** Coercion, integrity checks, decimal, datetime, JSON, ANALYZE, parallel sort, pipeline, SIMD, batch, PRAGMA listener.

**Key behaviors:**

#### Coercion (`coerce.go`)
Type coercion: Go `int` to `BIGINT`, Go `string` to `INT` (error), etc.

#### PRAGMA Listener (`pragma.go`)
`PragmaListener` interface (`OnPragmaChange`), `RegisterPragmaListener`, `UnregisterAllPragmaListeners`, `NotifyPragmaChange`. Distinct from `SQB/OP/PragmaResult` (single-row result operator) and `SQB/EX/Pragma` (operator under SQB finalization).

#### Integrity (`integrity.go`)
`IntegrityTable` verifies catalog consistency, row counts, and data corruption. Note: still in EX/integrity.go pending SQB finalization.

#### Decimal (`decimal.go`)
Decimal support for precise numeric calculations.

#### Datetime (`datetime.go`)
Datetime functions.

#### JSON (`json.go`)
JSON functions.

#### ANALYZE (`analyze.go`)
`Analyze`, `Vacuum` operators: scan a table, collect column statistics (NDV, null count, min/max) using reservoir sampling, build histograms, persist to the catalog. Pipeline: `EXEC ANALYZE table` → `buildWriterOp` → `Analyze.Next()` reads all rows from storage. Still in EX/analyze.go; will move to UT in SQB finalization.

#### EXPLAIN (`explain.go`)
`ExplainStmtOp`. Still in EX; will move to UT in SQB finalization.

#### Parallel Sort (`sort_parallel.go`)
Parallel sort with top-k and external merge. Still in EX; will move to UT in SQB finalization.

#### Pipeline Parallelism (`pipeline.go`)
Fan-out/fan-in pipeline parallelism. Still in EX; will move to UT in SQB finalization.

#### SIMD Dispatch (`simd_dispatch.go`)
SIMD-dispatched scalar functions.

#### Virtual Table (`virtual.go`)
`SqliteMaster` was moved to **OP** (not UT) in the SQB/WT extraction because it is an Operator.

#### Batch (`batch.go`)
Batch + Column types for columnar evaluation; canonical home in UT. `Batch.Pool()`/`GetBatch`/`Put` use `sync.Pool` to avoid per-batch GC pressure.

## SQB cluster extraction Outcome (2026-Q2 → 2026-Q3)

The original SQB/WT extraction (2026-06-29) shrank EX from 37 files / 48,907 LOC to 27 files / ≈38,000 LOC. Subsequent work continued the split:

- WT cluster created at `internal/SQB/WT/` with 18 files (`writers_admin.go`, `writers_ddl.go`, `writers_dml.go`, `alter_table.go`, `constraints.go`, `view.go`, `matview.go`, `virtual_init.go`, `source.go`, …). WT owns all DDL/DML/trigger/pragma dispatch.
- OP cluster fully extracted: `seq_scan.go`, `index_scan.go`, `index_only_scan.go`, `bitmap_scan.go`, `filter.go`, `project.go`, `sort_parallel.go`, `topn_sort.go`, `nljoin.go`, `hashjoin.go`, `mergejoin.go`, `parallel_hashjoin.go`, `hashcrossjoin.go`, `distinct.go`, `pragma.go`, `values.go`, `virtual.go`, `rowpool.go`, plus vectorized variants.
- AD absorbed `plan_node.go` (721 lines) and `explain.go` from EX.
- UT absorbed `analyze.go`, `integrity.go`, `fk.go` from EX (plus the original `coerce.go`, `decimal.go`, `datetime.go`, `json.go`, `parallel.go`, `batch.go`, `pipeline.go`, `simd_dispatch.go`, `string_column.go`, `txn_debug.go`, `pragma.go`).
- DT grew to ≈1100 LOC (was 137 LOC) and now hosts `fk_queue.go` (DEFERRABLE queue), `index_usage.go` (mirror of `AD/index_usage.go`), `storage.go` (key encoding + row storage moved from EX/store.go), `table_handle.go`, plus the schema-registry and view/matview/catalog lifecycle.
- The planner remains in EX (`planner.go` 917 lines + `cost.go` 822 lines + `join_order.go` 813 lines + `predicate.go` + `view_subquery.go` + `index_plan.go`) — production path until SQO/CO wires in via `Executor.RegisterOptimizer`.
- 0 new test regressions across the splits; all SQB tests pass with `-race`.

### Lock-order policy

```text
UnregisterAll and registration helpers always acquire TablesMu before StoreMu.
Failure to honour this ordering during further moves will deadlock on concurrent
WAL flush + schema re-registration. Test enforces: see coverage_test.go
(TestLockOrder_TablesMuBeforeStoreMu).
```

### Public-symbol compatibility for SYS callers

`EX` continues to re-export, as `var`-aliases or type aliases:
- `SetSessionCounterAccessor`, `SetCatalog`, `RegisterFromCatalog`, `RestoreInMemoryTables`
- `ErrEval`, `ErrDivByZero`, `ErrTypeMismatch`, `ErrSubquery`, `ErrTriggerAbort`
- `SessionCounterAccessor` interface

SYS/SE imports `EX.SetSessionCounterAccessor`. This remains unchanged through the SQB finalization WT/UT moves. SYS/TX imports `EX.RestoreInMemoryTables`.

### Residual files still in `internal/SQB/EX/`

The following files remain in EX as the active production path:

- Executor factory and glue: `ex.go`, `dispatch.go`, `stream.go`, `subq.go`, `executor_factory.go`, `source.go` (re-export), `constraint_error.go` (re-export).
- Active planner: `planner.go` (917 lines), `cost.go` (822 lines), `join_order.go` (813 lines), `predicate.go`, `view_subquery.go`, `index_plan.go`, `resolve_slots.go`, `expr_util.go`, `fold_cse.go`, `vec_transform.go`, `function_registry.go` (re-export), `plan_subquery_explain.go`, `plan_node.go` (dispatcher subtree, 111 lines — the canonical PlanNode tree is in `AD/plan_node.go`, 721 lines).
- The full SQO/CO migration is gated on `Executor.RegisterOptimizer` wiring a production caller (REQ001473-1481). Until that lands, EX.Planner remains the live code path.

## Open Issues

- **RANGE window frame spec:** ROWS implemented; RANGE deferred.
- **Multi-column hash join keys:** Single key column only. Composite keys
  deferred (REQ000684).
- **LEFT OUTER JOIN:** INNER via HashJoin, OUTER via NestedLoopJoin.
- **Parallel query execution:** opt-in via query hint. Auto-parallelism
  deferred.
- **In-memory tables** do not support transaction rollback (REQ000641).