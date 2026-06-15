# Razordata Architecture

> This document is the index. Full design details are in `docs/design/subsystems/*.md`.

## Modules Overview

| Subsystem | Clusters | Function Domains |
|---|---|---|
| `LOG` | `LG`, `HK` | `LG` — slog wrapper, log levels, rotation. `HK` — async event hooks: trace, metrics, profiling. |
| `FIL` | `DF`, `MF`, `LF`, `FS` | `DF` — block I/O (pread/pwrite), O_DIRECT, checksum. `MF` — meta.razor read/write, magic/version. `LF` — WAL segment files, handle pool. `FS` — directory management, path validation. |
| `MEM` | `BF`, `PC`, `SP` | `BF` — buffer pool: clock-sweep LRU, pin/unpin, O(1) hash lookup. `PC` — page slots, checksum verification, warm hint file. `SP` — sync.Pool for page buffers and iterators. |
| `WAL` | `WR`, `FL`, `RP` | `WR` — sequential append, LSN allocation, segment rotation. `FL` — fsync on commit, batch flush, write barrier. `RP` — WAL replay on startup, checkpoint detection, segment truncation. |
| `ENG` | `LS`, `ID`, `TB`, `SC`, `DP` | `LS` — LSM tree: skiplist memtable, SST writer/reader, bloom filter, leveled compaction, manifest. `ID` — primary key index (v1: key = primary key). `TB` — create/drop table, schema catalog. `SC` — column types, constraints, table definitions. `DP` — row serialization, SST block encoding, value encoding. |
| `TXN` | `MV`, `LC`, `SN`, `VL` | `MV` — version chain (lock-free skip list), CAS insertion, per-thread arena. `LC` — hazard pointers, epoch-based reclamation, lock-free read coordination. `SN` — read view, epoch registration, thread-local arena. `VL` — commit protocol, write-write conflict detection, transaction slots. |
| `SQL` | `LX`, `PS`, `PL`, `EX`, `RE` | `LX` — tokenization, keyword lookup, error recovery. `PS` — recursive-descent parser, AST construction. `PL` — query planning, cost estimation, index selection, plan memoization. `EX` — streaming operator executor. `RE` — constant folding, predicate pushdown, subquery flattening. |
| `SYS` | `SY`, `AP`, `SE`, `TX`, `ST` | `SY` — init, config validation, graceful shutdown, version, stats aggregation. `AP` — public API: Engine/Session/Transaction/Stmt, Options, error types. `SE` — session lifecycle, goroutine-safety, deadline, session stats. `TX` — transaction context, commit/rollback, savepoints. `ST` — statement preparation, parameter binding, type coercion. |

## Dependency Order

```
LOG → FIL → MEM → WAL → ENG → TXN → SQL → SYS
```

## Cross-Subsystem Interfaces

```go
// ENG — Iterator returned by storage reads
type Iterator interface {
    Next() bool
    Key() []byte
    Value() []byte
    Err() error
    Close()
}

// TXN — Transaction handle
type Tx interface {
    Get(ctx context.Context, key []byte) ([]byte, error)
    Insert(ctx context.Context, key, value []byte) error
    Delete(ctx context.Context, key []byte) error
    Commit(ctx context.Context) error
    Rollback(ctx context.Context) error
}

// SQL — Operator in the executor tree
type Operator interface {
    Next(ctx context.Context) (Row, error)
    Close() error
}
```

## Directory Structure

```
internal/
├── LOG/   # Logging (LG, HK)
├── FIL/   # File I/O (DF, MF, LF, FS)
├── MEM/   # Memory (BF, PC, SP)
├── WAL/   # Write-ahead log (WR, FL, RP)
├── ENG/   # Storage engine (LS, ID, TB, SC, DP)
├── TXN/   # Transaction (MV, LC, SN, VL)
├── SQL/   # SQL processing (LX, PS, PL, EX, RE)
└── SYS/   # System layer (SY, AP, SE, TX, ST)
```

## Detailed Design

Full design documents: `docs/design/subsystems/LOG.md` · `FIL.md` · `MEM.md` · `WAL.md` · `ENG.md` · `TXN.md` · `SQL.md` · `SYS.md`