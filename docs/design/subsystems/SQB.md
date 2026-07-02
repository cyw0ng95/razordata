# SQB — SQL Backend Execution Layer

## Overview

Consumes an operator tree (produced by `SQF/PL`) and executes it against the storage engine to return rows. The executor is a direct tree traverser with no virtual machine or bytecode layer. Implements the `Operator` interface (defined in `SQB/DT/types.go`), the `Row` data type, and all execution operators. Depends on `TXN`, `ENG`, `LOG`, and `SQF` (for AST types consumed by ANALYZE).

**Dependency direction:** SQB internal layout. The `Operator` interface and `Row` type now live in `SQB/DT`, shared by all SQB clusters. `SQF/PL` imports `SQB/DT` (not `SQB/EX`). Within SQB the cluster graph is DAG-shaped:

```
DT ← EX, EV, AG, AD, OP, UT, WT (terminal)
         ↑
EV ← AG (aggregate needs EvalValue)
OP ← DT (all operators), AD (plan_node.go cost estimation), AG (hashagg.go)
AD ← DT, OP, AG, EV (explain.go + plan_node.go + cost estimation)
EX ← AD, OP, WT, EV, AG, DT (executor factory + planner)
WT ← DT, EV, OP, AG, EX (writers depend on all execution components)
UT ← DT, EX (utilities — some import EX for type access)
```

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
| `DT` | **Shared data types.** Row/Value/Operator/ExecContext/Store/StatsCatalog types + aliases; schema registry (Tables/Schemas/StoreSchemas/InMemSchemas/TableIDs/TablePKs/RegisteredIndexes); view/matview/catalog registries (ViewRegistry/MatViewRegistry/CurrentCatalog); session counters (SessionCounterAccessor interface, CurrentSessionID atomic); AST traversal helpers (ContainsAggregate, ContainsWindowFunc); value conversion utilities (ValueFromAny, ValueToString, ValueFromAnySlice, ValueSliceToAny, Compare, ToInt64, EqualValueAny, IsValueTruthy); index usage tracking (IndexUsageStats). Files: `types.go`, `schema.go`, `storage.go`, `tx_state.go`, `index_usage.go`. Imported by every other SQB cluster; does not import EX/EV/AG. |
| `EX` | Executor factory: `Executor.Exec`/`Query`/`QueryStream`, statement cache, session lifecycle, `UnregisterAll()` (test reset). Files: `ex.go` (Executor, Exec/Query/QueryStream, UnregisterAll), `planner.go` (~6300 lines: `Planner`, all `plan*` methods, cost estimation, join ordering, N3), `plan_node.go` (`buildPlanNodeTree`, `operatorType`), `source.go` (`UnregisterAll`). Re-exports `SessionCounterAccessor`, `SetSessionCounterAccessor`, `SetCatalog`, `RegisterFromCatalog`, `RestoreInMemoryTables`, and `ErrEval`/`ErrDivByZero`/`ErrTypeMismatch`/`ErrSubquery`/`ErrTriggerAbort` for SYS callers. |
| `OP` | Operators. Leaf: `SeqScan`/`IndexScan` (`operators.go`), `BitmapScan` (`bitmap_scan.go`), `IndexOnlyScan` (`index_only_scan.go`), `SqliteMaster` (`virtual.go`). Intermediate: `Filter`/`Project`/`Sort`/`Limit`/`Offset` (`intermediate.go`), `CompoundOp` (`compound.go`), `Values`/`ValuesRows` (`values.go`). Join: `HashJoin` (`hashjoin.go`), `HashCrossJoin` (`hashcrossjoin.go`), `NestedLoopJoin` (`join.go`), `MergeJoin` (`mergejoin.go`), `ParallelHashJoin` (`parallel_hashjoin.go`). Parallel/vectorized: `ParallelSeqScan`/`ParallelIndexScan`/`ParallelUnionAll` (`operators_parallel.go`), `VectorizedSeqScan`/`VectorizedFilter` (`operators_vec.go`), `SortParallel` (`sort_parallel.go`). Other: `Distinct` (`distinct.go`), `PragmaResult` (`pragma.go`), `ExecContext` (`execctx.go`), `Store` (`store.go`), `IndexScanStrategy` (`indexscan_strategy.go`). |
| `EV` | Expression evaluation. `eval.go` (`EvalValue` entry point, all eval functions), `eval_vec.go` (`EvalBatch` vectorized batch predicate), `function_registry.go` (`ScalarFuncRegistry` map of native scalar functions). Public exports `EvalValue`/`EvalForTest`/`EvalBatch`/`ClearSubqueryCaches`/`ScalarFuncRegistry`; error sentinels `ErrEval`, `ErrDivByZero`, `ErrTypeMismatch`, `ErrSubquery`, `ErrIgnoreRow`, `ErrTriggerAbort`. |
| `AG` | Aggregation. `aggregate.go` (`Aggregate` operator, `EvalAggregateOver`), `aggregate_vec.go`, `hashagg.go` (`HashAggregate` + `Child()`/`GroupCols()` accessors), `hashagg_parallel.go` (ParallelHashAggregate), `window.go` (`WindowOperator` + `Input()`/`FuncName()` accessors), `aggregate_registry.go` (`AggregateFuncRegistry` map: sumImpl, avgImpl, minImpl, maxImpl, countImpl, plus four `*Distinct` variants). AG depends on `EV.EvalValue`. |
| `AD` | ADQC, plan cache, explain, plan node types. `adqc.go` (`AdaptiveOp`), `adqc_cache.go` (`AdqcCache` LRU, 256 entries), `adqc_fallback.go` (`FallbackOp`), `adqc_telemetry.go` (telemetry), `cache_stats.go` (cache statistics), `explain.go` (`ExplainStmtOp`), `plan_node.go` (`PlanNode` tree, `FormatPlanTree`, cost estimation functions, `Noop` operator, `ToJSON`/`ToDOT`/`ToTree` formatters). |
| `WT` | Write operators. `writers.go` (Insert/Update/Delete/Trigger/CreateTable/DropTable/CreateIndex/DropIndex/Pragma/Explain/Truncate/Reindex + `buildWriterOp` dispatcher), `source.go` (`buildInsertRow`, `applyUpdate`, `evalTriggerWhen`, `UnregisterAll`), `alter_table.go` (`AlterTable` operator), `constraints.go` (`memLookup`, `uniqueLookupWithApply`, `validateCheck`, `fillDefaults`), `view.go` (`CreateViewOperator`), `matview.go` (`CreateMatViewOperator`, `RefreshMatViewOperator`, `DropMatViewOperator`), `subq.go` (`injectOuter`, `runSubqueryPlan`). |
| `UT` | Utilities. `coerce.go` (Go type → SQL type coercion), `decimal.go`, `datetime.go`, `json.go`, `parallel.go` (`WorkerPool`), `batch.go` (`Batch` + `Column`), `simd_dispatch.go` (SIMD-dispatched scalar functions), `string_column.go`, `txn_debug.go`, `analyze.go` (`Analyze`, `Vacuum`), `integrity.go` (`IntegrityTable`), `pragma.go` (`PragmaListener` interface), `fk.go` (FK validation helpers). |

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
- `planner.go` (~6300 lines): `Planner`, all `plan*` methods, cost estimation, N3 join ordering, bushy join grouping, transitive equality inference. The `Planner` constructs operators from `OP`, `WT`, and `AG` clusters.
- `plan_node.go`: `buildPlanNodeTree` (walks the operator tree and builds a `*AD.PlanNode`), `operatorType` (maps operator types to string labels).
- `source.go`: `UnregisterAll()` (deletes all tables, views, indexes, and re-initializes the catalog for test isolation).

