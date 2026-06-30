# Design Doc Update List (Human Action Required)

> Generated: 2026-07-01
> Context: Review of `docs/compose/reports/dependency-structure-principles.md` (June 2026).
> AI cannot edit files under `docs/design/` per the Design Protection rule (AGENTS.md).
> Apply the diffs below verbatim; line numbers refer to current HEAD.

## Decision: no `internal/types/`

The principles report's central proposal — create `internal/types/` at "layer 3.5" as a neutral home for `Operator`/`Row`/`Value`/`ExecContext` — is **rejected**. Reasons:

1. The "SQF ↔ SQB import cycle" the report claims to break does not exist. `SQF/PL` has zero imports from `internal/SQB/**`. The report's own §1.2 admits the cycle is "conceptually" rather than structurally present.
2. `Operator` and `Row` are documented as living in `SQB/DT` (ARCH.md §Cross-Subsystem Interfaces, line 46–53). That is the design intent. DT is the legitimate home.
3. Per the Subsystem/Function Cluster rule (AGENTS.md, ARCH.md line 7), every package must be a cluster of a subsystem. There are no "neutral layer 3.5" packages. Adding `internal/types/` would require either (a) a new top-level subsystem with no place in the documented dependency chain, or (b) placing the package inside a subsystem where it has no cohesion. Neither is acceptable.
4. `ExecContext` is not a kit primitive. It carries per-statement runtime state (Outer chain, SubqueryCache, LastChanges, planner) and is not portable across subsystems.
5. The actual high-leverage work is cluster extraction (REQ001117/118/119/120/121/122/123), not type relocation.

`SQB/DT` stays. `internal/types/` does not get created.

---

## Files that need human edits

### 1. `docs/design/ARCH.md`

**Line 16** — `SQB` row in the Modules Overview table.

Current (paraphrased; line is one long row):

> `OP` — operators: Distinct, HashJoin, HashCrossJoin, PragmaResult, SqliteMaster (leaf operators continuing to move from EX: SeqScan, IndexScan, Filter, Project, Sort, Limit, Offset, NestedLoopJoin, Compound are scheduled for iter-36).
> `AD` — ADQC and cache: adqc*.go, cache_stats.go, index_usage.go; planner.go still in EX until iter-36.
> `WT` — write operators: writers.go, source.go, store.go, alter_table.go, fk.go (planned; directory does not yet exist).
> `EX` — executor factory, Executor, subq.go (injectOuter + runSubqueryPlan), plan_node.go (PlanNode tree for EXPLAIN), shape_specialize.go, matview.go.

Replace with:

> `OP` — operators. All leaf, intermediate, join, compound, parallel, and vectorized operators: `Distinct`, `HashJoin`, `HashCrossJoin`, `PragmaResult`, `SqliteMaster`, `SeqScan`, `IndexScan`, `IndexOnlyScan`, `IndexSeekScan`, `BitmapScan`, `Filter`, `Project`, `Sort`, `Limit`, `Offset`, `NestedLoopJoin`, `MergeJoin`, `CompoundOp`, `ParallelSeqScan`, `ParallelIndexScan`, `ParallelUnionAll`, `ParallelHashJoin`, `VectorizedSeqScan`, `VectorizedFilter`, `Values`, plus `indexscan_strategy.go` and `store.go` (storage key encoding, used by SeqScan/IndexScan). 19 source files. Populated in iter-36; no further file moves pending.
> `AD` — ADQC, cache, and the planner. `adqc.go`/`adqc_cache.go`/`adqc_fallback.go`/`adqc_telemetry.go` (ADQC wrappers + LRU + fallback + telemetry), `cache_stats.go`, `index_usage.go`, plus `planner.go` (~4900 lines), `plan_node.go`, `shape_specialize.go`, `memo.go` (planned for iter-36 to migrate from EX). Will not depend on EX.
> `WT` — write operators and DDL executors. Directory `internal/SQB/WT/` exists but is empty; files (`writers.go`, `source.go`, `store.go` (now in OP — see note), `alter_table.go`, `fk.go`, `view.go`, `constraints.go`, `matview.go`, `subq.go`, `indexscan_strategy.go`) remain in `SQB/EX/` pending iter-37. WT will depend on `SQB/DT`, `SQB/EV`, `SQB/OP`, and not on `SQB/EX`.
> `EX` — executor factory only. `ex.go` (`Executor.Exec`/`Query`/`QueryStream`, statement cache, session lifecycle), `subq.go` (`injectOuter` + `runSubqueryPlan`), `matview.go` (until migrated to WT or UT). Re-exports `SessionCounterAccessor`, `SetSessionCounterAccessor`, `SetCatalog`, `RegisterFromCatalog`, `RestoreInMemoryTables`, and the `ErrEval`/`ErrDivByZero`/`ErrTypeMismatch`/`ErrSubquery`/`ErrTriggerAbort` error sentinels from EV for SYS callers — these re-exports are temporary pending iter-37 cleanup. After iter-37 EX should hold only `ex.go` and a thin re-export surface, ideally eliminated by then.

> **NOTE on `store.go`**: There are two `store.go` files — one in `SQB/OP/` (storage key encoding + `encodeRow`/`decodeRow`/`extractPK`/`indexValueFor`, used by `SeqScan`/`IndexScan` and writers) and one in `SQB/DT/` (storage-engine-agnostic row codec helpers). The OP one was migrated from EX in iter-36. The DT one is the per-DT-type codec layer.

**Line 17** — `SYS` row.

Current:

> `SYS` | `SY`, `AP`, `SE`, `TX`, `ST` | ...

Replace with:

