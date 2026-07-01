# Dependency Policy

> Codifies the subsystem/cluster layering rules for Razordata.
> Companion to `docs/design/ARCH.md` (human-edited, authoritative) and
> `docs/compose/reports/dependency-structure-principles.md` (review report).
>
> This file is **AI-maintainable** (lives under `docs/development/`, not `docs/design/`).
> When the policy changes, update this file and add a REQ row to `REQUIREMENTS.md`.
> When a new subsystem or cluster is added, the design doc must be updated first
> by a human (Design Protection rule); this file follows.

## 1. Subsystem and Cluster Rules

### 1.1 The rule

> Every Go package in `internal/` is a **cluster** of exactly one **subsystem**.
> Every cluster has a name that is one or two uppercase letters
> (e.g., `LG`, `HK`, `PL`, `EX`).
> Subsystems are named after the dependency layer they sit at
> (e.g., `LOG`, `FIL`, `MEM`, `WAL`, `ENG`, `TXN`, `SQF`, `SQB`, `SYS`).

The full cluster table is in `docs/design/ARCH.md` §Modules Overview. AI agents must
not invent new clusters; the design doc is the source of truth.

### 1.2 What this forbids

- A package under `internal/` whose directory name does not match a documented cluster.
  Example: `internal/types/`, `internal/common/`, `internal/util/`, `internal/helpers/`
  are all forbidden — they would be unowned packages outside the Subsystem/Function
  Cluster taxonomy.
- A cluster that is not declared in `docs/design/ARCH.md`. Adding a cluster requires
  a human design-doc edit.
- A subsystem that does not appear in the dependency chain
  `LOG → FIL → MEM → WAL → ENG → TXN → SQF → SQB → SYS`.

### 1.3 What this requires

- Every file under `internal/SUBSYSTEM/CLUSTER/` must `package CLUSTER`.
- Every cluster must be importable as
  `github.com/cyw0ng95/razordata/internal/SUBSYSTEM/CLUSTER`.
- A cluster may import its own subsystem's other clusters and any lower-layer subsystem's
  clusters. It must not import a same-layer cluster (except for test files within the
  same subsystem, which may freely import sibling test packages).

## 2. Subsystem Dependency Order

The strict dependency order (top to bottom):

```
SYS (layer 9)  — public API, lifecycle
SQB (layer 8)  — SQL backend: parse, plan, execute
SQF (layer 7)  — SQL frontend: lex, parse, rewriter
TXN (layer 6)  — transactions: MVCC, OCC, slots
ENG (layer 5)  — storage: LSM, B-tree, catalog, types
WAL (layer 4)  — write-ahead log
MEM (layer 3)  — memory: buffer pool, sync.Pool
FIL (layer 2)  — file I/O: block, mmap, io_uring
LOG (layer 1)  — logging, hooks
```

A package at layer N may import from any layer M ≤ N. It must not import from any
layer M > N. It must not skip a layer without going through the layer's exposed
interface (e.g., SQB does not import `FIL/DF` directly; it goes through `ENG`).

## 3. Intra-Subsystem Cluster Rules

### 3.1 Default rule

Within a subsystem, clusters form a DAG. A cluster may import from any cluster at
the same layer or below. Test files (`*_test.go`) are exempt and may import
sibling clusters for setup.

### 3.2 SQB cluster DAG (current, as of 2026-07-01)

```
DT ← EX, EV, AG, AD, OP, UT (terminal)
    ↑
EV ← AG (aggregate needs EvalValue)
OP ← AD (planner constructs OP operators), AG (planner constructs AG operators)
EX ← AD (planner currently in EX, scheduled to move to AD as part of SQB finalization)
```

The current state of EX is the subject of REQ001117/118/119/120/121/122/123
(populating OP, EV, AG, AD, WT, UT clusters). After REQ001123 ships, EX
should hold only `ex.go` and the ExecContext glue.

### 3.3 SQB cluster dependencies (allowlist, post-SQB-finalization)

| Cluster | May import (production) |
|---|---|
| `SQB/DT` | `SQF/PL` (type aliases), `SYS/AP` (Value constructors), `LOG/LG` (logging) |
| `SQB/EV` | `SQB/DT`, `SQF/PL` (AST types), `SQB/AG` (EvalAggregateOver) |
| `SQB/AG` | `SQB/DT`, `SQB/EV` (EvalValue) |
| `SQB/OP` | `SQB/DT`, `SQB/EV` (filter predicates), `ENG/LS` (Iterator), `ENG/ID` (B-tree) |
| `SQB/AD` | `SQB/DT`, `SQB/EV`, `SQB/OP`, `SQB/AG`, `SQF/PL` (AST), `LOG/LG` |
| `SQB/WT` | `SQB/DT`, `SQB/EV`, `SQB/OP`, `SQB/AG`, `ENG/LS`, `LOG/LG` |
| `SQB/UT` | `SQB/DT`, `SQB/EV`, `LOG/LG` |
| `SQB/EX` | All of the above (executor factory) |

