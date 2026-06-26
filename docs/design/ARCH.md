# Razordata Architecture

> This document is the index. Full design details are in `docs/design/subsystems/*.md`.

## Modules Overview

| Subsystem | Clusters | Function Domains |
|---|---|---|
| `LOG` | `LG`, `HK` | `LG` — slog wrapper, log levels, rotation. `HK` — async event hooks: trace, metrics, profiling. |
| `FIL` | `DF`, `MF`, `LF`, `FS`, `IO` | `DF` — block I/O (pread/pwrite), O_DIRECT, checksum. `MF` — meta.razor read/write, magic/version. `LF` — WAL segment files, handle pool. `FS` — directory management, path validation. `IO` — Linux io_uring async I/O for data files (AIO poll-based, batched CQEs). |
| `MEM` | `BF`, `PC`, `SP`, `OF` | `BF` — buffer pool: clock-sweep LRU, pin/unpin, O(1) hash lookup. `PC` — page slots, checksum verification, warm hint file. `SP` — sync.Pool for page buffers and iterators. `OF` — overflow block handling for large values exceeding page size. |
| `WAL` | `WR`, `FL`, `RP` | `WR` — sequential append, LSN allocation, segment rotation, columnar WAL encoding, LZ4 compression. `FL` — fsync on commit, batch flush, write barrier. `RP` — WAL replay on startup, checkpoint detection, segment truncation, parallel replay. |
| `ENG` | `LS`, `ID`, `TB`, `CT`, `SC`, `DP`, `NM` | `LS` — LSM tree: skiplist memtable, SST writer/reader, bloom filter, leveled/tiered/hybrid compaction, rate-limited compaction, columnar SST block layout, per-block dictionary compression, subcompaction for L4+, storage policy with tiered device placement, SST page cache. `ID` — B-tree persistent index for secondary indexes (btree.razor), cursor-based scan. `TB` — create/drop/alter table, foreign key enforcement, views, triggers. `CT` — persistent catalog storage, schema versioning, bootstrap, encode/decode. Shared by TB and LS. `SC` — column types, constraints (NOT NULL, DEFAULT, PRIMARY KEY, UNIQUE, CHECK, FOREIGN KEY), table definitions, integrity checks. `DP` — row serialization, SST block encoding, value encoding. `NM` — NUMA topology detection, worker pinning for first-touch allocation. |
| `TXN` | `MV`, `LC`, `SN`, `VL` | `MV` — version chain (lock-free skip list), CAS insertion, per-transaction arena with per-NUMA pools, GC of obsolete versions. `LC` — hazard pointers, epoch-based reclamation, QSBR protocol, reclaim pool for deferred cleanup, goid tracking via atomic counter, epoch gosched for cooperative yielding. `SN` — read view, epoch registration, thread-local arena, version stack for multi-key reads. `VL` — commit protocol, write-write conflict detection, transaction slots, savepoint support. |
| `SQF` | `LX`, `PS`, `RE`, `PL` | `LX` — tokenization, keyword lookup, error recovery. `PS` — recursive-descent parser, AST construction with visitor pattern, CTE/recursive CTE parsing, window function parsing, ALTER TABLE parsing, subquery parsing. `PL` — query planning, cost estimation, index selection, plan memoization, selectivity estimation, hash agg planning, N3 join ordering, NDV-based selectivity. `RE` — constant folding, predicate pushdown, subquery flattening, join reorder. |
| `SQB` | `EX` | `EX` — streaming operator executor: SeqScan, IndexScan, Filter, Project, Sort, Limit, Insert, Update, Delete, HashJoin (radix-partitioned), NestedLoopJoin (outer/compound), HashCrossJoin, Aggregate/AggregateDistinct, window functions, ALTER TABLE executor, foreign key validation, CTE/recursive CTE executor, views, triggers, JSON functions, datetime functions, PRAGMA support, integrity checks, EXPLAIN, compound SELECT (UNION/INTERSECT/EXCEPT), parallel sort (top-k/external merge), pipeline parallelism (fan-out/fan-in), SIMD-dispatched scalar funcs, decimal support, coerce/type coercion, hash agg, memo-optimized plan, ExecContext threading, adaptive query compilation (ADQC), plan cache, ANALYZE table statistics. ST (Statistics) and QC (Query Cache) clusters deferred — see iter-35 plan. |
| `SYS` | `SY`, `AP`, `SE`, `TX`, `ST` | `SY` — init, config validation, graceful shutdown (6-phase), version, stats aggregation, signal handling. `AP` — public API: Engine/Session/Transaction/Stmt, Options, error types. `SE` — session lifecycle, goroutine-safety, deadline, session stats. `TX` — transaction context, commit/rollback, savepoints. `ST` — statement preparation, parameter binding, type coercion, prepared statement pool. |

## Dependency Order

```
LOG → FIL → MEM → WAL → ENG → TXN → SQF → SQB → SYS
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

// SQF/SQB — Operator in the executor tree (defined in SQB/EX, consumed by SQF/PL)
type Operator interface {
    Next(ctx context.Context) (Row, error)
    Close() error
}
```

## Error Contract

All user-facing errors are classified by `AP.Kind` and wrapped into `AP.Error` at subsystem boundaries. Lower layers define their own sentinels for internal use but must wrap into `AP.Error` when propagating to higher layers.

**Cross-layer wrapping rule:** When an error crosses a subsystem boundary (e.g., ENG → SYS, SQF → SQB), the receiving layer wraps it:
```go
// In SYS/SY when ENG returns an error:
if err != nil {
    return AP.Wrap(AP.KindCorrupt, err) // preserves chain via Unwrap()
}
```

