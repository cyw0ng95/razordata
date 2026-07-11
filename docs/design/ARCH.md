# Razordata Architecture

> This document is the index. Full design details are in `docs/design/subsystems/*.md`.

## Modules Overview

| Subsystem | Clusters | Function Domains |
|---|---|---|
| `LOG` | `LG`, `HK`, `EC` | `LG` — slog wrapper, log levels, rotation. `HK` — async event hooks: trace, metrics, profiling. `EC` — structured error types: Error/Kind/SQLSTATE/Code, classification, wrapping. All subsystems import LOG/EC. |
| `FIL` | `DF`, `MF`, `LF`, `FS`, `IO` | `DF` — block I/O (pread/pwrite), O_DIRECT, checksum. `MF` — meta.razor read/write, magic/version. `LF` — WAL segment files, handle pool. `FS` — directory management, path validation. `IO` — Linux io_uring async I/O for data files (AIO poll-based, batched CQEs). |
| `MEM` | `BF`, `SP`, `OF` | `BF` — buffer pool: sharded clock-sweep eviction, W-TinyLFU admission (4-row count-min sketch), pin/unpin, O(1) hash lookup, NUMA-aware shard allocation, hugetlbfs hint file (warm-start), optional `MAP_HUGETLB`. `SP` — sync.Pool for page buffers and iterators, with optional hugetlbfs-backed pre-allocation. `OF` — overflow block handling for large values exceeding page size. |
| `WAL` | `WR`, `FL`, `RP` | `WR` — sequential append, LSN allocation, segment rotation, columnar WAL encoding, LZ4 compression. `FL` — fsync on commit, batch flush, write barrier. `RP` — WAL replay on startup, checkpoint detection, segment truncation, parallel replay. |
| `ENG` | `LS`, `ID`, `TB`, `CT`, `SC`, `DP`, `NM` | `LS` — LSM tree: lock-free skiplist memtable (sharded), SST writer/reader, Ribbon filter (v2 SST, FPR ≈ 0.39%) with Bloom (v1) backward compat, leveled/tiered/hybrid compaction, rate-limited compaction, columnar SST block layout, per-block dictionary compression, subcompaction for L4+, storage policy with tiered device placement, SST page cache, L0 cache, borrowed/epoch iterators, index store. `ID` — B-tree persistent index for secondary indexes (btree.razor), cursor-based scan. `TB` — table DDL (delegate FK enforcement to `SQB/UT/fk.go` + `SQB/DT/fk_queue.go`; views/triggers to `SQB/WT`). `CT` — persistent catalog storage (`catalog.dat`, RCAT magic, V1/V2 schema versions), bootstrap, encode/decode. Shared by TB and LS. `SC` — column types, constraints (NOT NULL, DEFAULT, PRIMARY KEY, UNIQUE, CHECK, FOREIGN KEY with MATCH FULL/PARTIAL/SIMPLE and DEFERRABLE INITIALLY DEFERRED/IMMEDIATE), table definitions, integrity checks. `DP` — row serialization, SST block encoding, value encoding. `NM` — NUMA topology detection, worker pinning for first-touch allocation. |
| `TXN` | `MV`, `LC`, `SN`, `VL` | `MV` — version chain (lock-free skip list), CAS insertion, per-transaction arena with per-NUMA pools, GC of obsolete versions. `LC` — hazard pointers, epoch-based reclamation, QSBR protocol, reclaim pool for deferred cleanup, goid tracking via atomic counter, epoch gosched for cooperative yielding. `SN` — read view, epoch registration, thread-local arena, version stack for multi-key reads. `VL` — commit protocol, write-write conflict detection, transaction slots, savepoint support. |
| `SQF` | `LX`, `PS`, `RE`, `PL` | `LX` — tokenization, keyword lookup, error recovery. `PS` — recursive-descent parser, AST construction, CTE/recursive CTE parsing, window function parsing, ALTER TABLE parsing, subquery parsing. (Visitor pattern file `visitor.go` removed post-SQO cleanup — REQ001493.) `PL` — slimmed after SQO extraction: retains only `types.go` (Operator/Row aliases), `comparisons.go` (CompareValue/EqualValueValue shared with EV/OP/AG), `memo.go` + `learned.go` thin re-exports of `SQO/MM`. **Planning logic lives in `SQB/EX/planner.go` (production path) and `SQO/` (scaffolded, not wired in).** `RE` — constant folding, predicate pushdown, subquery flattening, SQL formatter (`SQF/RE/format.go` for EXPLAIN output). |
| `SQO` | `CP`, `CM`, `SL`, `JN`, `MM`, `RW`, `CO` | **SQL Query Optimizer** (see `docs/design/subsystems/SQO.md`). `CP` — Cost Params struct, defaults, calibration harness. `CM` — per-operator cost formulas (legacy + PG-style). `SL` — Selectivity estimation (NDV, MCV, range, OR-chain); hosts the `StatsCatalog` interface. `JN` — Join ordering (N3, bushy, multi-start). `MM` — Memoization (xxhash64 LRU) + LEO learned feedback. `RW` — Rewriting (predicate pushdown, subquery flatten, constant fold). `CO` — Core/Orchestrator (Optimizer interface, Plan entry, SELECT/DML/DDL dispatch). Sits between `SQF` (parser) and `SQB` (executor). **Status**: 7 clusters scaffolded; `Optimizer` interface and `Executor.RegisterOptimizer` injection seam wired (commit `2168426`); **production planner is still `SQB/EX/planner.go`** — no production caller invokes `RegisterOptimizer`. Migration REQs REQ001473-REQ001481 track the production wiring. |
| `SQB` | `EX`, `OP`, `EV`, `AG`, `AD`, `WT`, `UT`, `DT` | `DT` — shared data types: Row/Value/Operator/ExecContext/Store/StatsCatalog, schema registry, view/matview/index/catalog registries, session counters (SessionCounterAccessor), AST traversal helpers (ContainsAggregate, ContainsWindowFunc), value conversion utilities (ValueFromAny, ValueToString, Compare, ToInt64, EqualValueAny, IsValueTruthy), FK deferrable queue (`fk_queue.go`). Hosts the **canonical `Operator` interface** (consumed by `SQF/PL` and every SQB cluster; `SQF/PL.Operator` is a re-export alias). `EX` — executor factory, Executor, `subq.go` (injectOuter + runSubqueryPlan), `plan_node.go` (dispatcher subtree), `dispatch.go`, `source.go`, `stream.go`, `ex.go`, `executor_factory.go`. Active planner remains here (`planner.go`, ~917 lines, including `cost.go`, `join_order.go`, `predicate.go`, `view_subquery.go`) — production path until SQO migration completes. `OP` — fully extracted operator library: `seq_scan.go`, `index_scan.go`, `index_only_scan.go`, `bitmap_scan.go`, `filter.go`, `project.go`, `sort_parallel.go`, `topn_sort.go`, `nljoin.go`, `hashjoin.go`, `mergejoin.go`, `parallel_hashjoin.go`, `hashcrossjoin.go`, `distinct.go`, `pragma.go`, `values.go`, `virtual.go`, `rowpool.go`, vectorized variants. `EV` — evaluation: `eval.go`, `eval_vec.go`, `function_registry.go`, all scalar functions. `AG` — aggregation: `aggregate.go`, `aggregate_registry.go`, `hashagg.go`, `hashagg_parallel.go`, `window.go`, `aggregate_vec.go`. `AD` — adaptive query compilation + cache: `adqc*.go`, `cache_stats.go`, `index_usage.go`, plus `plan_node.go` (PlanNode tree, 721 lines) and `explain.go` (migrated from EX). `WT` — write operators and DDL: `writers_admin.go` (PRAGMAs), `writers_ddl.go`, `writers_dml.go`, `alter_table.go`, `constraints.go`, `view.go`, `matview.go`, `virtual_init.go`, `source.go`. `UT` — utilities: `coerce.go`, `integrity.go`, `decimal.go`, `datetime.go`, `json.go`, `parallel.go`, `batch.go`, `pipeline.go`, `simd_dispatch.go`, `string_column.go`, `txn_debug.go`, `pragma.go` (PragmaListener), `fk.go`, `analyze.go`. |
| `SYS` | `SY`, `AP`, `SE`, `TX`, `ST`, `DS`, `BK` | `SY` — init, config validation, graceful shutdown (6-phase), version, stats aggregation, signal handling. `AP` — public API: Engine/Session/Transaction/Stmt, Options, error types (split into `error_kind.go`, `error_code.go`, `error.go`, `error_classify.go`, `error_wrap.go`). `SE` — session lifecycle, goroutine-safety, deadline, session stats. `TX` — transaction context, commit/rollback, savepoints. `ST` — statement preparation, parameter binding, type coercion, prepared statement pool. `DS` — `database/sql` driver: DSN-based engine cache, reference-counted `Connector`/`Conn`/`Stmt`/`Rows`/`Tx`. `BK` — online backup/restore: point-in-time consistent backup via read-lock acquisition, file copy, integrity verification. |
| `DBG` | `TE`, `CT`, `IN`, `PR`, `DC`, `SK`, `CD`, `DI`, `JD` | **Build-tag gated** — all files carry `//go:build debug`. Zero code compiled without `-tags debug`. 9 clusters. `TE` — structured trace events with ring buffer, per-class enable/disable. `CT` — atomic counters, latency histograms, expvar export. `IN` — page/segment/buffer/txn inspectors. `PR` — on-demand pprof/trace/fgprof profiling via socket, signal, or error trigger. `DC` — per-subsystem runtime log level and trace class control. `SK` — UNIX domain socket command server for interactive debugging (commands include `heap`, `cpu N`, `goroutine`, plus `debug_join*` for the JD tracer). `CD` — causal-debug / correlation tracer (buffered events). `DI` — Debugger interface struct (`NewDebugger()` factory). `JD` — JOIN debug tracer backing `PRAGMA debug_join_tracing` (buffered events, ring buffer). Callers (LOG/HK, LOG/LG) provide always-compiled no-op stubs swapped by DBG init() when `debug` tag is active. |

