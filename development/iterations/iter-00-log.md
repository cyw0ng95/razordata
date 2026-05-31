# Iteration 0 — LOG (Structured Logging)

**Subsystem:** `LOG`
**Status:** pending
**Est. LOC:** ~1,500

## Overview

Foundation logging layer. Thin wrapper over `log/slog`. No business logic — purely OS/file primitives.

## Requirements

| ID | Requirement | Status |
|---|---|---|
| R01 | `Logger` interface: `Debug/Info/Warn/Error` with atomic level check | pending |
| R02 | `Sync()` flushes underlying handler | pending |
| R03 | `With(args ...any) Logger` returns a child logger | pending |
| R04 | `SetLevel(slog.Level)` updates level atomically | pending |
| R05 | JSON or text output format configurable via `Options.LogFormat` | pending |
| R06 | `HookRegistry` interface: `Register/Unregister` named hooks | pending |
| R07 | Bounded async channel dispatcher for hook events (non-blocking, drop on overflow) | pending |
| R08 | `OnLog(level, msg, args)` called in background goroutine | pending |
| R09 | `TraceHook` stub (SQL events come later) | pending |
| R10 | `MetricHook` stub (counter + histogram) | pending |
| R11 | `ProfileHook` stub (trigger on Error level) | pending |
| R12 | `go vet ./internal/LOG/...` zero warnings | pending |
| R13 | `go test ./internal/LOG/... -race -count=1` all green | pending |
| R14 | Benchmark: concurrent logging throughput | pending |

## Implementation

```
internal/LOG/
├── logger.go      # Logger interface, slog wrapper, atomic level
├── options.go     # Options struct, LogFormat
├── handler.go     # slog handler (JSON/text)
├── hook.go        # HookRegistry, event dispatcher
└── hook_builtin.go # TraceHook, MetricHook, ProfileHook stubs
```

## Deferred

- Log file rotation on size
- Log file compression after rotation
- `ProfileHook` CPU/memory dump on Error events