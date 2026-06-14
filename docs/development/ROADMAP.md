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
`docs/design/ARCH.md`.)

## Iterations Overview

| # | Name | Subsystem | Clusters | Status |
|---|---|---|---|
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
| 11b | I/O refinements | MADV_DONTNEED + mmap BlockDevice | `MEM/BF`, `FIL/DF` | done (v0.8.1) |
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
| 27 | Maturity Push | SLT 42/42, trigger parser, generated cols, SST dict compression, WAL columnar, rate-limited compactor, sub-compaction, compaction style, W-TinyLFU, off-heap pool, escape hints, QSBR, FROM-subquery parser fix | `SQL/PS`, `SQL/EX`, `ENG/LS`, `WAL/WR`, `MEM/BF`, `MEM/OF`, `TXN/MV`, `TXN/LC` | done (v0.27.0) |

All iterations through iter-26.10 complete. Released as v0.9.0–v0.26.6. Coverage details: `go test ./... -cover`.

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

See `docs/design/ARCH.md` for full structure. As of v0.6.3, SYS subsystem tests
are organized by function domain (AP/, SE/, ST/, SY/, TX/).



## Design Protection

Design documents in `docs/design/subsystems/*.md` are authoritative. Human-only edits.
