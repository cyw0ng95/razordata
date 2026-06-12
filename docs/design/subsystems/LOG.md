# LOG — Logging Layer

## Overview

The foundation of the entire database. Every other subsystem depends on this. Provides structured internal logging via `log/slog` with log level control, log rotation, and async event hooks.

## Dependencies

- Required: none (foundation)
- Consumed interfaces: none

## Exposed Interfaces

```go
// Logger is the main logging interface exposed to other subsystems
type Logger interface {
    Debug(msg string, args ...any)
    Info(msg string, args ...any)
    Warn(msg string, args ...any)
    Error(msg string, args ...any)
    With(args ...any) Logger
    SetLevel(level slog.Level)
    Sync() error
}

// Hook is the event hook interface
type Hook interface {
    OnLog(level slog.Level, msg string, args []any)
    Close() error
}

// HookRegistry manages registered hooks
type HookRegistry interface {
    Register(h Hook) error
    Unregister(name string) error
}
```

## Data Structures

### Logger

```go
type logger struct {
    impl     *slog.Logger
    level    atomic.Int32 // atomic log level
    dir      string
    maxSize  int64        // rotation threshold in bytes
    curSize  atomic.Int64 // current log file size
    rotation func() error // called when maxSize exceeded
}
```

- Wraps `log/slog` with an atomic `int32` level variable.
- `Info`, `Warn`, `Error`, `Debug` methods emit structured key-value pairs.
- Output: JSON for machine-readable logs, plain text for human-readable (configurable via `Options.LogFormat`).
- **Log file rotation:** when the log file exceeds `maxSize`, the current file is renamed with a timestamp suffix and a new file is opened.
- **Default output:** if no log file is configured (i.e., `dir` is empty), log output goes to standard error (`os.Stderr`) via `slog.NewTextHandler(os.Stderr, nil)`.
- **Debug-level allocation trade-off:** at `Debug` level, when `fmt.Sprintf` is used to format a message, the string is allocated on the heap. This is an accepted trade-off for debug mode — production `Debug` should be disabled via `SetLevel(slog.LevelWarn)` to avoid allocations in the hot path.

### HookRegistry

```go
type hookRegistry struct {
    hooks map[string]Hook
    ch    chan logEvent
    mu    sync.RWMutex
}

type logEvent struct {
    level  slog.Level
    msg    string
    args   []any
    ts     time.Time
}
```

- A map of named hooks registered at startup.
- Hooks receive log events asynchronously via a bounded channel (non-blocking send, drop on overflow).
- `OnLog` is called in a background goroutine — never blocks the logging path.
- **Built-in hooks:**
  - `TraceHook` (SQL query tracing with execution time)
  - `MetricHook` (throughput, latency histograms)
  - `ProfileHook` (CPU/memory dumps on error)

### LogEvent

```go
type logEvent struct {
    level  slog.Level
    msg    string
    args   []any
    ts     time.Time
}
```

- Bounded channel between logger and hook dispatcher.
- Drop on overflow — never backpressure the logging hot path.

## Function Clusters

| Cluster | Responsibility |
|---|---|
| `LG` | Logger: `slog` wrapper, level control, structured key-value output, log rotation |
| `HK` | Hook: event hooks registry, async dispatch, TraceHook, MetricHook, ProfileHook |

## Clusters

### LG — Logger

**Responsibility:** `slog` wrapper, log level control, structured key-value output, log rotation.

**Key behaviors:**
- Log level is checked before constructing the log record — zero overhead when level is disabled.
- Rotation is driven by `curSize` atomically updated after each write.
- `Sync()` flushes the underlying `slog` handler.

### HK — Hook

**Responsibility:** Event hooks registry, async dispatch, built-in hooks.

**Key behaviors:**
- Hooks are identified by name (string). Duplicate names overwrite the previous hook.
- `TraceHook` intercepts SQL query start/end events from `SQL/EX` and emits structured trace records.
- `MetricHook` collects counters (queries, rows, bytes) and histograms (latency) in a lock-free way.
- `ProfileHook` is triggered by `Error` level events; dumps CPU and heap profiles.

## Implementation Plan

1. **`internal/LOG/LG/logger.go`** — implement `Logger` with `slog` wrapper, atomic level, and rotation. Use `slog.NewJSONHandler` or `slog.NewTextHandler` based on `Options.LogFormat`.
2. **`internal/LOG/LG/rotation.go`** — implement rotation: rename current file, open new file, update `curSize`.
3. **`internal/LOG/HK/hook.go`** — implement `HookRegistry` with bounded channel dispatcher and `OnLog` dispatch loop.
4. **`internal/LOG/HK/trace.go`** — implement `TraceHook`: listen for SQL start/end events, emit structured trace.
5. **`internal/LOG/HK/metric.go`** — implement `MetricHook`: lock-free counters and histogram using `sync/atomic`.
6. **`internal/LOG/HK/profile.go`** — implement `ProfileHook`: `pprof.Lookup("heap").WriteTo` on `Error` events.
7. **Tests:** `logger_test.go` (concurrent logging, level filtering, rotation), `hook_test.go` (hook registration, event delivery, drop-on-overflow).

## Open Issues

- Should `ProfileHook` be enabled by default or opt-in?
- Should log files be compressed after rotation?
- How to expose hook metrics (e.g., drop count) via an admin endpoint?