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

## Shipped Requirements (Cross-Cutting)

The following cross-cutting requirements have been implemented and shipped; they are part of the design baseline. Per-subsystem requirements are documented in each subsystem design file under `## Shipped Requirements`.

### DDL — Data Definition Language

| ID | Requirement | Iteration |
|---|---|---|
| REQ000103 | `CREATE TABLE` with column types and `PRIMARY KEY` | iter-08 |
| REQ000104 | `DROP TABLE` | iter-08 |
| REQ000105 | `NOT NULL` constraint enforcement | iter-10 |
| REQ000106 | `DEFAULT` value substitution | iter-10 |
| REQ000129 | OPS — Online schema migration (`ALTER TABLE ADD/DROP COLUMN` without copy) | iter-12 (catalog) |

### DML — Data Manipulation Language

| ID | Requirement | Iteration |
|---|---|---|
| REQ000108 | `INSERT` with column list | iter-08 |
| REQ000109 | `UPDATE` with `WHERE` | iter-08 |
| REQ000110 | `DELETE` with `WHERE` | iter-08 |
| REQ000111 | `SELECT` with `WHERE` / `ORDER BY` / `LIMIT` / `OFFSET` | iter-08 |
| REQ000112 | `COUNT(*)` / `SUM` / `AVG` / `MIN` / `MAX` aggregates | iter-08 |
| REQ000114 | `DISTINCT` | iter-08 |
| REQ000115 | `IN` / `EXISTS` / scalar subqueries | iter-08 |
| REQ000116 | `JOIN` (INNER, CROSS) | iter-08 |

### API — Public API

| ID | Requirement | Iteration |
|---|---|---|
| REQ000124 | Parameter binding via `?` placeholders | iter-08 |
| REQ000125 | `EXPLAIN <query>` | iter-08 |

### OBS — Observability

| ID | Requirement | Iteration |
|---|---|---|
| REQ000130 | Query tracing via `TraceHook` | iter-00 |
| REQ000131 | Latency histograms via `MetricHook` | iter-00 |
| REQ000132 | CPU/heap profiling on error | iter-00 |
| REQ000133 | Structured stats aggregation (`Engine.Stats`) | iter-09 |

### QUAL — Quality Gates

| ID | Requirement | Iteration |
|---|---|---|
| REQ000134 | `go vet ./...` zero warnings | all |
| REQ000135 | `gofmt -s -l .` no drift | all |
| REQ000136 | `go test ./... -race -count=1` all green | all |
| REQ000137 | Property-based tests for storage (crash/recovery) | all |
| REQ000138 | `Benchmark*` for every storage component (catch any missing) | iter-17 |
| REQ000139 | No allocations in hot paths | all |
| REQ000140 | All public API methods goroutine-safe | iter-09 |
| REQ000141 | `log/slog` only — no `fmt.Printf` in library | all |
| REQ000142 | Error messages: lowercase, no trailing punctuation | all |
| REQ000143 | SQL/RE coverage: 49% → 80%+ | iter-27 |
| REQ000201 | SQL/PL coverage 30.6% to 98.8% | iter-20 |
| REQ000203 | Missing benchmarks (FIL/LF, LOG/HK, SQL/PS, SQL/PL have 0) | iter-17 |
| REQ000293 | Window operator test coverage (window_test.go) | iter-23 |
| REQ000294 | Cross-leaf cursor test coverage | iter-23 |

### TEST — SQLLogicTest Corpus Integration

| ID | Requirement | Iteration |
|---|---|---|
| REQ000323 | SQLLogicTest corpus mirror as git submodule (`tests/sqlcmp/corpus` from `MarvBeer/sqlite-test-suite`); build-tag-gated fetch | iter-25 |
| REQ000324 | SLT test file parser (`statement ok`, etc.) | iter-25 |
| REQ000325 | Driver interface (`Connect`/`Close`/`Exec`/`Query`; returns `ResultSet` with typed `Value` cells) | iter-25 |
| REQ000326 | Razordata driver implementation wrapping public `internal/SYS.Engine` API; SQL errors classified as `skipped` not `failed` | iter-25 |
| REQ000327 | SLT runner + type-aware result diff (`T`/`I`/`R`/`NULL`; `nosort`/`rowsort`/`valuesort`; `label` grouping) | iter-25 |
| REQ000328 | Corpus subset default target (~200 `.test` files from `select1-4`, `index/`, `evidence/`, `minmax/`, `cast/`, `null/`, `decimal/`, `datetime/`); calibrated pass-rate threshold; full corpus gated by `slt_corpus_full` tag | iter-25 |
| REQ000329 | Pure-Go reference oracle via `modernc.org/sqlite` dependency (test-only); fallback to hand-rolled `tests/sqlcmp/oracle/` if modernc fails to build in sandbox | iter-25 |
| REQ000330 | Dual runner: same SQL on Razordata + oracle; result-set diff after normalization | iter-25 |
| REQ000331 | Result-set normalization (int→int64, float round, strip whitespace, column-name sort, `''`/`NULL` config flag) | iter-25 |
| REQ000332 | Dual case authoring convention (`dualCase` struct, table-driven; seed ~50 cases across DDL/DML/aggregates/joins) | iter-25 |
| REQ000333 | JUnit XML output for CI consumption (pass/fail/skip per testcase) | iter-25 |
| REQ000334 | Coverage snapshot (`tests/sqlcmp/slt/coverage.json` per run; `coverage.baseline.json` committed; regression check) | iter-25 |
| REQ000335 | CI workflow: PR + nightly; run subset; upload JUnit; post pass-rate PR comment vs `main` | iter-25 (deferred — no `.github/` in repo) |
| REQ000336 | Developer guide: how to run, add cases, re-baseline coverage | iter-25 |
| REQ000337 | Architecture note lives in iteration doc (not `design/`); test harness is operational, not architectural | iter-25 |
| REQ000365 | `allProbeCases` undeclared — register `probeCases` in `AllCases()` | iter-26.1 (v0.26.3) |