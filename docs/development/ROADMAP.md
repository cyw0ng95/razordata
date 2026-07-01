# Razordata Development Roadmap

## Iterations Overview

| # | Name | Subsystem | Clusters | Status |
|---|---|---|---|---|
| 0 | LOG | Structured logging | `LG`, `HK` | done |
| 1 | FIL | File I/O | `DF`, `MF`, `LF`, `FS` | done |
| 2 | MEM | Buffer pool | `BF`, `SP` | done |
| 3 | WAL | Write-Ahead Log | `WR`, `FL`, `RP` | done |
| 4 | ENG/Memtable+SST | Lock-free skiplist + memtable + SST | `LS` | done |
| 5 | TXN/MVCC | Version chain + per-thread arena | `MV`, `LC`, `SN` | done |
| 6 | TXN/Protocol | Transaction slot + commit + WAL | `VL` | done |
| 7 | SQL/Core | Lexer + parser + rewriter | `LX`, `PS`, `RE` | done |
| 8 | SQL/Execute | Planner + executor | `PL`, `EX` | done |
| 9 | SYS+Integration | Public API + end-to-end | `AP`, `SY`, `SE`, `TX`, `ST` | done (v0.6.3) |
| 10 | NOT NULL / DEFAULT | Column constraints end-to-end | `PS`, `EX` | done (v0.7.0) |
| 11 | UNIQUE Constraint | Single + composite + multi-clause UNIQUE | `PS`, `EX` | done (v0.8.0) |
| 11.1 | I/O refinements | MADV_DONTNEED + mmap BlockDevice | `MEM/BF`, `FIL/DF` | done (v0.8.1) |
| 12 | Catalog Persistence | System catalog (single-file, atomic rename, schema versioning) | `LS`, `EX`, `SY` | done (v0.9.0) |
| 13 | WAL Corruption Recovery | Segment header + envelope CRC + bounded resync + Stats | `WAL/WR`, `WAL/RP` | done (v0.10.0) |
| 14 | Graceful Shutdown Completion | 6-phase sequence + per-subsystem Close + active-tx wait + config validation | `SYS/SY`, `SYS/AP`, `TXN/VL`, `ENG/LS`, `LOG/HK` | done (v0.10.1) |
| 15 | Finish-Line + iter-12b Quick Bugs | Session pooling, read-only mode, WriteBuffer, hazard pointer fix, iter-12b bug fixes (sstIterator, nextFileID), WAL stats surfacing | `SYS/SE`, `SYS/SY`, `WAL/FL`, `WAL/WR`, `TXN/LC`, `ENG/LS` | done (v0.11.0) |
| 16 | ParamBinding + iter-12b Path Fix + Quick Wins | binding + type coercion, SST path unification, bloom dynamic sizing, arena-on-slot, gzip log rotation, WAL/RP coverage | `SQL/EX`, `SYS/ST`, `ENG/LS`, `LOG/LG`, `TXN/VL`, `WAL/RP` | done (v0.12.0) |
| 17 | Storage Benchmarks + Quick Wins | ENG/LS benchmarks, WAL encoding, LOG docs, SQL/RE coverage lift | `ENG/LS`, `WAL/WR`, `LOG/HK`, `SQL/RE` | done (v0.13.0) |
| 18 | Storage Quality | BloomFilter alignment, per-thread arena init, batch commit, clock-sweep audit, plan memoization | `ENG/LS`, `TXN/MV`, `WAL/FL`, `MEM/BF`, `SQL/PL` | done (v0.14.0) |
| 19 | SIMD Vectorization + Parallel Query | Batch execution, columnar memory, SIMD predicates, parallel sort | `SQL/EX` | done (v0.15.0) |
| 20 | SQL Completeness | CHECK constraints, type affinity, OUTER JOIN, HAVING, CASE/EXISTS, DECIMAL, parser tests | `SQL/PS`, `SQL/EX`, `SQL/PL` | done (v0.17.0) |
| 21 | EXPLAIN + Advanced SQL | EXPLAIN support, UPSERT, RETURNING, CTE (WITH), SAVEPOINT | `SQL/LX`, `SQL/PS`, `SQL/PL`, `SQL/EX`, `SYS/SE` | done (v0.18.0) |
| 22 | Secondary Indexes MVP | CREATE/DROP INDEX, IndexScan real seek (LSM), index selection, index maintenance | `ENG/LS`, `SQL/PS`, `SQL/PL`, `SQL/EX` | done (v0.19.0) |
| 23 | Query Optimization & Storage Enhancement | ANALYZE, histogram selectivity, integrity_check, VACUUM, backup/restore, window functions, DATE/TIME/JSON types, B-tree index, SST compression | `SQL/EX`, `SQL/PL`, `SQL/PS`, `ENG/LS`, `ENG/ID`, `SYS`, `WAL` | done (v0.20.0–v0.22.0) |
| 24 | SQLite Compliance | Read-committed isolation, MVCC own-writes, foreign keys, ALTER TABLE, CREATE VIEW, Pragmas, FETCH FIRST | `TXN/VL`, `TXN/SN`, `SQL/EX`, `SQL/PS`, `SQL/PL`, `ENG/LS`, `SYS` | done (v0.23.0–v0.24.0) |
| 25 | SQLite Compatibility Test Suite | Pure-Go SQLLogicTest driver (parser, runner, type-aware diff, RazorDriver wrapping internal/SYS), corpus subset gate, modernc.org/sqlite dual-runner, JUnit XML, coverage snapshot + baseline regression check | `tests/sqlcmp/slt/`, `tests/sqlcmp/dual/` | done (v0.25.0) |
| 26 | Quality Hardening | 3 critical bug fixes (REQ000345/346/347), sync boundary tests, EX test helpers | `ENG/LS`, `SQL/EX`, `SYS/AP`, `SQL/PS`, `SQL/LX` | done (v0.26.0) |
| 26.1 | Bugfix sweep v1 | 7 bugfixes (REQ000357, 359-362, 364, 365) | `SQL/EX`, `SQL/PS`, `ENG/LS` | done (v0.26.3) |
| 26.2 | Bugfix sweep v2 | 5 bugfixes (REQ000355, 363, 366, 367, 368) | `SQL/EX`, `SQL/PS` | done (v0.26.4) |
| 26.3 | Bugfix sweep v3 | 5 bugfixes (REQ000378-382), 12 new dual-runner probes, 19 new unit tests | `SQL/EX`, `SQL/PS` | done (v0.26.5) |
| 26.4 | Compound SELECT | UNION/INTERSECT/EXCEPT parser+executor, correct precedence, trailing ORDER BY/LIMIT/OFFSET apply to compound, RE rewrite/format, 6 new dual-runner probes | `SQL/LX`, `SQL/PS`, `SQL/EX`, `SQL/RE` | done (v0.26.6) |
| 27 | Maturity Push | Phases 1-5 (25 REQs, v0.27.0): SLT 42/42, trigger parser, generated cols, SST dict compression, WAL columnar, rate-limited compactor, sub-compaction, compaction style, W-TinyLFU, off-heap pool, escape hints, QSBR, FROM-subquery parser fix, real goroutine ID, epoch background goroutine, reclaim pool, parallel WAL replay, 8-wide SIMD EvalBatch + CPU detection, radix hash join, columnar SST/PAX. Phase 6 (7 REQs, v0.27.1): REQ000074 IndexScan real seek, REQ000295 io_uring, REQ000296 Direct I/O + fixed-fd, REQ000301 async fsync, REQ000309 NUMA-aware placement, REQ000156 cost-based scan selection, REQ000437 full SQL aggregate DISTINCT support. Deferred REQs (v0.27.2): REQ000018 file locking/flock, REQ000126 foreign keys validation, REQ000244 ALTER TABLE executor, REQ000443 WAL alloc reduction verified, REQ000049 Schema cluster split, REQ000050 Deparser cluster split, REQ000300 Tier-aware storage scheduler, REQ000302 PMem-aware buffer pool, REQ000034 WAL compression, REQ000048 ENG/TB table registry, REQ000064 generational arena, REQ000305 arena epoch-reclaim, REQ000444 UPDATE deadlock fix, REQ000451 CREATE TEMP VIEW parser, REQ000452 INSERT OR REPLACE parser, REQ000448 SLT skipif/onlyif gating, REQ000445 NULL three-valued logic | `SQL/PS`, `SQL/EX`, `SQL/PL`, `ENG/LS`, `WAL/WR`, `MEM/BF`, `MEM/OF`, `TXN/MV`, `TXN/LC`, `FIL/FS`, `FIL/IO` (new) | done (v0.27.0); Phase 6 done (v0.27.1); Deferred done (v0.27.2) |
| 28 | Parser DDL Hardening + SQL Compliance + MV-OCC | 6 phases, 64+ REQs: Phase 0 (buildWriterOp routing — 12 REQs done), Phase 1 (parser DDL IF EXISTS / RENAME / PRAGMA / TRUNCATE / AUTOINCREMENT — 9 REQs done), Phase 2 (LIMIT/OFFSET, CAST, COALESCE, CASE, ORDER BY expr, HAVING, DISTINCT, constraints, window RANGE, scalar IN — 17 REQs done), Phase 3 (MV-OCC O(1) read-set validation — 1 REQ done), Phase 4 (bugfixes — 23+ REQs done + audit REQ000531/532/503/486/544 done), Phase 5 (operator codegen + adaptive compilation, REQ000311/313 shipped — codegen framework + ADQC wrappers), Phase 6 (integration polish — all green). Bugfix sweep: REQ000533 (`<>` lexer), REQ000535 (REPLACE INTO), REQ000503 (scalar IN), REQ000486 (INSERT OR IGNORE), REQ000544 (O(1) Row.Lookup). Correlated subquery re-execution fix (store-backed SeqScan). 39/39 packages green with `-race`; 42 done SLT gap tests. 46 TBD remaining. | `SQL/PS`, `SQL/EX`, `SQL/PL`, `TXN/MV` | done (v0.28.0) |
| 28.1 | Bugfix Sweep REQ000571–REQ000635 | 7 phases, 65 REQs: Phase 1 critical (REQ572/573/574/599/600/601 — data loss + panic); Phase 2 high-priority correctness (REQ575–578, 581, 602–611, 630 — SQL semantics + races); Phase 3 SQL eval (REQ580, 605–607, 619–621, 631); Phase 4 infra cleanups (REQ583–598, 612–618, 622); Phase 5 cosmetic / dead code (REQ589, 592, 597–598, 623–635); Phase 6 code reorganization (REQ582 — ps.go split); Phase 7 perf (REQ571 — SST page cache). Target v0.28.1. | `ENG/LS`, `WAL/RP`, `WAL/FL`, `MEM/BF`, `TXN/VL`, `TXN/MV`, `SQL/PS`, `SQL/EX`, `SQL/PL`, `SYS/SE`, `SYS/SY`, `SYS/TX`, `BK`, `FIL/DF`, `FIL/FS`, `LOG/HK`, `LOG/LG` | done (v0.28.1) |
| 28.2 | Remaining Bugfix Sweep + Cleanups | 6 phases, 47 REQs: Phase 0 (REQUIREMENTS.md DONE restore — 175 rows); Phase 1 high-priority (REQ608, 611, 630); Phase 2 critical perf (REQ571 — SST page cache, deferred); Phase 3 medium correctness (REQ614, 616, 617, 618, 632, 633, 595, 596, 530, 585); Phase 4 low-effort cleanups (REQ589, 592, 597, 598, 553, plus 6 doc-moves for already-shipped 28.1 items); Phase 5 medium evolutions (REQ540, 541, 545, 548, 550, 551, 552, 554, 555, 557, 562, 584, 587). 28 REQs shipped. Phase 2 deferred. | `ENG/LS`, `WAL/FL`, `WAL/WR`, `MEM/BF`, `TXN/VL`, `TXN/MV`, `SQL/PS`, `SQL/EX`, `SQL/PL`, `SYS/ST`, `SYS/SY`, `SYS/TX`, `BK`, `FIL/DF` | done (v0.28.2) |
| 28.3 | Final Bugfix Sweep | 19 paperwork moves + 11 bugfix REQs (2 code fixes, 9 verified pre-fixed). NaN IN fix, IS NULL PK fix, DELETE/UPDATE view resolution, trigger semicolons. 13 deferred to iter-29. | `SQL/EX`, `SQL/PS` | done (v0.28.3) |
| 29 | Full Sweep: Style, Modernization, Performance, PRAGMA, CLI/TUI | 43 REQs across 9 phases: Foundation (19), Modernization (7), Performance (8), Catalog & API (3), SQL Features (3), PRAGMA Support (7), SLT & Testing (4), CLI/TUI Foundation (3), XL Features (3). Key: PRAGMA table_info/database_list/index_list/table_list/foreign_key_list, multi-column hash join, LEFT OUTER JOIN flag, RANGE window frame, sqlite_master virtual table, disk full handling, configurable shutdown, emergency shutdown, shape specialization, vectorized string columns, block-level MVCC. | `SQL/EX`, `SQL/PS`, `ENG/LS`, `ENG/TB`, `ENG/CT` (new), `MEM/BF`, `WAL/WR`, `SYS/SY`, `SYS/AP`, `FIL/DF`, `FIL/MF`, `tests/slt` | done (v0.29.0) |
| 35 | SQF/SQB Subsystem Split | Split monolithic `internal/SQL/` into SQF (Frontend: LX/PS/RE/PL) and SQB (Backend: EX). 15 REQs: 952–958 (move files), 959–960 (deferred ST/QC), 962–963 (imports + cleanup), 964–966 (design docs + roadmap). Soft split — Operator/Row stay in SQB/EX. | `internal/SQL/` → `internal/SQF/`, `internal/SQB/` | done |
| 36 | Index Scan Optimizations | REQ001106 (BitmapHeapScan), REQ001107 (IndexOnlyScan), REQ001108 (Index Condition Pushdown). BitmapHeapScan: OR-of-equality on distinct indexed columns → BitmapHeapScan + cost integration. IndexOnlyScan: covering index detection for projection-only queries, 49% perf win. Index Condition Pushdown: decompose WHERE into residual predicates pushed into IndexScan, eliminating outer Filter. | `SQL/EX`, `SQL/OP` | done |
| 29.5 | Join Elimination + Bushy-Join Cleanup | REQ001155 (join elimination preserves ON-referenced joins), REQ001156 (groupBushyJoins chain-equi-join regression test), REQ001113 partial (2-table cross-join predicate distribution). select4.test: 351/3857 → 25/3857 failing. New REQ001162 filed for the 3-table+ cross-table-predicate execution bug (HashJoin/NLJ output column shuffling). | `SQB/EX` | done |
| 29.6 | Dual Test Bugfix Sweep | REQ001125 (CHANGES/TOTAL_CHANGES routed through session in driver), REQ001126 (correlated EXISTS — pre-fixed by REQ001098), REQ001127 (NOT IN with NULL — compiled filter skip), REQ001128 (UNION ALL/DISTINCT NULL PK auto-generation), REQ001129 (INSERT SELECT store-backed path). Dual tests: 418/427 → 424/427 passed. | `SQB/EX`, `SQB/OP`, `SQB/DT`, `SQB/EV`, `driver` | done |


All completed iterations released as v0.9.0–v0.29.0. Iteration 29 done. Coverage details: `go test ./... -cover`.

## Completion Criteria (All Iterations)

| Rule | Command | Target |
|---|---|---|---|
| Vet | `go vet ./...` | zero warnings |
| Format | `gofmt -s -l .` | no drift |
| Test suite | `go test ./... -race -count=1` | all green |

## Iteration Detail

Each iteration is documented in `development/iterations/iter-XXX.md`.
All eight iterations are now in `done` state.

## Directory Structure

See `docs/design/ARCH.md` for full structure. As of v0.6.3, SYS subsystem tests
are organized by function domain (AP/, SE/, ST/, SY/, TX/).



## Design Protection

Design documents in `docs/design/subsystems/*.md` are authoritative. Human-only edits.
