# Razordata Development Roadmap

## MVP Feature Scope (v1 — complete)

- **DDL:** CREATE TABLE, DROP TABLE
- **DML:** INSERT, UPDATE, DELETE, SELECT
- **Clauses:** WHERE, ORDER BY, LIMIT, OFFSET
- **Constraints:** PRIMARY KEY, NOT NULL, DEFAULT (PK exercised; NOT NULL/DEFAULT deferred to v1.1)
- **Types:** INTEGER, TEXT, BOOLEAN
- **Transactions:** BEGIN, COMMIT, ROLLBACK

The v1 dependency chain **LOG → FIL → MEM → WAL → ENG → TXN → SQL → SYS** is
complete as of v0.6.0. All eight iterations (0-9) are done; iter-08 and
iter-09 both shipped as fully-tested milestones.

## Out of Scope (v1)

Joins, subqueries, foreign keys, network server, external C dependencies.
(The SQL/EX executor already ships the join/subquery/aggregate/distinct
operators as v1.1+ code — they pass tests but are not v1 MVP per
`design/ARCH.md`.)

## Iterations Overview

| # | Name | Subsystem | Clusters | Tests | Status |
|---|---|---|---|---|---|
| 0 | LOG | Structured logging | `LG`, `HK` | 108 | done |
| 1 | FIL | File I/O | `DF`, `MF`, `LF`, `FS` | 187 | done |
| 2 | MEM | Buffer pool | `BF`, `SP` | 82 | done |
| 3 | WAL | Write-Ahead Log | `WR`, `FL`, `RP` | 132 | done |
| 4 | ENG/Memtable+SST | Lock-free skiplist + memtable + SST | `LS` | 203 | done |
| 5 | TXN/MVCC | Version chain + per-thread arena | `MV`, `LC`, `SN` | 66 | done |
| 6 | TXN/Protocol | Transaction slot + commit + WAL | `VL` | 93 | done |
| 7 | SQL/Core | Lexer + parser + rewriter | `LX`, `PS`, `RE` | 102 | done |
| 8 | SQL/Execute | Planner + executor | `PL`, `EX` | 67 | done |
| 9 | SYS+Integration | Public API + end-to-end | `AP`, `SY`, `SE`, `TX`, `ST` | 57 | done (v0.6.3) |
| 10 | NOT NULL / DEFAULT | Column constraints end-to-end | `PS`, `EX` | 16 | done (v0.7.0) |
| 11 | UNIQUE Constraint | Single + composite + multi-clause UNIQUE | `PS`, `EX` | 11 | done (v0.8.0); I/O refinements pending v0.8.1 |
| 11b | I/O refinements | MADV_DONTNEED + mmap BlockDevice | `MEM/BF`, `FIL/DF` | 4 | done (v0.8.1) |
| 12 | Catalog Persistence | System catalog (single-file, atomic rename, schema versioning) | `LS`, `EX`, `SY` | done (v0.9.0); 4 pre-existing LS bugs (REQ000186–189) deferred to iter-12b |
| 13 | WAL Corruption Recovery | Segment header + envelope CRC + bounded resync + Stats | `WAL/WR`, `WAL/RP` | done (v0.10.0); coverage 72.5% (target 85%) — see REQ000191 |
| 14 | Graceful Shutdown Completion | 6-phase sequence + per-subsystem Close + active-tx wait + config validation | `SYS/SY`, `SYS/AP`, `TXN/VL`, `ENG/LS`, `LOG/HK` | done (v0.10.1) |
| 15 | Finish-Line + iter-12b Quick Bugs | Session pooling, read-only mode, WriteBuffer, hazard pointer fix, iter-12b bug fixes (sstIterator, nextFileID), WAL stats surfacing | `SYS/SE`, `SYS/SY`, `WAL/FL`, `WAL/WR`, `TXN/LC`, `ENG/LS` | done (v0.11.0) |
|16 | ParamBinding + iter-12b Path Fix + Quick Wins | binding + type coercion, SST path unification, bloom dynamic sizing, arena-on-slot, gzip log rotation, WAL/RP coverage | `SQL/EX`, `SYS/ST`, `ENG/LS`, `LOG/LG`, `TXN/VL`, `WAL/RP` | done (v0.12.0) |
| 17 | Storage Benchmarks + Quick Wins | ENG/LS benchmarks, WAL encoding, LOG docs, SQL/RE coverage lift | `ENG/LS`, `WAL/WR`, `LOG/HK`, `SQL/RE` | done (v0.13.0) |
| 18 | Storage Quality | BloomFilter alignment, per-thread arena init, batch commit, clock-sweep audit, plan memoization | `ENG/LS`, `TXN/MV`, `WAL/FL`, `MEM/BF`, `SQL/PL` | done (v0.14.0) |
| 19 | SIMD Vectorization + Parallel Query | Batch execution, columnar memory, SIMD predicates, parallel sort | `SQL/EX` | done (v0.15.0) |
| 20 | SQL Completeness | CHECK constraints, type affinity, OUTER JOIN, HAVING, CASE/EXISTS, DECIMAL, parser tests | `SQL/PS`, `SQL/EX`, `SQL/PL` | done (v0.17.0) |
| 21 | EXPLAIN + Advanced SQL | EXPLAIN support, UPSERT, RETURNING, CTE (WITH), SAVEPOINT | `SQL/LX`, `SQL/PS`, `SQL/PL`, `SQL/EX`, `SYS/SE` | done (v0.18.0) |

