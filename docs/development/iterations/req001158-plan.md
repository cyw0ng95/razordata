# REQ001158 Execution Plan — Remove SQB/EX Re-exports

> **Status**: Partially complete. Phase 1 (consumer rewrite) and Phase 2 (delete 19 dead re-exports) shipped 2026-07-01 as commits `094c447` and `ea13758`. Phase 3 (migrate 44 internal-use re-exports) is tracked under REQ001161.
> **Scope verified against codebase** on 2026-07-01.

## 1. Actual Scope (vs. the original REQ text)

The original REQ001158 text listed 14 re-exports. The actual scope is larger:

- **99 re-exports** in `SQB/EX/ex.go` lines 27–301
- **12 of them were used in production** outside `SQB/EX/` (4 production consumer files + 1 missed: `SYS/SY/catalog_init.go`)
- **88 of them have zero production consumers** — but **44 of those 88 still have bare-name internal uses inside `SQB/EX/`** (the original plan miscounted these as "dead")

### Re-export consumer analysis (refined)

For each var re-export, "safe to delete" means: (a) no `EX.<name>` use outside `SQB/EX/`, AND (b) no bare `<name>` use inside `SQB/EX/` (i.e., all internal references use the source package prefix `DT.<name>` / `EV.<name>` / `OP.<name>` / `UT.<name>`).

| Category | Count | Action |
|---|---|---|
| Safe to delete (zero external + zero internal uses) | **19** | **Deleted in `ea13758`** |
| Has bare internal uses (need per-symbol migration) | **44** | **Tracked under REQ001161** |
| Total var re-exports evaluated | 63 | |

(The 12 type aliases `Row`, `Operator`, `ExecContext`, `ColInfo`, `TxWriter`, `InMemoryTxWriter`, `Value`, `ValueKind`, `Kind*`, `SeqScan`, etc. are type aliases, not var re-exports. They are kept as documented in `ARCH.md` §Cross-Subsystem Interfaces — zero cost at runtime, and they keep 500+ internal `Row` references working. Type aliases are a different category from var re-exports and are NOT the target of this REQ.)

### The 19 re-exports deleted in `ea13758`

All from `SQB/EX/ex.go`:

| Symbol | Source | Reason |
|---|---|---|
| `SetSessionCounterAccessor` | DT | 0 ext, 0 int |
| `ErrDivByZero` | EV | 0 ext, 0 int |
| `ErrTypeMismatch` | EV | 0 ext, 0 int |
| `ErrSubquery` | EV | 0 ext, 0 int |
| `ErrTriggerAbort` | EV | 0 ext, 0 int |
| `SetCatalog` | DT | 0 ext, 0 int |
| `RestoreInMemoryTables` | DT | 0 ext, 0 int |
| `ErrNoPKForStorage` | OP | 0 ext, 0 int |
| `ErrNoEngine` | OP | 0 ext, 0 int |
| `JoinKindLeft` | OP | 0 ext, 0 int |
| `JoinKindRight` | OP | 0 ext, 0 int |
| `JoinKindFull` | OP | 0 ext, 0 int |
| `NewValuesOp` | OP | 0 ext, 0 int |
| `NewValuesRowsOp` | OP | 0 ext, 0 int |
| `ValidateForeignKeyUpdateInMemory` | UT | 0 ext, 0 int |
| `ValidateForeignKeyDeleteInMemory` | UT | 0 ext, 0 int |
| `NewIndexScanWithBTree` | OP | 0 ext, 0 int |
| `encodeTablePrefix` | OP | 0 ext, 0 int |
| `MaintainIndexesOnDelete` | OP | 0 ext, 0 int |

### The 44 re-exports kept (tracked under REQ001161)

These all have bare-name internal uses inside `SQB/EX/`. They must be migrated symbol-by-symbol: rewrite every bare reference to use the source package prefix, then delete the re-export line. Top by use count:

