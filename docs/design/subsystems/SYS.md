# SYS — System Layer

## Overview

Top-level entry point. Exposes the public API to the application. Manages the database lifecycle: `Open`, `Close`, `Begin`, `Query`, `Exec`. Creates and owns sessions, which each hold a transaction. Handles graceful shutdown, config validation, and version metadata. Depends on `TXN`, `ENG`, and `LOG`.

## Dependencies

- Required: `TXN`, `ENG`, `LOG`
- Consumed interfaces: `Tx`, `Store`, `Logger`

## Exposed Interfaces

```go
// Engine is the top-level database handle
type Engine interface {
    Open(ctx context.Context, dir string, opts Options) error
    Close(ctx context.Context) error
    Begin(ctx context.Context) (Session, error)
    Stats() EngineStats
}

// Session is a single database connection
type Session interface {
    Query(ctx context.Context, sql string, args ...any) (*Rows, error)
    Exec(ctx context.Context, sql string, args ...any) (Result, error)
    Begin(ctx context.Context) (Transaction, error)
    Commit(ctx context.Context) error
    Rollback(ctx context.Context) error
    SetDeadline(deadline time.Time) error
    Stats() SessionStats
}

// Transaction is a session's current transaction
type Transaction interface {
    Query(ctx context.Context, sql string, args ...any) (*Rows, error)
    Exec(ctx context.Context, sql string, args ...any) (Result, error)
    Commit(ctx context.Context) error
    Rollback(ctx context.Context) error
    Savepoint(ctx context.Context, name string) error
    RollbackTo(ctx context.Context, name string) error
}

// Stmt is a prepared statement
type Stmt interface {
    Query(ctx context.Context, args ...any) (*Rows, error)
    Exec(ctx context.Context, args ...any) (Result, error)
    Close() error
}

// Options configures the engine
type Options struct {
    Dir          string        // database directory (required)
    PageSize     int           // block size in bytes, power of 2, default 4096
    MemTableSize int           // memtable size threshold in bytes, default 64 MB
    BufferPoolMB int           // buffer pool size in MB, default 256 MB
    WALSizeMB    int           // WAL segment size in MB, default 64 MB
    MaxLevel     int           // max LSM levels, default 7
    LogLevel     slog.Level    // default slog.LevelInfo
    LogFormat    string        // "json" or "text", default "text"
    ReadOnly     bool          // open in read-only mode, default false
    CreateIfMissing bool       // create database if dir does not exist, default true
}
```

## Data Structures

### Engine

```go
type engine struct {
    dir   string
    opts  Options

    // subsystems (constructed in order)
    log   *LOG.Logger
    fil   *FIL.FileManager
    mem   *MEM.BufferPool
    wal   *WAL.Writer
    eng   *ENG.Store
    txn   *TXN.TxnManager

    // lifecycle
    mu    sync.RWMutex
    closed atomic.Bool
}
```

- `Open`: validate `Options`, construct subsystems in dependency order (`LOG` → `FIL` → `MEM` → `WAL` → `ENG` → `TXN`), create directory if `CreateIfMissing`, call `WAL.RP.Replay()` to recover.
- `Close`: set `closed = true`, flush all pending writes (memtable, WAL), close all subsystems in reverse order.
- `Begin`: create a new `Session` with a fresh transaction from `TXN`.
- `Stats`: return aggregate stats from all subsystems (buffer pool hits, WAL records, compaction bytes, etc.).

### Session

```go
type session struct {
    engine   *engine
    txn      Transaction
    params   []any        // bound parameters for the current statement
    deadline atomic.Value // time.Time, set by SetDeadline
    id      uint64        // unique session ID for tracing
    stats   SessionStats
    mu      sync.Mutex    // protects txn, params
}
```

- Goroutine-safe: all public methods acquire `mu`.
- `Query` / `Exec`: parse SQL → bind params → execute → return result.
- `Begin`: start a new transaction. Returns `ErrLocked` if a transaction is already active in this session.
- `Commit` / `Rollback`: commit or abort the current transaction, then set `session.txn = nil`.
- `SetDeadline`: set a deadline for all subsequent operations in this session.

### Transaction

