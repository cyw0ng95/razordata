# Razordata Architecture

> This document is the index. Full design details are in `design/subsystems/*.md`.

## Modules Overview

| Subsystem | Clusters | Function Domains |
|---|---|---|
| `LOG` | `LG`, `HK` | `LG` — slog wrapper, log levels, rotation. `HK` — async event hooks: trace, metrics, profiling. |
| `FIL` | `DF`, `MF`, `LF`, `FS` | `DF` — block I/O (pread/pwrite), O_DIRECT, checksum. `MF` — meta.razor read/write, magic/version. `LF` — WAL segment files, handle pool. `FS` — directory management, path validation. |
| `MEM` | `BF`, `PC`, `SP` | `BF` — buffer pool: clock-sweep LRU, pin/unpin, O(1) hash lookup. `PC` — page slots, checksum verification, warm hint file. `SP` — sync.Pool for page buffers and iterators. |
| `WAL` | `WR`, `FL`, `RP` | `WR` — sequential append, LSN allocation, segment rotation. `FL` — fsync on commit, batch flush, write barrier. `RP` — WAL replay on startup, checkpoint detection, segment truncation. |
| `ENG` | `LS`, `ID`, `TB`, `SC`, `DP` | `LS` — LSM tree: skiplist memtable, SST writer/reader, bloom filter, leveled compaction, manifest. `ID` — primary key index (v1: key = primary key). `TB` — create/drop table, schema catalog. `SC` — column types, constraints, table definitions. `DP` — row serialization, SST block encoding, value encoding. |
| `TXN` | `MV`, `LC`, `SN`, `VL` | `MV` — version chain (lock-free skip list), CAS insertion, per-thread arena. `LC` — hazard pointers, epoch-based reclamation, lock-free read coordination. `SN` — read view, epoch registration, thread-local arena. `VL` — commit protocol, write-write conflict detection, transaction slots. |
| `SQL` | `LX`, `PS`, `PL`, `EX`, `RE` | `LX` — tokenization, keyword lookup, error recovery. `PS` — recursive-descent parser, AST construction. `PL` — query planning, cost estimation, index selection, plan memoization. `EX` — streaming operator executor (SeqScan, IndexScan, Filter, Project, Sort, Limit, Insert, Update, Delete). `RE` — constant folding, predicate pushdown, subquery flattening. |
| `SYS` | `SY`, `AP`, `SE`, `TX`, `ST` | `SY` — init, config validation, graceful shutdown, version, stats aggregation. `AP` — public API: Engine/Session/Transaction/Stmt, Options, error types. `SE` — session lifecycle, goroutine-safety, deadline, session stats. `TX` — transaction context, commit/rollback, savepoints. `ST` — statement preparation, parameter binding, type coercion. |

## Clusters Overview

### LOG

| Cluster | File | Key Features |
|---|---|---|
| `LG` | `logger.go` | slog wrapper, atomic level, JSON/text output, file rotation, zero-allocation hot path |
| `HK` | `hook.go` | hook registry, bounded async dispatch, TraceHook, MetricHook, ProfileHook |

### FIL

| Cluster | File | Key Features |
|---|---|---|
| `DF` | `df.go` | pread/pwrite block I/O, O_DIRECT support (with buffered fallback), CRC32 checksum |
| `MF` | `mf.go` | meta.razor read/write, magic validation, version check, catalog root pointer |
| `LF` | `lf.go` | WAL segment file management, handle pool, ftruncate, segment rotation |
| `FS` | `fs.go` | directory management, SyncDir, path validation (no .. or symlinks), MkdirAll |

### MEM

| Cluster | File | Key Features |
|---|---|---|
| `BF` | `bf.go` | clock-sweep LRU, O(1) hash table lookup, atomic pin/unpin, concurrent load prevention |
| `PC` | `pc.go` | page slot management, checksum on read, warm hint file (serialize/deserialize) |
| `SP` | `sp.go` | sync.Pool for page-sized and iterator buffers, pre-allocated New, cap-based Put |

### WAL

| Cluster | File | Key Features |
|---|---|---|
| `WR` | `wr.go` | sequential append, varint encoding, LSN allocation, 64 MB segment rotation |
| `FL` | `fl.go` | fsync on commit, batch flush via WaitGroup, write barrier, SyncDir |
| `RP` | `rp.go` | checkpoint detection, LSN-ordered replay, segment truncation, recovery |

