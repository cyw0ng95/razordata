# Razordata — Development Rules

> No network server. No external C deps. Go 1.22+. Single `go.mod`.

## Directory Structure

See `design/ARCH.md` for the full directory layout (subsystem → cluster → code).

## Build Order

See `design/ARCH.md` for the full build order (8 steps: LOG → FIL → MEM → WAL → ENG → TXN → SQL → SYS).

## Integration

Each iteration must integrate with already implemented parts. Before implementing, cross-check:
- Existing subsystem interfaces and concrete types for compatibility.
- File layouts, error types, and naming conventions for consistency.
- Any required adjustments to prior iterations (e.g., missing methods on existing types) and document them in the iteration plan's gap analysis.

## SQL Surface (MVP)

See `design/ARCH.md` for the full SQL surface and API shape.

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

1. **`development/iterations/iter-XX-*.md`** — mark status `done`, add an
   "Outcome" section summarizing what shipped, actual LoC, any deviations
   from the plan, and the final commit/tag.
2. **`development/ROADMAP.md`** — move the iteration from "Phase 1/2" /
   "Remaining Work" into the completed `Iterations Overview` table; add a
   release tag row in `Release Tags` if a new tag was cut.
3. **`development/REQUIREMENTS.md`** — for every `REQ` the iteration
   satisfied, move the row from `TBD` to `DONE` and set the `Iteration`
   column to the iter number.

Do this as a single commit at the end of the iteration (after the final
implementation commit, before the release tag). Do not defer doc updates
to a later session — the docs must reflect reality at the same commit
that cuts the tag.

## CI / Linting

```bash
go vet ./...           # zero warnings
gofmt -s -l .          # no drift
golangci-lint run      # or staticcheck
go test ./... -race -count=1
```

## Design Protection

All files under `design/` are the authoritative source of truth for the database. They define the formal subsystem/function-cluster system, architecture, data structures, and implementation plans.

**Any edit to any file in `design/` must be triggered by a human only.** AI agents must not generate, propose, or auto-edit content in any file under `design/`. This includes:

- `design/ARCH.md` — top-level architecture, subsystems, interfaces, build order
- `design/subsystems/*.md` — per-subsystem detailed design documents

When the user requests a design change, the AI should describe the change in full detail and let the human apply it, or ask the human to edit the file directly.

## Compatibility

- Go 1.22+ (use `slices`, `maps`, `iter`).
- Linux, macOS, Windows.
- Single `go.mod` — no nested modules.