All iterations through iter-21 complete. Released as v0.9.0–v0.18.0. Coverage details: `go test ./... -cover`.

## Completion Criteria (All Iterations)

| Rule | Command | State |
|---|---|---|
| Lint | `go vet ./...` — zero warnings | green |
| Format | `gofmt -s -l .` — no drift | green |
| Test | `go test ./... -race -count=1` — all green | green (race-stable across 5+ runs as of v0.6.x) |
| Benchmark | At least one `Benchmark*` per storage component | **partial** — `ENG/LS` has none (see iter-04 note). |

## Iteration Detail

Each iteration is documented in `development/iterations/iter-XXX.md`.
All eight iterations are now in `done` state.

## Directory Structure

See `design/ARCH.md` for full structure. As of v0.6.3, SYS subsystem tests
are organized by function domain (AP/, SE/, ST/, SY/, TX/).

## Release Tags

| Tag | Commit | Iterations |
|---|---|---|
| v0.1.0–v0.1.2 | LOG era | iter-00 |
| v0.2.0–v0.2.1 | FIL era | iter-01 |
| v0.3.0 | MEM era | iter-02 |
| v0.4.0–v0.4.1 | WAL + TXN/MV fix | iter-03, iter-05/06 |
| v0.5.0 | SQL/Execute close-out | iter-08 |
| v0.6.0 | SYS + Integration | iter-09 |
| v0.6.1 | Test failure fixes (LOG/LG + ENG/LS race) |
| v0.6.2 | Code quality: staticcheck, dead code, perf |
| **v0.6.3** | **SYS tests**: reorganized into function domains |
| **v0.7.0** | **NOT NULL / DEFAULT constraints** end-to-end (`AP.ErrConstraint`, `SQL/EX/constraints.go`) |
| v0.7.1 | Test reorganization: function-domain grouping, root `SYS/doc.go` cleanup. No code change. |
| **v0.8.0** | **UNIQUE constraint** single + composite + multi-clause (`SQL/PS` AST + parser, `SQL/EX` `checkUnique`) |
| **v0.8.1** | **I/O refinements**: `MADV_DONTNEED` hints on buffer eviction + `mmap` BlockDevice for SST reads (Linux build tag; pread fallback elsewhere) |
| **v0.9.0** | **Catalog Persistence** (`ENG/LS/catalog.go` + `SYS/SY/catalog_init.go`). `CREATE TABLE` / `DROP TABLE` survive `Close`/`Open` via an atomic-rename single-file format. Code in `87a3b61`. Recorded technical debt: four pre-existing LSM bugs (path mismatch, sstIterator state, checksum layout, double-`nextFileID`) deferred to iter-12b. |
| **v0.10.0** | **WAL Corruption Recovery** (`WAL/WR/header.go` + `WAL/RP/rp.go`). Segment header (12 B) + envelope CRC32-IEEE + bounded resync to `MaxRecordLen`; tail-of-segment torn writes tolerated (`TruncatedSegments++`), mid-segment corruption surfaces `ErrCorrupt` (`CorruptionFailures++`); Replayer.Stats() exposed. Code in `495bdac`. Coverage 72.5% (target 85%, gap tracked as REQ000191). |
| **v0.10.1** | **Graceful Shutdown Completion** (`SYS/SY/shutdown.go` + `validate.go` + `TXN/VL/cond.go` + 4 subsystem `Stop(ctx)` APIs). Implements SYS.md:215-282: stop accept (closed flag flips first), wait for active tx (30s, force-abort on timeout, see `ForceAbortAll`), flush (`eng.Sync` + `wal.Sync` + `fl.Sync`), stop background goroutines (compaction/flush/epoch; 5s budget each), close subsystems in reverse order, log final stats. New `ShutdownStats` type on `EngineStats.LastShutdown`. Iter-12's 4 pre-existing LS bugs (REQ000186-189) still surface on shutdown; deploy iter-12b first if production read-after-restart matters. See `iterations/iter-14-shutdown.md`. |
| **v0.11.0** | **Finish-Line + iter-12b Quick Bugs** (iter-15). Session pooling (`sync.Pool` for Session objects), read-only mode (`O_RDONLY` on DF+WAL, DML returns `ErrReadOnly`), WAL WriteBuffer (256 KB pre-allocated), hazard pointer fix (PublishCurrent/PublishNext split), sstIterator first-block fix, flushManager nextFileID dedup, WAL stats surfacing (TruncatedSegments/UnknownRecords/CorruptionFailures). Code in `b2f4396`. Test suite: 150s → <2s. |
| **v0.12.0** | **Param Binding + iter-12b Path Fix + Quick Wins** (iter-16). Placeholder end-to-end binding + Go-type-vs-SQL-column-type coercion (SYS/ST/st.go + SQL/EX WithParams propagation + CatalogColumn.Type encoded in catalog wire format). SST path unification (flush now writes the same hex-encoded sst/L<N>_<minkey-hex>_<maxkey-hex>_<id>.sst shape that compaction produces; integer primary keys no longer hit null bytes in the filename). Bloom filter dynamic sizing from keyCount (10 bits/key). Per-transaction arena moved onto transactionSlot so slot recycling reclaims the arena. Gzip compression of rotated log files. WAL/RP coverage lift: multi-segment truncateBeforeCheckpoint cases + ErrUnknownRecord branch covered (72.5% to 75.2%). Code in `7464cea`. |
| **v0.13.0** | **Storage Benchmarks + Quick Wins** (iter-17). ENG/LS benchmarks (skiplist, SST read/write, flush, compaction), WAL RTMerge record encoding, LOG debug allocation docs, SQL/RE AST normalization coverage lift to 65%. |
| **v0.14.0** | **Storage Quality** (iter-18). BloomFilter FNV-1a double-hash alignment with design, per-thread arena lazy init via sync.Pool, batch commit with sync.WaitGroup + write barrier, clock-sweep audit, plan memoization (SHA256 of AST binary encoding). |
| **v0.15.0** | **SIMD Vectorization + Parallel Query** (iter-19). Batch execution (1024-row columnar), columnar memory management (sync.Pool), SIMD predicate evaluation (4-wide unrolling + selection vectors), parallel sort (sample sort for top-k), parallel query execution (worker pool + fan-out/fan-in + channel merge). |
| **v0.17.0** | **SQL Completeness** (iter-20). CHECK constraint parsing + enforcement, type affinity system (SQLite-like 5 affinities), OUTER JOIN executor (LEFT/RIGHT/FULL), HAVING filter, CASE/EXISTS parser tests, DECIMAL type storage (big.Float), parameterized types VARCHAR(N)/DECIMAL(P,S), parser CASE/EXISTS tests, type tokens (NUMERIC/DATE/TIME/JSON/DECIMAL), SQL/PL coverage 30.6% → 98.8%. |
| **v0.18.0** | **EXPLAIN + Advanced SQL** (iter-21). Full EXPLAIN + EXPLAIN QUERY PLAN (SQLite-compatible output: id, parent, notused, detail), PlanNode tree with cost estimation, RETURNING clause for INSERT/UPDATE/DELETE, ON CONFLICT (UPSERT) with DO NOTHING, CTE (WITH) via naive inlining, SAVEPOINT/RELEASE/ROLLBACK TO with stack-based nested savepoints. 18 REQs across 4 blocks, 8 commits, ~2,800 LOC. |
| **v0.19.0** | **Secondary Indexes MVP** (iter-22). CREATE/DROP INDEX parsing, IndexScan real seek via LSM-backed index store, cost-based index selection in planner, index maintenance on INSERT/DELETE, column-level selectivity stats types. ~3,000 LOC, 8 commits, ~50 tests. |
| **v0.19.1** | **Test stability & speed**. Fix TestConcurrentTransactions key conflict storm (distinct keys k0-k9), TXN/VL test parallelization (25s → 1.4s), pool corruption fix, worker pool timeout handling. 5 files changed, 47 insertions. |

## Design Protection

Design documents in `design/subsystems/*.md` are authoritative. Human-only edits.
