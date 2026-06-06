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

All iterations complete. Coverage details: `go test ./... -cover`.

## Remaining Work

The v1 chain LOG → SYS is closed. Production-ready path: two phases.

### Phase 1: Correctness & Ops Foundation (iter-11 to iter-17)

| Iter | Requirements | Goal |
|---|---|---|
| iter-11 | REQ000107 | `UNIQUE` constraint |
| iter-12 | REQ000127 | Catalog persistence across restarts |
| iter-13 | REQ000035 | WAL corruption recovery policy |
| iter-14 | REQ000061 | Read-committed isolation (default) |
| iter-15 | REQ000062 | MVCC reads in transaction (SELECT sees own writes) |
| iter-16 | REQ000102 | Admin CLI (`razor-admin`: schema dump, vacuum, manual compaction) |
| iter-17 | REQ000044, REQ000138, REQ000143 | `ENG/LS` benchmarks + coverage lift |

### Phase 2: SQL Standards Compliance (iter-18 to iter-20)

| Iter | Requirements | Goal |
|---|---|---|
| iter-18 | REQ000113 | `GROUP BY` |
| iter-19 | REQ000117 | `OUTER JOIN` (LEFT/RIGHT/FULL) |
| iter-20 | REQ000126 | Foreign keys |

### Out of scope (deferred)

- Network server, Prometheus, session pooling, read-only mode, multi-process
- Secondary indexes, mmap, WAL/log compression, generational arena
- Backup/restore, online schema migration, advanced SQL features

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

## Design Protection

Design documents in `design/subsystems/*.md` are authoritative. Human-only edits.