```go
type transaction struct {
    session *session
    readTS  uint64
    savepoints map[string]uint64 // savepoint name → readTS
}
```

- Wraps `TXN.Tx`.
- `Savepoint` / `RollbackTo`: save the current `readTS` under a name; restore on `RollbackTo`.
- `Query` / `Exec`: delegate to `SQL/EX.Executor.Exec()`.

### Error Types

```go
var (
    ErrNotFound         = errors.New("razordata: key not found")
    ErrDuplicateKey     = errors.New("razordata: duplicate key")
    ErrLocked           = errors.New("razordata: resource locked")
    ErrCorrupt          = errors.New("razordata: data corrupt")
    ErrSyntax           = errors.New("razordata: syntax error")
    ErrTypeMismatch     = errors.New("razordata: type mismatch")
    ErrTxAborted        = errors.New("razordata: transaction aborted")
    ErrIO               = errors.New("razordata: I/O error")
    ErrUpgradeRequired  = errors.New("razordata: upgrade required")
    ErrReadOnly         = errors.New("razordata: read-only")
    ErrDeadlineExceeded = errors.New("razordata: deadline exceeded")
)

var retryable = []error{ErrIO, ErrLocked}
var fatal = []error{ErrTxAborted, ErrCorrupt, ErrSyntax, ErrTypeMismatch, ErrUpgradeRequired, ErrReadOnly}
```

- All errors wrap: I/O errors → structural errors → API-level errors.
- Error messages are lowercase, no trailing punctuation.
- Errors are wrapped with `fmt.Errorf("razordata: %w", err)` to preserve the error chain.
- **Retry classification:** callers should retry on `retryable` errors (with exponential backoff for `ErrLocked`); `fatal` errors must not be retried — they indicate application-level bugs or unrecoverable state.

### EngineStats

```go
type EngineStats struct {
    Version     string
    LSMTree     LSMTreeStats
    BufferPool  MEM.BufferStats
    WAL         WALStats
    Compaction   CompactionStats
    Uptime      time.Duration
}
```

### SessionStats

```go
type SessionStats struct {
    ID           uint64
    QueryCount   atomic.Int64
    RowsReturned atomic.Int64
    BytesRead    atomic.Int64
    BytesWritten atomic.Int64
    ActiveTXN    bool
}
```

## Function Clusters

| Cluster | Responsibility |
|---|---|
| `SY` | System: init, config validation, graceful shutdown, version, stats aggregation |
| `AP` | API: public interface, `Engine`/`Session`/`Transaction`/`Stmt`/`Options`, error types |
| `SE` | Session: session creation, lifecycle, goroutine-safety, deadline, session stats |
| `TX` | Transaction: transaction context, commit/rollback, savepoints |
| `ST` | Statement: preparation, parameter binding, type coercion, close |
| `BK` | Backup/Restore (REQ000259): online backup with read lock, file copy, LSN marker, integrity check, restore to fresh directory |

## Clusters

### SY — System

**Responsibility:** Global init, config validation, graceful shutdown signal handling, version, stats aggregation.

**Key behaviors:**

#### Open (Initialization)
- `Open`: validate `Options` (dir exists or `CreateIfMissing`, page size is power of 2, sizes are positive). Construct all subsystems in order: `LOG` → `FIL` → `MEM` → `WAL` → `ENG` → `TXN`. Call `WAL/RP.Replay()` to recover from crash.
- **Version:** `Version = "0.1.0"` (semantic versioning).
- **Config validation:** Before any subsystem is constructed, validate all fields:
  - `Dir`: must be non-empty, absolute path or relative to cwd.
  - `PageSize`: must be power of 2, range [1024, 65536].
  - `MemTableSize`: must be >= 1 MB, <= 1 GB.
  - `BufferPoolMB`: must be >= 64, <= 4096.
  - `WALSizeMB`: must be >= 16, <= 256.
  - `MaxLevel`: must be in range [3, 10].
  - Invalid options return `fmt.Errorf("razordata: invalid option: %s", field)` before any subsystem is constructed.

#### Close (Graceful Shutdown)
- `Close` follows a strict shutdown sequence to ensure data durability and goroutine safety:

```
Phase 1: Stop accepting new requests
  1.1. Set `closed = true` (atomic.Bool)
  1.2. Close `ctxCancel` to signal all goroutines
  1.3. All public API methods check `closed` — return `ErrClosed` if true

Phase 2: Wait for active transactions to complete (timeout: 30s)
  2.1. Acquire read lock on transaction slot array
  2.2. Count active transactions (slots with status == ACTIVE)
  2.3. If count > 0:
       - Log: "waiting for N active transactions to complete"
       - Wait on `sync.Cond` (broadcast when a transaction commits/aborts)
       - Timeout after 30s: force-abort remaining transactions
  2.4. For force-aborted transactions:
       - Write RTRollback to WAL
       - Log: "force-aborted transaction %d after shutdown timeout"

Phase 3: Flush pending writes
  3.1. Call `ENG.Flush()` — flush memtable to SST
  3.2. Call `WAL.Sync()` — fsync all pending WAL records
  3.3. Call `FIL.SyncDir()` — fsync directory entries

Phase 4: Stop background goroutines
  4.1. Stop compaction goroutine:
       - Send stop signal via `stopCh`
       - Wait for goroutine to exit (via `sync.WaitGroup`)
       - Timeout after 5s: log warning and proceed
  4.2. Stop epoch manager goroutine:
       - Close `drainCh` to signal exit
       - Wait for goroutine to exit
  4.3. Stop hook dispatcher goroutine (LOG/HK):
       - Close event channel
       - Wait for dispatcher to drain pending events
  4.4. Stop metric collection goroutine (if enabled):
       - Flush pending metrics
       - Close goroutine

Phase 5: Close subsystems in reverse order
  5.1. `TXN.Close()`:
       - Release all transaction slots
       - Clear hazard pointer sets
       - Free all arena buffers
  5.2. `ENG.Close()`:
       - Close memtable (release skiplist nodes)
       - Close manifest file
       - Release iterator pool
  5.3. `WAL.Close()`:
       - Final `fsync` on current segment
       - Close segment FD
       - Sync WAL directory
  5.4. `MEM.Close()`:
       - Write hint file (serialize hot working set)
       - Flush all dirty pages to disk
       - Release buffer pool slots
       - Return all buffers to `sync.Pool`
  5.5. `FIL.Close()`:
       - Close all open file handles (via `handles` map)
       - Close directory FDs (via `dirFDs` map)
       - Sync root directory
  5.6. `LOG.Close()`:
       - Flush all pending log events
       - Call `Sync()` on underlying slog handler
       - Close log file (if configured)

Phase 6: Cleanup and logging
  6.1. Log: "razordata shutdown complete"
  6.2. Aggregate final stats: uptime, total queries, total bytes read/written
  6.3. Write stats to `EngineStats` for post-mortem analysis
```

**Error Handling During Close:**
- On any error during shutdown, log the error with `slog.Error` and continue closing remaining subsystems.
- Shutdown is best-effort — the goal is to flush as much data as possible, not to guarantee 100% durability if an error occurs.
- After `Close()` returns, the `Engine` is unusable. Any subsequent API calls return `ErrClosed`.

#### Signal Handling
- Register signal handler in `Open()`:
  ```go
  sigCh := make(chan os.Signal, 1)
  signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
  go func() {
      <-sigCh
      log.Info("received shutdown signal")
      engine.Close()
      os.Exit(0)
  }()
  ```
- Users can override this by catching signals themselves and calling `Close()` manually.

#### Stats Aggregation
- `Stats()`: acquire read lock, aggregate stats from all subsystems:
  - `BufferPool`: hits, misses, pins, evicts
  - `WAL`: records written, bytes written, fsync count
  - `ENG`: memtable size, SST count per level, compaction bytes
  - `TXN`: transactions started, committed, aborted, conflicts
  - `SQL`: queries executed, rows returned, rows modified

### AP — API

**Responsibility:** Public API: `Open`, `Options`, error types, `EngineStats`.

**Key behaviors:**
- All public types are defined here: `Engine`, `Session`, `Transaction`, `Stmt`, `Options`, `Result`, `Rows`, all error vars.
- `Open` returns `*Engine`. All subsequent operations are through the returned interface.
- Error types are exported (`ErrNotFound`, etc.) so callers can use `errors.Is`.
- `Options` is validated in `SY/Open` — invalid options return an error before any subsystem is constructed.