### OP — Operators

**Responsibility:** All execution operators: leaf, intermediate, writer, join, and compound.

**Key behaviors:**

- Leaf nodes materialize rows by reading from `ENG` via the schema or from in-memory tables.
- Intermediate nodes are pull-based streaming. Parent calls `child.Next()`, processes the row, yields to its parent.
- No fully materialized intermediate sets unless `Sort` requires it.
- All operators accept `context.Context` for cancellation support.

#### Join Operators

- **HashJoin:** radix-partitioned hash join for INNER equi-joins. Single key column.
- **HashCrossJoin:** hash-probe equi-join for cross-join materialization. Full O(N+M) hash fallback when either side exceeds materialization limit.
- **MergeJoin:** sorted merge join for sorted inputs.
- **NestedLoopJoin:** outer join, non-equi join, cross join with block-mode batching (batch size 32, right-side cache for ≤256 rows).
- **ParallelHashJoin:** parallel hash join with partitioned build phase.

#### Compound SELECT

- `UNION`, `INTERSECT`, `EXCEPT` — set operations on result sets.
- `CompoundOp` handles the merge/dedup logic.

#### Parallel and Vectorized

- `ParallelSeqScan`/`ParallelIndexScan`/`ParallelUnionAll` — parallel leaf operators.
- `VectorizedSeqScan`/`VectorizedFilter` — vectorized operators.
- `SortParallel` — parallel sort with top-k and external merge.