> `SYS` | `SY`, `AP`, `SE`, `TX`, `ST`, `BK`, `DS` | ... `BK` — online backup and restore (REQ000259). `DS` — `database/sql` driver implementation (Go `database/sql` Conn/Stmt/Rows/Tx interface, shared engine cache per DSN).

**Line 75** — error-sentinel list.

Current:

> `SQB/EV`: `ErrEval`, `ErrDivByZero`, `ErrTypeMismatch`, `ErrSubquery`, `ErrIgnoreRow`, `ErrTriggerAbort` (re-exported by EX for backward compatibility with SYS callers)

Add a follow-up line:

> The EX re-export of the EV error sentinels is **temporary** and will be removed once SYS callers are updated to import `SQB/EV` directly. Tracked under REQ001124.

**Line 112** — directory tree.

Current:

> └── SQB/   # SQL Backend  (EX, OP, EV, AG, AD, WT, UT)

Update to:

> └── SQB/   # SQL Backend  (EX, OP, EV, AG, AD, WT, UT, DT)

(Adding `DT` to the parenthetical — the current list omits `DT` which is a real cluster per line 16.)

**Line 113** — directory tree.

Current:

> └── SYS/   # System layer (SY, AP, SE, TX, ST)

Update to:

> └── SYS/   # System layer (SY, AP, SE, TX, ST, BK, DS)

### 2. `docs/design/subsystems/SQB.md`

**Line 16** — `SQB` cluster table row. Replace parenthetical status notes with the same content as the ARCH.md SQB row update above.

**Lines 56–58** — `OP`, `EV`, `AD`, `WT`, `UT` cluster descriptions.

In the `OP` row, the text "Operators.go (SeqScan, IndexScan), ... scheduled to move to OP in iter-36" is **stale**. OP is already fully populated. Replace with the list of operators that are now in OP (see ARCH.md update above).

In the `AD` row, "planner.go still in EX until iter-36" is still accurate (planner.go is in EX), but add the note that iter-36 is the move target.

In the `WT` row, the line **"directory `internal/SQB/WT/` does not yet exist"** is wrong — the directory exists but is empty. Update to: "directory `internal/SQB/WT/` exists but is empty; files remain in `SQB/EX/` pending iter-37."

**Lines 60–61** — `WT` and `UT` rows. Note that `store.go` and `indexscan_strategy.go` are now in `SQB/OP/`, not in EX; update the WT file list accordingly.

**Lines 286–299** — `iter-35 Outcome` section. Update with iter-36 outcome once iter-36 lands (or leave as-is until then).

**Lines 319–327** — "Residual files still in `internal/SQB/EX/`" section. Update to reflect the current state of EX (15 files: `alter_table.go`, `constraints.go`, `ex.go`, `explain.go`, `join_strategy.go`, `matview.go`, `pipeline.go`, `planner.go`, `plan_node.go`, `shape_specialize.go`, `sort_parallel.go`, `source.go`, `subq.go`, `view.go`, `writers.go`).

### 3. `docs/design/subsystems/SYS.md`

Add cluster descriptions for `BK` and `DS` to the Function Clusters table (or wherever the subsystem's cluster list lives in this file). One paragraph each:

> **`BK` — Backup/Restore** (`internal/SYS/BK/`). Online backup acquires a read lock to block writes, copies all files (engine data, WAL, catalog) to the destination directory, then releases the lock. Restore verifies the backup integrity and copies it back to a fresh directory. REQ000259.

> **`DS` — database/sql driver** (`internal/SYS/DS/`). Implements `database/sql`'s `driver.Driver`, `driver.Connector`, `Conn`, `Stmt`, `Rows`, `Tx`. DSN format: `:memory:` for in-memory, `./path/to/db.razor` for file-backed. Connections for the same DSN share a single underlying `SYS/SY` engine via an engine cache, so `database/sql`'s connection pool doesn't create a fresh engine per Conn.

### 4. Files that do NOT need changes

- `docs/design/subsystems/LOG.md`, `FIL.md`, `MEM.md`, `WAL.md`, `ENG.md`, `TXN.md`, `SQF.md` — no drift in these subsystems detected.
- `docs/design/ARCH.md` §Cross-Subsystem Interfaces (lines 25–54) — `Operator`/`Row`/`Tx`/`Iterator` definitions are still correct.
- `docs/design/ARCH.md` §Error Contract, §Shared Constants — still correct.
- `docs/design/ARCH.md` §SQL Surface — still correct.

---

## Sanity check before applying

```bash
# Verify ARCH.md line 16 still mentions "scheduled to move to OP in iter-36":
grep -n "scheduled to move to OP" docs/design/ARCH.md
# Should NOT match after the update.

# Verify WT directory exists:
ls -d internal/SQB/WT/
# Should print: internal/SQB/WT/

# Verify no internal/types/ exists in code:
find . -path ./.git -prune -o -name '*.go' -print | xargs grep -l "internal/types" 2>/dev/null
# Should print nothing.
```

---

## Related: docs that AI can edit (no human action needed)

The following documents are in `docs/development/` and `docs/compose/reports/`, which AI is allowed to edit. They are being updated in the same commit that creates this handoff list:

- `docs/compose/reports/dependency-structure-principles.md` — revised to reflect the no-`internal/types/` decision and the actual current state.
- `docs/compose/reports/design-doc-update-list.md` — this file.
- `docs/development/DEPENDENCIES.md` (new) — codifies the cluster rule and the depcheck plan in a place AI can maintain.
- `docs/development/REQUIREMENTS.md` — adds REQ001124 (EX re-export removal) and re-states the iter-36/37 cluster-fill REQs with current file lists.
