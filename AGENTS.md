# Razordata — Development Rules

> No network server. No external C deps. Go 1.26+. Single `go.mod`.

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
- **Add tests per function/method** — every public API must have test coverage.
  Error paths, edge cases (empty, large, corrupt input, missing files),
  idempotency (double-close, sync-after-close), and boundary conditions
  are as important as happy paths.
- **Commit in-time** — after each requirement is implemented and its tests pass,
  commit immediately. Do not batch multiple requirements into one commit.

## Iteration Lifecycle

Complete in a single commit (after final impl, before release tag):

1. **`docs/development/iterations/iter-XX-*.md`** — mark `done`, add "Outcome"
   (shipped, LoC, deviations, commit/tag).
2. **`docs/development/REQUIREMENTS.md`** — for each satisfied REQ:
   a. Check design relevance; flag to human if it belongs in `docs/design/`.
   b. **Delete the row from `TBD`** — no `DONE` table; history serves as record.

Do not defer doc updates — docs must match reality at tag cut.

## Bug-To-Requirement Rule

Undiscovered bugs in an iteration scope **must be encoded as a `REQ` row** in
`docs/development/REQUIREMENTS.md` (same commit that closes the iteration).

Rules: one REQ per atomic bug; use next free number (append-only);
set Priority by blast radius; set Deps to surfacing iteration + touched packages;
reference source doc in `Touches`.

The `TBD` backlog is the source of truth — bare prose mentions are not acceptable.

## CI / Linting

```bash
go vet ./...           # zero warnings
gfmt -s -l .          # no drift
golangci-lint run     # or staticcheck
go test ./... -race -count=1
```

## REQUIREMENTS.md Maintenance

**Must be kept in-time.** Per REQ:

1. Read the REQ from `docs/development/REQUIREMENTS.md`.
2. Update in-place if scope changes.
3. Delete completed REQ from TBD — no `DONE` table.
4. Include REQ ID in commit message.
5. New bugs → add as REQ rows per Bug-To-Requirement Rule.

Never leave completed REQs in TBD.

## Running SQLLogicTest

Prerequisite: `git submodule update --init --recursive --depth 1`

```bash
# All files
cd tests/sqlcmp && go test -tags slt_corpus -run TestSLT_PerFile -v ./slt/
# Single file
cd tests/sqlcmp && go test -tags slt_corpus -run 'TestSLT_PerFile/select1' -v ./slt/
# Pattern match
cd tests/sqlcmp && go test -tags slt_corpus -run 'TestSLT_PerFile/evidence' -v ./slt/
# Custom corpus root
RAZOR_SLT_ROOT=../corpus/test go test -tags slt_corpus -run TestSLT_PerFile -v ./slt/
```

Per-file pass/fail is reported. `first failure context` shows first 5 failures with diagnostics.
Use `sqlite3` to verify expected behavior when troubleshooting.

## Debugging

See `docs/development/DEBUG.md` for full debug manual (build tags, PRAGMA, socket, verbosity).

Quick: `go build -tags debug ./cmd/razor` → `PRAGMA debug_join_tracing = detailed` → query → `PRAGMA debug_join_flush`.

## Design Protection

`docs/design/` files are authoritative — human-only edits. When a design change is requested,
describe it fully and let the human apply it, or ask the human to edit directly.

## SLT Corpus Protection

`corpus/test/` is read-only. Agents may run tests against it, but must never
add, modify, or delete corpus files.

## Compatibility

- Go 1.26+ (use `slices`, `maps`, `iter`, `cmp`, `math/rand/v2`, `for range N`).
- Linux, macOS, Windows.
- Single `go.mod` — no nested modules.