## Dependency Order

```
LOG → FIL → MEM → WAL → ENG → TXN → SQF → SQO → SQB → SYS → DBG
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
    Delete(ctx context.Context, key, value []byte) error
    Commit(ctx context.Context) error
    Rollback(ctx context.Context) error
}

// LOG/EC — Structured error (single source of truth, all subsystems import LOG/EC)
type Error struct {
    Kind    Kind     // KindIO, KindCorrupt, KindSyntax, etc.
    Code    Code     // e.g. "RZR-IO-001"
    Message string
    Cause   error    // wrapped underlying error
}

// Kind enumerates error classifications
type Kind int

// Code is the human-readable error code string
type Code string

// SQF/PL — Operator interface (defined in SQF/PL, re-exported by SQB/DT)
//
// `Operator` lives in `SQF/PL/types.go` (the only package that can host it
// without an import cycle — it is imported by 45+ files across DT, OP, EX, EV,
// AG, UT, WT, AD and SQO). `SQB/DT` re-exports the interface as `dt.Operator`
// so every SQB cluster can implement it without importing `SQF/PL`. The same
// pattern applies to `Row`, `ExecContext`, `QueryPlanner`, `TxWriter`.
type Operator interface {
    Next(ctx context.Context) (Row, error)
    Close() error
}
```

