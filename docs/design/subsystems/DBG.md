# DBG — Debug Subsystem

## Overview

Observability and debugging layer consumed by developers and operators. Provides structured trace events (ftrace-alike), atomic counters and latency histograms (perf_events-alike), state inspectors for pages/buffer/transactions (pg_buffercache/pageinspect-alike), on-demand profiling via Go's runtime/pprof, per-subsystem dynamic log control (dynamic debug-alike), and a UNIX domain socket command server (proc/sys-alike).

**Critical design rule: No other subsystem imports `internal/DBG/`.** All push paths (trace events, counters, log level checks) go through interfaces defined in `LOG/HK` or `LOG/LG` — packages far lower in the dependency chain. DBG registers itself as a hook *implementation* at engine startup; consumers never know DBG exists.

**All defaults are disabled.** Every DBG feature is gated by a `bool` or `0`-valued option. Without explicit opt-in, the engine runs identically to a build without DBG compiled in — no extra goroutines, no atomic overhead, no allocations.

## Dependency Direction

```
LOG/LG (subsystem logger factory)  ←  DBG/DC provides override map
LOG/HK (EventSink, MetricHook)     ←  DBG/TE + DBG/CT implement these hooks
   ↑                                        ↑
   │  (subsystems call these)        (DBG registers at startup)
   │
 SQB, ENG, WAL, TXN, etc.
   │
   │  (pull interfaces: socket, CLI, PRAGMA)
   ▼
DBG/SK (socket server)
DBG/IN (inspectors)   ← reads stats/state by calling *into* SYS/AP.Engine
DBG/PR (profiling)    ← calls runtime/pprof directly, no subsystem involvement
```

- **Push path** (trace event emission, counter observation): subsystems call `LOG/HK.EventSink.Emit()` / `LOG/HK.MetricHook.Observe()`. DBG registers its implementation of these interfaces during `SYS/SY.Open`. If DBG is disabled, the hooks are no-ops.
- **Pull path** (socket, CLI, PRAGMA): external tools or the `razor debug` CLI talk to the UNIX socket. The socket dispatches to DBG/SK, which reads subsystem state via the public `SYS/AP.Engine` interface. PRAGMA handlers in `SQB/UT` also call `Engine.Debug()` — but PRAGMAs are user-initiated, not subsystem-initiated.
- **Log control path**: subsystems create their logger via `LOG/LG.NewSubsystemLogger(name, defaultLevel)` at init time. DBG/DC only writes to the atomic level map that LOG/LG reads. No subsystem imports `internal/DBG/DC`.

## Dependencies

- **Reads from:** `LOG/LG` (subsystem logger registry), `LOG/HK` (hook registration), `SYS/AP` (Engine interface for Inspectors), all subsystem stats types (embedded in `EngineStats`)
- **Implements:** `LOG/HK.EventSink` (trace ring), `LOG/HK.MetricHook` (counter accumulation), `LOG/HK.ProfileHook` (extended profile dumper)
- **Writes to:** per-subsystem `atomic.Int32` level map in `LOG/LG` (log level overrides)
- **Not imported by:** any other subsystem — zero import edges into `internal/DBG/`
- **Build position:** after SYS (leaf — nothing depends on it)

## Exposed Interfaces

**Pull interfaces only — called by external tools / the socket / CLI.**
Push interfaces (trace event emission, counter increment, log level check) are defined in `LOG/HK` and `LOG/LG` — see those subsystem docs.

