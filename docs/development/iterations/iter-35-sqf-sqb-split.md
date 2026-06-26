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
  - `PL/` — Planner (owns the `Operator` interface and `*plan` output)
- **`internal/SQB/`** — SQL Backend. Execute the operator tree, collect rows.
  - `EX/` — Core executor + operators
  - `ST/` — Statistics (ANALYZE, StatsCatalog impl)
  - `QC/` — Query Cache (adaptive compilation, plan cache)

The `Operator` interface currently lives in `SQB.EX/ex.go`; it is moved to `SQF.PL/operator.go`
so PL can construct the interface while EX implements it. This makes the dependency
direction strictly `SQB → SQF` (frontend has no knowledge of backend).

## Requirements

- REQ000952: `SQF` subsystem skeleton — new directory layout under `internal/SQF/` with
  `LX/`, `PS/`, `RE/`, `PL/` subdirectories.
- REQ000953: `SQB` subsystem skeleton — new directory layout under `internal/SQB/` with
  `EX/`, `ST/`, `QC/` subdirectories.
- REQ000954: Move `LX/` files from `internal/SQL/LX` to `internal/SQF/LX`.
- REQ000955: Move `PS/` files from `internal/SQL/PS` to `internal/SQF/PS`.
- REQ000956: Move `RE/` files from `internal/SQL/RE` to `internal/SQF/RE`.
- REQ000957: Move `PL/` files from `internal/SQL/PL` to `internal/SQF/PL`.
- REQ000958: Move `EX/` core files from `internal/SQL/EX` to `internal/SQB/EX` (excluding
  `analyze.go`, `adqc*.go`, `cache_stats.go`).
- REQ000959: Move `analyze.go` from `internal/SQL/EX` to `internal/SQB/ST` (Statistics cluster).
- REQ000960: Move `adqc.go`, `adqc_cache.go`, `adqc_fallback.go`, `adqc_telemetry.go`,
  `cache_stats.go` from `internal/SQL/EX` to `internal/SQB/QC` (Query Cache cluster).
- REQ000961: Move `Operator` interface from `internal/SQL/EX/ex.go` to
  `internal/SQF/PL/operator.go`. Update SQB.EX to import SQF.PL for the interface.
- REQ000962: Update all `internal/SQL/*` import paths across the codebase
  (~150 Go files in `internal/`, `cmd/`, `tests/`) to use the new `SQF/` and `SQB/`
  prefixes.
- REQ000963: Remove the empty `internal/SQL/` directory after migration.
- REQ000964: Flag for human: update `docs/design/ARCH.md` Modules Overview table to
  replace the single `SQL` row with separate `SQF` and `SQB` rows.
- REQ000965: Flag for human: split `docs/design/subsystems/SQL.md` into
  `docs/design/subsystems/SQF.md` and `docs/design/subsystems/SQB.md`.
- REQ000966: Add iter entries to `docs/development/ROADMAP.md` for iter-35.

## Status

**pending** — design docs (REQ000964, REQ000965) are human-only per AGENTS.md Design
Protection rule. Code migration is ready to proceed independently.

## Outcome

(populated after completion)

## Plan

### Phase 0 — Preparation (REQ000952-953, REQ000961)

1. Create directory skeleton: `internal/SQF/{LX,PS,RE,PL}/`, `internal/SQB/{EX,ST,QC}/`.
2. Create `internal/SQF/PL/operator.go` with the `Operator` interface extracted from
   `internal/SQL/EX/ex.go` (lines defining `Operator`, `Row`, `Cols`, `Types`, etc.).
3. Verify `go build ./internal/SQL/...` still succeeds (interface referenced from both
   old and new locations during migration).

### Phase 1 — Move frontend files (REQ000954-957)

For each frontend cluster, run:

```bash
git mv internal/SQL/LX/*.go internal/SQF/LX/
git mv internal/SQL/PS/*.go internal/SQF/PS/
git mv internal/SQL/RE/*.go internal/SQF/RE/
git mv internal/SQL/PL/*.go internal/SQF/PL/
```

Then `sed -i 's|internal/SQL/LX|internal/SQF/LX|g'` (and similar for PS/RE/PL) across
all `.go` files. Build + test after each cluster.

### Phase 2 — Move backend core + ST + QC (REQ000958-960)

1. Move `internal/SQL/EX/` core files (excluding `analyze.go`, `adqc*.go`, `cache_stats.go`)
   to `internal/SQB/EX/`.
2. Move `analyze.go` (and its test) to `internal/SQB/ST/`. Update package declaration
   from `EX` to `ST`. The `buildWriterOp` dispatcher in ex.go must import SQB.ST
   to call `NewAnalyze`.
3. Move `adqc*.go` and `cache_stats.go` (and their tests) to `internal/SQB/QC/`. Update
   package declarations. The adaptive compilation wrappers reference `Operator`
   (now in SQF.PL).

### Phase 3 — Cleanup (REQ000962-963)

1. Update all remaining `internal/SQL/*` import paths. Use `grep -rln "internal/SQL/"`
   to find stragglers.
2. Verify `internal/SQL/` is empty: `ls internal/SQL/`.
3. `git rm -r internal/SQL/`.
4. Run full build + test suite.

### Phase 4 — Documentation flags (REQ000964-966)

These cannot be done by the AI agent (AGENTS.md Design Protection). Flag in the
commit message and the iter doc; human will:

- Update `docs/design/ARCH.md` Modules Overview table.
- Split `docs/design/subsystems/SQL.md` into `SQF.md` and `SQB.md`.
- Add iter-35 row to `docs/development/ROADMAP.md`.

Historical iter docs (iter-28, iter-29, iter-33, iter-34) that reference the old
`internal/SQL/EX/...` paths in their file tables are records of past work and do
not need rewriting — the commit history is the source of truth.

## Risk Assessment

- **High blast radius**: ~150 import path changes across 120+ files. Mitigated by
  scripted `sed` and atomic commit.
- **Operator interface location**: changing from `SQB.EX` to `SQF.PL` reverses the
  default import direction. Pre-flight verification that no SQF code (LX/PS/RE/PL)
  imports SQB.
- **Test file package declarations**: tests live in the same directory and use the
  same package name. Moving files preserves the package name, so no test code
  needs to change beyond imports.
- **Pre-existing TXN/MV race failure**: unrelated to this migration. Will not be
  affected by path changes.
- **SLT corpus**: the corpus is content, not Go code, so unaffected by path changes.

## Verification

- `go build ./...` — all packages compile
- `go test ./... -race -count=1` — 36/36 packages green (TXN/MV pre-existing failure
  noted)
- `go vet ./...` — no new warnings
- `gofmt -s -l .` — no drift in migrated files
- `grep -rln "internal/SQL/" .` returns no Go files (only historical MDs)
