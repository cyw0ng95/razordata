# SQB — SQL Backend Execution Layer

## Overview

Consumes an operator tree (produced by `SQF/PL`) and executes it against the storage engine to return rows. The executor is a direct tree traverser with no virtual machine or bytecode layer. Implements the `Operator` interface (defined in `SQB/DT/types.go`), the `Row` data type, and all execution operators. Depends on `TXN`, `ENG`, `LOG`, and `SQF` (for AST types consumed by ANALYZE).

**Dependency direction:** SQB internal layout. The `Operator` interface and `Row` type now live in `SQB/DT`, shared by all SQB clusters. `SQF/PL` imports `SQB/DT` (not `SQB/EX`). Within SQB the cluster graph is DAG-shaped:

```
DT ← EX, EV, AG, AD, OP, UT (terminal)
        ↑
EV ← AG (aggregate needs EvalValue)
OP ← AD (planner constructs OP operators), AG (planner constructs AG operators), EX (executor owns planner and operators)
EX ← AD (planner currently lives in EX, will move to AD in iter-36)
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
| `DT` | **Shared data types.** Row/Value/Operator/ExecContext/Store/StatsCatalog types + aliases; schema registry (Tables/Schemas/StoreSchemas/InMemSchemas/TableIDs/TablePKs/RegisteredIndexes); view/matview/catalog registries (ViewRegistry/MatViewRegistry/CurrentCatalog); session counters (SessionCounterAccessor interface, CurrentSessionID atomic); AST traversal helpers (ContainsAggregate, ContainsWindowFunc); value conversion utilities (ValueFromAny, ValueToString, ValueFromAnySlice, ValueSliceToAny, Compare, ToInt64, EqualValueAny, IsValueTruthy). Imported by every other SQB cluster; does not import EX/EV/AG. |
| `EX` | Executor factory: `Executor.Exec`/`Query`/`QueryStream`, statement cache, session lifecycle, subq.go (injectOuter + runSubqueryPlan helpers used by `(*Planner).ExecuteSubquery`), plan_node.go (PlanNode tree for EXPLAIN), shape_specialize.go (DetectShape), matview.go (CreateMatView/RefreshMatView/DropMatView). Re-exports `SessionCounterAccessor`, `SetSessionCounterAccessor`, `SetCatalog`, `RegisterFromCatalog`, `RestoreInMemoryTables`, and the `ErrEval`/`ErrDivByZero`/`ErrTypeMismatch`/`ErrSubquery`/`ErrTriggerAbort` error sentinels from EV for SYS callers. Several operator files still reside in EX pending iter-36 (`operators.go`, `intermediate.go`, `join.go`, `operators_parallel.go`, `operators_vec.go`, `compound.go`, `planner.go`, `plan_node.go`, `shape_specialize.go`, `writers.go`, `source.go`, `store.go`, `alter_table.go`, `fk.go`, `view.go`, `constraints.go`, `analyze.go`, `integrity.go`, `explain.go`, `sort_parallel.go`, `pipeline.go`, `values.go`). |
| `OP` | Operators. `Distinct`, `HashJoin`, `HashCrossJoin` (already present before iter-35). iter-35 leaf operators moved from EX: `PragmaResult` (single-row PRAGMA row), `SqliteMaster` (sqlite_master virtual table). Operators.go (SeqScan, IndexScan), intermediate.go (Filter, Project, Sort, Limit, Offset), join.go (NestedLoopJoin), operators_parallel.go (ParallelSeqScan/ParallelIndexScan/ParallelUnionAll), operators_vec.go (VectorizedSeqScan/VectorizedFilter), compound.go (CompoundOp for UNION/INTERSECT/EXCEPT), values.go (Values/ValuesRows) are scheduled to move to OP in iter-36. |
| `EV` | Expression evaluation. `eval.go` (`EvalValue` entry point, all eval functions: evalBinaryValue, evalUnaryValue, evalBetween, evalCast, evalCase, evalInValue, evalInHashValue, evalBinaryShortCircuit, evalAggregate, evalFunction, evalWindowFunc, evalRaise, 33 scalar functions via bridges), `eval_vec.go` (`EvalBatch` vectorized batch predicate, hoisted operators `CompareFloat64ColLit`/`CompareInt64ColLit`/`CompareInt64Cols`/`CompareStringColLit`, `SwapOp`, `InvertSelection`, `BatchValueAt`), `function_registry.go` (`ScalarFuncRegistry` map of native scalar functions: LENGTH, UPPER, LOWER, IFNULL, COALESCE, NULLIF, NOW). Public exports `EvalValue`/`EvalForTest`/`EvalBatch`/`ClearSubqueryCaches`/`ScalarFuncRegistry`; error sentinels `ErrEval`, `ErrDivByZero`, `ErrTypeMismatch`, `ErrSubquery`, `ErrIgnoreRow`, `ErrTriggerAbort`. |
| `AG` | Aggregation. `aggregate.go` (`Aggregate` operator, `EvalAggregateOver`), `aggregate_vec.go`, `hashagg.go` (`HashAggregate` + `Child()`/`GroupCols()` accessors), `hashagg_parallel.go` (ParallelHashAggregate), `window.go` (`WindowOperator` + `Input()`/`FuncName()` accessors), `aggregate_registry.go` (`AggregateFuncRegistry` map: sumImpl, avgImpl, minImpl, maxImpl, countImpl, plus four `*Distinct` variants). AG depends on `EV.EvalValue`. |
| `AD` | ADQC and cache. `adqc.go` (`AdaptiveOp`), `adqc_cache.go` (`AdqcCache` LRU, 256 entries), `adqc_fallback.go` (`FallbackOp` safe-fallback wrapper), `adqc_telemetry.go` (telemetry), `cache_stats.go` (cache statistics), `index_usage.go` (index-skip tracking). `planner.go` (~4900 lines: `Planner`, `plan`/`joinPlan`/`tableInfo`/`joinTableInfo`, `NewPlanner`, `planInsert`/`planUpdate`/`planDelete`/`planSelect*`/`planCompound`/`planAggregation`/`planOrdering`/`planLimitOffset`, `*Planner.Plan`, `*Planner.ExecuteSubquery`, memoization, cost estimation, N3 join ordering, selectivity), `plan_node.go` (PlanNode tree), and `shape_specialize.go` are currently still in EX and scheduled to migrate to AD in iter-36 — blocked only on moving the operator types SeqScan/IndexScan/Filter/Project/Sort/Limit/Offset/NestedLoopJoin out of EX first (currently direct field access keeps the planner tied to EX). |
| `WT` | Write operators. **Planned but not yet created** — directory `internal/SQB/WT/` does not yet exist. Will host (currently in EX): `writers.go` (Insert/Update/Delete/Trigger/CreateTable/DropTable/CreateIndex/DropIndex/Pragma/Explain/Truncate/Reindex/DropView/DropTrigger/UnsupportedOp/AttachOp/DetachOp + buildWriterOp dispatcher, ~2500 lines), `source.go` (buildInsertRow/applyUpdate/evalTriggerWhen/UnregisterAll), `store.go` (key encoding, encodeRow/decodeRow, extractPK/maintainIndexesOnInsert-Delete-Update), `alter_table.go` (AlterTable operator), `fk.go` (FK validation), `view.go` (CreateViewOperator), `constraints.go` (memLookup/uniqueLookupWithApply/validateCheck). Iter-37 plans this move. WT will depend on DT, EV (for `EV.EvalValue` in DEFAULT), OP (operators from writers), and AG (none). It will NOT depend on EX. |
| `UT` | Utilities. Already in place: `coerce.go` (Go type → SQL type coercion), `decimal.go`, `datetime.go`, `json.go`, `parallel.go` (`WorkerPool`), `batch.go` (`Batch` + `Column` for columnar evaluation), `pipeline.go`, `simd_dispatch.go`, `string_column.go`, `txn_debug.go`, plus a small `pragma.go` that hosts the `PragmaListener` interface (`OnPragmaChange`, `RegisterPragmaListener`, `UnregisterAllPragmaListeners`, `NotifyPragmaChange`) — distinct from `SQB/OP/PragmaResult` (single-row PRAGMA row operator) and `SQB/EX/Pragma` (the actual PRAGMA operator under iter-36). Still in EX pending iter-37: `analyze.go`, `integrity.go`, `explain.go`, `sort_parallel.go`, `pipeline.go`, `view.go` (actually planned for WT), `matview.go` (planned for UT as a utility). |

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
- `matview.go`: `CreateMatViewOperator`, `RefreshMatViewOperator`, `DropMatViewOperator`.

**Pending iter-36 split (these remain in EX until iter-36 ships):** `planner.go`, `operators.go` (SeqScan, IndexScan, tableSchemaEntry), `intermediate.go` (Filter, Project, Sort, Limit, Offset), `join.go` (NestedLoopJoin), `join_strategy.go`, `operators_parallel.go` (ParallelSeqScan, ParallelIndexScan, ParallelUnionAll), `operators_vec.go` (VectorizedSeqScan, VectorizedFilter), `compound.go` (CompoundOp), `constraints.go` (memLookup, uniqueLookupWithApply), `values.go` (Values, ValuesRows), `explain.go` (ExplainStmtOp), `pipeline.go` (operatorPipelineAdapter), `analyze.go`, `integrity.go`, `sort_parallel.go`, `fk.go`, `view.go`, `store.go`, `source.go`, `writers.go`, `alter_table.go`, `indexscan_strategy.go`. These will move to OP, AD, UT, and WT in subsequent iterations.

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

// Leaf operators scheduled to move from EX in iter-36
type SeqScan   struct{ table string; filter Expr; schema *TableSchema; iter Iterator }
type IndexScan struct{ ... }

// Intermediate operators scheduled to move from EX in iter-36
type Filter    struct{ child Operator; predicate Expr }
type Project   struct{ child Operator; cols []string }
type Sort      struct{ child Operator; keys []Expr; asc []bool }
type Limit     struct{ child Operator; n int64 }
type Offset    struct{ child Operator; n int64 }

// Join operators scheduled to move from EX in iter-36
type NestedLoopJoin struct{ left, right Operator; cond Expr }

// Compound + parallel + vectorized (still in EX; scheduled iter-36)
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
- **NestedLoopJoin:** outer join, non-equi join, cross join with block-mode batching (batch size 32, right-side cache for ≤256 rows). Note: NestedLoopJoin is currently in EX/join.go and will move to OP in iter-36 along with `join_strategy.go`.

#### Compound SELECT

- `UNION`, `INTERSECT`, `EXCEPT` — set operations on result sets.
- `compound.go` handles the merge/dedup logic; still in EX pending iter-36.

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

**Subquery execution model:** `EvalValue` for an `*PS.ExistsExpr`/`*PS.SubqueryExpr`/IN-subquery reads `ec.Planner` from `ExecContext` (via `OP.ExecContextFromRow`) and falls back to `outer.GetPlanner()`, then calls `planner.ExecuteSubquery(ctx, subquery, outer, params)`. The concrete `ExecuteSubquery` method is implemented on `*EX.Planner` (lives in `EX/planner.go` today, migrates to `AD/planner.go` in iter-36) and exercises the subquery plan via `injectOuter` + `runSubqueryPlan` (both in `EX/subq.go`). The interface is added to `pl.QueryPlanner` in `SQF/PL/types.go` so eval.go never depends on `*Planner` directly.

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

**Removed from this cluster during iter-35 split:** `evalWindowFunc` (the placeholder, returns the "requires WindowOperator execution" error) was moved to `EV/eval.go` to keep `EV → AG → EV` from cycling.

### AD — ADQC/Planning

**Responsibility:** Adaptive query compilation, ADQC cache, plan cache (future), selectivity estimation (future).

**Currently in AD:**
- `adqc.go`, `adqc_cache.go`, `adqc_fallback.go`, `adqc_telemetry.go`: ADQC wrappers (`AdaptiveOp`, `AdqcCache`, `FallbackOp`), telemetry.
- `cache_stats.go`: cache statistics. Reserved home for the plan cache once the planner migrates in iter-36.
- `index_usage.go`: index-skip tracking (used by SeqScan to record when an available index was bypassed).

**Still in EX (pending iter-36):**
- `planner.go` (~4900 lines): `Planner`, `plan`, `joinPlan`, `tableInfo`, `joinTableInfo`, `planInsert`, `planUpdate`, `planDelete`, `planCreateTable`, `planDropTable`, `planSelect`, `planSelectScan`, `planSelectJoins`, `planSelectSubquery`, `planSelectSqliteMaster`, `planCompound`, `planAggregation`, `planOrdering`, `planLimitOffset`, `*Planner.Plan`, `*Planner.ExecuteSubquery`, `*Planner.SetPool`, `*Planner.SetStatsCatalog`, `*Planner.InvalidateCache`, `*Planner.SetJoinBufferSize`, `*Planner.SetMaxMemoryPerQuery`, `NewPlanner`, `ParallelThreshold`, `estimateCost`, `estimatePredicateSelectivity`, `estimateRowCount`, `findTableForColumn`, `extractTablesFromExpr`, `walkExprForTables`, `canPushDown`, `splitPredicatesByTable`, `equiJoinKey`, `extractEquiJoinKeys`, `extractSingleOnEquiKey`, `selectIndex`, `n3JoinOrdering`, `n3JoinOrderingMultiStart`, `exhaustiveJoinOrder`, `estimateJoinOrderCost`, `findPredicatesForPair`, `findPredicatesForSet`, `hasIndexOnTable`, `joinResultRows`, `populatePlan`, `propagatePlanner`, `propagateExecContext`, `hasAnyWindowFunc`, `isStarExpr`. The `planner.go` split to AD is blocked on moving `SeqScan`, `IndexScan`, `Filter`, `Project`, `Sort`, `Limit`, `Offset`, `NestedLoopJoin` operators out of EX. Until then, direct field access (`v.child`, `v.funcName`, `v.groupCols`) keeps planner.go's imports confined to EX.
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

**Status:** **Cluster pending.** Directory `internal/SQB/WT/` does not yet exist. Files remain in `internal/SQB/EX/` until iter-37 ships.

- `writers.go` (~2500 lines): `Insert`, `Update`, `Delete`, `Trigger`, `CreateTable`, `DropTable`, `CreateIndex`, `DropIndex`, `Pragma`, `Explain`, `Truncate`, `Reindex`, `DropView`, `DropTrigger`, `UnsupportedOp`, `AttachOp`, `DetachOp` operators; `buildWriterOp` dispatcher.
- `source.go`: `buildInsertRow`, `applyUpdate`, `evalTriggerWhen`, `UnregisterAll` (test reset).
- `store.go`: key encoding (`rowKey`, `pkToBytes`, `int64ToBytesBigEndian`), `encodeRow`/`decodeRow`, `extractPK`, `extractPKForUpdate`, `maintainIndexesOnInsert/Delete/Update`, `indexValueFor`, `tablePrefix`, `encodeTablePrefix`.
- `alter_table.go`: `AlterTable` operator (`ADD COLUMN`, `DROP COLUMN`, `RENAME TO`, `RENAME COLUMN`).
- `fk.go`: FK validation during writes (`applyFKCheck`, `validateFKOnDelete`, `validateFKOnInsert`, `validateFKOnUpdate`).
- `view.go`: `CreateViewOperator`.
- `constraints.go`: `memLookup`, `memLookupAdapter`, `uniqueLookupWithApply`, `validateCheck`, `fillDefaults`.
- `indexscan_strategy.go`: index-scan strategy helpers (`ScanStrategy`, `storeIter` interface); consumed by writers during secondary index maintenance.

**Cross-package dependencies (when WT is created):** WT will depend on DT (Row/Value/Schema registry), EV (`EV.EvalValue` for DEFAULT expressions), OP (it constructs `OP.NewSqliteMaster` and consumes other OP operators), and AG (`Trigger`/REWRITE actions can use Aggregate operators). It will NOT depend on EX.

### UT — Utilities

**Responsibility:** Coercion, integrity checks, decimal, datetime, JSON, ANALYZE, parallel sort, pipeline, SIMD, batch, PRAGMA listener.

**Key behaviors:**

#### Coercion (`coerce.go`)
Type coercion: Go `int` to `BIGINT`, Go `string` to `INT` (error), etc.

#### PRAGMA Listener (`pragma.go`)
`PragmaListener` interface (`OnPragmaChange`), `RegisterPragmaListener`, `UnregisterAllPragmaListeners`, `NotifyPragmaChange`. Distinct from `SQB/OP/PragmaResult` (single-row result operator) and `SQB/EX/Pragma` (operator under iter-36).

#### Integrity (`integrity.go`)
`IntegrityTable` verifies catalog consistency, row counts, and data corruption. Note: still in EX/integrity.go pending iter-36.

#### Decimal (`decimal.go`)
Decimal support for precise numeric calculations.

#### Datetime (`datetime.go`)
Datetime functions.

#### JSON (`json.go`)
JSON functions.

#### ANALYZE (`analyze.go`)
`Analyze`, `Vacuum` operators: scan a table, collect column statistics (NDV, null count, min/max) using reservoir sampling, build histograms, persist to the catalog. Pipeline: `EXEC ANALYZE table` → `buildWriterOp` → `Analyze.Next()` reads all rows from storage. Still in EX/analyze.go; will move to UT in iter-37.

#### EXPLAIN (`explain.go`)
`ExplainStmtOp`. Still in EX; will move to UT in iter-36.

#### Parallel Sort (`sort_parallel.go`)
Parallel sort with top-k and external merge. Still in EX; will move to UT in iter-37.

#### Pipeline Parallelism (`pipeline.go`)
Fan-out/fan-in pipeline parallelism. Still in EX; will move to UT in iter-36.

#### SIMD Dispatch (`simd_dispatch.go`)
SIMD-dispatched scalar functions.

#### Virtual Table (`virtual.go`)
`SqliteMaster` was moved to **OP** (not UT) in iter-35 because it is an Operator.

#### Batch (`batch.go`)
Batch + Column types for columnar evaluation; canonical home in UT. `Batch.Pool()`/`GetBatch`/`Put` use `sync.Pool` to avoid per-batch GC pressure.

## iter-35 Outcome (2026-06-29)

- EX shrank from 37 files / 48,907 LOC to 27 files / ≈38,000 LOC.
- Net delta: ~10,900 LOC moved out of EX into DT, EV, AG, OP (over three commits).
- DT grew to ≈1100 LOC (was 137 LOC). Carries the entire schema-registry and view/matview/catalog lifecycle plus the utilities needed by EV and AG (ValueFromAny, Compare, ToInt64, EqualValueAny, IsValueTruthy, ContainsAggregate, ContainsWindowFunc, SessionCounterAccessor, GetCurrentSessionID, etc.).
- 0 new test regressions; all SQB tests pass with `-race`. Pre-existing `SYS/SY/TestShutdown_NoGoroutineLeak` failure unchanged.
- 156 `Eval()` → `EvalValue()` migration sites (from iter-32 prior work) all kept working through the package moves.
- Lock-order invariant `TablesMu → StoreMu` preserved by `UnregisterAll`; verified by `TestLockOrder_TablesMuBeforeStoreMu` (no test regression).
- Cycle breaking for `planner.go` ⇄ `aggregate.go`: `containsAggregate`/`containsWindowFunc` moved to DT; `planner.go` now calls `DT.ContainsAggregate`/`DT.ContainsWindowFunc`.
- Cycle breaking for `eval.go` ⇄ `planner.go`: added `ExecuteSubquery(ctx, stmt, outer, params) ([]Row, error)` method to `pl.QueryPlanner`; `eval.go` reads `ExecContext.Planner` and calls `planner.ExecuteSubquery(...)`. `(*EX.Planner).ExecuteSubquery` is the concrete implementation; it stays in EX until the planner migrates to AD.
- Cycle breaking for `eval.go` ⇄ `aggregate.go`: `evalWindowFunc` (placeholder returning "requires WindowOperator execution") was moved from AG/window.go to EV/eval.go to prevent AG ↔ EV cycling.
- Accessor methods added so cross-package field access is gated: `AG.Aggregate.Child()`, `AG.HashAggregate.Child()`, `AG.HashAggregate.GroupCols()`, `AG.WindowOperator.Input()`, `AG.WindowOperator.FuncName()`.
- Cross-package helper functions exported (lowercase → CapitalCase) only where EX tests required it: `NumericArithValue`, `CompareInt64ColLit`, `CompareInt64Cols`, `CompareFloat64ColLit`, `CompareStringColLit`, `SwapOp`, `InvertSelection`, `BatchValueAt`, `MatchLike`, `GlobValue`, `ConcatValue`, `EvalAbs`, `EvalInHashValue`, `EvalInValue`, `EvalAggregateOver` (the latter via AG re-export), `ScalarFuncRegistry`, `InHashCache`, `InHashCache.Int64Set`. The lowercase names are not in the package's public surface; EX test calls were updated to the exported names.
- New `DT` public utilities consumed by EX: `DT.Compare` (`Compare`), `DT.EqualValueAny` (`EqualValueAny`), `DT.ToInt64` (`ToInt64`), `DT.IsValueTruthy` (`IsValueTruthy`), `DT.ValueFromAny` (`ValueFromAny`), `DT.ValueToString` (`ValueToString`), `DT.NullValue`, `DT.New*Value` (`NewIntValue`, etc.).

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

SYS/SE imports `EX.SetSessionCounterAccessor`. This remains unchanged through the iter-36 WT/UT moves. SYS/TX imports `EX.RestoreInMemoryTables`.

### Residual files still in `internal/SQB/EX/`

The following files remain in EX and have not yet been migrated:

- Executor factory and glue: `ex.go`, `subq.go`, `matview.go`, `plan_node.go`, `shape_specialize.go` (the latter three paired with the planner for iter-36).
- Operators awaiting OP migration: `operators.go` (SeqScan, IndexScan, tableSchemaEntry), `intermediate.go` (Filter, Project, Sort, Limit, Offset), `join.go` (NestedLoopJoin), `join_strategy.go`, `operators_parallel.go`, `operators_vec.go`, `compound.go` (CompoundOp), `values.go` (Values, ValuesRows).
- Writes awaiting WT creation: `writers.go`, `source.go`, `store.go`, `alter_table.go`, `fk.go`, `view.go`, `constraints.go`, `indexscan_strategy.go`.
- Utilities awaiting UT migration: `analyze.go`, `integrity.go`, `explain.go`, `sort_parallel.go`, `pipeline.go`.
- Planner waiting on the above to land: `planner.go` (~4900 lines).

## Open Issues

- **RANGE window frame spec:** ROWS implemented; RANGE deferred.
- **Multi-column hash join keys:** Single key column only. Composite keys
  deferred (REQ000684).
- **LEFT OUTER JOIN:** INNER via HashJoin, OUTER via NestedLoopJoin.
- **Parallel query execution:** opt-in via query hint. Auto-parallelism
  deferred.
- **In-memory tables** do not support transaction rollback (REQ000641).