```go
// Debugger is the top-level debug handle, accessible via Engine.Debug().
// Only used by the socket server, the `razor debug` CLI, and integration tests.
// No database subsystem calls Debugger directly.
type Debugger interface {
    // Socket returns the UNIX socket path (empty if disabled).
    Socket() string

    // SetLogLevel changes the log level for a subsystem at runtime.
    // subsystem is one of "LOG", "FIL", "MEM", "WAL", "ENG", "TXN",
    // "SQF", "SQB", "SYS", "DBG", or "ALL".
    SetLogLevel(subsystem string, level slog.Level) error

    // EnableTrace enables/disables a trace event class by name.
    EnableTrace(class string, enabled bool) error

    // DumpProfile triggers an on-demand profile write.
    // kind: "cpu", "heap", "goroutine", "mutex", "block", "trace".
    // dur: duration (used for cpu and trace profiles; zero = default).
    // Returns the file path written.
    DumpProfile(kind string, dur time.Duration) (string, error)

    // Stats returns a snapshot of all debug counters and latencies.
    Stats() DebugStats

    // InspectPage returns a parsed dump of a raw page.
    // segment: "data" for data SST, "index" for btree.razor.
    InspectPage(segment string, blockNum uint32) (PageDump, error)

    // ActiveTxns returns a snapshot of in-flight transactions.
    ActiveTxns() []TxnInfo

    // Close cleans up the socket, profile goroutines, and signal handlers.
    Close() error
}

// DebugStats aggregates every counter and histogram that DBG tracks.
type DebugStats struct {
    Uptime        time.Duration
    Version       string
    BuildTags     []string

    // Per-subsystem log levels (active at this moment)
    LogLevels     map[string]slog.Level

    // Trace events — ring buffer state
    TraceEvents    TraceRingStats

    // Counters (cumulative)
    QueriesTotal     atomic.Int64
    QueriesSlow      atomic.Int64      // queries exceeding SlowQueryThreshold
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
    LockContentionNs atomic.Int64       // cumulative ns spent waiting

    // Latency histograms (µs buckets)
    QueryLatency       LatencyHist     // per-query execution
    CommitLatency      LatencyHist     // per-commit fsync wall
    PageReadLatency    LatencyHist
    PageWriteLatency   LatencyHist
    CompactionLatency  LatencyHist
}

// PageDump is the result of InspectPage.
type PageDump struct {
    Segment     string
    BlockNum    uint32
    PageSize    int
    Checksum    uint32
    ChecksumOK  bool
    BlockType   string                // "data", "index", "overflow", "meta"
    ItemCount   int
    FreeSpace   int
    Items       []ItemDump
    RawHex      string                // first 128 bytes as hex
}

// ItemDump is a single entry within a page dump.
type ItemDump struct {
    Offset  int
    Key     string                    // hex-encoded
    KeyLen  int
    ValueLen int
    Tombstone bool
}

// TxnInfo is a snapshot of one in-flight transaction.
type TxnInfo struct {
    ID        uint64
    Age       time.Duration
    State     string                  // "active", "prepared", "committing"
    ReadLSN   uint64
    Queries   int
    GoroutineID uint64
}

// TraceEvent is a single structured trace event.
type TraceEvent struct {
    Class     string                  // "query", "txn", "wal_checkpoint", "compaction", ...
    Severity  slog.Level
    Timestamp time.Time
    Duration  time.Duration           // zero for point events
    Fields    map[string]any
}

// TraceRingStats describes the ring buffer state.
type TraceRingStats struct {
    Capacity    int
    Used        int
    Dropped     int                   // events dropped due to full ring
    Enabled     map[string]bool       // per-class enable/disable
}

// LatencyHist is a fixed-bucket latency histogram (µs).
type LatencyHist struct {
    Count       int64
    Sum         int64                 // total µs
    Buckets     [16]int64             // log2-spaced from 1µs to ~65s
}
```

## Data Structures

### Debugger

```go
// Only constructed when Options.Debug is true.
// When false, the debugger field in engine is nil.
type debugger struct {
    // subsystems (read-only references, obtained at construction)
    sys *SYS.Engine

    // socket (nil when EnableDebugSocket is false)
    sock     net.Listener
    sockPath string

    // profile control
    profileDir string                 // DebugDir
    cpuMu      sync.Mutex             // one CPU profile at a time

    // trace ring buffer (nil when TraceEventCapacity == 0)
    traceRing     *traceRingBuf
    traceEnabled  map[string]bool

    // dynamic log control
    logLevels   map[string]*atomic.Int32  // per-subsystem slog.Level

    // counters (atomic primitives, not the full struct — they live in SYS/AP)
    counters    *DebugStats

    // lifecycle
    wg      sync.WaitGroup
    closed  atomic.Bool
}
```

