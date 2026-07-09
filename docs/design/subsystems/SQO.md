# SQO — SQL Query Optimizer

> **Status**: Designed but not yet implemented. See REQ001473-REQ001481 in `docs/development/REQUIREMENTS.md`.

## Overview

The SQL Query Optimizer is responsible for transforming parsed ASTs into optimized
operator trees that the backend (`SQB/EX`) can execute. It is the cost-based
optimizer of the Razordata query pipeline, positioned between `SQF` (parser) and
`SQB` (executor) in the dependency chain:

```
SQF (parser) → SQO (optimizer) → SQB (executor)
```

SQO consumes AST nodes from `SQF/PS`, produces `DT.Operator` trees defined in
`SQB/DT`, and depends on statistics from `ENG/LS` via the `StatsCatalog`
interface. It never touches the disk directly — that is the executor's job.

## Motivation

The current optimizer code lives in two locations:

1. `internal/SQF/PL/` — memoization (xxhash64 LRU), learned-model stub, predicate cache
2. `internal/SQB/EX/` — cost model, selectivity estimation, join ordering, planner_select (~5100 lines)

This split has caused structural problems:

- **No clear subsystem boundary**: 5100 lines of optimizer code coexist with
  executor code in the same `SQB/EX` package, making it impossible to test the
  optimizer in isolation.
- **Naming collision**: `SQF/PL` (planner) competes with the historical notion
  of the planner as a single concept, but only handles memoization today.
- **Two optimization concerns conflated**: rewrite rules (logical optimization)
  live in `SQF/RE`, while cost-based enumeration (physical optimization) lives
  in `SQB/EX`. Both are conceptually optimizer concerns.
- **Calibration deferred**: cost model uses PG-compatible dimensionless units
  (`seq_page_cost=1.0`, `random_page_cost=4.0`) that cannot be auto-calibrated
  against actual execution time.

## Architecture: 6 Clusters

```
internal/SQO/
├── CP/    Cost Params          (CostParams struct, defaults, calibration harness)
├── CM/    Cost Models          (per-operator cost formulas, dispatch)
├── SL/    Selectivity          (predicate → row-count estimation)
├── JN/    Join ordering        (N3, bushy join detection, multi-start)
├── MM/    Memoization          (xxhash64 LRU plan cache + LEO learned feedback)
├── RW/    Rewriting            (predicate pushdown, subquery flatten, constant fold)
└── CO/    COre/Orchestrator    (Optimizer interface, Plan entry, SELECT/DML/DDL dispatch)
```

**Note**: All Go files live under a cluster directory — no `.go` files directly
under `internal/SQO/`. This matches the convention used by `SQF/`, `SQB/`, `SYS/`
and every other subsystem (see `ARCH.md`).

### Cluster Responsibilities

#### SQO/CP — Cost Params

Owns the `CostParams` struct and calibration. Single source of truth for cost
coefficients. Two operating modes:

- **Legacy mode**: dimensionless units (PG-compatible: `SeqPageCost=1.0`,
  `RandomPageCost=4.0`, `CPUTupleCost=0.01`, `CPUIndexTupleCost=0.005`,
  `CPUOperatorCost=0.0025`)
- **Absolute-time mode** (target): `time.Duration` units (e.g.,
  `SeqScanNanos = 50ns` per row), auto-calibrated by `SQO/CL/calibrate.go`
  micro-benchmarks against the live store.

```go
package CP

type CostParams struct {
    // Legacy dimensionless units (REQ001104, PG-compatible defaults)
    SeqPageCost       float64
    RandomPageCost    float64
    CPUTupleCost      float64
    CPUIndexTupleCost float64
    CPUOperatorCost   float64

    // Future absolute-time units (REQ001450)
    SeqScanNanos      time.Duration
    IndexNanos        time.Duration
    HashBuildNanos    time.Duration
    HashProbeNanos    time.Duration
    NLJOuterNanos     time.Duration
    SortNanos         time.Duration

    // Mode flag
    AbsoluteTime bool
}

func Default() CostParams
func (cp *CostParams) Set(opts CostOptions)
```

#### SQO/CM — Cost Models

Owns per-operator cost formulas. Two implementations: legacy (constant per-op)
and PG-style (rows × page-cost). Dispatched by `estimateCost`:

| Operator | Legacy | PG-style |
|---|---|---|
| `SeqScan` | `1.0` | `rows × SeqPageCost` |
| `IndexScan` (seek) | `0.05` | `rows × CPUIndexTupleCost + rows × RandomPageCost / 100` |
| `IndexScan` (range) | `0.1` | `2×` (seek cost) |
| `Filter` | `child × selectivity` | `child + child × sel × CPUOperatorCost` |
| `Project` / `Limit` / `Offset` | `child` | `child (+ CPUTupleCost for Project)` |
| `Sort` | `child × (1 + log₂(child))` | `child × (1 + log₂(child)) + child × CPUOperatorCost` |
| `Aggregate` | `child + 1` | `child + 1` |
| `NestedLoopJoin` | `left × right` | `left × (right + CPUOperatorCost)` |
| `HashJoin` | `left + right` | `right + left × CPUOperatorCost + right × CPUTupleCost` |
| `MergeJoin` | `left + right + 1` | `left + right + CPUTupleCost` |
| `BitmapHeapScan` | `0.05 × N + 0.05` | (legacy only — REQ001444) |
| `IndexOnlyScan` | `0.03` | (legacy only — REQ001444) |
| DML/DDL | `1.0` | `1.0` |

```go
package CM

func Estimate(op DT.Operator, cp CP.CostParams) float64
func EstimateLegacy(op DT.Operator) float64
func EstimatePGStyle(op DT.Operator, cp CP.CostParams) float64
```

#### SQO/SL — Selectivity

Owns predicate → row-count estimation. Three formula families:

- **Equality** (`col = literal`): `1 / NDV` (PG `eqjoinsel`)
- **Range** (`col < / > / ≤ / ≥ literal`): `(1 - null_frac) / 3`
- **IN-list with MCVs**: `(1 - ∏(1 - pᵢ)) + tailSel` (REQ001057b, CockroachDB-style)
- **OR-chain** (`col = X OR col = Y`): `1 - (1 - 1/NDV)^k` per column group (REQ001219)

```go
package SL

func JoinPredSel(pred PS.Expr, rowCount float64, stats StatsCatalog) float64
func EqSelectivity(stats *ls.ColumnStats, lit []byte) float64
func RangeSelectivity(stats *ls.ColumnStats, low, high []byte) float64
func InListSelectivity(list []PS.Expr, rowCount float64, mcvs [][]byte, freqs []float64) float64
func NDVFromExpr(expr PS.Expr, stats StatsCatalog) float64
func NullFracFromExpr(expr PS.Expr, stats StatsCatalog) float64
```

The `SL` name avoids conflict with `SYS/SE` (session).

#### SQO/JN — Join Ordering

Owns the join ordering algorithms. SQLite-style N3 with multi-start:

- `N3`: N-Nearest-Neighbors (N=12-18, adaptive by join size)
- `n3JoinOrderingMultiStart`: try every candidate base table
- `groupBushyJoins`: detect independent equi-join pairs for bushy execution
- `n3HeapMaxSize`: bounded heap (default 24, REQ001436 adaptive sizing)

```go
package JN

func N3(baseTable string, joins []JoinClause, where []PS.Expr) (order []string, cost float64)
func MultiStart(tables []string, joins []JoinClause) (bestOrder []string, bestCost float64)
func GroupBushy(joins []JoinClause) []BushyGroup
func EstimateJoinCost(leftRows, rightRows int, predicates []PS.Expr, hasIndex bool) float64
```

#### SQO/MM — Memoization

Owns plan cache and learned feedback. Two components:

- **Memo**: xxhash64 AST fingerprint → cached plan, LRU eviction (default 4096
  entries), schema-version invalidation on DDL
- **LearnedModel** (LEO-style post-execution feedback, REQ001438): per-predicate
  correction factor `actual / estimated`, applied synchronously between
  queries in single-connection model

```go
package MM

type Memo struct { ... }
type LearnedModel struct { ... }

func NewMemo(capacity int) *Memo
func (m *Memo) Get(key string) (*Plan, bool)
func (m *Memo) Put(key string, p *Plan)
func (m *Memo) Invalidate(schemaVersion uint64)

func (lm *LearnedModel) Record(predSig string, actual, estimated int64)
func (lm *LearnedModel) Apply(predSig string) float64
func SerializeKey(stmt PS.Stmt, schemaVersion uint64) string
```

#### SQO/RW — Rewriting

Owns logical optimization (rule-based). Three passes:

- **Predicate pushdown**: push WHERE predicates down to scans
- **Subquery flattening**: convert correlated subqueries to joins where possible
- **Constant folding**: evaluate constant expressions at plan time

```go
package RW

func PushDownPredicates(plan DT.Operator, where []PS.Expr) DT.Operator
func FlattenSubqueries(stmt PS.Stmt) PS.Stmt
func FoldConstants(expr PS.Expr) PS.Expr
```

