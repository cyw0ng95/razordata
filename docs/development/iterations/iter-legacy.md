# Iterations 0–26 — Legacy Compact

**Compaction date:** 2026-06-13
**Source:** iter-00 through iter-26.10 (individual doc files)
**Reason:** Early iteration documents contained detailed REQ tables, design alignment notes, and outcome narratives that are now superseded by the consolidated `REQUIREMENTS.md` and `ROADMAP.md`. This file preserves the release mapping and one-paragraph summary of each.

## Release Map

| Iter | Tag(s) | Theme | LoC |
|------|--------|-------|-----|
| 0 | (pre-release) | LOG: structured logging + hooks | ~1,500 |
| 1 | (pre-release) | FIL: file I/O, mmap, fsync | ~1,200 |
| 2 | (pre-release) | MEM: buffer pool, sync.Pool | ~1,500 |
| 3 | v0.1.0 | WAL: write-ahead log, segments, CRC | ~2,500 |
| 4 | v0.2.0 | ENG/LSM: lock-free skiplist, memtable, SST, flush | ~4,000 |
| 5 | v0.3.0 | TXN/MVCC: version chain, per-txn arena | ~2,000 |
| 6 | v0.4.0 | TXN/Protocol: commit, WAL integration | ~1,800 |
| 7 | v0.5.0 | SQL/Core: lexer, parser, rewriter | ~3,500 |
| 8 | v0.6.0 | SQL/Execute: planner, executor | ~4,500 |
| 9 | v0.6.3 | SYS: engine integration, session, transaction | ~2,000 |
| 10 | v0.7.0 | Constraints: NOT NULL, DEFAULT | ~800 |
| 11 | v0.8.0 / v0.8.1 | UNIQUE + I/O refinements (mmap, madvise) | ~1,200 |
| 12 | v0.9.0 | Catalog persistence | ~1,500 |
| 13 | v0.10.0 | WAL corruption recovery | ~800 |
| 14 | v0.10.1 | Graceful shutdown (6-phase) | ~1,200 |
| 15 | v0.11.0 | Finish-line + session pooling, ReadOnly | ~1,500 |
| 16 | v0.12.0 | Param binding + quick wins | ~2,000 |
| 17 | v0.13.0 | LS benchmarks + RE normalization + RTMerge | ~2,500 |
| 18 | (folded into 17) | WAL batch commit (precursor) | ~155 |
| 19 | v0.15.0 | SIMD + parallel execution foundation | ~3,000 |
| 20 | v0.17.0 | TXN correctness + SQL completeness + type system | ~4,000 |
| 21 | v0.18.0 | EXPLAIN + Advanced SQL (CTE, UPSERT, SAVEPOINT) | ~2,800 |
| 22 | v0.19.0 / v0.19.1 | Secondary Indexes MVP (LSM-backed) + test stability | ~3,000 |
| 23 | v0.20.0–v0.22.0 | Query optimization, statistics, integrity, window functions, JSON, B-tree index, SST compression | ~8,500 |
| 24 | v0.23.0–v0.24.0 | SQLite compliance (RC isolation, FK, ALTER TABLE, CREATE VIEW, Pragmas) | ~3,500 |
| 25 | v0.25.0 | SQLite Compatibility Test Suite (SLT driver, dual-runner, corpus) | ~3,560 |
| 26 | v0.26.0 | Quality Hardening (3 critical bugs: data loss, test isolation, empty-table aggregate) | ~1,200 |
| 26.1 | v0.26.3 | Bugfix sweep v1 (7 bugfixes: NULL semantics, SELECT no-FROM, flush race) | ~500 |
| 26.2 | v0.26.4 | Bugfix sweep v2 (5 bugfixes: GROUP_CONCAT, hidden PK, comma-join) | ~400 |
| 26.3 | v0.26.5 | Bugfix sweep v3 (5 bugfixes: HAVING COUNT*, NOT LIKE/IN, ABS/HEX/ROUND) | ~250 |
| 26.4 | v0.26.6 | Compound SELECT (UNION/INTERSECT/EXCEPT) | ~300 |
| 26.5 | v0.26.7 | Core scalar functions batch 1 (21 functions: char, concat, instr, etc.) | ~1,200 |
| 26.6 | v0.26.8 | Core scalar functions batch 2 (7 functions: glob, soundex, unhex, etc.) | ~500 |
| 26.7 | v0.26.9 | Lexer/parser small additions (bitwise, ||, %, COALESCE, NULLIF) | ~200 |
| 26.8 | v0.26.10 | Bug sweep v4 (Session.Query streaming, int64 overflow) | ~250 |
| 26.9 | v0.26.11 | Logger v2 (clock-sweep fix, ProfileHook, Prometheus) | ~250 |
| 26.10 | v0.26.12 | WAL batch sync + encode optimization (2 allocs → 2 allocs, -32% latency) | ~150 |