- **Construction guard:** `SYS/SY.Open` checks `Options.Debug`. When false — no `debugger` struct, no goroutines, no hook registrations. The `engine.debug` field stays nil. When true, calls `DBG.New(engine, opts)` after all other subsystems are initialized.
- `New(engine, opts)`: allocates the `debugger` struct, reads `Options.DebugDir`, conditionally allocates trace ring (only when `TraceEventCapacity > 0`), conditionally starts socket listener (only when `EnableDebugSocket`), registers `EventSink`/`MetricHook`/`ProfileHook` implementations with `LOG/HK`, registers the per-subsystem level map with `LOG/LG`, conditionally installs signal handlers.
- `Close`: stop socket listener, stop signal handlers, drain goroutines, flush remaining trace events to log.

### Options (additions to `SYS/AP.Options`)

All fields default to disabled/zero. Without explicit opt-in, DBG is never constructed.

```go
type Options struct {
    // ... existing fields ...

    // Debug is the master switch. When false (default), DBG is not
    // constructed — no goroutines, no socket, no hook registrations.
    // Individual sub-flags (EnableDebugSocket, TraceEventCapacity, etc.)
    // are AND-gated: they have no effect unless Debug is true.
    Debug                bool

    // DebugDir is the directory for socket file and profile dumps.
    // Default: <dbdir>/.debug/ (only used when Debug=true).
    DebugDir             string

    // EnableDebugSocket starts a UNIX domain socket at <DebugDir>/debug.sock.
    // Requires Debug=true. Default false.
    EnableDebugSocket    bool

    // EnableDebugSignals registers SIGUSR1/SIGUSR2/SIGHUP handlers for
    // on-demand profile dumps and stats snapshots.
    // Requires Debug=true. Default false.
    EnableDebugSignals   bool

    // TraceEventCapacity is the ring buffer size for structured trace events.
    // Zero (default) disables the ring — no trace events are captured.
    // Requires Debug=true.
    TraceEventCapacity   int

    // SlowQueryThreshold logs and counts queries exceeding this duration.
    // Zero (default) disables slow query tracking.
    // Requires Debug=true. Recommend 100ms.
    SlowQueryThreshold   time.Duration

    // EnableMutexProfiling enables runtime.SetMutexProfileFraction(1) and
    // runtime.SetBlockProfileRate(1) at startup.
    // Has ~3% throughput cost. Default false.
    // Requires Debug=true.
    EnableMutexProfiling bool
}
```

**Option defaults at a glance:**

| Option | Default | Effect when non-default |
|---|---|---|
| `Debug` | `false` | Master switch — when false, nothing below is checked |
| `EnableDebugSocket` | `false` | UNIX socket listener |
| `EnableDebugSignals` | `false` | SIGUSR1/SIGUSR2/SIGHUP handlers |
| `EnableMutexProfiling` | `false` | Runtime mutex/block profile sampling (~3% cost) |
| `TraceEventCapacity` | `0` | Ring buffer for structured trace events |
| `SlowQueryThreshold` | `0` | Auto-log queries exceeding this duration |
| `DebugDir` | `""` → `<dbdir>/.debug/` | Socket + profile output directory |

Every row defaults to off/zero. Nothing happens at runtime unless one or more of these is explicitly set alongside `Debug=true`.

### Trace Ring Buffer Design

Fixed-capacity, lock-free-ish circular buffer:

```go
type traceRingBuf struct {
    buf  []TraceEvent
    cap  int
    head atomic.Uint64       // next write index (monotonic)
}
```

- Writers call `Put(event)` which CAS-increments `head`, indexes `(head-1) % cap`, and stores the event.
- Readers (socket handler, stats aggregation) snapshot the buffer by reading the tail-to-head range under a shared read lock or by atomically copying the `head` value and iterating.
- When the ring wraps, the oldest events are silently overwritten. `TraceRingStats.Dropped` tracks overwrite count.
- **Disabled (cap=0):** unconditional no-op — no branches, no allocations. The `Put` method returns immediately. This is the critical path for zero-overhead when DBG is off.

## Function Clusters

