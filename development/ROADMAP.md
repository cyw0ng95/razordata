# Razordata Development Roadmap

## MVP Feature Scope

- **DDL:** CREATE TABLE, DROP TABLE
- **DML:** INSERT, UPDATE, DELETE, SELECT
- **Clauses:** WHERE, ORDER BY, LIMIT, OFFSET
- **Constraints:** PRIMARY KEY, NOT NULL, DEFAULT
- **Types:** INTEGER, TEXT, BOOLEAN
- **Transactions:** BEGIN, COMMIT, ROLLBACK (MVCC)

## Out of Scope (v1)

Joins, subqueries, foreign keys, network server, external C dependencies.

## Iterations Overview

| # | Name | Subsystem | Est. LOC | Status |
|---|---|---|---|---|
| 0 | LOG | Structured logging | ~1,500 | pending |
| 1 | FIL | File I/O | ~2,500 | pending |
| 2 | MEM | Buffer pool | ~2,000 | pending |
| 3 | WAL | Write-Ahead Log | ~2,000 | pending |
| 4 | ENG/Memtable | Lock-free skiplist + memtable | ~2,000 | pending |
| 5 | ENG/SST | SST writer + reader + manifest | ~3,500 | pending |
| 6 | ENG/Schema+Read | Row serialization + read path | ~2,000 | pending |
| 7 | TXN/MVCC | Version chain + per-thread arena | ~3,000 | pending |
| 8 | TXN/Protocol | Transaction slot + commit + WAL | ~2,000 | pending |
| 9 | SQL/Core | Lexer + parser + rewriter | ~2,500 | pending |
| 10 | SQL/Execute | Planner + executor | ~3,000 | pending |
| 11 | SYS+Integration | Public API + end-to-end | ~3,000 | pending |

## Completion Criteria (All Iterations)

| Rule | Command |
|---|---|
| Lint | `go vet ./...` — zero warnings |
| Format | `gofmt -s -l .` — no drift |
| Test | `go test ./... -race -count=1` — all green |
| Benchmark | At least one `Benchmark*` per storage component |

## Iteration Detail

Each iteration is documented in `development/iterations/iter-XXX.md`.

## Design Protection

All design documents in `design/subsystems/*.md` are authoritative. Updates to design must be applied by a human — AI agents must not auto-edit design files.