### ENG

| Cluster | File | Key Features |
|---|---|---|
| `LS` | `skiplist.go`, `memtable.go`, `sst_writer.go`, `sst_reader.go`, `manifest.go`, `flush.go`, `compaction.go`, `read.go` | lock-free skiplist memtable, SST block encoding (restart points, delta), bloom filter, leveled compaction, atomic manifest versioning, min-heap merge iterator |
| `ID` | `id.go` | (placeholder) — v1 uses primary key as the LSM key directly |
| `TB` | `tb.go` | CREATE TABLE, DROP TABLE, schema registry, system catalog access |
| `SC` | `sc.go` | column types (INT/BIGINT/FLOAT/BOOL/TEXT/BLOB), constraints, ValidateRow |
| `DP` | `dp.go` | row serialization, block encoding, value encode/decode, null bitmap |

### TXN

| Cluster | File | Key Features |
|---|---|---|
| `MV` | `version.go`, `arena.go` | lock-free version skip list (CAS head), per-thread arena, version commit (CAS endTS) |
| `LC` | `hazard.go`, `epoch.go` | hazard pointer set (publish/clear), epoch manager (register/exit/reclaim), background reclamation goroutine |
| `SN` | `snapshot.go` | read view creation, version chain traversal with hazard pointers, close and deregister |
| `VL` | `slot.go`, `validate.go`, `protocol.go`, `gc.go` | pre-allocated slot array (1024), Begin/Validate/Commit/Abort protocol, write-write conflict detection, GC of obsolete versions |

### SQL

| Cluster | File | Key Features |
|---|---|---|
| `LX` | `lx.go`, `token.go` | token value types, keyword map, peek/advance, error recovery (emit T_EOF) |
| `PS` | `ps.go`, `expr.go` | recursive descent parser, LL(1) grammar, operator precedence (comparison > add/sub > mul/div > unary > primary), line/col error reporting |
| `PL` | `pl.go` | plan memoization (AST fingerprint), cost estimation, index selection, sort pushdown, LIMIT pushdown |
| `EX` | `ex.go`, `operators.go`, `eval.go` | streaming pull-based executor, SeqScan/IndexScan/Filter/Project/Sort/Limit/Insert/Update/Delete, expression evaluation, context cancellation |
| `RE` | `re.go` | constant folding, predicate pushdown, subquery flattening (WHERE IN) |

### SYS

| Cluster | File | Key Features |
|---|---|---|
| `SY` | `sy.go`, `shutdown.go` | Engine.Open (subsystem construction, WAL replay), Engine.Close (flush, stop goroutines), graceful shutdown (SIGTERM/SIGINT), Stats aggregation, version |
| `AP` | `ap.go` | Engine/Session/Transaction/Stmt interfaces, Options struct, all error types, EngineStats |
| `SE` | `se.go` | session from sync.Pool, Query/Exec (with mu lock), SetDeadline (atomic.Value), SessionStats |
| `TX` | `tx.go` | transaction wrapper (wraps TXN.Tx), Savepoint/RollbackTo (readTS), Query/Exec (delegate to SQL/EX) |
| `ST` | `st.go` | Prepare (parse+plan, memoized), Bind (type validation), Query/Exec, Close (return to pool), type coercion |

## Dependency Order

```
LOG → FIL → MEM
           └→ WAL → ENG → TXN → SQL
                               └→ SYS
```

## Key Interfaces

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

// WAL — Write batch submitted to WAL
type WriteBatch struct {
    TxnID uint64
    Recs  []LogRecord
}

// FIL — Block buffer from the buffer pool
type Page struct {
    ID    uint64
    Data  []byte
    Dirty bool
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

Each subsystem has a dedicated design document:

| Subsystem | Detailed Design |
|---|---|
| `LOG` | `design/subsystems/LOG.md` |
| `FIL` | `design/subsystems/FIL.md` |
| `MEM` | `design/subsystems/MEM.md` |
| `WAL` | `design/subsystems/WAL.md` |
| `ENG` | `design/subsystems/ENG.md` |
| `TXN` | `design/subsystems/TXN.md` |
| `SQL` | `design/subsystems/SQL.md` |
| `SYS` | `design/subsystems/SYS.md` |