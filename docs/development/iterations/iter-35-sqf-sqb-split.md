# Iteration 35: SQL Subsystem Split — SQF (Frontend) + SQB (Backend)

**Date**: 2026-06-26
**Author**: AI Agent
**Status**: pending

## Goal

Split the monolithic `internal/SQL/` subsystem into two distinct subsystems following
the same pattern as other subsystems (LOG, MEM, FIL, etc.):

- **`internal/SQF/`** — SQL Frontend. Tokenize, parse, rewrite, plan.
  - `LX/` — Lexer
  - `PS/` — Parser (AST)
  - `RE/` — Rewriter
  - `PL/` — Planner
- **`internal/SQB/`** — SQL Backend. Execute the operator tree, collect rows.
  - `EX/` — Core executor + operators (~160 files; analyze.go, adqc*.go,
    cache_stats.go remain here in this iteration)

The split is at the **directory level** (cluster reorganization). The `Operator`
interface, `Row` struct, `Value` type, `Analyze`, `AdaptiveOp`, and `CacheStats`
all stay in `SQB/EX` for this iteration. Two reasons:

1. **Import cycle**: `SQB/QC/adqc.go` (wrapping Operator) and `SQB/EX/ex.go`
   (calling NewAdaptiveOp) form a cycle. Moving QC to a separate cluster
   would require `Operator` to live in a package both can import (a shared
   `SQF/PL` would work but is a bigger refactor).
2. **`buildWriterOp` dispatch**: `SQB/EX/ex.go` calls `ST.NewAnalyze` and
   `QC.NewAdaptiveOp`. Splitting these into separate packages would either
   create cycles or require an interface registry.

**Resulting dependency direction:** SQF → SQB (frontend depends on backend for
the operator/row type contract). This is a soft split — the directory layout
follows the SQF/SQB convention, but the type-level boundary is documented for
a future iteration to tighten.

## Requirements

- REQ000952: `SQF` subsystem skeleton — new directory layout under `internal/SQF/`
  with `LX/`, `PS/`, `RE/`, `PL/` subdirectories.
- REQ000953: `SQB` subsystem skeleton — new directory layout under `internal/SQB/`
  with `EX/` subdirectory. (ST and QC deferred — see REQ000959/960.)
- REQ000954: Move `LX/` files from `internal/SQL/LX` to `internal/SQF/LX`.
- REQ000955: Move `PS/` files from `internal/SQL/PS` to `internal/SQF/PS`.
- REQ000956: Move `RE/` files from `internal/SQL/RE` to `internal/SQF/RE`.
- REQ000957: Move `PL/` files from `internal/SQL/PL` to `internal/SQF/PL`.
- REQ000958: Move `EX/` files from `internal/SQL/EX` to `internal/SQB/EX` (all
  files; analyze, adqc, cache_stats stay in EX in this iteration).
- REQ000959: DEFERRED — `SQB/ST` extraction blocked on import cycle.
- REQ000960: DEFERRED — `SQB/QC` extraction blocked on import cycle.
- REQ000962: Update all `internal/SQL/*` import paths across the codebase
  (~150 Go files in `internal/`, `cmd/`, `tests/`) to use the new `SQF/`
  and `SQB/` prefixes.
- REQ000963: Remove the empty `internal/SQL/` directory after migration.
- REQ000964: Flag for human: update `docs/design/ARCH.md` Modules Overview
  table to replace the single `SQL` row with separate `SQF` and `SQB` rows.
- REQ000965: Flag for human: split `docs/design/subsystems/SQL.md` into
  `docs/design/subsystems/SQF.md` and `docs/design/subsystems/SQB.md`.
- REQ000966: Add iter entries to `docs/development/ROADMAP.md` for iter-35.

## Status

**pending** — design docs (REQ000964, REQ000965) are human-only per AGENTS.md
Design Protection rule. Code migration is ready to proceed independently.

## Outcome

(populated after completion)

## Plan

### Phase 0 — Preparation

1. Create directory skeleton: `internal/SQF/{LX,PS,RE,PL}/`,
   `internal/SQB/{EX,ST,QC}/`.
