# LOG — Logging Layer

## Overview

The foundation of the entire database. Every other subsystem depends on this. Provides structured internal logging via `log/slog` with log level control, log rotation, async event hooks, and **structured error types** (`Error`, `Kind`, `SQLSTATE`, `Wrap`, `Classification`) that all subsystems import directly.

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

## Error Contract

### Error (EC cluster)

```go
type Error struct {
    Kind    Kind
    Code    Code
    Message string
    Module  Module
    Layer   Layer
    SQLSTATE SQLSTATE
    Cause   error
    Fields  map[string]string
}
```

**Kind** enumerates error classifications: `KindNotFound`, `KindDuplicateKey`, `KindLocked`, `KindCorrupt`, `KindSyntax`, `KindTypeMismatch`, `KindTxAborted`, `KindIO`, `KindUpgradeRequired`, `KindReadOnly`, `KindDeadlineExceeded`, `KindConstraint`, `KindClosed`, `KindInvalidOptions`, `KindInternal`, `KindNotImplemented`, `KindConflict`, `KindResourceExhausted`, `KindParse`.

**Code** is a human-readable code like `RZR-IO-001`. `SQLSTATE` maps to standard SQLSTATE codes (e.g., `08006` for `KindLocked`, `42000` for `KindSyntax`).

**Wrap function:**
```go
func Wrap(kind Kind, err error) *Error
func WrapAt(kind Kind, module Module, layer Layer, err error) *Error
```

**Classification:**
```go
func Classify(err error) Classification
func IsKind(err error, kind Kind) bool
func IsRetryable(err error) bool
func IsFatal(err error) bool
```

All subsystems import `internal/LOG/EC` directly. Error types from low-level subsystems are wrapped by higher-level subsystems into `*EC.Error` at boundary crossings. Per-package sentinel errors (`ENG/LS.ErrNotFound`, `WAL/WR.ErrCorrupt`) remain for internal use.

### Layer (EC cluster)

```go
type Layer string

const (
    LayerSQL    Layer = "sql"
    LayerTXN    Layer = "txn"
    LayerENG    Layer = "eng"
    LayerWAL    Layer = "wal"
    LayerFIL    Layer = "fil"
    LayerMEM    Layer = "mem"
    LayerLOG    Layer = "log"
    LayerIO     Layer = "io"
    LayerConfig Layer = "config"
    LayerINT    Layer = "int" // internal errors
)
```

### Module (EC cluster)

```go
type Module string // e.g. "SQB/EV", "TXN/VL"
```

### SQLSTATE (EC cluster)

```go
type SQLSTATE string // e.g. "08006"
```

## Function Clusters

| Cluster | Responsibility |
|---|---|
| `LG` | Logger: `slog` wrapper, level control, structured key-value output, log rotation |
| `HK` | Hook: event hooks registry, async dispatch, TraceHook, MetricHook, ProfileHook |
| `EC` | Error Codes: Error/Kind/Code/SQLSTATE types, Wrap, Classify, IsKind, IsRetryable, IsFatal |

## Implementation Plan

1. **`internal/LOG/LG/logger.go`** — implement `Logger` with `slog` wrapper, atomic level, and rotation. Use `slog.NewJSONHandler` or `slog.NewTextHandler` based on `Options.LogFormat`.
2. **`internal/LOG/LG/rotation.go`** — implement rotation: rename current file, open new file, update `curSize`.
3. **`internal/LOG/HK/hook.go`** — implement `HookRegistry` with bounded channel dispatcher and `OnLog` dispatch loop.
4. **`internal/LOG/HK/trace.go`** — implement `TraceHook`: listen for SQL start/end events, emit structured trace.
5. **`internal/LOG/HK/metric.go`** — implement `MetricHook`: lock-free counters and histogram using `sync/atomic`.
6. **`internal/LOG/HK/profile.go`** — implement `ProfileHook`: `pprof.Lookup("heap").WriteTo` on `Error` events.
7. **`internal/LOG/EC/error.go`** — implement `Error` struct, `Error()`/`Unwrap()`/`Format()`, JSON marshal/unmarshal.
8. **`internal/LOG/EC/kind.go`** — implement `Kind` enum (19 types) with `String()` method and `layerForKind` mapping.
9. **`internal/LOG/EC/wrap.go`** — implement `Wrap`/`WrapAt` functions with auto-filled Code/SQLSTATE from Kind.
10. **`internal/LOG/EC/classify.go`** — implement `Classify`/`IsKind`/`IsRetryable`/`IsFatal`/`AsError`.
11. **Tests:** `logger_test.go` (concurrent logging, level filtering, rotation), `hook_test.go` (hook registration, event delivery, drop-on-overflow), `error_test.go` (wrap/unwrap chain, kind classification, SQLSTATE mapping), `classification_test.go` (retryable/fatal flags, chain traversal).

## Shipped Requirements

The following requirements have been implemented and shipped; they are now part of the design baseline.

### LG / HK — Logging Layer

| ID | Requirement | Iteration |
|---|---|---|
| REQ000001 | `Logger` wraps `log/slog` with atomic level control | shipped |
| REQ000002 | Structured key-value output (JSON/text) | shipped |
| REQ000003 | Log file rotation on size threshold | shipped |
| REQ000004 | Hook registry with async dispatch | shipped |
| REQ000005 | `TraceHook` for SQL query tracing | shipped |
| REQ000006 | `MetricHook` for throughput/latency counters | shipped |
| REQ000007 | `ProfileHook` for CPU/heap dump on error | shipped |
| REQ000008 | Bounded channel: drop on overflow, never block log path | shipped |
| REQ000009 | Log compression after rotation (gzip) | shipped |
| REQ000169 | Debug-level allocation trade-off documentation (level check before allocation) | shipped |
| REQ000193 | MetricHook counters wiring | shipped |
| REQ000194 | Implement TraceHook for SQL query tracing (start/end with timing) | shipped |
| REQ000195 | Implement ProfileHook (pprof dump on Error events) | shipped |

## Open Issues

- Should `ProfileHook` be enabled by default or opt-in?
- ~~Should log files be compressed after rotation?~~ **(Resolved — see REQ000009)**
- How to expose hook metrics (e.g., drop count) via an admin endpoint?