| Cluster | Responsibility |
|---|---|
| `TE` | Trace Events: structured ring-buffer events at engine boundaries, per-class enable/disable |
| `CT` | Counters: atomic counters + latency histograms, expvar export, debug PRAGMA backends |
| `IN` | Inspectors: page dumps, buffer pool state, active transactions, SST/WAL content inspection |
| `PR` | Profiling: on-demand CPU/heap/goroutine/mutex/block/trace profiles via socket, signal, and error triggers |
| `DC` | Dynamic Control: per-subsystem log level, trace event class enable/disable, threshold configuration |
| `SK` | Socket: UNIX domain socket command server, protocol dispatch, response formatting |

## Clusters

### TE — Trace Events

**Responsibility:** Structured, bounded, low-overhead trace events at key database boundaries. Modelled on Linux ftrace/tracepoints and FoundationDB's structured trace events.

**Design rule:** The `EventSink` interface that callers use is defined in `LOG/HK`. DBG/TE implements it. Subsystems import `LOG/HK` (low in the chain), never `DBG/TE`.

```go
// In LOG/HK — the interface callers depend on.
type EventSink interface {
    Emit(ctx context.Context, class string, fields map[string]any) error
}
```

**Key behaviors:**

- `Implements LOG/HK.EventSink`: receives trace events emitted by any subsystem. Writes to the ring buffer. No-op when `Debug=false`.
- `Emit` flow: caller in SQB/ENG/etc calls `LOG/HK.EventSink.Emit(ctx, "query", fields)` → dispatches to DBG/TE (if registered) which writes to the ring buffer.
- `Stats()`: reads head index, counts filled and dropped slots.
- Registered event classes (passed as `class` parameter):
  - `"query"` — SQL text (truncated to 256 chars), duration, rows returned, pages touched.
  - `"txn"` — begin/commit/rollback with txn ID, duration, read/write flag.
  - `"wal_checkpoint"` — old LSN, new LSN, segments freed, duration, bytes written.
  - `"compaction"` — input level, output level, input bytes, output bytes, duration, files created.
  - `"cache_evict"` — page ID, eviction reason, pin count at eviction.
  - `"lock_wait"` — lock class, goroutine IDs involved, wait duration.
  - `"slow_query"` — same as query but only for queries exceeding `SlowQueryThreshold`.

### CT — Counters

**Responsibility:** Atomic counters and latency histograms for engine-level observability. Analogue of RocksDB Statistics class, PostgreSQL pg_stat_*, and Linux perf_events software counters.

**Design rule:** The `MetricHook` interface that callers use is defined in `LOG/HK`. DBG/CT implements it. Subsystems import `LOG/HK`, never `DBG/CT`.

**Key behaviors:**

- `Implements LOG/HK.MetricHook`: receives counter observations from all subsystems. Accumulates into the `DebugStats` struct.
- All counters are `atomic.Int64` fields on a shared `DebugStats` struct owned by `SYS/AP`.
- **Types of counters:**
  - **Monotonic counters:** cumulative totals (queries, rows, pages, WAL bytes, compactions, fsyncs, cache hits/misses, txn commits/aborts).
  - **Gauges:** current snapshots (active txns, pinned pages, open sessions).
  - **Latency histograms:** fixed 16-bucket log2-spaced histograms (1µs to ~65s). Bucket boundaries: 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192, 16384, 32768 µs (and overflow). Each bucket is `atomic.Int64`.
- **Increment points:** wired into existing hot paths via the existing `LOG/HK.MetricHook` pattern — no new indirection, no import of `internal/DBG/`.
- **expvar export:** `expvar.Publish("razordata", ...)` registers a `Var` that serializes the entire `DebugStats` struct as JSON. Accessible via the socket's `expvar` command.

### IN — Inspectors

**Responsibility:** State inspection commands that dump or parse internal database structures. Modelled on pg_buffercache, pageinspect, SQLite integrity_check, sqlite3_analyzer.

**Key behaviors:**