## Per-Iteration Summary

### iter-00 — LOG (Structured Logging)
Foundation: `log/slog` wrapper, `Logger`/`Hook`/`HookRegistry` interfaces, three stub hooks (Trace, Metric, Profile). Atomic level check; async dispatcher with bounded channel; drop-on-overflow. Subsystem: `LOG` (`LG`, `HK`).

### iter-01 — FIL (File I/O)
File subsystem foundation: `BlockDevice` with pwrite/pread, mmap-backed zero-copy reads, fsync primitives. Lock-free page cache, allocation tracking. Subsystem: `FIL` (`FS`, `LF`, `MF`, `DF`).

### iter-02 — MEM (Buffer Pool)
Buffer pool with clock-sweep LRU eviction, sync.Pool for zero-allocation hot path, atomic hand + refKey, dirty-page tracking, hint-file warmup. Subsystem: `MEM` (`BF`, `SP`).

### iter-03 — WAL (Write-Ahead Log)
Write-ahead log: append-only segments, varint length prefix, CRC32 envelopes, atomic bool for closed flag, LSN allocation, sync policy. Subsystem: `WAL` (`WR`, `FL`, `RP`).

### iter-04 — ENG/LSM (Lock-Free Skiplist + Memtable + SST + Flush)
LSM-tree storage engine: lock-free skiplist memtable (atomic CAS), immutable memtable rotation, SST block encoding, flush manager with snapshot-consistent reads, manifest tracking. Subsystem: `ENG` (`LS`, `ID`).

### iter-05 — TXN/MVCC (Version Chain + Per-Transaction Arena)
Multi-version concurrency control: per-txn arena for version chain allocation, MVCC version nodes, snapshot timestamps, conflict detection. Subsystem: `TXN` (`MV`, `LC`).

### iter-06 — TXN/Protocol (Transaction Slot + Commit + WAL)
Transaction commit protocol: write intents, WAL integration, 2PC-style commit/rollback, group commit coordination, isolation level basics. Subsystem: `TXN` (`VL`, `SN`).

### iter-07 — SQL/Core (Lexer + Parser + Rewriter)
SQL frontend: tokenizer with precedence handling, recursive-descent parser, AST nodes for DML/DDL/queries, algebraic rewriter for subquery flattening, placeholder binding. Subsystem: `SQL` (`LX`, `PS`, `RE`).

### iter-08 — SQL/Execute (Planner + Executor)
SQL backend: rule-based planner, operator tree (SeqScan/Filter/Project/Join/Aggregate), vectorized eval where applicable, INSERT/UPDATE/DELETE executors, RETURNING clause. Subsystem: `SQL` (`EX`, `PL`).

### iter-09 — SYS + Integration
Public API surface: `AP.Engine`, `AP.Session`, `AP.Transaction`, `AP.Stmt`, `Options`, error types. Engine wiring across all subsystems, Open/Close lifecycle. Subsystem: `SYS` (`SY`, `SE`, `TX`, `ST`, `AP`).

### iter-10 — Constraints: NOT NULL, DEFAULT
Column-level constraints in parser + executor: NOT NULL enforcement, DEFAULT value evaluation (literals, functions), schema validation. Shipped v0.7.0.

### iter-11 — UNIQUE + I/O Refinements (v0.8.0 + v0.8.1)
UNIQUE constraint enforcement (BTree-based, single + composite + multi-clause), mmap large reads, madvise hints for hot/cold pages, I/O batching. Two-stage release: constraint logic in v0.8.0, I/O refinements in v0.8.1. Subsystem: SQL/MEM/FIL.

### iter-12 — Catalog Persistence
System catalog durable across restarts: `CREATE TABLE`/`DROP TABLE` written to WAL, replayer restores catalog on Open, in-memory cache synced with on-disk state.

