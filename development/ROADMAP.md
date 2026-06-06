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
| 12 | Catalog Persistence | System catalog (single-file, atomic rename, schema versioning) | `LS`, `EX`, `SY` | done in code (v0.9.0 tag pending); 4 pre-existing LS bugs (REQ000186–189) deferred to iter-12b |
| 13 | WAL Corruption Recovery | Segment header + envelope CRC + bounded resync + Stats | `WAL/WR`, `WAL/RP` | done in code (v0.10.0 tag pending); coverage 72.5% (target 85%) — see REQ000191 |

All iterations through iter-13 complete in code. v0.9.0 and v0.10.0 release tags are pending cut. Coverage details: `go test ./... -cover`.

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
| **v0.9.0** *(tag pending)* | **Catalog Persistence** (`ENG/LS/catalog.go` + `SYS/SY/catalog_init.go`). `CREATE TABLE` / `DROP TABLE` survive `Close`/`Open` via an atomic-rename single-file format. Code in `87a3b61`. Recorded technical debt: four pre-existing LSM bugs (path mismatch, sstIterator state, checksum layout, double-`nextFileID`) deferred to iter-12b. |
| **v0.10.0** *(tag pending)* | **WAL Corruption Recovery** (`WAL/WR/header.go` + `WAL/RP/rp.go`). Segment header (12 B) + envelope CRC32-IEEE + bounded resync to `MaxRecordLen`; tail-of-segment torn writes tolerated (`TruncatedSegments++`), mid-segment corruption surfaces `ErrCorrupt` (`CorruptionFailures++`); Replayer.Stats() exposed. Code in `495bdac`. Coverage 72.5% (target 85%, gap tracked as REQ000191). |

## Design Protection

Design documents in `design/subsystems/*.md` are authoritative. Human-only edits.
