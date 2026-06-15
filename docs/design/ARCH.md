# Razordata Architecture

> This document is the index. Full design details are in `docs/design/subsystems/*.md`.

## Modules Overview

| Subsystem | Clusters | Function Domains |
|---|---|---|
| `LOG` | `LG`, `HK` | `LG` — slog wrapper, log levels, rotation. `HK` — async event hooks: trace, metrics, profiling. |
| `FIL` | `DF`, `MF`, `LF`, `FS`, `IO` | `DF` — block I/O (pread/pwrite), O_DIRECT, checksum. `MF` — meta.razor read/write, magic/version. `LF` — WAL segment files, handle pool. `FS` — directory management, path validation. `IO` — Linux io_uring async I/O for data files (AIO poll-based, batched CQEs). |
| `MEM` | `BF`, `PC`, `SP`, `OF` | `BF` — buffer pool: clock-sweep LRU, pin/unpin, O(1) hash lookup. `PC` — page slots, checksum verification, warm hint file. `SP` — sync.Pool for page buffers and iterators. `OF` — overflow block handling for large values exceeding page size. |
| `WAL` | `WR`, `FL`, `RP` | `WR` — sequential append, LSN allocation, segment rotation, columnar WAL encoding (REQ000314), LZ4 compression (REQ000315). `FL` — fsync on commit, batch flush, write barrier. `RP` — WAL replay on startup, checkpoint detection, segment truncation, parallel replay (REQ000316). |
| `ENG` | `LS`, `ID`, `TB`, `SC`, `DP`, `NM` | `LS` — LSM tree: skiplist memtable, SST writer/reader, bloom filter, leveled/tiered/hybrid compaction (REQ000320), rate-limited compaction (REQ000318), columnar SST block layout (REQ000314), per-block dictionary compression (REQ000297), subcompaction for L4+ (REQ000319), storage policy with tiered device placement (REQ000300). `ID` — B-tree persistent index for secondary indexes (btree.razor), cursor-based scan. `TB` — create/drop/alter table, schema catalog, foreign key enforcement, views, triggers. `SC` — column types, constraints (NOT NULL, DEFAULT, PRIMARY KEY, UNIQUE, CHECK, FOREIGN KEY), table definitions, integrity checks (REQ000124). `DP` — row serialization, SST block encoding, value encoding. `NM` — NUMA topology detection, worker pinning for first-touch allocation (REQ000309). |
| `TXN` | `MV`, `LC`, `SN`, `VL` | `MV` — version chain (lock-free skip list), CAS insertion, per-transaction arena, GC of obsolete versions. `LC` — hazard pointers, epoch-based reclamation (REQ000175), QSBR protocol (REQ000302), reclaim pool for deferred cleanup, goid tracking via atomic counter (REQ000181), epoch gosched for cooperative yielding. `SN` — read view, epoch registration, thread-local arena, version stack for multi-key reads. `VL` — commit protocol, write-write conflict detection, transaction slots, savepoint support. |
| `SQL` | `LX`, `PS`, `PL`, `EX`, `RE` | `LX` — tokenization, keyword lookup, error recovery. `PS` — recursive-descent parser, AST construction, CTE/recursive CTE parsing, window function parsing, ALTER TABLE parsing, subquery parsing. `PL` — query planning, cost estimation, index selection, plan memoization, selectivity estimation, hash agg planning (REQ000306). `EX` — streaming operator executor: SeqScan, IndexScan, Filter, Project, Sort, Limit, Insert, Update, Delete, HashJoin (radix-partitioned, REQ000312), NestedLoopJoin (outer/compound), Aggregate/AggregateDistinct, window functions, ALTER TABLE executor, foreign key validation (REQ000126), CTE/recursive CTE executor, views, triggers, JSON functions, datetime functions, PRAGMA support, integrity checks, EXPLAIN, compound SELECT (UNION/INTERSECT/EXCEPT), parallel sort (top-k/external merge), pipeline parallelism (fan-out/fan-in), SIMD-dispatched scalar funcs, decimal support, coerce/type coercion, hash agg, memo-optimized plan. `RE` — constant folding, predicate pushdown, subquery flattening, join reorder. |
| `SYS` | `SY`, `AP`, `SE`, `TX`, `ST` | `SY` — init, config validation, graceful shutdown, version, stats aggregation, signal handling. `AP` — public API: Engine/Session/Transaction/Stmt, Options, error types. `SE` — session lifecycle, goroutine-safety, deadline, session stats. `TX` — transaction context, commit/rollback, savepoints. `ST` — statement preparation, parameter binding, type coercion, prepared statement pool. |

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
├── FIL/   # File I/O (DF, MF, LF, FS, IO/io_uring)
├── MEM/   # Memory (BF, PC, SP, OF)
├── WAL/   # Write-ahead log (WR, FL, RP)
├── ENG/   # Storage engine (LS, ID, TB, SC, DP, NM)
├── TXN/   # Transaction (MV, LC, SN, VL)
├── SQL/   # SQL processing (LX, PS, PL, EX, RE)
└── SYS/   # System layer (SY, AP, SE, TX, ST)
```

## Detailed Design

Full design documents: `docs/design/subsystems/LOG.md` · `FIL.md` · `MEM.md` · `WAL.md` · `ENG.md` · `TXN.md` · `SQL.md` · `SYS.md`