### SE — Session

**Responsibility:** Session creation, lifecycle, goroutine-safety, deadline, session stats.

**Key behaviors:**
- `NewSession(engine) *session`: allocate from a `sync.Pool`. On `Put` back to the pool, clear `txn`, `params`, and `deadline` fields to avoid stale data in reused sessions.
- `Query` / `Exec`: acquire `mu`, bind params, call `SQL/EX.Exec()`, release `mu`.
- `Begin`: if `session.txn` is `nil` and not active, create a new transaction; if `session.txn` is already active, return `ErrLocked`. The session-level `mu` ensures no concurrent transactions within a session.
- `Commit` / `Rollback`: commit or abort the current transaction, then set `session.txn = nil`.
- `SetDeadline`: set a `time.Time` in an `atomic.Value`. All subsequent `Query`/`Exec` calls respect this deadline.
- `Stats`: return `SessionStats` including query count, rows returned, bytes read/written.
- Goroutine-safety: `mu` is held for the duration of each operation. Sessions are not shareable between goroutines by default.

### TX — Transaction

**Responsibility:** Transaction context, commit/rollback, savepoints.

**Key behaviors:**
- Wraps `TXN.Tx`. Exposes the `Transaction` interface.
- `Savepoint`: store `readTS` under a name. `RollbackTo`: restore `readTS` from a savepoint name.
- `Query` / `Exec`: delegate to `SQL/EX.Exec()` with the current transaction.

### ST — Statement

**Responsibility:** Query/Exec preparation, parameter binding, type coercion, close.

**Key behaviors:**
- `Prepare(sql string) (*stmt, error)`: parse the SQL, build an AST, plan it (memoized), store the plan.
- `Bind(args ...any)`: store args, validate types against parameter count.
- `Query(ctx, args)`: bind args, execute plan, return rows.
- `Exec(ctx, args)`: bind args, execute plan, return result.
- `Close()`: release the plan (if not shared), return to `sync.Pool`.
- Type coercion: if a Go `int` is passed but the column is `BIGINT`, cast. If a Go `string` is passed but the column is `INT`, return `ErrTypeMismatch`.

### BK — Backup/Restore

**Responsibility:** Online backup and restore with read lock, file copy, and integrity check.

**Key behaviors:**
- `Backup(ctx, dest, opts)`: acquires read lock to block writers, copies all database files (engine data, WAL, catalog) to destination directory, releases lock. `BackupOptions` supports compression flag and LSN marker for consistency tracking.
- `Restore(ctx, source, dest)`: verifies backup integrity (checks LSN marker, directory structure), copies files to a fresh directory for a clean database instance.

1. **`internal/SYS/AP/ap.go`** — `Engine` interface, `Options` struct, all error types, `EngineStats`.
2. **`internal/SYS/SY/sy.go`** — `engine` struct, `Open` (config validation, subsystem construction), `Stats`, version constant.
3. **`internal/SYS/SY/shutdown.go`** — graceful shutdown: 6-phase close sequence, signal handling (`os/signal`), active transaction wait, background goroutine stop, subsystem close order, error handling.
4. **`internal/SYS/SY/validate.go`** — `validateOptions(opts Options) error`: field-by-field validation with descriptive error messages.
5. **`internal/SYS/SE/se.go`** — `session` struct, `NewSession`, `Query`, `Exec`, `Begin`, `Commit`, `Rollback`, `SetDeadline`, `Stats`. Session pooling via `sync.Pool`.
6. **`internal/SYS/TX/tx.go`** — `transaction` struct, `Query`, `Exec`, `Commit`, `Rollback`, `Savepoint`, `RollbackTo`.
7. **`internal/SYS/ST/st.go`** — `stmt` struct, `Prepare`, `Bind`, `Query`, `Exec`, `Close`. Plan memoization, type coercion.
8. **`internal/SYS/BK/bk.go`** — online backup and restore: read lock, file copy, LSN marker, integrity verification.
9. **Integration tests:**
   - `engine_test.go` — `Open`/`Close`, concurrent sessions, graceful shutdown, error types.
   - `shutdown_test.go` — simulate SIGTERM, verify 6-phase close sequence, test timeout handling.
   - `validate_test.go` — invalid options (page size not power of 2, negative sizes), verify rejection.