**Classification:** `AP.IsKind(err, kind)` uses `errors.As` to traverse the chain, matching errors from any layer. This replaces per-sentinel `errors.Is` checks for classification purposes.

**Per-package sentinels** (internal use, not exposed to callers):
- `ENG/LS`: `ErrNotFound`, `ErrClosed`, `ErrBloomMiss`, `ErrCatalogCorrupt`, etc.
- `WAL/WR`: `ErrTruncatedRecord`, `ErrCorrupt`, etc.
- `TXN/MV`: `ErrNotFound`, `ErrDuplicateKey`, `ErrArenaExhausted`, etc.
- `SQB/EX`: `ErrNotImplemented`, `ErrNoRows`, `ErrEval`, etc.

Each package uses a consistent prefix (`"ls: "`, `"wr: "`, `"txn: "`, `"ex: "`) for debuggability.

## Shared Constants & Platform Packages

Several constants and utilities are duplicated across packages. The following rules define what must be centralized:

**Block/page size:** Single source of truth is `FIL/DF.DefaultBlockSize` (4096). All other packages (`MEM/SP`, `ENG/ID`, `FIL/MF`) reference it; none define independent copies.

**WAL segment size:** Single source of truth is `WAL/WR.SegSize` (64 MiB). `WAL/RP` references it; no private copies.

**Memtable/buffer pool defaults:** Single source of truth is `SYS/AP.Options` defaults (`DefaultPageSize`, `DefaultMemTableSize`, `DefaultBufferPoolMB`, etc.). Subsystem `Options` structs are populated from `AP.Options` at construction time, not from independent defaults.

**Catalog logic:** `ENG/LS` and `ENG/TB` share identical catalog magic bytes, header size, schema versions, bootstrap, and encode/decode logic. Shared code lives in `ENG/catalog/`; LS adds index/stats extensions on top.

**Lifecycle guards:** All `closed` guards use `atomic.Bool` (Go 1.19+). No custom `atomicBool` wrappers, no plain `bool` for goroutine-shared state.

**Stats types:** `SYS/AP` embeds subsystem stats types (`ls.ReadStats`, `bf.BufferStats`, `rp.Stats`, `vl.TxnStats`) rather than re-declaring subset structs. This prevents field drift.

**CRC32 polynomial:** `ENG/LS` uses Koopman (better error detection for SST blocks); all other packages use IEEE. This is intentional and documented.

## Directory Structure

```
cmd/
└── razor/           # rdcli entry point (thin, no DB logic)

internal/
├── LOG/   # Logging (LG, HK)
├── FIL/   # File I/O (DF, MF, LF, FS, IO/io_uring)
├── MEM/   # Memory (BF, PC, SP, OF)
├── WAL/   # Write-ahead log (WR, FL, RP)
├── ENG/   # Storage engine (LS, ID, TB, CT, SC, DP, NM)
├── TXN/   # Transaction (MV, LC, SN, VL)
├── SQF/   # SQL Frontend (LX, PS, RE, PL)
├── SQB/   # SQL Backend  (EX)
└── SYS/   # System layer (SY, AP, SE, TX, ST)
```

## Detailed Design

Full design documents: `docs/design/subsystems/LOG.md` · `FIL.md` · `MEM.md` · `WAL.md` · `ENG.md` · `TXN.md` · `SQF.md` · `SQB.md` · `SYS.md` · `CLI.md` · `TUI.md`

## SQL Surface

### DDL — Data Definition Language

The engine supports `CREATE TABLE` with column types and `PRIMARY KEY`, `DROP TABLE`, `NOT NULL` constraint enforcement, `DEFAULT` value substitution, and online schema migration (`ALTER TABLE ADD/DROP COLUMN` without table copy).

### DML — Data Manipulation Language

Full DML support: `INSERT` with column list, `UPDATE` with `WHERE`, `DELETE` with `WHERE`, `SELECT` with `WHERE` / `ORDER BY` / `LIMIT` / `OFFSET`. Aggregate functions (`COUNT(*)`, `SUM`, `AVG`, `MIN`, `MAX`). `DISTINCT`. Subqueries (`IN`, `EXISTS`, scalar). `JOIN` (INNER, CROSS). Compound `SELECT` (`UNION`, `INTERSECT`, `EXCEPT`). Window functions (`ROW_NUMBER`, `RANK`, `LAG`, `LEAD`). UPSERT (`INSERT ... ON CONFLICT`). `RETURNING` clause. `WITH` (CTE) and recursive CTE.

### API — Public API

Parameter binding via `?` placeholders. `EXPLAIN <query>`. Prepared statements with type coercion.

### Observability

Query tracing via `TraceHook`. Latency histograms via `MetricHook`. CPU/heap profiling on error. Structured stats aggregation (`Engine.Stats`).

### Quality Gates

- `go vet ./...` zero warnings
- `gofmt -s -l .` no drift
- `go test ./... -race -count=1` all green
- Property-based tests for storage (crash/recovery)
- `Benchmark*` for every storage component
- No allocations in hot paths
- All public API methods goroutine-safe
- `log/slog` only — no `fmt.Printf` in library
- Error messages: lowercase, no trailing punctuation

### SQLLogicTest Corpus Integration

SQLLogicTest corpus mirror as git submodule (`tests/sqlcmp/corpus`). SLT test file parser, driver interface, type-aware result diff. Dual runner: same SQL on Razordata + `modernc.org/sqlite` oracle; result-set diff after normalization. JUnit XML output for CI. Coverage snapshot with regression check.