- `InspectPage(segment, blockNum)`: read a raw 4 KB page from the relevant file, verify checksum from the page header, parse block type, item pointers, and key/value entries. Returns `PageDump`. Handles errors gracefully (checksum mismatch is reported, not returned as fatal error).
- `BufferPool()`: snapshot the buffer pool state — one entry per cached page: segment, block number, dirty flag, pin count, hit count (from clock-sweep LRU). Iterates `MEM/BF` (which already tracks these metrics).
- `ActiveTxns()`: snapshot `TXN/VL` transaction slots — txn ID, age, state, read LSN. Reads the `vl.TxnStats` type already exposed by `SYS/AP`.
- `SSTStats(segment)`: for a given SST file, report entry count, tombstone count, bloom filter false-positive rate, level, estimated reclaimable space.
- `WalInfo()`: current LSN, segment count, oldest checkpoint LSN, total WAL bytes.
- Backs these PRAGMAs:
  - `PRAGMA buffer_pool` — one row per cached page.
  - `PRAGMA active_txns` — one row per in-flight transaction.
  - `PRAGMA dump_page(segment, block)` — single row with hex + parsed header.
  - `PRAGMA sst_stats` — per-SST summary.

### PR — Profiling

**Responsibility:** On-demand triggers for Go runtime profiles. Extends the existing `LOG/HK/profile.go` ProfileHook.

**Key behaviors:**

- `DumpProfile(kind, dur)`: wraps `pprof.Lookup(kind).WriteTo(file, 0)` for named profiles, `pprof.StartCPUProfile`/`pprof.StopCPUProfile` for CPU, `runtime/trace.Start`/`runtime/trace.Stop` for execution trace.
- **Profile types:**
  - `"heap"` — `pprof.Lookup("heap")` (already implemented in ProfileHook)
  - `"cpu"` — 5s CPU profile (already implemented)
  - `"goroutine"` — `pprof.Lookup("goroutine")` (new)
  - `"mutex"` — requires `runtime.SetMutexProfileFraction(1)` at startup (new)
  - `"block"` — requires `runtime.SetBlockProfileRate(1)` at startup (new)
  - `"trace"` — `runtime/trace` execution trace, default 3s (new)
  - `"allocs"` — `pprof.Lookup("allocs")` (new)
- **fgprof integration** (build-tag gated): when built with `debug_fgprof`, adds `"fgprof"` profile type that captures both on-CPU and off-CPU stacks — uniquely valuable for I/O-bound database workloads.
- **Trigger sources:**
  1. **Socket command** — `cpu 10\n` → 10s CPU profile written to `DebugDir`.
  2. **Signal handler** — `SIGUSR1` → heap + goroutine; `SIGUSR2` → CPU (5s).
  3. **Error-triggered** — existing `ProfileHook` on `slog.LevelError` events. Extended to include goroutine profile.
  4. **Auto-profile** — when `SlowQueryThreshold` is set, queries exceeding it optionally trigger a 1s CPU profile (configurable).
- **Mutex/block rate setup:** when `Options.EnableMutexProfiling` is true, set `runtime.SetMutexProfileFraction(1)` and `runtime.SetBlockProfileRate(1)` during initialization. These have a ~3% throughput cost — off by default.
- **Directory:** profile files land in `DebugDir` with names `<kind>-<unix-ts>.pprof` (or `.trace` for execution traces).

### DC — Dynamic Control

**Responsibility:** Runtime toggling of debug knobs without restart. Modelled on Linux dynamic debug (per-module/per-function log level) and ftrace event enable/disable.

**Design rule:** The per-subsystem `slog.Logger` is created by `LOG/LG`, not by DBG. Subsystems call `LOG/LG.NewSubsystemLogger(name)` — an import of `LOG`, which is the lowest package in the chain. DBG/DC only provides the runtime-level-override map that `LOG/LG` consults. When DBG is disabled (`Debug=false`), no override map exists and `NewSubsystemLogger` returns a plain `slog.Logger` with zero extra overhead.

```go
// In LOG/LG — the factory subsystems call.
// When dbgLevels is nil (DBG not initialized), returns a plain logger
// that only checks the global slog level — no extra atomic load.
// When dbgLevels is non-nil, each Enabled() call also checks the
// per-subsystem override.
func NewSubsystemLogger(name string, defaultLevel slog.Level, dbgLevels *atomic.Int32) *slog.Logger
```