### iter-13 — WAL Corruption Recovery (v0.10.0)
Recovery policy: detect torn writes at segment tail, skip-and-log vs. fail-loud, CRC verification on replay, idempotent replayer, envelope CRC, bounded resync, Stats surfacing. Coverage 72.5% (target 85%) — REQ000191 noted as follow-up.

### iter-14 — Graceful Shutdown (v0.10.1)
6-phase teardown per SYS.md:215-282: stop accepting → drain active tx → flush pending → stop background → close subsystems → report stats. Per-subsystem Close, active-tx wait, config validation. Force-abort timeout for stuck transactions.

### iter-15 — Finish-Line + Quick Bugs
Session pooling (`sync.Pool`), ReadOnly mode (skip WAL), validateOptions, active-tx wait, basic admin CLI. Shipped v0.11.0.

### iter-16 — Param Binding + Path Fix + Quick Wins
Parameter binding in prepared statements, hidden rowid for no-PK tables, comma-join → CROSS JOIN rewrite, GROUP_CONCAT dispatch, subquery planner threading. Shipped v0.12.0.

### iter-17 — Benchmarks + RE Normalization + RTMerge
LS read/write benchmarks, RE subquery normalization, log level audit, RTMerge record type for compaction manifests, BatchSync coordination. Shipped v0.13.0.

### iter-18 — WAL Batch Commit (folded into iter-17)
Original standalone plan for WAL group commit. Code merged into iter-17 as `BatchSync`/`StartBatch`/`EndBatch` API. ~155 LOC.

### iter-19 — SIMD + Parallel Execution Foundation
Phase 1: SIMD-style vectorized eval batch interface (EvalBatch with uint16 selection vectors). Phase 2 (deferred): actual AVX2 intrinsics, parallel operators.

### iter-20 — TXN Quick Wins + SQL Completeness + Type System
Hazard pointer Publish fix, epoch manager background goroutine, FullHistogram selectivity, CHECK constraints, type coercion table, decimal type, JOIN simplification. Shipped v0.17.0.

### iter-21 — EXPLAIN + Advanced SQL (v0.18.0)
Full EXPLAIN and EXPLAIN QUERY PLAN with SQLite-compatible output (id, parent, notused, detail). PlanNode tree with cost estimation, operator tree visualization. RETURNING clause for INSERT/UPDATE/DELETE. UPSERT (ON CONFLICT DO NOTHING). CTE (WITH) via naive inlining. SAVEPOINT/RELEASE/ROLLBACK TO with stack-based nested savepoints. 18 REQs across 4 blocks, 5 commits, ~2,800 LOC.

### iter-22 — Secondary Indexes MVP (v0.19.0)
CREATE/DROP INDEX parsing, IndexScan real seek via LSM-backed index store, cost-based index selection in planner, index maintenance on INSERT/UPDATE/DELETE, catalog V1→V2 migration, column-level selectivity stats types. 4 REQs, 8 commits, ~50 tests, ~3,000 LOC. Post-iteration fix (v0.19.1): concurrent test key conflicts, TXN/VL test parallelization.

### iter-23 — Query Optimization & Storage Enhancement (v0.20.0–v0.22.0)
Three phases:
- Phase 1 (v0.20.0): ANALYZE executor with reservoir sampling, histogram-based selectivity, PRAGMA integrity_check, VACUUM, backup/restore, `razor` CLI.
- Phase 2 (v0.21.0): Window functions (ROW_NUMBER, RANK, DENSE_RANK, LAG, LEAD), DATE/TIME/TIMESTAMP types, JSON type with operators.
- Phase 3 (v0.22.0): B-tree secondary index package (ENG/ID), IndexScan B-tree integration, SST prefix bloom filters, SST block compression (flate).
~8,500 LOC, all phases complete.

### iter-24 — SQLite Compliance (v0.23.0–v0.24.0)
Read-committed isolation, per-statement snapshot, MVCC own-writes visibility, FK parsing + INSERT enforcement, ALTER TABLE parsing, CREATE VIEW, Pragmas (cache_size, journal_mode, synchronous), FETCH FIRST, parseInterval validation, LAG/LEAD offset. ~3,500 LOC, 11 commits.

### iter-25 — SQLite Compatibility Test Suite (v0.25.0)
Pure-Go SQLLogicTest driver (parser + runner + type-aware diff + RazorDriver wrapping `internal/SYS`), corpus submodule skeleton, curated PR subset (~200 files, 30% threshold), modernc.org/sqlite dual-runner (pure-Go, no CGO), JUnit XML output, coverage snapshot with baseline regression detection. 15 REQs, 27 files, ~3,560 LOC.

