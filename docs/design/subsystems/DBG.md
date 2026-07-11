# DBG — Debug Subsystem (Build-Tag Gated)

## Design Principle: Zero Footprint Without `debug` Tag

Every DBG file in `internal/DBG/` carries `//go:build debug`.
**Without `-tags debug`**, zero DBG code is compiled — no variables, no structs,
no init functions, no goroutines.  The binary is identical byte-for-byte to a
build that never had DBG.

Downstream consumers (`LOG/HK`, `LOG/LG`) provide default no-op stubs that are
always compiled.  Subsystems call `LOG/HK.EventSink.Emit()` unconditionally;
without `debug` the stub is a zero-line function that the compiler inlines away.
With `debug`, DBG's `init()` swaps the global impl pointer to the real one.

## Build Tags

| Tag | Effect | Rationale |
|-----|--------|-----------|
| `debug` | Master tag — compiles all of `internal/DBG/`, registers real sinks, starts socket listener | One tag covers the common case. `go build -tags debug` produces a debug binary. |
| `dbg_profiling` | Enables `runtime.SetMutexProfileFraction(1)` and `SetBlockProfileRate(1)` at startup | These have ~3% permanent throughput cost even when no profile is being captured. Separate tag so `debug` alone does not incur this. |
| `dbg_fgprof` | Adds fgprof "off-CPU" profile support | Optional dependency (~500 LOC). Gated separately to avoid pulling in an extra module for most users. |

**Tag interactions:** `dbg_profiling` and `dbg_fgprof` have no effect without
`debug`.  Normal usage: `go build -tags "debug,dbg_profiling" ./cmd/razor`.

## Dependency Direction

```
LOG/LG (subsystem logger factory)  ←  swapped by DBG/DC at init (with debug tag)
LOG/HK (EventSink, MetricHook)     ←  swapped by DBG/TE + DBG/CT at init
   ↑                                        ↑
   │  (subsystems call these)        (DBG init() registers at startup)
   │
 SQB, ENG, WAL, TXN, etc.
   │
   │  (pull interfaces: socket, CLI, PRAGMA — only compiled with debug tag)
   ▼
DBG/SK (socket server)
DBG/IN (inspectors)   ← reads stats/state by calling into SYS/AP.Engine
DBG/PR (profiling)    ← calls runtime/pprof directly
```

### Stub Pattern (LOG/HK and LOG/LG)

```go
// LOG/HK/sink_stub.go — always compiled, //go:build !debug
package HK

type eventSink struct{}
func (eventSink) Emit(ctx context.Context, class string, fields map[string]any) {}

var DefaultSink EventSink = eventSink{} // subsystems call HK.DefaultSink.Emit()
```

```go
// LOG/HK/sink.go — only compiled with //go:build debug
package HK

import "github.com/cyw0ng95/razordata/internal/DBG"

func init() {
    DefaultSink = DBG.NewTraceSink()
}
```

The same pattern is used for `MetricHook`, `ProfileHook`, and the per-subsystem
log level map in `LOG/LG`.

## Critical Design Rule

**No other subsystem imports `internal/DBG/`.**  All push paths (trace events,
counters, log level checks) go through interfaces defined in `LOG/HK` or
`LOG/LG` — packages far lower in the dependency chain.  DBG registers itself
as a hook *implementation* at init time via `init()`; consumers never know DBG
exists.

Without the `debug` tag, the `LOG/HK` and `LOG/LG` stubs are compile-time
no-ops.  With the `debug` tag, `init()` swaps the globals.

## Dependencies

- **Reads from:** `LOG/LG` (subsystem logger registry), `LOG/HK` (hook
  registration), `SYS/AP` (Engine interface for Inspectors), all subsystem
  stats types (embedded in `EngineStats`)
- **Implements:** `LOG/HK.EventSink` (trace ring), `LOG/HK.MetricHook` (counter
  accumulation), `LOG/HK.ProfileHook` (extended profile dumper)
- **Writes to:** per-subsystem `atomic.Int32` level map in `LOG/LG` (log level
  overrides)
- **Not imported by:** any other subsystem — zero import edges into `internal/DBG/`
- **Build position:** after SYS (leaf — nothing depends on it)
- **Build tag:** all files in `internal/DBG/` carry `//go:build debug`

## Exposed Interfaces