9. **Benchmark tests:** `engine_bench.go` — throughput benchmark: `go test -bench=BenchmarkEngine -benchtime=10s`.

## Shipped Requirements

The following requirements have been implemented and shipped; they are now part of the design baseline.

### SY / AP / SE / TX / ST / BK — System Layer

| ID | Requirement | Iteration |
|---|---|---|
| REQ000087 | `Engine.Open` with `Options` validation | iter-09 |
| REQ000088 | `Engine.Close` with graceful shutdown | iter-09 |
| REQ000089 | `Engine.Begin` → `Session` | iter-09 |
| REQ000090 | `Engine.Stats` aggregation | iter-09 |
| REQ000091 | Error type taxonomy (retryable vs fatal) | iter-09 |
| REQ000092 | `Session.Query` / `Session.Exec` | iter-09 |
| REQ000093 | `Session.SetDeadline` with `atomic.Value` | iter-09 |
| REQ000094 | `Transaction.Commit` / `Rollback` | iter-09 |
| REQ000095 | `Transaction.Savepoint` / `RollbackTo` | iter-09 |
| REQ000096 | `Stmt.Prepare` / `Query` / `Exec` / `Close` | iter-09 |
| REQ000097 | SIGTERM/SIGINT graceful shutdown handler | iter-09 |
| REQ000098 | Session pooling (`sync.Pool`) | iter-15 |
| REQ000099 | `ReadOnly` mode in `Options` (skip WAL writes, O_RDONLY opens) | iter-15 |
| REQ000128 | OPS — Point-in-time backup / restore (snapshot engine dir, restore to a copy) | iter-08 |
| REQ000130 | OBS — Query tracing via `TraceHook` | iter-00 |
| REQ000131 | OBS — Latency histograms via `MetricHook` | iter-00 |
| REQ000132 | OBS — CPU/heap profiling on error | iter-00 |
| REQ000133 | OBS — Structured stats aggregation (`Engine.Stats`) | iter-09 |
| REQ000146 | 6-phase graceful shutdown sequence per SYS.md:215-282 | iter-14 |
| REQ000152 | `validateOptions` with field-by-field checks per SYS.md:198-214 | iter-14 |
| REQ000153 | Active-tx wait (30s timeout, force-abort on timeout) | iter-14 |
| REQ000154 | Background-goroutine coordination (compaction, flush, epoch, hook dispatcher) | iter-14 |
| REQ000166 | Per-subsystem `Close()` ordering in Phase 5 of shutdown | iter-14 |
| REQ000172 | 6-phase graceful shutdown implementation per SYS.md:196-283 | iter-14 |
| REQ000178 | `validateOptions` (duplicate of REQ000152, same code) | iter-14 |
| REQ000242 | Pragmas (cache_size, journal_mode, synchronous) | iter-24 |
| REQ000259 | Backup/restore API (snapshot engine dir to copy) | iter-23 |
| REQ000260 | Admin CLI `razor` (schema dump, vacuum, integrity check) | iter-23 |
| REQ000261 | Integrity check (`PRAGMA integrity_check`) | iter-23 |

## Open Issues

- ~~Should sessions be pooled (reuse inactive sessions)?~~ Resolved: yes, via `sync.Pool`. **(REQ000098)**
- ~~Should we support read-only mode~~ Resolved: yes, `ReadOnly` option implemented. **(REQ000099)**
- ~~How to handle `SetDeadline` cancellation~~ Resolved: uses `context.WithDeadline` internally. **(REQ000093)**
- Should the engine support a metrics endpoint (Prometheus)? Future work — add an admin interface.
- What is the optimal timeout for waiting active transactions during shutdown? 30s is the default; may need tuning based on workload. **(REQ000153)**
- Should force-aborted transactions during shutdown be rolled back to a savepoint instead of full abort?
- Should the shutdown sequence be configurable (e.g., skip waiting for transactions in emergency shutdown)?
- ~~Should backup support incremental backups or only full backup?~~ **(REQ000259 ships full backup; incremental is future work)**