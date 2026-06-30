# Dependency Structure for Razordata: Principles & Pattern

> Research report — June 2026
> Problem: Subsystem and cluster dependency structure is hard to maintain
> Goals: Strict layering (interface at boundary), clear principles, maintainable patterns

---

## Table of Contents

1. [Current State Analysis](#1-current-state-analysis)
2. [Principle 1 — Strict Layering via Interface Boundaries](#2-principle-strict-layering-via-interface-boundaries)
3. [Principle 2 — Package-Oriented Design](#3-principle-package-oriented-design)
4. [Principle 3 — Stable-Dependency Principle (Reversal)](#4-principle-stable-dependency-principle-reversal)
5. [Principle 4 — Cluster Extraction Policy](#5-principle-cluster-extraction-policy)
6. [Principle 5 — Dumpster Prevention (Against DTs)](#6-principle-dumpster-prevention)
7. [Principle 6 — Automated Enforcement](#7-principle-automated-enforcement)
8. [Revised Subsystem Dependency Graph](#8-revised-subsystem-dependency-graph)
9. [Revised SQB Cluster Map](#9-revised-sqb-cluster-map)
10. [Actionable Steps](#10-actionable-steps)

---

## 1. Current State Analysis

### 1.1 Current Dependency Chain

```
LOG → FIL → MEM → WAL → ENG → TXN → SQF → SQB → SYS
```

On paper this is a clean linear chain. In practice, **SQB breaks the layering**:

- `SQB/DT` imports `SQF/PL` types (`Operator`, `Row`, `Value`, `ExecContext`) — meaning the "backend" directly depends on the "frontend."
- `SQB/EV` must call back into the planner for subquery execution (`eval.go → planner.ExecuteSubquery`), bridged through `SQF/PL.QueryPlanner` interface — a reasonable pattern.
- `SQF/PL` imports `SQB/DT` for `Operator` interface — creating an **implicit import cycle** SQF → SQB → SQF that was broken by extracting DT but remains conceptually fragile.
- `SQB/EX` re-exports sentinels and functions from `SQB/EV` for backward compatibility with `SYS` — an adapter pattern that adds indirection without clear ownership.

### 1.2 Current Pain Points

| Pain point | Root cause | Impact |
|---|---|---|
| **DT dumping ground** | DT started as `types.go` but grew to ~1100 LOC carrying schema registries, view/matview lifecycle, value conversion, AST helpers, session management | Every change to DT recompiles all of SQB; no ownership boundary |
| **Planner stuck in EX** | Planner.go (~4900 lines) blocked on operator moves — direct field access to types still in EX | One of the largest files in the project cannot move to its logical home (AD) |
| **EX re-exports** | `EX` re-exports `ErrEval`, `SessionCounterAccessor`, etc. for SYS callers | SYS imports EX to get EV's symbols — wrong ownership |
| **Fragile cycle breaks** | `eval.go ⇄ aggregate.go` broken by moving helpers to DT; `eval.go ⇄ planner.go` broken via `QueryPlanner` interface | DT carries code that doesn't belong there; interfaces create opaque dependencies |
| **No dependency enforcement** | Nothing prevents DT from importing EX, or SQB from importing ENG directly | Architecture drifts silently with every PR |

### 1.3 What We Know From iter-35

The iter-35 extraction was successful (EX shrank from 48,907 LOC to ~38,000 LOC, no new test failures). It demonstrated:

- **Accessor methods** (`AG.Aggregate.Child()`) break cross-package field access — workable but verbose
- **Interface mediation** (`SQF/PL.QueryPlanner`) breaks import cycles — the right pattern
- **Re-exports** are necessary during migration but must be temporary
- **DT as middle ground** works but is already showing strain at 1100 LOC

---

## 2. Principle 1 — Strict Layering via Interface Boundaries

### 2.1 The Rule

> **A subsystem may only depend on subsystems at strictly lower layers.**
> When A needs behavior from B at the same level or higher, A defines an interface and B implements it — B imports A, not the reverse.

This is the **Dependency Inversion Principle** applied at subsystem granularity.

### 2.2 Current Violations

| Violation | Direction | Fix |
|---|---|---|
| `SQB/DT` imports `SQF/PL` | SQB (layer 8) imports SQF (layer 7) | Define `Row`, `Value`, `Operator`, `ExecContext` types in a minimal type package NOT owned by either subsystem — or define them in SQB/DT and have SQF/PL import them (keeping SQF → SQB direction, which is downhill per the dependency chain) |
| `SQB/EV` needs `planner.ExecuteSubquery` | SQB needs to call from `SQF/PL` | Already fixed via `QueryPlanner` interface — the pattern is correct but the interface belongs in SQB, not SQF |
| `SQB/EX` re-exports `ErrEval` for `SYS` | `SYS` should import `SQB/EV` directly | Drop the re-exports; update SYS import paths |

### 2.3 The Correct Data Flow

```
  Layer          Package            Defines Interface    Implements Interface
  ------         -------            -----------------    --------------------
  SYS (9)        SYS/SY, SE, TX     —                    —
  SQB (8)        SQB/{EV,OP,AD}     Planner, Eval        —
  SQF (7)        SQF/PL             Operator, Row*       Planner (via SQB/AD)*
  TXN (6)        TXN/{MV,LC,VL}     Tx                   —
  ENG (5)        ENG/LS, ID         Iterator, Store       —
  WAL (4)        WAL/WR, RP         —                     —
  MEM (3)        MEM/BF, PC         —                     —
  FIL (2)        FIL/DF, IO         —                     —
  LOG (1)        LOG/LG, HK         —                     —
```

*\*This is the crux: `Operator` and `Row` types are needed by BOTH SQF (to build the operator tree) and SQB (to execute it). If SQB defines them and SQF imports SQB, we have a downhill import (7 → 8, which violates the layer direction LOG→FIL→MEM→WAL→ENG→TXN→SQF→SQB→SYS).*

*Solution: Define `Operator` and `Row` in a NEUTRAL package at a lower layer (say `ENG/AP` or a dedicated minimal package) that BOTH SQF and SQB import without depending on each other.*

### 2.4 The Neutral Base Package Pattern

```
internal/
  types/               ← NEW: neutral types at layer 3.5 (after MEM, before WAL)
    operator.go         Operator interface { Next(ctx) (Row, error) Close() error }
    row.go              Row struct { Cols, Types, Data, Outer, ... }
    value.go            Value type + constructors + utilities
    exec_context.go     ExecContext
    store.go            Store interface
    stats_catalog.go    StatsCatalog interface
    planner_provider.go PlannerProvider interface { ExecuteSubquery(...) }
    errors.go           ErrNotImplemented, ErrNoRows, ErrClosed
  
  SQF/                  imports internal/types/, internal/LOG/
    LX/
    PS/
    RE/
    PL/                 imports internal/types/, internal/SQB/EV* (for selectivity estimation)
  
  SQB/
    DT/                 REMOVED — subsumed by internal/types/
    EV/                 imports internal/types/, internal/SQF/PL (AST types)
    OP/                 imports internal/types/, internal/SQB/EV
    ...
```

**Key insight**: Once `Operator`, `Row`, `Value` live in a neutral package at layer 3.5 (after MEM, before WAL — they need no I/O), the entire dependency graph simplifies. SQF and SQB become independent siblings that both depend on a common lower layer.

---

## 3. Principle 2 — Package-Oriented Design

From William Kennedy's Package Oriented Design (Ardan Labs, 2017):

### 3.1 Three Package Categories

| Category | Location | Characteristics | Examples in Razordata |
|---|---|---|---|
| **Kit / Foundation** | `internal/types/` | No application policy, no logging, no panics, return root cause errors only | `Row`, `Value`, `Operator`, `Store` — foundational types the project depends on |
| **Internal / Subsystem** | `internal/{LOG,FIL,...,SYS}/` | Allowed to set policy, allowed to log, wrap errors with context, majority of business logic | All 9 subsystems |
| **Internal / Platform** | `internal/platform/` (or `internal/<subsys>/platform/`) | Foundational but subsystem-specific, no logging, no panic, no policy | `SQF/LX/token.go`, `SQB/UT/coerce.go` |

### 3.2 Validation Rules for Razordata

1. **Subsystems at the same layer may NOT import each other.** Example: `SQB/EV` may not import `SQB/OP` — if EV needs Operator behavior, it uses the `Operator` interface from `internal/types/`, not concrete types from OP.

2. **No subsystem may import across more than one layer gap.** Example: `SQB` should not import `FIL` directly — it goes through `ENG`'s `Store` interface.

3. **Cluster-level imports within a subsystem follow the same rules.** Example: inside SQB, `EX` → `OP` → `EV` is fine (downhill), but `OP` → `EX` is not (uphill).

4. **Test packages within a cluster are exempt** — they can import sibling clusters for test setup.

### 3.3 Current Validation Map

| Import | Current | Valid? (per POD) | Fix |
|---|---|---|---|
| SQB/DT → SQF/PL | `Operator`, `Row` types | No — SQB (layer 8) imports SQF (layer 7) | Move types to `internal/types/` at layer 3.5 |
| SQB/EV → SQF/PL | AST types (`Expr`, `SelectStmt`) | Yes — SQF is lower | OK |
| SQF/PL → SQB/DT | `Operator` interface | Yes (technically downhill in the current chain) but fragile | Will become SQF/PL → internal/types/ after move |
| SQB/EV → SQB/AG | `EvalAggregateOver` | Yes — EV is lower than AG in the cluster DAG | Conditionally OK |
| SYS/SE → SQB/EX | `SetSessionCounterAccessor` | No — SYS should talk to SQB/EV directly | Update import |
| SYS/SY → SQB/EX | `ErrEval` re-export | No — SYS should import SQB/EV | Update import |

---

## 4. Principle 3 — Stable-Dependency Principle (Reversal)

### 4.1 The Rule

> **Depend in the direction of stability.** Stable packages (low change frequency, many dependents) should be depended ON. Volatile packages (frequent change, few dependents) should depend ON stable packages.

### 4.2 Stability Assessment of Current Clusters

| Cluster | Change Frequency | Depends On | Depended-By | Assessment |
|---|---|---|---|---|
| `internal/types/` (proposed) | Very low | Nothing stable | Everything | ✓ Ideal — maximally stable |
| `SQB/DT` (current) | Medium | SQF/PL | Everything in SQB | ✗ Problem — DT is stable but depends on volatile SQF/PL |
| `SQF/PL` | High | DT, LX, PS | SQB/EX, SQB/AD | ✗ Volatile package depended on by stable SQB |
| `SQB/EX` | Low (shrinking) | All SQB clusters | SYS | ✓ Good — stable, few dependents |
| `SQB/EV` | Low | SQF/PL, types, AG | OP, AG, AD, WT, EX | ✓ Good — stable, many dependents |
| `SQB/OP` | Low-Medium | types, EV | EX, AD | ✓ Good |
| `ENG/LS` | Medium | MEM, FIL | TXN, SQB | ✓ OK — stable interface, volatile impl |
| `ENG/ID` | High | MEM, FIL | SQB | ✗ Problem — volatile (B-tree tuning) but depended on by stable SQB |

### 4.3 Reversal Applied

The key reversal needed: **`Operator` and `Row` should not be defined in SQF or in SQB.** They are the most depended-on types in the system and they belong in the most stable package — `internal/types/` — which changes only when the fundamental data model changes.

This is analogous to **SQLite's approach**: the `sqlite3_stmt` type (prepared statement) and `sqlite3_value` type (value) are defined in the core API layer, not in the frontend or backend.

---

## 5. Principle 4 — Cluster Extraction Policy

### 5.1 The Rule

> **A cluster is a directory with a single `package <name>` declaration and zero import cycles.**
> A cluster is fully extracted when ALL of its files are in its directory, and the remaining source directory has been updated to import it.

### 5.2 Extraction Criteria

A file belongs in a cluster when:

1. **Cohesion**: All functions in the file serve the same conceptual purpose (e.g., "eval", "aggregate", "store I/O")
2. **Change velocity**: Files with different change frequencies belong in different clusters
3. **Dependency direction**: The file imports only from equal-or-lower clusters
4. **Test locality**: Tests for the file live alongside it in the same cluster

### 5.3 Remaining Extraction (iter-36/37)

| File | Current | Target | Blocked By | Priority |
|---|---|---|---|---|
| `planner.go` (~4900 LOC) | EX/ | AD/ | Operator types still in EX (direct field access `v.child`, `v.funcName`, `v.groupCols`) | **HIGH** — largest file, wrong home |
| `operators.go` (SeqScan, IndexScan) | EX/ | OP/ | None technically; accessor methods exist | HIGH |
| `intermediate.go` (Filter, Project, Sort, Limit, Offset) | EX/ | OP/ | Same as above | HIGH |
| `join.go` (NLJ) + `join_strategy.go` | EX/ | OP/ | Same | HIGH |
| `compound.go` | EX/ | OP/ | Same | MEDIUM |
| `values.go` | EX/ | OP/ | Should define `ValuesRow` as `Row` with a constructor — trivial | MEDIUM |
| `writers.go` (~2500 LOC) | EX/ | WT/ | WT directory creation, import update | HIGH |
| `source.go`, `store.go` | EX/ | WT/ | WT creation | HIGH |
| `alter_table.go`, `fk.go`, `view.go`, `constraints.go` | EX/ | WT/ | WT creation | HIGH |
| `explain.go`, `analyze.go` | EX/ | UT/ | None | MEDIUM |
| `sort_parallel.go`, `pipeline.go` | EX/ | UT/ | None | MEDIUM |
| `eval_test.go`, `eval_req772_test.go` | EX/ | EV/ | None | LOW |
| `aggregate_*_test.go`, `window_test.go` | EX/ | AG/ | None | LOW |
| `adqc*.go`, `planner*.go`, `memo.go` | EX/ | AD/ | Blocked on operator move (planner.go) | HIGH |
| `matview.go` | EX/ | WT or AD | WT creation | MEDIUM |
| `shape_specialize.go` | EX/ | AD/ (with planner) or OP/ | Depends on AG types | MEDIUM |
| `plan_node.go` | EX/ | AD/ (with planner) | Direct field access blocked | MEDIUM |
| `subq.go` | EX/ | WT/ (injectOuter + runSubqueryPlan) | WT creation | MEDIUM |

**Optimization**: The planner.go blockage can be solved by **finishing the OP move first** (moving operators, intermediate, join, compound, values out of EX), then moving the planner, then WT.

---

## 6. Principle 5 — Dumpster Prevention (Against DTs)

### 6.1 The Problem

`SQB/DT` started as `types.go` (137 LOC after iter-34) but grew to ~1100 LOC by carrying:

- **Core types**: `Row`, `Value`, `Operator`, `ExecContext` — stable, low change
- **Schema registry**: `Tables`, `Schemas`, `StoreSchemas`, `InMemSchemas`, `TableIDs`, `TablePKs`, `RegisteredIndexes` + guards `TablesMu`/`StoreMu` — medium change, complex locking
- **View/matview lifecycle**: `RegisterView`, `LookupView`, `RegisterMatView`, `LookupMatView` — medium change
- **Catalog rehydration**: `SetCatalog`, `RegisterFromCatalog`, `RestoreInMemoryTables` — medium change
- **Value utilities**: `ValueFromAny`, `Compare`, `ToInt64`, `EqualValueAny`, `IsValueTruthy` — stable, low change
- **AST helpers**: `ContainsAggregate`, `ContainsWindowFunc` — stable, low change
- **Session management**: `SessionCounterAccessor` interface, `CurrentSessionID` — stable

### 6.2 The Prevention Rule

> **When a package exceeds 300 lines of non-test, non-comment code that serves more than one purpose, split it.**

A "purpose" is defined as a set of functions that change together and are imported by a distinct set of callers.

### 6.3 DT Split Proposal

Instead of a single `DT` cluster, the responsibilities should be distributed:

| Cluster/File | Contents | Rationale |
|---|---|---|
| `internal/types/` (NEW, layer 3.5) | `Operator`, `Row`, `Value`, `ExecContext`, `Store`, `StatsCatalog`, `PlannerProvider` interfaces + `ErrNotImplemented`, `ErrNoRows`, `ErrClosed` | **Stable foundation** — changes only when data model changes. Imported by EVERYTHING above layer 3.5 |
| `internal/SQB/` schema package (new file in a shared location, or `ENG/schema/`) | `Schema` store + `TablesMu`/`StoreMu` + registration helpers + view/matview lifecycle | **Schema-related** — co-located with where schema is enforced (ENG/TB or SQB/WT). Change velocity is medium, not stable |
| Keep in `SQB/EV` | `ValueFromAny`, `Compare`, `ToInt64`, `EqualValueAny`, `IsValueTruthy` | These are **eval utilities** — they belong with EV, not in a kitchen-sink package |
| Keep in `SQF/PL` (or `internal/types/`) | `ContainsAggregate`, `ContainsWindowFunc` | These are **AST traversal helpers** — they belong with the query planner |
| Move to `SYS/AP` | `SessionCounterAccessor` interface, `CurrentSessionID` | These are **system-level concepts** — session management is a SYS concern |

**Net result**: DT is eliminated entirely. Each piece of DT moves to where it logically belongs.

---

## 7. Principle 6 — Automated Enforcement

### 7.1 The Rule

> **Architecture that isn't enforced is aspirational.**
> Add a `go vet` check or CI step that verifies the dependency graph.

### 7.2 Tools

| Tool | How It Works | For Razordata |
|---|---|---|
| `go list -deps ./...` | Lists transitive deps per package | Manual audit (existing) |
| `staticcheck` / `golangci-lint` | Has `ST` (style) rules | Already configured |
| **Custom `depcheck`** (recommended) | A Go program that reads package import paths and checks against an allowlist | See below |
| `go-arch-lint` / `go-arch-tool` | Third-party architecture linting for Go | Alternative to custom |

### 7.3 Proposed `depcheck` Architecture (Minimal)

A small Go program (`tests/depcheck/`) that enforces:

```go
// Layer definitions
var allowed = map[string][]string{
    "internal/LOG/":     {},
    "internal/FIL/":     {"internal/LOG/"},
    "internal/MEM/":     {"internal/LOG/", "internal/FIL/"},
    "internal/WAL/":     {"internal/LOG/", "internal/FIL/", "internal/MEM/"},
    "internal/ENG/":     {"internal/LOG/", "internal/FIL/", "internal/MEM/"},
    "internal/TXN/":     {"internal/LOG/", "internal/ENG/"},
    "internal/types/":   {},   // Layer 3.5 — depends on nothing
    "internal/SQF/":     {"internal/LOG/", "internal/types/"},
    "internal/SQB/":     {"internal/types/", "internal/SQF/", "internal/TXN/", "internal/ENG/", "internal/LOG/"},
    "internal/SYS/":     {"internal/SQB/", "internal/SQF/", "internal/LOG/"},
}

// Within a subsystem, cluster-level restrictions
var clusterRules = map[string][]string{
    "internal/SQB/DT": nil,    // no intra-SQB imports (or REMOVE DT entirely)
    "internal/SQB/EV": {"SQB/types", "SQB/AG"},   // EV can import AG (EvalAggregateOver)
    "internal/SQB/OP": {"SQB/EV", "SQB/types"},
    "internal/SQB/AG": {"SQB/EV"},                 // AG can import EV
    "internal/SQB/AD": {"SQB/types", "SQB/EV", "SQB/OP", "SQB/AG"},
    "internal/SQB/WT": {"SQB/types", "SQB/EV", "SQB/OP", "SQB/AG"},
    "internal/SQB/UT": {"SQB/types", "SQB/EV"},
    "internal/SQB/EX": {"SQB/AD", "SQB/OP", "SQB/WT", "SQB/UT"},  // EX imports all
}
```

Run via `go test ./tests/depcheck/` in CI. Fails on any violation.

### 7.4 Incremental Enforcement

Don't add this until the current extraction work (iter-36/37) is complete. Otherwise every move triggers false failures.

**Implementation plan**: create `tests/depcheck/` after iter-37, run in CI, break the build on violations. Teams can add exceptions by editing the allowlist (reviewed at code review).

---

## 8. Revised Subsystem Dependency Graph

### 8.1 Current (with problems)

```
LOG → FIL → MEM → WAL → ENG → TXN → SQF → SQB → SYS
                                        ↑     ↓
                                        └─────┘  (DT ⇄ PL cycle)
```

### 8.2 Proposed (strict layering)

Introduce `internal/types/` at a neutral low layer:

```
Layer 1:   LOG (LG, HK)
Layer 2:   FIL (DF, MF, LF, FS, IO)
Layer 3:   MEM (BF, PC, SP, OF)
Layer 3.5: types/  ← NEW — Operator, Row, Value, ExecContext, Store, StatsCatalog, PlannerProvider
Layer 4:   WAL (WR, FL, RP)
Layer 5:   ENG (LS, ID, TB, CT, SC, DP, NM)
Layer 6:   TXN (MV, LC, SN, VL)
Layer 7:   SQF (LX, PS, RE, PL)
Layer 8:   SQB (EX, OP, EV, AG, AD, WT, UT)
Layer 9:   SYS (SY, AP, SE, TX, ST)
```

**Strict rules**:
- A layer may import from any lower layer
- A layer may NOT import from any higher layer
- A layer may NOT skip a layer (e.g., SQB may not import FIL/DF directly — it must go through ENG)
- The neutral `types/` layer imports NOTHING from above MEM

### 8.3 Import Direction Per Subsystem

| Subsystem | May Import | May NOT Import |
|---|---|---|
| `LOG` | (none) | Everything |
| `FIL` | `LOG` | `MEM`, `WAL`, `ENG`, `TXN`, `types`, `SQF`, `SQB`, `SYS` |
| `MEM` | `LOG`, `FIL` | `WAL`, `ENG`, `TXN`, ... |
| `types/` | (none) | Everything |
| `WAL` | `LOG`, `FIL`, `MEM` | `ENG`, `TXN`, ... |
| `ENG` | `LOG`, `FIL`, `MEM`, `types` | `WAL`, `TXN`, `SQF`, `SQB`, `SYS` |
| `TXN` | `LOG`, `ENG`, `types` | `SQF`, `SQB`, `SYS` |
| `SQF` | `LOG`, `types` | `ENG`, `TXN` directly |
| `SQB` | `LOG`, `types`, `SQF` (AST types), `TXN` (Tx), `ENG` (Store) | `SYS`, `FIL` directly |
| `SYS` | Everything (it's the top layer) | N/A |

---

## 9. Revised SQB Cluster Map

### 9.1 Current Cluster DAG

```
DT ← EX, EV, AG, AD, OP, UT
        ↑
EV ← AG
OP ← AD (planner constructs OP), AG (planner constructs AG), EX
EX ← AD (planner currently in EX)
```

### 9.2 Proposed Cluster DAG (with `types/` at lower layer)

```
                   types/ (layer 3.5 — neutral)
                  /    |     \
                 ↓     ↓      ↓
    ┌──────── EV ←── AG ──→ OP ──→ WT
    │          ↑              ↑      ↑
    │          │              │      │
    └──────────┴────── AD ────┘      │
                   ↑                 │
                   │                 │
                   └─── UT ──────────┘
                   ↑
                   │
                  EX (executor factory, imports all)
```

Key changes:
- **DT is eliminated** — `types/` replaces it for core types; schema mgmt moves to ENG or SQB; value utils move to EV; AST helpers move to SQF/PL; session mgmt moves to SYS/AP
- **EV is pure foundation** — no exports to DT or EX
- **OP is pure operators** — no direct dependency on EX
- **AD is pure planning** — no dependency on EX
- **EX is the executor factory** — imports everything but nothing imports EX except SYS

### 9.3 Import Map (revised)

| Cluster | Imports From |
|---|---|
| `SQB/types` (if kept as thin alias package — REMOVED in ideal plan) | `internal/types/` |
| `SQB/EV` | `internal/types/`, `internal/SQF/PL` (AST types), `SQB/AG` (EvalAggregateOver) |
| `SQB/AG` | `internal/types/`, `SQB/EV` (EvalValue) |
| `SQB/OP` | `internal/types/`, `SQB/EV` (EvalValue for filter predicates) |
| `SQB/AD` | `internal/types/`, `SQB/EV`, `SQB/OP`, `SQB/AG`, `SQF/PL` |
| `SQB/WT` | `internal/types/`, `SQB/EV`, `SQB/OP`, `SQB/AG`, `ENG` (Store interface) |
| `SQB/UT` | `internal/types/`, `SQB/EV` |
| `SQB/EX` | All of the above |
| `internal/types/` | (nothing — not even LOG) |

---

## 10. Actionable Steps

### Phase 1: Create `internal/types/` (replace DT)

**Estimated: 1-2 days.**

1. Create `internal/types/` directory
2. Move `Operator`, `Row`, `Value` (and constructors), `ExecContext`, `Store`, `StatsCatalog`, `PlannerProvider` from `SQB/DT` → `internal/types/`
3. Move `ErrNotImplemented`, `ErrNoRows`, `ErrClosed` to `internal/types/`
4. Update all import paths in SQF, SQB, and SYS
5. Move `ContainsAggregate`, `ContainsWindowFunc` → `SQF/PL/ast_helpers.go`
6. Move `ValueFromAny`, `Compare`, `ToInt64`, `EqualValueAny`, `IsValueTruthy` → `SQB/EV/value_helpers.go`
7. Move `SessionCounterAccessor`, `CurrentSessionID` → `SYS/AP/session.go`
8. Decide ownership of schema registry (`Tables`, `Schemas`, `TablesMu`, etc.) — either `ENG/schema/` or a dedicated `SQB/schema/` package. This is a design decision: schema is used by SQB/WT for writes, SQB/AD for planning, and ENG/TB for DDL. **Recommendation**: `internal/schema/` (layer 5) — it's a cross-cutting concern.
9. **Verify**: `go build ./...`, `go test ./... -race -count=1`, ensure `SQB/DT` has zero files left (or only thin aliases if backward compat needed)

### Phase 2: Finish iter-36 (operator + AD extraction)

**Estimated: 3-5 days.**

1. Move all operator files from EX → OP: `operators.go`, `intermediate.go`, `join.go`, `join_strategy.go`, `operators_parallel.go`, `operators_vec.go`, `compound.go`, `values.go`
2. Move planner from EX → AD: `planner.go`, `plan_node.go`, `shape_specialize.go`, `memo.go`, `selectivity*.go`, `cost_*_test.go`
3. Move ADQC files from EX → AD if not already there: `adqc*.go`
4. Move `matview.go` → AD (or WT — design decision)
5. Update all imports
6. **Verify**: `go build ./...`, `go test ./... -race -count=1`

### Phase 3: Create SQB/WT and finish iter-37

**Estimated: 2-3 days.**

1. Create `internal/SQB/WT/` directory
2. Move: `writers.go`, `source.go`, `store.go`, `alter_table.go`, `fk.go`, `view.go`, `constraints.go`, `indexscan_strategy.go`, `subq.go`
3. Move remaining UT files: `explain.go`, `analyze.go`, `sort_parallel.go`, `pipeline.go`, `integrity.go`
4. Move remaining test files: `eval_test.go` → EV, `aggregate_*_test.go` → AG, etc.
5. Update all imports
6. **Verify**: `go build ./...`, `go test ./... -race -count=1`

### Phase 4: Clean up re-exports

**Estimated: 1 day.**

1. Remove all re-exports from `SQB/EX`:
   - `ErrEval`, `ErrDivByZero`, `ErrTypeMismatch`, `ErrSubquery`, `ErrTriggerAbort` — SYS should import SQB/EV directly
   - `SessionCounterAccessor` — SYS should import SYS/AP directly
   - `SetCatalog`, `RegisterFromCatalog`, `RestoreInMemoryTables` — callers should use the owning package
2. Update all SYS imports to point directly to the owning packages
3. **Verify**: `go build ./...`, `go test ./... -race -count=1`

### Phase 5: Add depcheck enforcement

**Estimated: 1 day.**

1. Create `tests/depcheck/main.go` with the allowlist defined in §7
2. Run via CI: `go test ./tests/depcheck/`
3. Add to `Makefile` or CI config
4. Document the architecture rules in `docs/development/DEPENDENCIES.md` (or `AGENTS.md`)
5. **Verify**: depcheck passes, all existing tests pass

---

## Summary

The root cause of the dependency mess is that **the most shared types in the system (`Operator`, `Row`, `Value`) were owned by one of the consuming subsystems (SQF then SQB/DT)**. This forced every consumer to import the owner, creating cycles and preventing clean extraction.

**The fix is one architectural move**: extract these types into a neutral `internal/types/` package at layer 3.5 (after MEM, before WAL). This single change:

- Eliminates the SQF ↔ SQB import cycle at the root
- Allows SQB cluster extraction to complete (no DT bottleneck)
- Enables strict dependency enforcement (one neutral foundation instead of ad-hoc ownership)
- Reduces total import confusion — every cluster imports `types/` instead of importing a kitchen-sink DT

Combined with the six principles (strict layering, package-oriented design, stable-dependency, extraction policy, dumpster prevention, automated enforcement), the result is a structure that can be reasoned about, checked automatically, and maintained by a growing team without silent architectural drift.