Currently lives in `internal/SQF/RE/` and `internal/SQB/EX/predicate.go` —
migration consolidates them here.

### Top-level: `SQO/CO/`

The orchestrator. Defines the public `Optimizer` interface consumed by `SQB/EX`:

```go
package CO

type Optimizer interface {
    Plan(stmt PS.Stmt) (DT.Operator, error)
    SetCostParams(cp CP.CostParams)
    SetStatsCatalog(stats StatsCatalog)
    InvalidateCache()
}

type StatsCatalog interface {
    TableStats(table string) *TableStats
    ColumnStats(table, col string) *ColumnStats
    Invalidate(schemaVersion uint64)
}

func New(store DT.Store) Optimizer
func NewWithOptions(opts Options) Optimizer
```

## Cluster Dependency Graph

```
internal/SQO/
  CM → CP   (cost models use params)
  SL → CP   (selectivity uses row count from catalog)
  JN → SL   (join order uses selectivity)
  RW → SL   (rewriting uses selectivity)
  MM        (no SQO deps; uses SQB/DT types only)
  CO → CM, SL, JN, MM, RW  (orchestrates all)
```

External dependencies:

```
SQF/PS  (AST nodes)  ← input
SQB/DT  (Row/Value/Operator interface)  ← output
ENG/LS  (StatsCatalog impl)  ← input
TXN/MV  (version chain interface for plan visibility)  ← input
```

## Subsystem Placement in Dependency Order

```
LOG → FIL → MEM → WAL → ENG → TXN → SQF → SQO → SQB → SYS → DBG
                              ▲    ▲    ▲
                           解析  优化  执行
```

`SQO` sits between `SQF` (parser) and `SQB` (executor). The historical
`SQF/PL` and `SQF/RE` clusters are absorbed into `SQO` — `SQF` reduces to
parser-only (`LX` + `PS`).

## Migration Plan from Current State

### Current state (pre-migration)

| Location | Lines | Role |
|---|---|---|
| `internal/SQF/PL/` | 11 files | Memoization + learned model + predicate cache |
| `internal/SQF/RE/` | ? files | Constant folding + subquery flatten |
| `internal/SQB/EX/cost.go` | 814 | Cost model + selectivity + row count |
| `internal/SQB/EX/join_order.go` | 813 | N3 + bushy |
| `internal/SQB/EX/planner.go` | 866 | Planner struct, DML/DDL dispatch |
| `internal/SQB/EX/planner_select.go` | 1565 | SELECT plan generation |
| `internal/SQB/EX/predicate.go` | 836 | Selectivity helpers, predicate manipulation |
| `internal/SQB/EX/planner_stats_propagation.go` | 206 | Stats propagation |

### Migration REQs

| REQ | Title | Effort |
|---|---|---|
| REQ001473 | Establish `internal/SQO/` skeleton + interfaces | 1 day |
| REQ001474 | Migrate `cost.go` to `internal/SQO/{CP,CM,SL}/` | 3 days |
| REQ001475 | Migrate `join_order.go` to `internal/SQO/JN/` | 2 days |
| REQ001476 | Migrate `SQF/PL/memo.go` + `learned.go` to `internal/SQO/MM/` | 1 day |
| REQ001477 | Migrate `SQF/RE/*` to `internal/SQO/RW/` | 2 days |
| REQ001478 | Migrate `planner.go` + `planner_select.go` to `internal/SQO/` | 3 days |
| REQ001479 | Update `EX` to depend on `SQO.Optimizer` interface | 0.5 day |
| REQ001480 | Delete `SQF/PL/` and `SQF/RE/` | 0.5 day |
| REQ001481 | **Update `ARCH.md` and `AGENTS.md` to reflect `SQO` subsystem (this doc)** | 0.5 day |

**Total**: ~13.5 days mechanical migration + tests.

## Design Decisions

### D1: Why `SQO` and not `QPO`?

`SQO` extends the existing S-prefix naming pattern (`SQF`, `SQB`):

| Subsystem | Name | Meaning |
|---|---|---|
| `SQF` | SQL Frontend | parser + AST |
| `SQO` | SQL Query Optimizer | optimizer (NEW) |
| `SQB` | SQL Backend | executor + operators |

`QPO` would break this pattern. `SQO` makes the Frontend → Optimizer → Backend
pipeline self-documenting.

### D2: Cluster naming — `SL` not `SE`

`SE` is already used by `SYS/SE/` (session lifecycle). The selectivity cluster
uses `SL` (SeLectivity) to avoid collision.

### D3: Absolute-time cost model