`SQB/EX` may be imported by `SYS/*` only. No other SQB cluster may import `SQB/EX`
in production code (test files are exempt).

### 3.4 SYS cluster dependencies

| Cluster | May import |
|---|---|
| `SYS/SY` | All subsystems (init and shutdown) |
| `SYS/AP` | All subsystems except `SYS/SY` (public API) |
| `SYS/SE` | `SQB/DT`, `SQB/EV`, `LOG/LG` (session lifecycle) |
| `SYS/TX` | `TXN/VL`, `SQB/DT`, `LOG/LG` (transaction context) |
| `SYS/ST` | `SQF/LX`, `SQF/PS`, `SQB/DT`, `LOG/LG` (statement preparation) |
| `SYS/BK` | `ENG/LS`, `WAL/WR`, `LOG/LG` (backup/restore) |
| `SYS/DS` | `SYS/SY`, `SYS/AP` (database/sql driver) |

## 4. Re-export Policy

### 4.1 The rule

> A cluster must not re-export symbols from another cluster via `var X = other.X`
> or `type X = other.X` for the purpose of backward compatibility.
> Callers should import the owning cluster directly.

### 4.2 Currently-violating re-exports (REQ001158)

`SQB/EX/ex.go` re-exports:
- `ErrEval`, `ErrDivByZero`, `ErrTypeMismatch`, `ErrSubquery`, `ErrTriggerAbort` from `SQB/EV`
- `SessionCounterAccessor`, `SetSessionCounterAccessor`, `GetCurrentSessionID`, `SetCatalog`, `RegisterFromCatalog`, `RestoreInMemoryTables` from `SQB/DT`
- `ErrTableNotRegisteredForStorage`, `ErrNoPKForStorage`, `ErrNoEngine` from `SQB/OP`

These must be removed and callers updated to import the owning packages directly.
Tracked under REQ001158.

### 4.3 Type aliases are allowed

`type Row = pl.Row` (a Go type alias, not a re-export) is allowed. It does not
re-define the type; it is the same type with two names. The intent of an alias
is to make a type importable from a package other than its defining package
without creating an ownership fork. Use type aliases for this; use direct
re-exports (`var X = other.X`) sparingly and only for backward compat windows
that have a planned end date.

## 5. New Package Proposal Checklist

Before creating a new package under `internal/`:

1. **Is it a cluster of an existing subsystem?** If yes, name the package after a
   cluster name from `docs/design/ARCH.md` §Modules Overview. If the cluster is
   not in the table, **stop and ask the human to update the design doc first**.
2. **Is it a new subsystem?** This requires a human design-doc edit to add the
   subsystem row, define its layer, and update the dependency chain. Do not do
   this without explicit human approval.
3. **Is it a "neutral foundation" / "shared types" / "common" / "util" package?**
   These are not allowed. Place types in the cluster that owns them, or in the
   cluster where they were historically defined (e.g., `SQB/DT` for the SQB
   shared types). If you find yourself wanting a neutral package, you are
   probably looking for a refactor inside an existing cluster, not a new package.
4. **Could the new code live in an existing cluster?** If yes, do that instead.

## 6. Depcheck Tooling

Tracked under REQ001159. A `tests/depcheck/` Go program (or `_test.go`) will
walk the import graph and assert the allowlists in §3.3 and §3.4. It will
land as a warning during the SQB cluster fills and promote to failure after REQ001123
(SQB finalization) ships.

## 7. Updating This Document

When the design doc (`docs/design/ARCH.md` or `docs/design/subsystems/*.md`)
changes, this file must be updated to match. The flow is:

1. Human updates `docs/design/`.
2. AI updates `docs/development/DEPENDENCIES.md` (§3.3, §3.4 allowlists) to
   match the new cluster layout.
3. AI updates `tests/depcheck/` allowlist to match.
4. AI runs depcheck, verifies the codebase still conforms.
5. AI commits the doc and depcheck changes together.

If you cannot do step 1 because the design doc change requires human approval,
add a REQ row tracking the change instead. Do not silently change the policy.
