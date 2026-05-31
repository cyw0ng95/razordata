# Razordata — Development Rules

> No network server. No external C deps. Go 1.22+. Single `go.mod`.

## Directory Structure

See `design/ARCH.md` for the full directory layout (subsystem → cluster → code).

## Build Order

See `design/ARCH.md` for the full build order (8 steps: LOG → FIL → MEM → WAL → ENG → TXN → SQL → SYS).

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