| Symbol | Source | Internal uses | Migration target |
|---|---|---|---|
| `ErrNoRows` | DT | 179 | `DT.ErrNoRows` (or `OP.ErrNoRows` which is canonical) |
| `NewSeqScan` | OP | 51 | `OP.NewSeqScan` |
| `NewFilter` | OP | 35 | `OP.NewFilter` |
| `NewVectorizedSeqScan` | OP | 24 | `OP.NewVectorizedSeqScan` |
| `NewSeqScanWithStore` | OP | 13 | `OP.NewSeqScanWithStore` |
| `EncodeRow` | OP | 11 | `OP.EncodeRow` |
| `NewProject` | OP | 10 | `OP.NewProject` |
| `NewSort` | OP | 9 | `OP.NewSort` |
| `NewParallelIndexRangeScan` | OP | 9 | `OP.NewParallelIndexRangeScan` |
| `NewIndexScanWithRange` | OP | 8 | `OP.NewIndexScanWithRange` |
| `NewVectorizedFilter` | OP | 8 | `OP.NewVectorizedFilter` |
| `NewLimit` | OP | 7 | `OP.NewLimit` |
| `tablePrefix` | OP | 7 | `OP.TablePrefix` (canonical) |
| `NewParallelSeqScan` | OP | 6 | `OP.NewParallelSeqScan` |
| `NewIndexScan` | OP | 6 | `OP.NewIndexScan` |
| `NewOffset` | OP | 5 | `OP.NewOffset` |
| `NewIndexScanWithIndex` | OP | 5 | `OP.NewIndexScanWithIndex` |
| `NewNestedLoopJoin` | OP | 4 | `OP.NewNestedLoopJoin` |
| `NewParallelSeqScanRow` | OP | 4 | `OP.NewParallelSeqScanRow` |
| `JoinKindInner` | OP | 4 | `OP.JoinKindInner` |
| `decodeRow` | OP | 4 | `OP.DecodeRow` (canonical) |
| `NewParallelUnionAll` | OP | 3 | `OP.NewParallelUnionAll` |
| `NewCompoundOp` | OP | 3 | `OP.NewCompoundOp` |
| `NewIndexScanWithStore` | OP | 3 | `OP.NewIndexScanWithStore` |
| `ErrTableNotRegisteredForStorage` | OP | 3 | `OP.ErrTableNotRegisteredForStorage` |
| `RowKey` | OP | 8 (corrected) | `OP.RowKey` |
| `newValuesOp` | OP | 2 | `OP.NewValuesOp` (keep canonical capitalization) |
| `newValuesRowsOp` | OP | 1 | `OP.NewValuesRowsOp` |
| `NewParallelIndexScan` | OP | 2 | `OP.NewParallelIndexScan` |
| `JoinKindCross` | OP | 2 | `OP.JoinKindCross` |
| `ExtractPK` | OP | 2 | `OP.ExtractPK` |
| `ExtractPKForUpdate` | OP | 2 | `OP.ExtractPKForUpdate` |
| `buildIndexKey` | OP | 2 | `OP.BuildIndexKey` |
| `NewIntegrityCheck` | UT | 2 | `UT.NewIntegrityCheck` |
| `NewAnalyze` | UT | 2 | `UT.NewAnalyze` |
| `NewVacuumWithStore` | UT | 2 | `UT.NewVacuumWithStore` |
| `ValidateForeignKeyDeleteInMemory` | UT | 2 | `UT.ValidateForeignKeyDeleteInMemory` |
| `newValuesRowsOp` (line 266) | OP | 1 | `OP.NewValuesRowsOp` |
| `NewIntegrityCheckWithStore` | UT | 1 | `UT.NewIntegrityCheckWithStore` |
| `NewAnalyzeWithStore` | UT | 1 | `UT.NewAnalyzeWithStore` |
| `NewVacuum` | UT | 1 | `UT.NewVacuum` |
| `SchemaFromRowSchema` | OP | 1 | `OP.SchemaFromRowSchema` |
| `RegisterFromCatalog` | DT | 2 | `DT.RegisterFromCatalog` |
| `ErrEval` | EV | 2 | `EV.ErrEval` |
| `MaintainIndexesOnInsert` | OP | 1 | `OP.MaintainIndexesOnInsert` |
| `MaintainIndexesOnUpdate` | OP | 1 | `OP.MaintainIndexesOnUpdate` |

## 2. Phases 1 and 2 — shipped

### Phase 1: Consumer rewrite (commit `094c447`)

Updated 4 production consumer files to import the owning packages directly:
- `internal/SYS/SE/se.go` — added `DT`, `EV`, `OP` imports; routed 14 EX.* references
- `internal/SYS/ST/st.go` — dropped EX import entirely; added DT import
- `internal/SYS/TX/tx.go` — dropped EX import entirely; added DT import
- `cmd/razor/main.go` — added DT import (kept EX for `EX.Executor`)

### Phase 2: Delete dead re-exports (commit `ea13758`)

Deleted 19 var re-exports from `SQB/EX/ex.go` (listed above).

Also fixed a missed consumer: `internal/SYS/SY/catalog_init.go` was using `executor.SetCatalog` and `executor.RegisterFromCatalog` via an aliased EX import. Updated to `DT.SetCatalog` and `DT.RegisterFromCatalog` and dropped the EX import.

## 3. Phase 3 — Migration of 44 internal-use re-exports (REQ001161)

This is a separate, larger iteration. The recommended approach:

1. Order symbols by use count descending (ErrNoRows first at 179, then NewSeqScan at 51, etc.).
2. For each symbol, run a per-symbol find/replace that adds the source prefix to every bare use inside `SQB/EX/`.
3. Verify `go build ./...` and `go test ./... -race -count=1` after each symbol.
4. Delete the re-export line in `ex.go`.
5. Commit per symbol or per small batch (5-10 symbols per commit).

Total internal sites to rewrite: ~470 across the 44 symbols. Most can be done with a single `sed` per file; some need manual review for ambiguous contexts (e.g., `newValuesOp` vs `NewValuesOp` capitalization).

## 4. Verification (post Phase 2)

- `go build ./...` — passes
- `go test ./... -count=1` — no new failures. Pre-existing failures unchanged:
  - `TestPageCache_Eviction` in `internal/ENG/LS` (pre-existing on `a0a8c46`)
  - `TestDual_AllSeededCases` in `tests/sqlcmp/dual` (documented in REQ001125-001129)
- `go vet` — no new warnings
- `gofmt` — no drift in changed files

## 5. Lessons learned

1. **The "production consumer" count is a necessary but not sufficient criterion for safe deletion.** A re-export can have zero external callers but heavy internal callers. The Python script's first pass used a buggy negative-lookbehind regex that miscounted bare uses; the corrected count (179 for ErrNoRows, 51 for NewSeqScan, etc.) revealed that 44 of the 88 "dead" re-exports are actually live.
2. **Aliased imports (`executor "github.com/.../SQB/EX"`) hide consumer patterns.** `executor.SetCatalog` doesn't show up in `EX.SetCatalog` greps. A two-pass check is needed: grep for `EX.<symbol>` AND for the aliased-prefix form.
3. **The original REQ scope estimate was off by 4x** (14 vs 99 re-exports, vs 12 actually used). Future REQ scope-estimate work should grep the actual code rather than infer from the report text.
