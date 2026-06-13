# Iterations 0-20 — Legacy Compact

**Compaction date:** 2026-06-13
**Source:** iter-00 through iter-20 (22 individual doc files)
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

## See Also

- `REQUIREMENTS.md` — full requirement matrix (TBD + DONE)
- `ROADMAP.md` — release tags and theme notes
- `iter-21-*.md` onward — current iterations (kept individually)

