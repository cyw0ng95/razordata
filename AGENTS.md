# Razordata — Development Rules

> No network server. No external C deps. Go 1.22+. Single `go.mod`.

## Directory Structure

See `docs/design/ARCH.md` for the full directory layout (subsystem → cluster → code).

## Build Order

See `docs/design/ARCH.md` for the full build order (8 steps: LOG → FIL → MEM → WAL → ENG → TXN → SQL → SYS).

## Integration

Each iteration must integrate with already implemented parts. Before implementing, cross-check:
- Existing subsystem interfaces and concrete types for compatibility.
- File layouts, error types, and naming conventions for consistency.
- Any required adjustments to prior iterations (e.g., missing methods on existing types) and document them in the iteration plan's gap analysis.

## SQL Surface (MVP)

See `docs/design/ARCH.md` for the full SQL surface and API shape.

## Performance Rules

- **No allocations in hot paths** — pre-allocate buffers.
- **Sequential WAL writes** — fsync only on commit.
- **Zero-copy reads** — page cache returns pointers, callers borrow.
- **No `map[string]interface{}` in data paths** — fixed-size structs.
- **Profiler-gated** — measure with `pprof` before optimizing.
- **Benchmarks required** — every storage component in `*_test.go`.

## Concurrency

- `Engine.Write()` is the sole write path — serial.
- Reads are lock-free via MVCC (except table handle acquisition).
- `sync.Pool` for reusable page buffers.
- All public API methods must be goroutine-safe.

## Error Handling

- All errors returned as `error` — no panic in library code.
- Internal panics caught, logged, returned as wrapped errors.
- Messages: lowercase, no trailing punctuation.
- Wrap chain: I/O → structural → API.

## Logging

- `log/slog` only. No `fmt.Printf` or `log.Printf`.
- Levels: `Error`, `Warn`, `Info`, `Debug`.

## File Format

- One db = one dir named `<name>.razor/`.
- Contains: `meta.razor`, `wal.razor`, `data/`.
- Page size: 4 KB (power of 2).

## Testing

- Table-driven tests for parser and executor.
- Property-based tests for storage (crash/recovery).
- `go test ./... -race -count=1` must pass.
- No network, no external services in tests.
- **Add tests per function/method — every public API must have test coverage.
  Error paths, edge cases (empty, large, corrupt input, missing files),
  idempotency (double-close, sync-after-close), and boundary conditions
  are as important as happy paths. Strive for concrete, comprehensive coverage
  on key foundational modules before moving on.**
- **Commit in-time — after each requirement is implemented and its tests pass,
  commit immediately. Do not batch multiple requirements into one commit.
  Each commit is a stable checkpoint.**

## Iteration Lifecycle

When an iteration is complete, update the tracking docs in this order:

1. **`docs/development/iterations/iter-XX-*.md`** — mark status `done`, add an
   "Outcome" section summarizing what shipped, actual LoC, any deviations
   from the plan, and the final commit/tag.
2. **`docs/development/ROADMAP.md`** — move the iteration from "Phase 1/2" /
   "Remaining Work" into the completed `Iterations Overview` table; add a
   release tag row in `Release Tags` if a new tag was cut.
3. **`docs/development/REQUIREMENTS.md`** — for every `REQ` the iteration
   satisfied:
   a. **Check design relevance first**: if the REQ describes a design
      decision, interface contract, data structure, or architectural
      invariant that belongs in `docs/design/`, flag it to the human —
      design docs are human-only edits (see Design Protection below).
   b. **Delete the row from `TBD`** entirely. There is no `DONE` table;
      the `TBD` table is the sole working backlog. Once a REQ is
      shipped, it leaves the file — the commit history and iteration
      plan serve as the permanent record.

Do this as a single commit at the end of the iteration (after the final
implementation commit, before the release tag). Do not defer doc updates
to a later session — the docs must reflect reality at the same commit
that cuts the tag.

## Bug-To-Requirement Rule

When an iteration discovers a concrete bug that it does **not** fix in
its own scope (pre-existing latent defects, deferred items, follow-ups
called out in the "Gap Analysis" / "Deviations" section), the bug
**must be encoded as a `REQ` row and added to
`docs/development/REQUIREMENTS.md`** in the same commit that closes the
iteration. A bug that lives only in an iteration doc's narrative
section is invisible to the planning workflow and will be forgotten.

Encoding rules:

- One `REQ` per atomic bug. Do not bundle multiple distinct defects
  into a single row — they have different fix surfaces, different
  effort estimates, and different test plans.
- Use the next free `REQ` number (the file is append-only; never reuse
  a number even if a row was deleted).
- Set `Priority` based on the bug's blast radius: `critical` if it
  can lose data / corrupt state, `high` if it can crash a process,
  `medium` if it produces wrong output silently, `low` if it is
  cosmetic or only affects non-default code paths.
- Set `Deps` to the iteration that surfaced the bug (so the next
  planner can sequence the fix correctly) and any code packages the
  fix will touch.
- Reference the source doc in the `Touches` column: e.g. "see
  iter-12-catalog.md Gap Analysis Bug 1" so the bug is traceable
  back to its discovery context.
- The `TBD` row is the working-state. Once fixed, it is deleted
  entirely — there is no `DONE` table.

The "current unfixed bugs" backlog lives in the `TBD` section of
`docs/development/REQUIREMENTS.md` and is the source of truth for future
iteration planning. A bare prose mention in an iteration doc is no
longer acceptable.

## CI / Linting

```bash
go vet ./...           # zero warnings
gofmt -s -l .          # no drift
golangci-lint run      # or staticcheck
go test ./... -race -count=1
```

## Design Protection

All files under `docs/design/` are the authoritative source of truth for the database. They define the formal subsystem/function-cluster system, architecture, data structures, and implementation plans.

**Any edit to any file in `docs/design/` must be triggered by a human only.** AI agents must not generate, propose, or auto-edit content in any file under `docs/design/`. This includes:

- `docs/design/ARCH.md` — top-level architecture, subsystems, interfaces, build order
- `docs/design/subsystems/*.md` — per-subsystem detailed design documents

When the user requests a design change, the AI should describe the change in full detail and let the human apply it, or ask the human to edit the file directly.

## Compatibility

- Go 1.22+ (use `slices`, `maps`, `iter`).
- Linux, macOS, Windows.
- Single `go.mod` — no nested modules.