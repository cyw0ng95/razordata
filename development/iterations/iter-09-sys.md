# Iteration 9 — SYS + Integration

**Subsystem:** `SYS`
**Status:** done
**Est. LOC:** ~3,000
**Actual LoC:** ~1,600 (5 packages: AP, SY, SE, TX, ST)
**Latest:** v0.6.3 — test reorganization into function domains
**Notes:** v1 transaction isolation is **read-uncommitted** between
transactions and **read-your-own-writes** within a transaction. Writes
land in the engine on the calling goroutine; the VL Tx is acquired for
concurrency control and abort accounting. ROLLBACK restores the
pre-transaction value from a per-tx snapshot. See R29.

## Overview

Public API and end-to-end integration. Top-level engine, session, transaction, statement. Depends on all subsystems.

## Dependencies

- Required: `TXN`, `ENG`, `LOG`
- Consumed interfaces: `TxnManager`, `Store`, `Logger`

## Test Organization (v0.6.3)

Tests are organized into function-domain subdirectories matching
`design/ARCH.md`:

```
internal/SYS/
├── AP/ap_test.go           # API tests: error types, constants, version
├── SE/session_test.go      # Session tests: lifecycle, stats, deadline
├── ST/stmt_test.go         # Statement tests: prepare, exec, close
├── SY/
│   ├── engine_test.go      # Engine tests: Open/Close/Stats
│   ├── crud_test.go        # CRUD operations tests
│   ├── e2e_test.go         # End-to-end integration tests
│   └── bench_test.go       # Throughput benchmarks
└── TX/
    └── transaction_test.go # Transaction tests: savepoints, commit/rollback
```

Note: `init_test.go` files in ST/, SY/, and TX/ ensure SE package
initialization registers Session constructor.

## Design Alignment

Directory structure matches `design/subsystems/SYS.md`:
```
internal/SYS/
├── AP/               # API cluster
│   ├── ap.go         # Engine/Session/Transaction/Stmt/Options, error types, EngineStats
│   └── ap_test.go    # API tests
├── SY/               # System cluster
│   ├── sy.go         # engine struct, Open/Close/Stats, version
│   ├── shutdown.go   # graceful shutdown, signal handling
│   ├── engine_test.go
│   ├── crud_test.go
│   ├── e2e_test.go
│   ├── bench_test.go
│   └── init_test.go
├── SE/               # Session cluster
│   ├── se.go         # session struct, Query/Exec/Begin/Commit/Rollback/SetDeadline/Stats
│   └── session_test.go
├── TX/               # Transaction cluster
│   ├── tx.go         # transaction struct, Query/Exec/Commit/Rollback/Savepoint/RollbackTo
│   ├── transaction_test.go
│   └── init_test.go
└── ST/               # Statement cluster
    ├── st.go         # stmt struct, Prepare/Bind/Query/Exec/Close
    ├── stmt_test.go
    └── init_test.go
```

Note: Top-level `SYS` directory contains no source code files directly —
all code resides in function-domain subdirectories per ARCH.md design.

## Requirements