The legacy dimensionless units (`SeqPageCost=1.0`, etc.) are PG-compatible but
not auto-calibratable. The target design supports `time.Duration` units:

```go
type CostParams struct {
    SeqPageCost       float64  // legacy
    SeqScanNanos      time.Duration  // absolute
    AbsoluteTime      bool  // mode flag
}
```

This enables auto-calibration via micro-benchmarks (REQ001449) — run
representative workloads on the live store, measure actual nanoseconds per
operator, populate `CostParams`. The result: a cost model that honestly
reflects what each operator costs on **this** machine, **this** workload, **this**
data layout.

### D4: LEO feedback loop

PostgreSQL 8.3 had LEO but PG 17 dropped it (race conditions in multi-connection
servers). Razordata is single-connection — the natural fit. Each query's
`actual_rows / estimated_rows` becomes a per-predicate correction factor
applied synchronously before the next query's optimization.

### D5: Plan cache key = xxhash64(AST) + schema version

The current xxhash64 + LRU memo (REQ000584) is preserved. Schema version bump
on DDL invalidates all cached plans via key mismatch — this is more aggressive
than PG's generic/custom plan approach and simpler to reason about.

## Subsystem Mapping Summary

| Old location | New location | Cluster |
|---|---|---|
| `SQF/PL/pl.go` (Planner struct, Plan) | `SQO/CO/optimizer.go` | CO |
| `SQF/PL/memo.go` (xxhash64, LRU) | `SQO/MM/memo.go` | MM |
| `SQF/PL/learned.go` (LearnedModel stub) | `SQO/MM/learned.go` | MM |
| `SQF/PL/predicate_cache.go` | `SQO/MM/predicate_cache.go` | MM |
| `SQF/RE/const_fold.go` | `SQO/RW/const_fold.go` | RW |
| `SQF/RE/subquery_flatten.go` | `SQO/RW/subquery.go` | RW |
| `SQF/RE/predicate_pushdown.go` | `SQO/RW/predicate.go` | RW |
| `SQB/EX/planner.go:459-500` (CostParams struct) | `SQO/CP/params.go` | CP |
| `SQB/EX/cost.go:17-110` (estimateCostLegacy) | `SQO/CM/legacy.go` | CM |
| `SQB/EX/cost.go:116-203` (estimateCostWithParams) | `SQO/CM/pg_style.go` | CM |
| `SQB/EX/cost.go:205-328` (selectivity) | `SQO/SL/*.go` | SL |
| `SQB/EX/cost.go:383-404` (estimateJoinCost) | `SQO/JN/cost.go` | JN |
| `SQB/EX/cost.go:447-604` (joinPredSel) | `SQO/SL/predicates.go` | SL |
| `SQB/EX/cost.go:627-703` (estimateInListSelectivity) | `SQO/SL/in_list.go` | SL |
| `SQB/EX/cost.go:731-792` (NDV/null helpers) | `SQO/SL/stats_helpers.go` | SL |
| `SQB/EX/join_order.go` (all 813 lines) | `SQO/JN/n3.go`, `multi_start.go`, `bushy.go` | JN |
| `SQB/EX/planner_select.go` (1565 lines) | `SQO/plan/select.go` | top-level |
| `SQB/EX/planner_stats_propagation.go` | `SQO/CP/stats_propagation.go` | CP |
| `SQB/EX/predicate.go` (selectivity helpers) | `SQO/SL/util.go` | SL |

## Open Questions

1. **Calibration timing**: Should calibration run at every `Engine.Open` (slow
   startup) or only on first open + cache results in `meta.razor`?
2. **LEO persistence**: Should correction factors survive process restart, or
   be session-only?
3. **Rewriter pass ordering**: Should predicate pushdown run before or after
   subquery flattening? Current code has them in separate phases; SQO may want
   to interleave.
4. **CP/CM separation**: should they be merged into a single `cost/` package?
   Currently CP=struct, CM=formulas — clean separation but two packages to navigate.

## References

- `docs/design/ARCH.md` — Razordata architecture (subsystem table, dependency order)
- `docs/compose/reports/REPORT.md` — SQLite/PG/modern DBMS optimizer comparison,
  Razordata gap analysis, refactoring roadmap
- `internal/SQB/EX/cost.go` — Current cost model source (will migrate to SQO)
- `internal/SQB/EX/planner.go:459-500` — Current CostParams struct
- `internal/SQB/EX/join_order.go` — Current N3 implementation (will migrate to SQO/JN)
- `internal/SQF/PL/memo.go` — Current xxhash64 LRU (will migrate to SQO/MM)