**Pull interfaces only — called by external tools / the socket / CLI.**
Push interfaces (trace event emission, counter increment, log level check) are
defined in `LOG/HK` and `LOG/LG` — see those subsystem docs.  All of these
interfaces are only compiled when `//go:build debug` is satisfied.

```go
// Debugger is the top-level debug handle, accessible via Engine.Debug().
// Only compiled with //go:build debug.  Without the tag, Engine.Debug()
// returns nil.
type Debugger interface {
    Socket() string
    SetLogLevel(subsystem string, level slog.Level) error
    EnableTrace(class string, enabled bool) error
    DumpProfile(kind string, dur time.Duration) (string, error)
    Stats() DebugStats
    InspectPage(segment string, blockNum uint32) (PageDump, error)
    ActiveTxns() []TxnInfo
    Close() error
}

type DebugStats struct {
    Uptime        time.Duration
    Version       string
    BuildTags     []string
    LogLevels     map[string]slog.Level
    TraceEvents   TraceRingStats
    QueriesTotal     atomic.Int64
    QueriesSlow      atomic.Int64
    RowsReturned     atomic.Int64
    RowsWritten      atomic.Int64
    PagesRead        atomic.Int64
    PagesWritten     atomic.Int64
    WALBytesWritten  atomic.Int64
    WALFsyncs        atomic.Int64
    CompactionsTotal atomic.Int64
    CompactionsBytes atomic.Int64
    CacheHits        atomic.Int64
    CacheMisses      atomic.Int64
    TxnCommits       atomic.Int64
    TxnAborts        atomic.Int64
    LockContentionNs atomic.Int64
    QueryLatency       LatencyHist
    CommitLatency      LatencyHist
    PageReadLatency    LatencyHist
    PageWriteLatency   LatencyHist
    CompactionLatency  LatencyHist
}
```

## Function Clusters

| Cluster | Responsibility | Build Tag |
|---------|---------------|-----------|
| `TE` | Trace Events: structured ring-buffer events, per-class enable/disable | `debug` |
| `CT` | Counters: atomic counters + latency histograms, expvar export | `debug` |
| `IN` | Inspectors: page dumps, buffer pool state, active transactions | `debug` |
| `PR` | Profiling: on-demand CPU/heap/goroutine/mutex/block/trace profiles | `debug`; mutex/block requires `dbg_profiling` |
| `DC` | Dynamic Control: per-subsystem log level, trace class enable/disable | `debug` |
| `SK` | Socket: UNIX domain socket command server, protocol dispatch (`heap`, `cpu N`, `goroutine`, `debug_join`, `debug_join_flush`, `debug_join_filter`, `debug_join_summary`) | `debug` |
| `CD` | Causal Debug / correlation tracer: buffered events for cross-subsystem correlation | `debug` |
| `DI` | Debugger interface: `NewDebugger()` factory struct | `debug` |
| `JD` | JOIN Debug tracer: backs `PRAGMA debug_join_tracing`, ring buffer for join-level event capture | `debug` |

### TE — Trace Events

Ring buffer implementation (`LOG/HK.EventSink`).  Fixed-capacity circular buffer,
lock-free CAS write.  No-op stub compiled without `debug` tag.

### CT — Counters

Atomic counters and latency histograms (`LOG/HK.MetricHook`).  All counters are
`atomic.Int64` fields on a shared `DebugStats` struct.  `expvar.Publish` at init.

### IN — Inspectors

State inspection commands:
- `InspectPage(segment, blockNum)` — parse raw 4 KB page
- `BufferPool()` — snapshot page cache state
- `ActiveTxns()` — snapshot in-flight transactions
- Backs PRAGMA `buffer_pool`, `active_txns`, `dump_page`, `sst_stats`

### CD — Causal Debug

Correlation tracer (`internal/DBG/CD/{buffer,event,tracer}.go`).  Buffers events
across subsystem boundaries so a single correlation ID can be followed through
log → SQB → WAL → ENG.

### DI — Debugger

Debugger interface struct (`internal/DBG/DI/di.go`).  `NewDebugger()` factory
returns a configured `Debugger` that combines a tracer, counters, and inspectors.

### JD — JOIN Debug

