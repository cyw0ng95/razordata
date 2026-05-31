# Iteration 11 — SYS + Integration

**Subsystem:** `SYS`
**Status:** pending
**Est. LOC:** ~3,000

## Overview

Public API and end-to-end integration. Top-level engine, session, transaction, statement. Depends on all subsystems.

## Requirements

| ID | Requirement | Status |
|---|---|---|
| R01 | `Engine` interface: `Open/Close/Begin/Stats` | pending |
| R02 | `Options` struct: Dir, PageSize (power of 2), MemTableSize, BufferPoolMB, WALSizeMB, MaxLevel, LogLevel, LogFormat, ReadOnly, CreateIfMissing | pending |
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
| R13 | `SetDeadline`: store deadline in `atomic.Value`, respected on all subsequent operations | pending |
| R14 | `Session.Stats`: query count, rows returned, bytes read/written | pending |
| R15 | `Transaction` interface: `Query/Exec/Commit/Rollback/Savepoint/RollbackTo` | pending |
| R16 | `Stmt` interface: `Query/Exec/Close` | pending |
| R17 | `Stmt.Prepare`: parse SQL, build AST, plan (memoized), store plan | pending |
| R18 | `Stmt.Bind`: validate parameter count and types against schema | pending |
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

```
internal/SYS/
├── ap.go          # Engine/Session/Transaction/Stmt/Options, error types, EngineStats
├── engine.go      # engine struct, Open/Close/Stats
├── session.go     # session struct, Query/Exec/Begin/Commit/Rollback/SetDeadline/Stats
├── transaction.go # transaction struct, Query/Exec/Commit/Rollback/Savepoint/RollbackTo
├── statement.go   # stmt struct, Prepare/Bind/Query/Exec/Close
└── shutdown.go    # graceful shutdown, signal handling
```

## Deferred

- Session pooling (`sync.Pool`)
- Prometheus metrics endpoint
- Read-only mode (`ReadOnly = true`)
- Admin interface