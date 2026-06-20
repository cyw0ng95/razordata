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
    InMemory     bool          // pure in-memory mode, no disk I/O
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
- `Close`: 6-phase graceful shutdown sequence (see below).
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
- Sessions are pooled via `sync.Pool` for reuse. On `Put` back to the pool, clear `txn`, `params`, and `deadline` fields to avoid stale data in reused sessions.

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

The error system uses a `Kind`-based classification for programmatic error handling. All user-facing errors should be wrappable into `AP.Error` with an appropriate `Kind`.

```go
// Kind classifies errors for programmatic handling.
type Kind int

const (
    KindNotFound Kind = iota + 1
    KindDuplicateKey
    KindLocked
    KindCorrupt
    KindSyntax
    KindTypeMismatch
    KindTxAborted
    KindIO
    KindUpgradeRequired
    KindReadOnly
    KindDeadlineExceeded
    KindConstraint
    KindClosed
    KindInvalidOptions
)

// Error is the structured error type for all user-facing errors.
type Error struct {
    Kind    Kind
    Message string
    Err     error // underlying error, if any
}

func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.Err }

func New(kind Kind, msg string) *Error { ... }
func Wrap(kind Kind, err error) *Error { ... }

// IsKind checks whether err (or any error in its chain) is an *Error with the given Kind.
func IsKind(err error, kind Kind) bool {
    var e *Error
    if errors.As(err, &e) {
        return e.Kind == kind
    }
    return false
}
```

Backward-compatible sentinel variables are retained for `errors.Is` during migration:
```go
var (
    ErrNotFound         = New(KindNotFound, "razordata: key not found")
    ErrDuplicateKey     = New(KindDuplicateKey, "razordata: duplicate key")
    ErrLocked           = New(KindLocked, "razordata: resource locked")
    ErrCorrupt          = New(KindCorrupt, "razordata: data corrupt")
    ErrSyntax           = New(KindSyntax, "razordata: syntax error")
    ErrTypeMismatch     = New(KindTypeMismatch, "razordata: type mismatch")
    ErrTxAborted        = New(KindTxAborted, "razordata: transaction aborted")
    ErrIO               = New(KindIO, "razordata: I/O error")
    ErrUpgradeRequired  = New(KindUpgradeRequired, "razordata: upgrade required")
    ErrReadOnly         = New(KindReadOnly, "razordata: read-only")
    ErrDeadlineExceeded = New(KindDeadlineExceeded, "razordata: deadline exceeded")
    ErrAlreadyOpen      = New(KindInvalidOptions, "razordata: engine already open")
    ErrNotOpen          = New(KindClosed, "razordata: engine not open")
    ErrClosed           = New(KindClosed, "razordata: engine closed")
    ErrInvalidOptions   = New(KindInvalidOptions, "razordata: invalid options")
    ErrNoActiveTxn      = New(KindTxAborted, "razordata: no active transaction")
    ErrUnknownSavepoint = New(KindNotFound, "razordata: unknown savepoint")
    ErrConstraint       = New(KindConstraint, "razordata: constraint violation")
)
```

**Error classification:**
- `RetryableErrors`: errors where callers should retry with exponential backoff (`KindIO`, `KindLocked`).
- `FatalErrors`: errors that must not be retried — they indicate application-level bugs or unrecoverable state (all other kinds).

**Cross-layer error contract:**
- Lower layers (ENG, FIL, WAL, TXN) define their own sentinels for internal use.
- At subsystem boundaries (SYS/SY, SYS/SE, SQL/EX), lower-layer errors must be wrapped into `AP.Error` with the appropriate `Kind` using `AP.Wrap(kind, err)`.
- This ensures `AP.IsKind(err, KindCorrupt)` matches errors from any layer, not just AP-level sentinels.

**Prefix convention:**
- All AP-level errors use `"razordata: "` prefix.
- Package-internal errors use shorter prefixes for debugging (`"ls: "`, `"wr: "`, `"ex: "`, etc.).

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
| `BK` | Backup/Restore: online backup with read lock, file copy, LSN marker, integrity check, restore to fresh directory |
| `DS` | database/sql driver implementation |

## Clusters

### SY — System