2. Verify `go build ./internal/SQL/...` still succeeds before any moves.

### Phase 1 — Move frontend files (REQ000954-957)

For each frontend cluster, run:

```bash
git mv internal/SQL/LX/*.go internal/SQF/LX/
git mv internal/SQL/PS/*.go internal/SQF/PS/
git mv internal/SQL/RE/*.go internal/SQF/RE/
git mv internal/SQL/PL/*.go internal/SQF/PL/
```

Then `sed -i 's|internal/SQL/LX|internal/SQF/LX|g'` (and similar for PS/RE/PL)
across all `.go` files. Build + test after each cluster.

### Phase 2 — Move backend core + ST + QC (REQ000958-960)

1. Move `internal/SQL/EX/` core files (excluding `analyze.go`, `adqc*.go`,
   `cache_stats.go`) to `internal/SQB/EX/`.
2. Move `analyze.go` (and its test) to `internal/SQB/ST/`. Update package
   declaration from `EX` to `ST`. The `buildWriterOp` dispatcher in
   `SQB/EX/ex.go` must import `SQB/ST` to call `NewAnalyze`.
3. Move `adqc*.go` and `cache_stats.go` (and their tests) to
   `internal/SQB/QC/`. Update package declarations. The adaptive compilation
   wrappers reference `Operator` (stays in SQB/EX).

### Phase 3 — Cleanup (REQ000962-963)

1. Update all remaining `internal/SQL/*` import paths. Use
   `grep -rln "internal/SQL/"` to find stragglers.
2. Verify `internal/SQL/` is empty: `ls internal/SQL/`.
3. `git rm -r internal/SQL/`.
4. Run full build + test suite.

### Phase 4 — Documentation flags (REQ000964-966)

These cannot be done by the AI agent (AGENTS.md Design Protection). Flag in
the commit message and the iter doc; human will:

- Update `docs/design/ARCH.md` Modules Overview table.
- Split `docs/design/subsystems/SQL.md` into `SQF.md` and `SQB.md`.
- Add iter-35 row to `docs/development/ROADMAP.md`.

Historical iter docs (iter-28, iter-29, iter-33, iter-34) that reference the
old `internal/SQL/EX/...` paths in their file tables are records of past work
and do not need rewriting — the commit history is the source of truth.

## Soft Split Trade-off

This iteration achieves a **directory-level split** with clear cluster
boundaries. The type-level split (Operator/Row/Value in SQF/PL, strict
SQB → SQF) is deferred:

- `SQF/PL` continues to import `SQB/EX` for the `Operator` and `Row` types
  used when constructing plan trees.
- The dependency direction is **soft**: SQF → SQB, with the contract surface
  (Operator/Row/Value) owned by SQB.

A future iteration can harden this by:
1. Moving `Operator` interface and `Row`/`Value` types to `SQF/PL`.
2. Refactoring the row construction logic to live with the type.
3. Verifying the result has zero SQF → SQB imports.

Tracked as a follow-up REQ (not yet numbered) in the design notes.

## Risk Assessment

- **High blast radius**: ~150 import path changes across 120+ files. Mitigated
  by scripted `sed` and atomic commit.
- **Operator/Row stay in SQB/EX**: SQF/PL imports SQB/EX for these types. The
  soft dependency is documented but not eliminated.
- **Test file package declarations**: tests live in the same directory and use
  the same package name. Moving files preserves the package name, so no test
  code needs to change beyond imports.
- **Pre-existing TXN/MV race failure**: unrelated to this migration. Will
  not be affected by path changes.
- **SLT corpus**: the corpus is content, not Go code, so unaffected by path
  changes.

## Verification

- `go build ./...` — all packages compile
- `go test ./... -race -count=1` — 36/36 packages green (TXN/MV pre-existing
  failure noted)
- `go vet ./...` — no new warnings
- `gofmt -s -l .` — no drift in migrated files
- `grep -rln "internal/SQL/" .` returns no Go files (only historical MDs)