## Error Contract

All user-facing errors are defined in `LOG/EC` (Error Codes cluster). The `LOG/EC` cluster is the single source of truth for `Error`, `Kind`, `Code`, `SQLSTATE`, `Wrap`, `IsKind`, and `Classification`. Every subsystem imports `LOG/EC` directly — never `SYS/AP.Error`.

**Cross-layer wrapping rule:** When an error crosses a subsystem boundary (e.g., ENG → SYS, SQF → SQB), the receiving layer wraps it:
```go
// In SYS/SY when ENG returns an error:
if err != nil {
    return AP.Wrap(AP.KindCorrupt, err) // preserves chain via Unwrap()
}
```

**Classification:** `ec.IsKind(err, kind)` uses `errors.As` to traverse the chain, matching errors from any layer. This replaces per-sentinel `errors.Is` checks for classification purposes.

**Per-package sentinels** (internal use, not exposed to callers):
- `ENG/LS`: `ErrNotFound`, `ErrClosed`, `ErrBloomMiss` (returned when the Ribbon/Bloom filter rejects a key without a disk read), `ErrCatalogCorrupt`, etc.
- `WAL/WR`: `ErrTruncatedRecord`, `ErrCorrupt`, etc.
- `TXN/MV`: `ErrNotFound`, `ErrDuplicateKey`, `ErrArenaExhausted`, etc.
- `SQB/DT`: `ErrNotImplemented`, `ErrNoRows`, `ErrClosed`
- `SQB/EV`: `ErrEval`, `ErrDivByZero`, `ErrTypeMismatch`, `ErrSubquery`, `ErrIgnoreRow`, `ErrTriggerAbort` (re-exported by EX for backward compatibility with SYS callers)
- `SQB/EX`: `SessionCounterAccessor` interface and the Executor factory

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
├── LOG/   # Logging (LG, HK, EC)
├── FIL/   # File I/O (DF, MF, LF, FS, IO/uring)
├── MEM/   # Memory (BF, SP, OF)
├── WAL/   # Write-ahead log (WR, FL, RP)
├── ENG/   # Storage engine (LS, ID, TB, CT, SC, DP, NM)
├── TXN/   # Transaction (MV, LC, SN, VL)
├── SQF/   # SQL Frontend (LX, PS, RE, PL)
├── SQO/   # SQL Query Optimizer (CP, CM, SL, JN, MM, RW, CO)  [scaffolded; production in SQB/EX]
├── SQB/   # SQL Backend  (EX, OP, EV, AG, AD, WT, UT, DT)
├── SYS/   # System layer (SY, AP, SE, TX, ST, DS, BK)
└── DBG/   # Debug        (TE, CT, IN, PR, DC, SK, CD, DI, JD)  [//go:build debug]
```

## Detailed Design

Full design documents: `docs/design/subsystems/LOG.md` · `FIL.md` · `MEM.md` · `WAL.md` · `ENG.md` · `TXN.md` · `SQF.md` · `SQO.md` · `SQB.md` · `SYS.md` · `DBG.md` · `CLI.md` · `TUI.md`

## SQL Surface

### DDL — Data Definition Language

`CREATE TABLE` (incl. `STRICT` table type and `WITHOUT ROWID` rejection at the storage layer), `CREATE TEMP/TEMPORARY TABLE` (REQ001326), `DROP TABLE`, `CREATE INDEX` (incl. partial-index `WHERE` clause, REQ001386), `DROP INDEX`, `CREATE VIEW` (incl. TEMP), `DROP VIEW`, `CREATE MATERIALIZED VIEW`, `DROP MATERIALIZED VIEW`, `REFRESH MATERIALIZED VIEW [CONCURRENTLY]`, `CREATE TRIGGER` (incl. TEMP trigger, BEFORE/AFTER/INSTEAD OF — REQ001370), `DROP TRIGGER`, `ALTER TABLE` (ADD/DROP/RENAME COLUMN, RENAME TO, ALTER SET/DROP DEFAULT, cascading rules on DROP COLUMN), `TRUNCATE TABLE`, `REINDEX`, `ATTACH/DETACH DATABASE` (REQ000908), `CREATE VIRTUAL TABLE` (USING module). Constraint types: `PRIMARY KEY`, `NOT NULL`, `UNIQUE`, `DEFAULT`, `CHECK`, `FOREIGN KEY` with all five reference actions (`CASCADE`/`RESTRICT`/`SET NULL`/`SET DEFAULT`/`NO ACTION`), `MATCH FULL/PARTIAL/SIMPLE` (REQ001310), and `DEFERRABLE INITIALLY DEFERRED/IMMEDIATE` (parser-level; runtime defer queue in `SQB/DT/fk_queue.go` — REQ001310). `GENERATED ALWAYS AS … VIRTUAL|STORED` columns (REQ001338/339).

### DML — Data Manipulation Language

Full DML support: `INSERT` with column list and all five conflict actions (OR REPLACE/IGNORE/ROLLBACK/ABORT/FAIL), `UPDATE … WHERE … FROM`, `UPDATE … RETURNING`, `DELETE … WHERE … RETURNING`, `SELECT` with `WHERE` / `ORDER BY` (with NULLS FIRST/LAST) / `LIMIT` / `OFFSET` (incl. `FETCH FIRST/NEXT`). Aggregate functions (`COUNT(*)`, `SUM`, `AVG`, `MIN`, `MAX`, `GROUP_CONCAT` with SEPARATOR). `DISTINCT`. Subqueries (`IN`, `EXISTS`, scalar). `JOIN` (INNER, CROSS, LEFT/RIGHT/FULL OUTER; nested-loop, hash radix-partitioned, merge, parallel hash). Compound `SELECT` (`UNION`, `UNION ALL`, `INTERSECT`, `EXCEPT`). Window functions (`ROW_NUMBER`, `RANK`, `DENSE_RANK`, `LAG`, `LEAD`, aggregates over PARTITION BY with ROWS/RANGE frames, `EXCLUDE` clause, `FILTER` clause). UPSERT (`INSERT ... ON CONFLICT DO NOTHING/UPDATE`, partial-index targets, `RETURNING`). `WITH` (CTE) and recursive CTE. JSON functions: `json_extract`, `json_object`, `json_array`, `json_type`, `json_set/insert/replace/remove`. DateTime: `NOW`, `date`, `time`, `datetime`, `strftime`. `EXPLAIN`, `EXPLAIN ANALYZE`, `EXPLAIN FORMAT TREE/JSON/DOT`.

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