### EV — Evaluation

**Responsibility:** All scalar function evaluation, vectorized batch evaluation, NULL propagation, scalar function dispatch.

**Public surface (replaces lowercase names):**
- `EvalValue(e PS.Expr, row *Row, params []any) (Value, error)` — entry point used by Filter, Project, Insert builders, planner, exec test harness. Dispatches to `evalLiteral`, `evalColumnRef`, `evalUnaryValue`, `evalBinaryValue`, `evalBinaryShortCircuit`, `evalCast`, `evalCase`, `evalBetween`, `evalInValue`, `evalInHashValue`, `evalAggregate`, `evalWindowFunc`, `evalFunction`, `evalRaise`.
- `EvalForTest(e, row, params) (any, error)` — convenience wrapper returning `Value.ToAny()`.
- `EvalBatch(expr, batch, params) []uint16` — vectorized predicate evaluation over a columnar batch; returns selection vector (nil = all match, empty []uint16 = none, non-empty = indices of matching rows).
- `ClearSubqueryCaches()` — resets global and correlated subquery caches for test isolation.
- Scalar function registry: `ScalarFuncRegistry` (map, exported) — formerly `scalarFuncRegistry`. Native scalar functions: LENGTH, UPPER, LOWER, IFNULL, COALESCE, NULLIF, NOW, CHANGES, LAST_INSERT_ROWID, TOTAL_CHANGES, SUBSTR, etc. Registered via `init()` calls in `function_registry.go`.
- Error sentinels: `ErrEval`, `ErrDivByZero`, `ErrTypeMismatch`, `ErrSubquery`, `ErrIgnoreRow`, `ErrTriggerAbort`. (EX re-exports the subset used by SYS.)

**Subquery execution model:** `EvalValue` for an `*PS.ExistsExpr`/`*PS.SubqueryExpr`/IN-subquery reads `ec.Planner` from `ExecContext` (via `OP.ExecContextFromRow`) and falls back to `outer.GetPlanner()`, then calls `planner.ExecuteSubquery(ctx, subquery, outer, params)`. The concrete `ExecuteSubquery` method is implemented on `*EX.Planner` (in `EX/planner.go`) and exercises the subquery plan via `injectOuter` + `runSubqueryPlan` (both in `WT/subq.go`). The interface is added to `pl.QueryPlanner` in `SQF/PL/types.go` so eval.go never depends on `*Planner` directly.

### AG — Aggregation

**Responsibility:** Aggregate execution, hash aggregation, parallel hash aggregation, window functions.

**Files in AG:** `ag_types.go`, `aggregate.go`, `aggregate_vec.go`, `hashagg.go`, `hashagg_parallel.go`, `window.go`, `aggregate_registry.go`.

**Public surface:**
- `NewAggregate(child Operator, groupCols, aggs []PS.Expr) *Aggregate` — streaming GROUP BY aggregator.
- `(*Aggregate).Next(ctx) (Row, error)`, `(*Aggregate).Close() error`, `(*Aggregate).WithParams(p []any) Operator`, `(*Aggregate).SetExpandStar()`, `(*Aggregate).Child() Operator` (accessor for cross-package use), `(*Aggregate).AppendRow(row)`.
- `EvalAggregateOver(e PS.Expr, rows []Row, params []any) (any, error)` (formerly lowercase `evalAggregateOver`) — exported because cross-cluster callers (function_registry tests, hash agg reuse) need it.
- `NewHashAggregate(child Operator, groupCols, aggs []PS.Expr) *HashAggregate`.
- `(*HashAggregate).Child() Operator`, `(*HashAggregate).GroupCols() []PS.Expr` accessors (replacing direct field access from `EX/plan_node.go`).
- `NewWindowOperator(input Operator, funcName string, args []PS.Expr, spec *PS.WindowSpec, cols []string) *WindowOperator`.
- `(*WindowOperator).Input() Operator`, `(*WindowOperator).FuncName() string` accessors.
- `AggregateFuncRegistry` (map, exported) — registry consulted by `EvalAggregateOver`. Implementations: `sumImpl`, `avgImpl`, `minImpl`, `maxImpl`, `countImpl`, plus the four `*Distinct` variants (`sumDistinctImpl`, `avgDistinctImpl`, `minDistinctImpl`, `maxDistinctImpl`).