**Responsibility:** Global init, config validation, graceful shutdown signal handling, version, stats aggregation.

**Key behaviors:**

#### Open (Initialization)
- `Open`: validate `Options` (dir exists or `CreateIfMissing`, page size is power of 2, sizes are positive). Construct all subsystems in order: `LOG` → `FIL` → `MEM` → `WAL` → `ENG` → `TXN`. Call `WAL/RP.Replay()` to recover from crash.
- **InMemory mode:** when `Options.InMemory` is true, bypass all disk subsystems. Construct only logger, sync pool, and a raw `EX.NewExecutor()` (no store, no catalog). All state is ephemeral.
- **Config validation:** Before any subsystem is constructed, validate all fields:
  - `Dir`: must be non-empty, absolute path or relative to cwd.
  - `PageSize`: must be power of 2, range [1024, 65536].
  - `MemTableSize`: must be >= 1 MB, <= 1 GB.
  - `BufferPoolMB`: must be >= 64, <= 4096.
  - `WALSizeMB`: must be >= 16, <= 256.
  - `MaxLevel`: must be in range [3, 10].
  - Invalid options return `fmt.Errorf("razordata: invalid option: %s", field)` before any subsystem is constructed.

#### Close (Graceful Shutdown)
- `Close` follows a strict 6-phase shutdown sequence to ensure data durability and goroutine safety:

**Phase 1: Stop accepting new requests**
- Set `closed = true` (atomic.Bool)
- All public API methods check `closed` — return `ErrClosed` if true

**Phase 2: Wait for active transactions to complete (timeout: 30s)**
- Count active transactions (slots with status == ACTIVE)
- If count > 0: wait on `sync.Cond` (broadcast when a transaction commits/aborts)
- Timeout after 30s: force-abort remaining transactions

**Phase 3: Flush pending writes**
- `ENG.Flush()` — flush memtable to SST
- `WAL.Sync()` — fsync all pending WAL records
- `FIL.SyncDir()` — fsync directory entries

**Phase 4: Stop background goroutines (timeout: 5s each)**
- Stop compaction goroutine
- Stop flush manager
- Stop epoch manager

**Phase 5: Close subsystems in reverse order**
- TXN → ENG → WAL → MEM → FIL → LOG

**Phase 6: Log final stats**

**Error Handling During Close:**
- On any error during shutdown, log the error with `slog.Error` and continue closing remaining subsystems.
- Shutdown is best-effort — the goal is to flush as much data as possible, not to guarantee 100% durability if an error occurs.

#### Signal Handling
- `InstallSignalHandler(ctx, engine)` subscribes to SIGINT and SIGTERM and calls `Close` with the provided context on the first signal. A second signal aborts the wait and returns immediately. Returns a stop function that the caller invokes to unsubscribe and release the internal goroutine.
- The handler is idempotent: calling stop multiple times is safe (guarded by `sync.Once`).

#### Stats Aggregation
- `Stats()`: acquire read lock, aggregate stats from all subsystems:
  - `BufferPool`: hits, misses, pins, evicts
  - `WAL`: records written, bytes written, fsync count
  - `ENG`: memtable size, SST count per level, compaction bytes
  - `TXN`: transactions started, committed, aborted, conflicts

### AP — API

**Responsibility:** Public API: `Open`, `Options`, error types, `EngineStats`.

**Key behaviors:**
- All public types are defined here: `Engine`, `Session`, `Transaction`, `Stmt`, `Options`, `Result`, `Rows`, all error vars.
- `Open` returns `*Engine`. All subsequent operations are through the returned interface.
- Error types are exported (`ErrNotFound`, etc.) so callers can use `errors.Is`.
- `Options` is validated in `SY/Open` — invalid options return an error before any subsystem is constructed.
- `Options.InMemory` enables pure in-memory mode. When true, `Dir` is ignored (must be `:memory:`). No WAL, SST, catalog, or filesystem I/O. State is lost on Close.

### SE — Session

**Responsibility:** Session creation, lifecycle, goroutine-safety, deadline, session stats.