| ID | Requirement | Status |
|---|---|---|
| R01 | `Engine` interface: `Open(ctx, dir, opts) error`, `Close(ctx) error`, `Begin(ctx) (Session, error)`, `Stats() EngineStats` | done |
| R02 | `Options` struct: Dir, PageSize (power of 2, default 4096), MemTableSize (default 64 MB), BufferPoolMB (default 256 MB), WALSizeMB (default 64 MB), MaxLevel (default 7), LogLevel (default slog.LevelInfo), LogFormat ("json"/"text"), ReadOnly (default false), CreateIfMissing (default true) | done |
| R03 | Error types: ErrNotFound, ErrDuplicateKey, ErrLocked, ErrCorrupt, ErrSyntax, ErrTypeMismatch, ErrTxAborted, ErrIO, ErrUpgradeRequired, ErrReadOnly, ErrDeadlineExceeded | done |
| R04 | Retry classification: retryable (ErrIO, ErrLocked) vs fatal (all others) | done |
| R05 | `Engine.Open`: validate options (dir exists or CreateIfMissing, page size power of 2, positive sizes) | done |
| R06 | `Engine.Open`: construct subsystems in dependency order (LOG → FIL → MEM → WAL → ENG → TXN) | done |
| R07 | `Engine.Open`: call `WAL.Replay` on startup | done |
| R08 | `Engine.Close`: flush memtable + WAL, stop background goroutines, close all subsystems in reverse order | done |
| R09 | `Engine.Stats`: aggregate stats from all subsystems into `EngineStats` | done |
| R10 | Version constant: `"0.5.0"` | done |
| R11 | `Session` interface: `Query/Exec/Begin/Commit/Rollback/SetDeadline/Stats` | done |
| R12 | Session goroutine-safety: mutex held for duration of each operation | done |
| R13 | `SetDeadline(deadline time.Time)`: store in `atomic.Value`, respected on all subsequent operations | done |
| R14 | `SessionStats`: ID, QueryCount, RowsReturned, BytesRead, BytesWritten, ActiveTXN (atomic) | done |
| R15 | `Transaction` interface: `Query/Exec/Commit/Rollback/Savepoint/RollbackTo` | done |
| R16 | `Stmt` interface: `Query/Exec/Close` | done |
| R17 | `Stmt.Prepare(sql)`: parse SQL, build AST, plan (memoized), store plan | done |
| R18 | `Stmt.Bind(args ...any)`: validate parameter count and types against schema | done |
| R19 | Graceful shutdown: `os.Signal` handling for SIGTERM/SIGINT | done |
| R20 | Shutdown: drain pending writes (memtable + WAL), close all subsystems | done |
| R21 | End-to-end CRUD: `CREATE TABLE` → `INSERT` → `SELECT` (WHERE/ORDER BY/LIMIT) → `UPDATE` → `DELETE` | done |
| R22 | End-to-end transaction: `BEGIN` → `INSERT` → `COMMIT` → data persists | done |
| R23 | End-to-end rollback: `BEGIN` → `INSERT` → `ROLLBACK` → data absent | done |
| R24 | Concurrent sessions: two sessions reading/writing simultaneously — no data loss, no corruption | done |
| R25 | Graceful shutdown test: SIGTERM → flush + close → clean restart | done |
| R26 | `go vet ./internal/SYS/...` zero warnings | done |
| R27 | `go test ./internal/SYS/... -race -count=1` all green | done |
| R28 | Benchmark: throughput (INSERT/SELECT per second) under `go test -bench=.` | done |
| R29 | v1 transaction isolation: read-uncommitted between transactions, read-your-own-writes within. ROLLBACK restores the pre-tx value via shadow writeSet | done |

## Implementation

### Phase 1: API (`AP/ap.go`)

1. `Options` struct as described
2. All error type variables: `var ErrNotFound = errors.New("razordata: key not found")`
3. `EngineStats`: LSMTree, BufferPool, WAL, Compaction stats (from each subsystem)
4. `Engine` interface defined

### Phase 2: Engine (`SY/sy.go`)

1. `engine` struct: dir, opts, log, fil, mem, wal, eng, txn
2. `Open`: validate opts → new Logger → new FileManager (FS) → new SegmentManager (LF) → new BlockDevice (DF) → new SyncPool (SP) → new BufferPool (BF) → new Writer (WR) → new Flusher (FL) → new Replayer (RP) → open Store (LS) → new TxnManager (VL) → call Replay
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

1. `transaction` struct: session, tx (VL Tx), writeSet (key → pre-tx value), savepoints (map[string]uint64)
2. Wraps VL Tx. Delegates Query/Exec to Executor.
3. **Shadow writeSet for R29 ROLLBACK**: every DML write records the pre-tx value (or "absent") so ROLLBACK can restore.
4. `Savepoint(name)`: snapshot current writeSet+overrides. `RollbackTo(name)`: restore.

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