**Cross-package dependency:** `AG/aggregate.go` calls `EV.EvalValue` to evaluate inner expressions inside aggregate arguments. `AG/EvalAggregateOver` is invoked by `EV/eval.go` for placeholder/partial aggregates. `EX/planner.go` constructs `AG.NewAggregate` / `AG.NewHashAggregate` / `AG.NewWindowOperator` with `EV.EvalValue`-compatible children.

### AD — ADQC/Planning

**Responsibility:** Adaptive query compilation, ADQC cache, explain formatting, plan node types, cost estimation.

**Files in AD:**
- `adqc.go`, `adqc_cache.go`, `adqc_fallback.go`, `adqc_telemetry.go`: ADQC wrappers (`AdaptiveOp`, `AdqcCache`, `FallbackOp`), telemetry.
- `cache_stats.go`: cache statistics.
- `explain.go`: `ExplainStmtOp` operator — formats the plan tree into `sqlite3`-compatible `EXPLAIN` output rows.
- `plan_node.go`: `PlanNode` tree structure, `FormatPlanTree` (formats a `PlanNode` tree into `[]DT.Row`), cost estimation functions (`EstimateFilterCost`, `EstimateProjectCost`, `EstimateSortCost`, `EstimateLimitCost`, `EstimateOffsetCost`, `EstimateDistinctCost`, `EstimateAggregateCost`, `EstimateIndexCost`, `EstimateJoinCost`), `AnalyzePlanForBottlenecks` (identifies performance bottlenecks), `Noop` operator, `ToJSON`/`ToDOT`/`ToTree` formatters.

**Note:** `plan_node.go` in AD provides `PlanNode` types and formatting. `plan_node.go` in EX provides `buildPlanNodeTree` (walks the operator tree and builds a `*AD.PlanNode`) and `operatorType` (maps operator types to string labels).

### WT — Write Operators

**Responsibility:** Write operators, source operators, store operations, DDL executors, FK, views, triggers, materialized views.

**Files in WT:**
- `writers.go` (~2200 lines): `Insert`, `Update`, `Delete`, `Trigger`, `CreateTable`, `DropTable`, `CreateIndex`, `DropIndex`, `Pragma`, `Explain`, `Truncate`, `Reindex` operators; `buildWriterOp` dispatcher.
- `source.go`: `buildInsertRow`, `applyUpdate`, `evalTriggerWhen`, `UnregisterAll` (test reset).
- `alter_table.go`: `AlterTable` operator (`ADD COLUMN`, `DROP COLUMN`, `RENAME TO`, `RENAME COLUMN`).
- `constraints.go`: `memLookup`, `memLookupAdapter`, `uniqueLookupWithApply`, `validateCheck`, `fillDefaults`.
- `view.go`: `CreateViewOperator`, `DropViewOperator`.
- `matview.go`: `CreateMatViewOperator`, `RefreshMatViewOperator`, `DropMatViewOperator`.
- `subq.go`: `injectOuter` (walks operator trees wrapping leaf/intermediate operators in `outerInjector` for correlated subqueries) and `runSubqueryPlan` (drives a `*pl.PlanResult` and collects rows). Used by `(*EX.Planner).ExecuteSubquery`.

**Cross-package dependencies:** WT depends on DT (Row/Value/Schema registry), EV (`EV.EvalValue` for DEFAULT expressions), OP (operators), and AG (`Aggregate` for trigger REWRITE actions). It depends on EX for `UnregisterAll` and test helpers.

### UT — Utilities

**Responsibility:** Coercion, integrity checks, decimal, datetime, JSON, ANALYZE, parallel sort, pipeline, SIMD, batch, PRAGMA listener, FK validation helpers.