**Key behaviors:**

- `SetLogLevel(subsystem, level)`: updates an `atomic.Int32` in a per-subsystem level map. The `LOG/LG` handler reads this map on every `Enabled()` call — zero allocation when disabled.
- **How it works in practice:** each subsystem constructs its logger at init time via `LOG/LG.NewSubsystemLogger("ENG", slog.LevelInfo)`. The returned handler stores two levels: (1) the global minimum (from `slog.SetLogDefaultLevel`), and (2) the per-subsystem override (read from an `atomic.Int32` shared with DBG/DC). If neither passes, the handler returns without formatting.
- **Default state:** all subsystems inherit the global `Options.LogLevel`. Per-subsystem overrides are stored as `atomic.Int32` values, initialized to `-1` (meaning "use global default").
- `EnableTrace(class, enabled)`: updates the `traceEnabled` map in DBG/TE. DBG's `EventSink.Emit` checks this before writing to the ring buffer.
- `SetSlowQueryThreshold(dur)`: updates the slow-query threshold at runtime. Zero disables slow query tracking.
- **Socket commands:**
  - `log LEVEL SUBSYSTEM` — set per-subsystem level (e.g., `log debug SQB`)
  - `log LEVEL ALL` — set all subsystems
  - `trace CLASS on|off` — enable/disable a trace class
  - `slow N` — set slow query threshold in ms
  - `config` — dump current config (all log levels, trace classes, thresholds)

### SK — Socket

**Responsibility:** UNIX domain socket command server for interactive debugging. Modelled on Linux proc/sys filesystem and gops agent.

**Key behaviors:**

- **Socket path:** `<DebugDir>/debug.sock`. Created on `Debugger.Open()` when `EnableDebugSocket=true`.
- **Listen/accept:** single goroutine, `net.Listen("unix", path)`. Accepts one connection at a time (serialized by a mutex to avoid concurrent access to profile dumps). Each connection is a simple text-line protocol: the client sends one line, the server sends the response and closes.
- **Protocol:** stateless, line-based, text commands:
  ```
  heap\n                           → pprof protobuf bytes + \nEND\n
  cpu N\n                         → N-second CPU profile + \nEND\n
  goroutine\n                     → pprof protobuf + \nEND\n
  mutex\n                         → pprof protobuf + \nEND\n
  block\n                         → pprof protobuf + \nEND\n
  trace N\n                       → N-second execution trace + \nEND\n
  memstats\n                      → JSON MemStats + \nEND\n
  expvar\n                        → JSON expvar dump + \nEND\n
  stats\n                         → JSON DebugStats + \nEND\n
  stack\n                         → text goroutine stacks + \nEND\n
  page <segment> <block>\n        → JSON PageDump + \nEND\n
  buffer\n                        → JSON buffer pool snapshot + \nEND\n
  txns\n                          → JSON active txns + \nEND\n
  log <level> <subsystem>\n       → OK \nEND\n
  trace <class> on|off\n          → OK \nEND\n
  slow <ms>\n                     → OK \nEND\n
  gc\n                            → trigger GC + \nEND\n
  help\n                          → command list + \nEND\n
  ```
- **Client tools:** the `razor` CLI has a `debug` subcommand: `razor debug <dbdir> heap` that connects to the socket and pipes output to stdout.
- **Security:** the socket is created with `os.FileMode(0700)` (owner-only access). The socket path is inside the database directory, which is already permission-controlled by the OS.
- **Error handling:** on any parse or execution error, the server sends `ERROR <message>\nEND\n` and continues listening. No panic escapes the dispatch goroutine.
- **Socket removal:** on `Close()`, remove the socket file with `os.Remove(path)`. This is safe even if the process crashes (the stale file is cleaned up on next `Listen`).

## Implementation Plan

Pre-requisite: all existing subsystems are stable. DBG is a leaf — its implementation order is inside-out (low-level building blocks first, then the socket that ties them together).