**Key behaviors:**
- `NewSession(engine) *session`: allocate from a `sync.Pool`. On `Put` back to the pool, clear `txn`, `params`, and `deadline` fields to avoid stale data in reused sessions.
- `Query` / `Exec`: acquire `mu`, bind params, call `SQL/EX.Exec()`, release `mu`.
- `Begin`: if `session.txn` is `nil` and not active, create a new transaction; if `session.txn` is already active, return `ErrLocked`.
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
- Engine-level prepared statement cache: LRU + ref-counted, `Get`/`Put`/`Release`/`Clear`/`Size`.

### BK — Backup/Restore

**Responsibility:** Online backup and restore with read lock, file copy, and integrity check.

**Key behaviors:**
- `Backup(ctx, dest, opts)`: acquires read lock to block writers, copies all database files (engine data, WAL, catalog) to destination directory, releases lock. `BackupOptions` supports compression flag and LSN marker for consistency tracking.
- `Restore(ctx, source, dest)`: verifies backup integrity (checks LSN marker, directory structure), copies files to a fresh directory for a clean database instance.

### DS — database/sql Driver

**Responsibility:** Go `database/sql/driver` implementation. Registered via `init()` as `sql.Register("razor", &Driver{})`.

**Key behaviors:**
- DSN parsing: `:memory:` → in-memory mode; file path → on-disk database via `v1.Open`.
- `OpenConnector` implements `driver.DriverContext` (Go 1.10+) for context-aware connections.
- Value conversion: `int64`, `float64`, `string`, `[]byte`, `bool` → `driver.Value`; `nil` → `nil`.
- Rows are materialized on `Query` (collected into `[]AP.Row`), then served via `Next`/`Close`.

```go
import (
    "database/sql"
    _ "github.com/cyw0ng95/razordata/internal/SYS/DS"
)
db, _ := sql.Open("razor", ":memory:")
```

## Implementation Plan

1. **`internal/SYS/AP/ap.go`** — `Engine` interface, `Options` struct, all error types, `EngineStats`.
2. **`internal/SYS/SY/sy.go`** — `engine` struct, `Open` (config validation, subsystem construction), `Stats`, version constant.
3. **`internal/SYS/SY/shutdown.go`** — graceful shutdown: 6-phase close sequence, signal handling (`os/signal`), active transaction wait, background goroutine stop, subsystem close order, error handling.
4. **`internal/SYS/SY/validate.go`** — `validateOptions(opts Options) error`: field-by-field validation with descriptive error messages.
5. **`internal/SYS/SE/se.go`** — `session` struct, `NewSession`, `Query`, `Exec`, `Begin`, `Commit`, `Rollback`, `SetDeadline`, `Stats`. Session pooling via `sync.Pool`.
6. **`internal/SYS/TX/tx.go`** — `transaction` struct, `Query`, `Exec`, `Commit`, `Rollback`, `Savepoint`, `RollbackTo`.
7. **`internal/SYS/ST/st.go`** — `stmt` struct, `Prepare`, `Bind`, `Query`, `Exec`, `Close`. Plan memoization, type coercion.
8. **`internal/SYS/BK/bk.go`** — online backup and restore: read lock, file copy, LSN marker, integrity verification.
9. **`internal/SYS/DS/ds.go`** — driver registration, DSN parse, Connector.
10. **`internal/SYS/DS/conn.go`** — driver.Conn, driver.Result.
11. **`internal/SYS/DS/stmt.go`** — driver.Stmt, value conversion.
12. **`internal/SYS/DS/rows.go`** — driver.Rows (materialized).
13. **`internal/SYS/DS/tx.go`** — driver.Tx.
14. **Integration tests:**
    - `engine_test.go` — `Open`/`Close`, concurrent sessions, graceful shutdown, error types.
    - `shutdown_test.go` — signal handler double-stop safety, context cancellation, 6-phase close sequence.
    - `validate_test.go` — invalid options (page size not power of 2, negative sizes), verify rejection.

## Open Issues

- Should the engine support a metrics endpoint (Prometheus)? Future work — add an admin interface.
- What is the optimal timeout for waiting active transactions during shutdown? 30s is the default; may need tuning based on workload.
- Should force-aborted transactions during shutdown be rolled back to a savepoint instead of full abort?
- Should the shutdown sequence be configurable (e.g., skip waiting for transactions in emergency shutdown)?