**Files in UT:**
- `coerce.go`: Go type → SQL type coercion.
- `decimal.go`: Decimal support for precise numeric calculations.
- `datetime.go`: Datetime functions.
- `json.go`: JSON functions.
- `parallel.go`: `WorkerPool` for parallel execution.
- `batch.go`: `Batch` + `Column` types for columnar evaluation; `Batch.Pool()`/`GetBatch`/`Put` use `sync.Pool` to avoid per-batch GC pressure.
- `simd_dispatch.go`: SIMD-dispatched scalar functions.
- `string_column.go`: String column operations.
- `txn_debug.go`: Transaction debugging utilities.
- `analyze.go`: `Analyze`, `Vacuum` operators: scan a table, collect column statistics (NDV, null count, min/max) using reservoir sampling, build histograms, persist to the catalog.
- `integrity.go`: `IntegrityTable` verifies catalog consistency, row counts, and data corruption.
- `pragma.go`: `PragmaListener` interface (`OnPragmaChange`), `RegisterPragmaListener`, `UnregisterAllPragmaListeners`, `NotifyPragmaChange`. Distinct from `SQB/OP/PragmaResult` (single-row result operator) and `SQB/WT/Pragma` (operator in WT).
- `fk.go`: FK validation helpers (`applyFKCheck`, `validateFKOnDelete`, `validateFKOnInsert`, `validateFKOnUpdate`).

## SQB Cluster Extraction Outcomes

### Initial Extraction (2026-06-29)
- EX shrank from 37 files / 48,907 LOC to 27 files / ≈38,000 LOC.
- Net delta: ~10,900 LOC moved out of EX into DT, EV, AG, OP (over three commits).
- DT grew to ≈1100 LOC (was 137 LOC). Carries the entire schema-registry and view/matview/catalog lifecycle plus the utilities needed by EV and AG (ValueFromAny, Compare, ToInt64, EqualValueAny, IsValueTruthy, ContainsAggregate, ContainsWindowFunc, SessionCounterAccessor, GetCurrentSessionID, etc.).
- 0 new test regressions; all SQB tests pass with `-race`. Pre-existing `SYS/SY/TestShutdown_NoGoroutineLeak` failure unchanged.
- 156 `Eval()` → `EvalValue()` migration sites (from prior SQL split prior work) all kept working through the package moves.
- Lock-order invariant `TablesMu → StoreMu` preserved by `UnregisterAll`; verified by `TestLockOrder_TablesMuBeforeStoreMu` (no test regression).

### Subsequent Extractions (WT, UT, AD, OP completion)
- EX further shrank from 27 files to 4 production files: `ex.go`, `planner.go`, `plan_node.go`, `source.go`.
- WT cluster created with 7 files: `writers.go`, `source.go`, `alter_table.go`, `constraints.go`, `view.go`, `matview.go`, `subq.go`.
- OP cluster expanded to 19 files with all operator types (leaf, intermediate, join, compound, parallel, vectorized).
- UT cluster expanded to 13 files with all utilities (coerce, datetime, json, analyze, integrity, etc.).
- AD cluster expanded to 7 files with ADQC, explain, and plan node types.
- No cycles introduced; the DAG dependency order is maintained.

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

SYS/SE imports `EX.SetSessionCounterAccessor`. This remains unchanged through all cluster fills. SYS/TX imports `EX.RestoreInMemoryTables`.

## Open Issues

- **RANGE window frame spec:** ROWS implemented; RANGE deferred.
- **Multi-column hash join keys:** Single key column only. Composite keys
  deferred (REQ000684).
- **LEFT OUTER JOIN:** INNER via HashJoin, OUTER via NestedLoopJoin.
- **Parallel query execution:** opt-in via query hint. Auto-parallelism
  deferred.
- **In-memory tables** do not support transaction rollback (REQ000641).

## Current Cluster Summary (2026-07-02)

| Cluster | Production Files | Responsibility |
|---|---|---|
| `DT` | 5 | Shared types, schema registry, value utilities |
| `EV` | 4 | Expression evaluation (EvalValue, EvalBatch, scalar functions) |
| `AG` | 6 | Aggregation (Aggregate, HashAggregate, WindowOperator) |
| `AD` | 7 | ADQC, explain, plan node types, cost estimation |
| `OP` | 19 | All operators (leaf, intermediate, join, compound, parallel, vectorized) |
| `EX` | 4 | Executor factory, planner (~6300 LOC), plan node tree builder |
| `WT` | 7 | Write operators (Insert/Update/Delete/DDD/DDL/triggers/views/matviews) |
| `UT` | 13 | Utilities (coerce, datetime, json, analyze, integrity, etc.) |

Total: 7 clusters, 65 production files. EX is the only cluster that still carries the `Planner` (~6300 LOC in `planner.go`) and the executor factory.