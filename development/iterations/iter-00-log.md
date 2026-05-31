# Iteration 0 — LOG (Structured Logging)

**Subsystem:** `LOG`
**Status:** done
**Est. LOC:** ~1,500

## Overview

Foundation logging layer. Thin wrapper over `log/slog`. No business logic — purely OS/file primitives.

## Dependencies

- Required: none (foundation)
- Consumed interfaces: none

## Design Alignment

Directory structure matches `design/subsystems/LOG.md`:
```
internal/LOG/
├── LG/               # Logger cluster
│   ├── logger.go    # Logger interface, slog wrapper, atomic level
│   ├── logger_test.go
│   └── logger_bench.go
└── HK/               # Hook cluster
    ├── hook.go      # HookRegistry, event dispatcher
    ├── hook_test.go
    ├── trace.go     # TraceHook stub
    ├── metric.go    # MetricHook stub
    └── profile.go   # ProfileHook stub
```

## Requirements

| ID | Requirement | Status |
|---|---|---|
| R01 | `Logger` interface: `Debug/Info/Warn/Error` with atomic level check | done |
| R02 | `Sync()` flushes underlying handler | done |
| R03 | `With(args ...any) Logger` returns a child logger | done |
| R04 | `SetLevel(slog.Level)` updates level atomically | done |
| R05 | JSON or text output format configurable via `Options.LogFormat` | done |
| R06 | `Hook` interface: `OnLog(level, msg, args)`, `Close` | done |
| R07 | `HookRegistry` interface: `Register/Unregister` named hooks | done |
| R08 | Bounded async channel dispatcher for hook events (non-blocking, drop on overflow) | done |
| R09 | `logEvent` struct: level, msg, args, ts | done |
| R10 | `OnLog` called in background goroutine — never blocks logging path | done |
| R11 | `TraceHook` stub: implements `Hook`, listens for SQL events (stub — real impl later) | done |
| R12 | `MetricHook` stub: implements `Hook`, lock-free counters/histograms (stub) | done |
| R13 | `ProfileHook` stub: implements `Hook`, triggered by Error level (stub) | done |
| R14 | `go vet ./internal/LOG/...` zero warnings | done |
| R15 | `go test ./internal/LOG/... -race -count=1` all green | done |
| R16 | Benchmark: concurrent logging throughput | done |

## Implementation

### Phase 1: Logger (`LG/logger.go`)

1. Define `Logger` interface matching design: `Debug/Info/Warn/Error/With/SetLevel/Sync`
2. Implement `logger` struct wrapping `*slog.Logger`
3. `levelp *atomic.Int32` for atomic level check before every log call (zero overhead when disabled)
4. Choose `slog.NewJSONHandler` or `slog.NewTextHandler` based on `Options.LogFormat`
5. Default output: `os.Stderr` via `slog.NewTextHandler(os.Stderr, nil)` when no file configured
6. `With` returns a new `logger` with extra args merged into the underlying slog logger
7. `Sync()` uses interface assertion for `Handler.Sync()` (Go 1.24+), falls back to no-op

### Phase 2: Hook (`HK/hook.go`)

1. Define `Hook` interface: `OnLog(level, msg, args)`, `Close() error`
2. Define `logEvent` struct: level, msg, args, ts
3. Implement `hookRegistry` struct: hooks map, bounded channel (`ch chan logEvent`), `sync.RWMutex`
4. `Register(h Hook)` — hooks identified by name, duplicate names overwrite previous
5. `Unregister(name string)` — remove hook by name
6. Dispatch loop: `for event := range ch { for _, h := range hooks { go h.OnLog(...) } }`
7. Bounded channel size 1024. Non-blocking send (`select default`), drop on overflow

### Phase 3: Built-in Hooks (`HK/trace.go`, `metric.go`, `profile.go`)

1. `TraceHook`: implements `Hook`, returns immediately (real impl when SQL/EX is built)
2. `MetricHook`: implements `Hook`, returns immediately (real impl when metrics are needed)
3. `ProfileHook`: implements `Hook`, returns immediately (trigger on Error level — stub)

### Phase 4: Benchmarks

1. `logger_bench.go`: `BenchmarkConcurrentLog` — concurrent logging throughput

## Key Decisions

| Decision | Choice | Reason |
|---|---|---|
| Level field | `*atomic.Int32` pointer | Avoids copying `noCopy`; shared via pointer in `With()` |
| Hook channel buffer | 1024 | Non-blocking, recoverable overflow |
| `With` return | `Logger` interface | Caller doesn't know implementation |
| Default output | `os.Stderr` | Never lose logs, even without config |
| Hook dispatch | `go h.OnLog(...)` in loop | Never blocks logging hot path |

## Deferred to v2

- Log file rotation on size (`LG/rotation.go` stub only)
- Log file compression after rotation
- `ProfileHook` CPU/memory dump on Error events (stub only)
- Hook metrics exposure via admin endpoint