1. **`internal/LOG/LG/subsystem.go`** — Add `NewSubsystemLogger(name, defaultLevel)` factory and per-subsystem `atomic.Int32` level map. This is the only change to the LOG subsystem. Test: verify level changes take effect immediately, verify zero-cost disabled path.
2. **`internal/DBG/TE/te.go`** — Trace Events: ring buffer implementation, `Emit` (implements `LOG/HK.EventSink`), per-class gating, `Stats`. Test: verify ring wraps correctly, verify dropped count, verify disable completely skips allocation.
3. **`internal/DBG/CT/ct.go`** — Counters: `NewDebugStats()`, histogram `Observe(µs)`, `expvar.Publish`, wire into existing `MetricHook`. Test: verify counter increments survive concurrent access, verify latency histogram buckets are correct.
4. **`internal/DBG/IN/in.go`** — Inspectors: `InspectPage`, `BufferPool`, `ActiveTxns`, `SSTStats`, `WalInfo`. Test: inspect a known page and verify parsed fields match expected values.
5. **`internal/DBG/PR/pr.go`** — Profiling: `DumpProfile` dispatcher, extend existing ProfileHook, signal handler setup for SIGUSR1/SIGUSR2. Mutation profile rate setup. fgprof integration (build-tag gated). Test: trigger each profile kind and verify file is written with correct format.
6. **`internal/DBG/SK/sk.go`** — Socket: UNIX listener, connection dispatch, command parsing, response formatting. Test: connect with `net.Dial("unix", path)`, send each command, verify response. Test: malformed input returns `ERROR`.
7. **`internal/DBG/dbg.go`** — Debugger struct, `New`, `Close`, `Stats` aggregation, integration with `SYS/SY.Open` (construct after all subsystems, wire through `Options.Debug` flag).
8. **PRAGMA integration** — register PRAGMA handlers in `SQB/UT/pragma.go`:
   - `PRAGMA query_stats` → `DBG/CT`
   - `PRAGMA engine_counters` → `DBG/CT`
   - `PRAGMA buffer_pool` → `DBG/IN`
   - `PRAGMA active_txns` → `DBG/IN`
   - `PRAGMA dump_page(segment, block)` → `DBG/IN`
   - `PRAGMA sst_stats` → `DBG/IN`
   - `PRAGMA wal_stats` → `DBG/CT`
9. **CLI integration** — `razor debug <dbdir> <command> [args]` subcommand in `cmd/razor/`. Connects to the UNIX socket, sends the command, writes response to stdout.
10. **Integration tests:**
    - `debugger_test.go` — open engine with `Debug=true`, exercise all socket commands, verify responses.
    - `profile_test.go` — trigger profiles via signal and socket, verify files exist and have valid pprof headers.
    - `trace_ring_test.go` — emit 2× capacity events, verify oldest are dropped.
    - `dynamic_debug_test.go` — set per-subsystem log level, verify a Debug log from that subsystem appears while others don't.

## Shipped Requirements

TBD (first iteration will add rows here)

## Open Issues

- **Should the debug socket be a separate process instead?** A sidecar process connecting to the engine via shared memory has isolation benefits but adds deployment complexity. Decision: in-process UNIX socket for now. Revisit if memory-safety concerns arise.
- **Configurable trace ring buffer size:** 4096 hardcoded is a guess. The ring is in-memory only (lost on restart). Acceptable for debugging. Make it configurable via `Options.TraceEventCapacity` in the current design.
- **Lock ordering validator (lockdep analogue):** can be built as a separate package (`internal/DBG/LC/`) that wraps `sync.Mutex`/`sync.RWMutex` with lock-class tracking. P1 — not in the initial implementation.
- **Resource leak detector (kmemleak analogue):** periodic scan for unreleased page references, transaction slots, goroutines. P1 — defer.
- **fgprof dependency management:** add `debug_fgprof` build tag so the dependency is optional. The package is small (~500 lines) and has no C deps.
- **How to handle the debug socket when running multiple engines in one process:** socket path should include a pid or a user-provided instance name. Default: `<DebugDir>/debug-<pid>.sock`. Documented.
- **Should trace events survive a crash?** Not in the initial design — the ring buffer is in-memory only. A future disk-backed trace ring (mmap'd file) could survive crashes. Low priority.