Join-level tracer (`internal/DBG/JD/{buffer,event,tracer,e2e_test}.go`).  Backs
the `PRAGMA debug_join_tracing = detailed|basic|off` family and the SK socket
commands `debug_join`, `debug_join_flush`, `debug_join_filter`,
`debug_join_summary`.  Ring buffer per join group; selected events flow to the
trace ring buffer (`TE`) for unified inspection.

### PR — Profiling

On-demand profile dumps via socket/signal.  `DumpProfile(kind, dur)` dispatches
to `pprof.Lookup(kind).WriteTo` or `pprof.StartCPUProfile`.  With `dbg_profiling`
tag, sets mutex/block sampling rates at init.  With `dbg_fgprof` tag, adds
fgprof support for off-CPU stack capture.

### DC — Dynamic Control

Runtime toggling of debug knobs: per-subsystem log level, trace class
enable/disable, slow query threshold.  Writes to `atomic.Int32` map shared
with `LOG/LG`.

### SK — Socket

UNIX domain socket command server at `<DebugDir>/debug.sock`.  Single-goroutine,
line-based text protocol.  Commands: `heap`, `cpu N`, `goroutine`, `mutex`,
`block`, `trace N`, `stats`, `page <segment> <block>`, `buffer`, `txns`,
`log <level> <subsystem>`, `trace <class> on|off`, `slow <ms>`, `gc`, `help`.

## Options

No `Options.Debug` boolean — the build tag IS the switch.  Instead:

```go
type Options struct {
    // ... existing fields ...

    // DebugDir is the directory for socket file and profile dumps.
    // Only meaningful when built with -tags debug.  Default: <dbdir>/.debug/.
    DebugDir string

    // EnableDebugSocket starts a UNIX domain socket.
    // Default false.  Only meaningful with -tags debug.
    EnableDebugSocket bool

    // EnableDebugSignals registers SIGUSR1/SIGUSR2/SIGHUP handlers.
    // Default false.  Only meaningful with -tags debug.
    EnableDebugSignals bool

    // TraceEventCapacity is the ring buffer size for structured trace events.
    // Zero disables the ring.  Only meaningful with -tags debug.
    TraceEventCapacity int

    // SlowQueryThreshold logs and counts queries exceeding this duration.
    // Zero disables.  Only meaningful with -tags debug.
    SlowQueryThreshold time.Duration
}
```

Without `-tags debug`, `DebugDir`, `EnableDebugSocket`, etc. are parsed but
never acted on — DBG's `init()` never runs, `Engine.Debug()` returns nil.

## Implementation Order

1. **`LOG/HK/stub.go`** — always-compiled no-op sink, metric hook, profile hook.
   Swap globals (replaced by DBG init when `debug` tag is active).

2. **`LOG/LG/subsystem.go`** — logger factory with per-subsystem `atomic.Int32`
   level map.  No-op stub when not compiled with `debug`.

3. **`internal/DBG/TE/`** — ring buffer, `Emit` impl, per-class gating.
   All files carry `//go:build debug`.

4. **`internal/DBG/CT/`** — `DebugStats`, histogram, expvar, `MetricHook` impl.

5. **`internal/DBG/IN/`** — inspectors: page, buffer pool, txns.

6. **`internal/DBG/PR/`** — profile dispatcher, signal handlers.
   Mutex/block setup gated by `//go:build dbg_profiling`.
   fgprof gated by `//go:build dbg_fgprof`.

7. **`internal/DBG/SK/`** — UNIX socket, command dispatch, protocol.

8. **`internal/DBG/dbg.go`** — `Debugger` struct, `New`, `Close`, integration
   with `SYS/SY.Open`.

9. **PRAGMA integration** — `SQB/UT/pragma.go` gated by `//go:build debug`.

10. **CLI integration** — `cmd/razor/debug.go` gated by `//go:build debug`.

## Effect on Build

| Build command | Binary size | DBG code | Runtime behavior |
|---|---|---|---|
| `go build ./cmd/razor` | Production | Zero | No-op stubs, Engine.Debug() returns nil |
| `go build -tags debug ./cmd/razor` | +~200KB | All clusters | Full debugging, socket at <DebugDir> |
| `go build -tags "debug,dbg_profiling"` | +~200KB | +mutex/block rates | Same + profile sampling overhead |
| `go build -tags "debug,dbg_fgprof"` | +~210KB | +fgprof | Same + off-CPU profiles available |