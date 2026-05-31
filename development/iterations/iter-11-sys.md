# Iteration 11 — SYS + Integration

**Subsystem:** `SYS`
**Status:** pending
**Est. LOC:** ~3,000

## Overview

Public API and end-to-end integration. Top-level engine, session, transaction, statement. Depends on all subsystems.

## Dependencies

- Required: `TXN`, `ENG`, `LOG`
- Consumed interfaces: `TxnManager`, `Store`, `Logger`

## Design Alignment

Directory structure matches `design/subsystems/SYS.md`:
```
internal/SYS/
├── AP/               # API cluster
│   └── ap.go         # Engine/Session/Transaction/Stmt/Options, error types, EngineStats
├── SY/               # System cluster
│   ├── sy.go         # engine struct, Open/Close/Stats, version
│   ├── sy_test.go
│   ├── sy_bench.go   # engine throughput benchmark
│   └── shutdown.go   # graceful shutdown, signal handling
├── SE/               # Session cluster
│   └── se.go         # session struct, Query/Exec/Begin/Commit/Rollback/SetDeadline/Stats
├── TX/               # Transaction cluster
│   └── tx.go         # transaction struct, Query/Exec/Commit/Rollback/Savepoint/RollbackTo
└── ST/               # Statement cluster
    └── st.go         # stmt struct, Prepare/Bind/Query/Exec/Close
```

## Requirements

| ID | Requirement | Status |
|---|---|---|
| R01 | `Engine` interface: `Open(ctx, dir, opts) error`, `Close(ctx) error`, `Begin(ctx) (Session, error)`, `Stats() EngineStats` | pending |
| R02 | `Options` struct: Dir, PageSize (power of 2, default 4096), MemTableSize (default 64 MB), BufferPoolMB (default 256 MB), WALSizeMB (default 64 MB), MaxLevel (default 7), LogLevel (default slog.LevelInfo), LogFormat ("json"/"text"), ReadOnly (default false), CreateIfMissing (default true) | pending |
| R03 | Error types: ErrNotFound, ErrDuplicateKey, ErrLocked, ErrCorrupt, ErrSyntax, ErrTypeMismatch, ErrTxAborted, ErrIO, ErrUpgradeRequired, ErrReadOnly, ErrDeadlineExceeded | pending |
| R04 | Retry classification: retryable (ErrIO, ErrLocked) vs fatal (all others) | pending |
| R05 | `Engine.Open`: validate options (dir exists or CreateIfMissing, page size power of 2, positive sizes) | pending |
| R06 | `Engine.Open`: construct subsystems in dependency order (LOG → FIL → MEM → WAL → ENG → TXN) | pending |
| R07 | `Engine.Open`: call `WAL.Replay` on startup | pending |
| R08 | `Engine.Close`: flush memtable + WAL, stop background goroutines, close all subsystems in reverse order | pending |
| R09 | `Engine.Stats`: aggregate stats from all subsystems into `EngineStats` | pending |
| R10 | Version constant: `"0.1.0"` | pending |
| R11 | `Session` interface: `Query/Exec/Begin/Commit/Rollback/SetDeadline/Stats` | pending |
| R12 | Session goroutine-safety: mutex held for duration of each operation | pending |
| R13 | `SetDeadline(deadline time.Time)`: store in `atomic.Value`, respected on all subsequent operations | pending |
| R14 | `SessionStats`: ID, QueryCount, RowsReturned, BytesRead, BytesWritten, ActiveTXN (atomic) | pending |
| R15 | `Transaction` interface: `Query/Exec/Commit/Rollback/Savepoint/RollbackTo` | pending |
| R16 | `Stmt` interface: `Query/Exec/Close` | pending |
| R17 | `Stmt.Prepare(sql)`: parse SQL, build AST, plan (memoized), store plan | pending |
| R18 | `Stmt.Bind(args ...any)`: validate parameter count and types against schema | pending |
| R19 | Graceful shutdown: `os.Signal` handling for SIGTERM/SIGINT | pending |
| R20 | Shutdown: drain pending writes (memtable + WAL), close all subsystems | pending |
| R21 | End-to-end CRUD: `CREATE TABLE` → `INSERT` → `SELECT` (WHERE/ORDER BY/LIMIT) → `UPDATE` → `DELETE` | pending |
| R22 | End-to-end transaction: `BEGIN` → `INSERT` → `COMMIT` → data persists | pending |
| R23 | End-to-end rollback: `BEGIN` → `INSERT` → `ROLLBACK` → data absent | pending |
| R24 | Concurrent sessions: two sessions reading/writing simultaneously — no data loss, no corruption | pending |
| R25 | Graceful shutdown test: SIGTERM → flush + close → clean restart | pending |
| R26 | `go vet ./internal/SYS/...` zero warnings | pending |
| R27 | `go test ./internal/SYS/... -race -count=1` all green | pending |
| R28 | Benchmark: throughput (INSERT/SELECT per second) under `go test -bench=.` | pending |

## Implementation

### Phase 1: API (`AP/ap.go`)

1. `Options` struct as described
2. All error type variables: `var ErrNotFound = errors.New("razordata: key not found")`
3. `EngineStats`: LSMTree, BufferPool, WAL, Compaction stats (from each subsystem)
4. `Engine` interface defined

### Phase 2: Engine (`SY/sy.go`)

1. `engine` struct: dir, opts, log, fil, mem, wal, eng, txn
2. `Open`: validate opts → new Logger → new FileManager → new BufferPool → new Writer → new Store → new TxnManager → call Replay
3. `Close`: set closed=true → flush memtable → sync WAL → stop background goroutines → close all in reverse
4. `Begin`: return new Session
5. `Stats`: aggregate from all subsystems

### Phase 3: Session (`SE/se.go`)

1. `session` struct: engine, txn (Transaction), params, deadline (atomic.Value), id, stats, mu
2. `NewSession(engine) *session`: from `sync.Pool`
3. `Query/Exec`: acquire mu → bind params → execute → return. Respect deadline via context.
4. `Begin/Commit/Rollback`: manage session.txn. Return ErrLocked if txn already active.
5. `SetDeadline`: store in atomic.Value
6. `Stats`: return SessionStats

### Phase 4: Transaction (`TX/tx.go`)

1. `transaction` struct: session, readTS, savepoints (map[string]uint64)
2. Wraps TXN Tx. Delegates Query/Exec to Executor.
3. `Savepoint(name)`: store readTS. `RollbackTo(name)`: restore readTS.

### Phase 5: Statement (`ST/st.go`)

1. `stmt` struct: sql, plan, params, engine
2. `Prepare`: parse → rewrite → plan (memoized)
3. `Bind`: store args, validate count
4. `Query/Exec`: execute plan with bound args
5. `Close`: return to pool

### Phase 6: Shutdown (`SY/shutdown.go`)

1. `os.Signal` goroutine: listen on channel for SIGTERM/SIGINT
2. On signal: call Engine.Close with timeout
3. Drain: wait for pending writes, then close

## Deferred to v2

- Session pooling (`sync.Pool`)
- Prometheus metrics endpoint
- Read-only mode (`ReadOnly = true`)
- Admin interface