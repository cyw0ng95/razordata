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
- `Begin`: start a new transaction (or reuse existing if none).
- `Commit` / `Rollback`: commit or abort the current transaction.
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
    ErrNotFound     = errors.New("razordata: key not found")
    ErrDuplicateKey = errors.New("razordata: duplicate key")
    ErrLocked       = errors.New("razordata: resource locked")
    ErrCorrupt      = errors.New("razordata: data corrupt")
    ErrSyntax       = errors.New("razordata: syntax error")
    ErrTypeMismatch = errors.New("razordata: type mismatch")
    ErrTxAborted    = errors.New("razordata: transaction aborted")
    ErrIO           = errors.New("razordata: I/O error")
    ErrUpgradeRequired = errors.New("razordata: upgrade required")
    ErrReadOnly     = errors.New("razordata: read-only")
    ErrDeadlineExceeded = errors.New("razordata: deadline exceeded")
)
```

- All errors wrap: I/O errors → structural errors → API-level errors.
- Error messages are lowercase, no trailing punctuation.
- Errors are wrapped with `fmt.Errorf("razordata: %w", err)` to preserve the error chain.

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

## Clusters

### SY — System

**Responsibility:** Global init, config validation, graceful shutdown signal handling, version, stats aggregation.

**Key behaviors:**
- `Open`: validate `Options` (dir exists or `CreateIfMissing`, page size is power of 2, sizes are positive). Construct all subsystems. Call `WAL/RP.Replay()`.
- `Close`: set `closed = true`, flush all pending writes (memtable, WAL), stop background goroutines (compaction, epoch manager), close all subsystems in reverse order.
- `Stats`: aggregate stats from all subsystems into `EngineStats`.
- Graceful shutdown: on `SIGTERM` / `SIGINT`, call `Close()`. On `Close()` error, log and continue closing other subsystems.
- Version: `Version = "0.1.0"` (semantic versioning).

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
- `NewSession(engine) *session`: allocate from a `sync.Pool` (reuse inactive sessions).
- `Query` / `Exec`: acquire `mu`, bind params, call `SQL/EX.Exec()`, release `mu`.
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

## Implementation Plan

1. **`internal/SYS/AP/ap.go`** — `Engine` interface, `Options` struct, all error types, `EngineStats`.
2. **`internal/SYS/SY/sy.go`** — `engine` struct, `Open`, `Close`, `Stats`, version constant.
3. **`internal/SYS/SE/se.go`** — `session` struct, `NewSession`, `Query`, `Exec`, `Begin`, `Commit`, `Rollback`, `SetDeadline`, `Stats`.
4. **`internal/SYS/TX/tx.go`** — `transaction` struct, `Query`, `Exec`, `Commit`, `Rollback`, `Savepoint`, `RollbackTo`.
5. **`internal/SYS/ST/st.go`** — `stmt` struct, `Prepare`, `Bind`, `Query`, `Exec`, `Close`.
6. **`internal/SYS/SY/shutdown.go`** — graceful shutdown: signal handling (`os/signal`), drain pending writes, close subsystems.
7. **Integration tests:** `engine_test.go` — `Open`/`Close`, concurrent sessions, graceful shutdown, error types.
8. **Benchmark tests:** `engine_bench.go` — throughput benchmark: `go test -bench=BenchmarkEngine -benchtime=10s`.

## Open Issues

- Should sessions be pooled (reuse inactive sessions)? Yes, via `sync.Pool`.
- Should we support read-only mode (`ReadOnly = true`)? Yes, skip WAL writes, open files read-only.
- How to handle `SetDeadline` cancellation? Use `context.WithDeadline` internally.
- Should the engine support a metrics endpoint (Prometheus)? Future work — add an admin interface.