### iter-26 — Quality Hardening (v0.26.0)
3 critical bug fixes: REQ000347 (silent data loss in LSM flush — wrong memtable selection, L0 manifest bug, SST orphan block), REQ000346 (test isolation — UnregisterAll missed registeredIndexes/views), REQ000345 (empty-table aggregate returns 0 rows vs 1). Sync boundary tests (0B-10GB roundtrip). 3 REQs, ~1,200 LOC.

### iter-26.1 — Bugfix Sweep v1 (v0.26.3)
7 bugfixes: REQ000357 (SELECT no-FROM), REQ000359-362 (NULL semantics in concat/arith/IS NULL/= NULL), REQ000364 (flush WaitGroup race), REQ000365 (allProbeCases undeclared). 7 dual-runner probe cases.

### iter-26.2 — Bugfix Sweep v2 (v0.26.4)
5 bugfixes: REQ000355 (GROUP_CONCAT dispatch), REQ000363 (GROUP_CONCAT empty→NULL), REQ000366 (subquery planner store threading), REQ000367 (hidden rowid for no-PK tables), REQ000368 (comma-join → CROSS JOIN). 5 dual-runner probe cases.

### iter-26.3 — Bugfix Sweep v3 (v0.26.5)
5 bugfixes: REQ000378 (HAVING COUNT(*)), REQ000379 (chained unary minus regression), REQ000380+381 (NOT LIKE / NOT IN parser), REQ000382 (ABS / HEX / ROUND scalar functions). 12 new dual-runner probe cases; 19 new unit tests.

### iter-26.4 — Compound SELECT (v0.26.6)
REQ000383: UNION/INTERSECT/EXCEPT parser + executor with correct precedence (INTERSECT binds tighter), trailing ORDER BY/LIMIT/OFFSET apply to compound result. RE rewrite/format support. 3 commits, ~300 LOC. Dual-runner: 67/67/0.

### iter-26.5 — Core Scalar Functions Batch 1 (v0.26.7)
28 REQs closed (REQ000384-404/407-412/414/417): session counters (CHANGES/LAST_INSERT_ROWID/TOTAL_CHANGES); string functions (CHAR/CONCAT/CONCAT_WS/FORMAT/LTRIM/RTRIM/REPLACE/QUOTE); type/info functions (TYPEOF/OCTET_LENGTH/UNICODE/SQLITE_VERSION/SOURCE_ID); conditional (IIF); search (INSTR); numeric (SIGN/MAX/MIN/RANDOM/RANDOMBLOB/ZEROBLOB). 21 new functions + session state. ~1,200 LOC. Dual-runner: 90/90/0.

### iter-26.6 — Core Scalar Functions Batch 2 (v0.26.8)
7 remaining core scalar functions: GLOB (pattern matching), SOUNDEX (phonetic encoding), UNHEX (hex→BLOB), UNISTR (escape decoder), LIKELIHOOD/LIKELY/UNLIKELY (no-op planner hints). ~500 LOC. Dual-runner: 100/100/0.

### iter-26.7 — Lexer/Parser Small Additions (v0.26.9)
REQ000350/351/352/353/354: bitwise operators (&,|,^,~), concat (||), modulo (%), COALESCE and NULLIF as special forms (parens-less). ~200 LOC. Dual-runner: 107/107/0.

### iter-26.8 — Bug Sweep v4 (v0.26.10)
REQ000348/292: Session.Query now streams rows via Next/Close on AP.Rows; EX.QueryStream returns streaming iterator; int64 multiplication overflow now correctly handles MIN_INT64 × -1 and large values. ~250 LOC.

### iter-26.9 — Logger v2 (v0.26.11)
REQ000161/322/101: clock-sweep LRU fix (second-pass eviction picks lowest refKey); ProfileHook dumps heap profile on Error events (rate-limited 1/5s); metricHook exports Prometheus text format. ~250 LOC.

### iter-26.10 — WAL Batch Sync + Encode Optimization (v0.26.12)
REQ000443: encodeRecord reduced from 4 allocs to 2 allocs (-50%), 217ns→148ns (-32%); pre-sized body and out slices eliminate growth; CRC correctly computed over body only. ~150 LOC.

## See Also

- `REQUIREMENTS.md` — full requirement matrix (TBD + DONE)
- `ROADMAP.md` — release tags and theme notes
