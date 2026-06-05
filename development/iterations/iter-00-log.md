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
| R09 | `LogEvent` struct (exported): level, msg, args, ts | done |
| R10 | `OnLog` called in background goroutine — never blocks logging path | done |
| R11 | `TraceHook` stub: implements `Hook`, listens for SQL events (stub — real impl later) | done |
| R12 | `MetricHook` stub: implements `Hook`, lock-free counters/histograms (stub) | done |
| R13 | `ProfileHook` stub: implements `Hook`, triggered by Error level (stub) | done |
| R14 | `go vet ./internal/LOG/...` zero warnings | done |
| R15 | `go test ./internal/LOG/... -race -count=1` all green | done |
| R16 | Benchmark: concurrent logging throughput | done |
| R17 | `SetOutput(io.Writer)` changes destination writer dynamically | done |
| R18 | `With()` shares level with parent — `SetLevel` on either affects both | done |
| R19 | `LogEvent` is exported with `Level`, `Msg`, `Args`, `TS` fields | done |
| R20 | `HookRegistry` tracks `Dropped` and `Dispatched` statistics | done |

## Implementation

### Phase 1: Logger (`LG/logger.go`)

1. Define `Logger` interface matching design: `Debug/Info/Warn/Error/With/SetLevel/Sync`
2. Implement `logger` struct wrapping `*slog.Logger`
3. `sharedLogger` struct holds level, format, output, mu — both parent and child share same pointer
4. Level stored in `sharedLogger` and passed as `&s.level` to `slog.HandlerOptions` (pointer, not atomic)
5. Choose `slog.NewJSONHandler` or `slog.NewTextHandler` based on `Options.LogFormat`
6. Default output: `os.Stderr` via `slog.NewTextHandler(os.Stderr, nil)` when no file configured
7. `With` returns a new `logger` with extra args merged into the underlying slog logger; shares `shared` pointer so `SetLevel` on either affects both
8. `Sync()` uses interface assertion for `Handler.Sync()`, falls back to no-op

### Phase 2: Hook (`HK/hook.go`)

1. Define `Hook` interface: `OnLog(level, msg, args)`, `Close() error`
2. Define `LogEvent` struct (exported): level, msg, args, ts
3. Implement `HookRegistry` struct: hooks map, bounded channel, `sync.RWMutex`, done channel
4. `Register(h Hook)` — hooks identified by name, duplicate names overwrite previous
5. `Unregister(name string)` — remove hook by name
6. `Emit(level, msg, args)` — non-blocking send to channel; `select default` drops on overflow
7. `Stats()` returns `Registered`, `ChannelCap`, `Dropped`, `Dispatched` via `atomic.Int64` counters
8. Dispatch loop: `for event := range ch { dispatched.Add(1); for _, h := range hooks { go h.OnLog(...) } }`
9. Bounded channel size 1024 (default). Non-blocking send drops event and increments `dropped`

### Phase 3: Built-in Hooks (`HK/trace.go`, `metric.go`, `profile.go`)

1. `TraceHook`: implements `Hook`, returns immediately (real impl when SQL/EX is built)
2. `MetricHook`: implements `Hook`, returns immediately (real impl when metrics are needed)
3. `ProfileHook`: implements `Hook`, returns immediately (trigger on Error level — stub)

### Phase 4: Benchmarks

1. `logger_bench.go`: `BenchmarkConcurrentLog` — concurrent logging throughput

## Key Decisions

| Decision | Choice | Reason |
|---|---|---|
| Level field | `sharedLogger` struct pointer | Both parent and child share same pointer; `SetLevel` on either affects both |
| Hook channel buffer | 1024 | Non-blocking, recoverable overflow |
| `With` return | `Logger` interface | Caller doesn't know implementation |
| Default output | `os.Stderr` | Never lose logs, even without config |
| Hook dispatch | `go h.OnLog(...)` in loop | Never blocks logging hot path |
| `LogEvent` | Exported struct | Allows external consumers to inspect hook events |
| Stats counters | `atomic.Int64` | Lock-free reads in hot path (`Stats()`) |

## Deferred to v2

- Log file rotation on size (`LG/rotation.go` stub only)
- Log file compression after rotation
- `ProfileHook` CPU/memory dump on Error events (stub only)
- Hook metrics exposure via admin endpoint