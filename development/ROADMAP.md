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

| # | Name | Subsystem | Clusters | Est. LOC | Tests | Cov | Benches | Status |
|---|---|---|---|---|---|---|---|---|
| 0 | LOG | Structured logging | `LG`, `HK` | ~1,500 | 108 | 71.6 / 98.0 | 0 / 0 | done |
| 1 | FIL | File I/O | `DF`, `MF`, `LF`, `FS` | ~2,500 | 188 | 83.9 / 80.0 / 76.0 / 76.0 | 5 / 0 / 0 / 0 | done |
| 2 | MEM | Buffer pool | `BF`, `SP` | ~2,000 | 82 | 74.7 / 95.5 | 5 / 4 | done |
| 3 | WAL | Write-Ahead Log | `WR`, `FL`, `RP` | ~2,000 | 132 | 85.8 / 90.3 / 71.1 | 4 / 0 / 0 | done |
| 4 | ENG/Memtable+SST | Lock-free skiplist + memtable + SST + manifest + flush | `LS` | ~8,000 | 195 | 75.2 | 0 | done |
| 5 | TXN/MVCC | Version chain + per-thread arena | `MV`, `LC`, `SN` | ~3,000 | 67 | 91.8 / 98.0 / 82.4 | 6 / 3 / 0 | done |
| 6 | TXN/Protocol | Transaction slot + commit + WAL | `VL` | ~2,000 | 93 | 98.7 | 5 | done |
| 7 | SQL/Core | Lexer + parser + rewriter | `LX`, `PS`, `RE` | ~2,500 | 102 | 89.5 / 66.6 / 49.0 | 2 / 4 / 0 | done |
| 8 | SQL/Execute | Planner + executor | `PL`, `EX` | ~3,000 | 0 / 42 | n/a / 72.8 | 0 / 8 | partial (EX has ~4.4k LoC of planner/eval/operators/writers; no wired end-to-end driver; PL/ has only a stub) |
| 9 | SYS+Integration | Public API + end-to-end | `SY`, `AP`, `SE`, `TX`, `ST` | ~3,000 | 0 | n/a | 0 | pending |

**Coverage / Benchmark legend:** each iter's cluster columns are listed in the order the clusters appear in the design dir tree. `0 / 42` for iter-08 = "0 in PL / 42 in EX".

**Iteration-by-iteration status notes:**

- **iter-04 (ENG/LS)** — 0 benchmarks, which violates the AGENTS.md rule that every storage component must have a benchmark. Needs `Benchmark*` for skiplist insert/get, SST write/read, and flush.
- **iter-05 (TXN/SN)** — 0 benchmarks, but SN is small and read-only; arguably exempt.
- **iter-07 (SQL/RE)** — 49.0% coverage is the lowest in the project. Parser/rewriter paths are exercised but error/edge branches aren't.
- **iter-08 (SQL/Execute)** — partially scaffolded: `EX/` has ~4.4k LoC (planner, memo, eval, operators, writers, joins, subqueries, aggregates, 8 benchmarks) but no wired end-to-end driver. The iter-08 spec's planned `PL/` cluster is mostly empty; the planner code lives in `EX/`. The `iter-08.md` requirements list is fully pending.

## Completion Criteria (All Iterations)

| Rule | Command |
|---|---|
| Lint | `go vet ./...` — zero warnings |
| Format | `gofmt -s -l .` — no drift |
| Test | `go test ./... -race -count=1` — all green |
| Benchmark | At least one `Benchmark*` per storage component |

## Iteration Detail

Each iteration is documented in `development/iterations/iter-XXX.md`.

## Design Alignment

All iterations use directory structures that match `design/subsystems/*.md`:

```
internal/
├── LOG/LG/  # Logger: slog wrapper, sharedLogger (level pointer shared in With()), SetOutput
├── LOG/HK/  # Hook: LogEvent (exported), HookRegistry with Dropped/Dispatched stats
├── FIL/DF/  # DataFile: block I/O, checksum
├── FIL/MF/  # MetaFile: meta.razor, magic/version
├── FIL/LF/  # LogFile: WAL segment handles
├── FIL/FS/  # FileSystem: path validation, SyncDir
├── MEM/BF/  # Buffer pool: clock-sweep LRU, hash table, hint file
├── MEM/SP/  # Sync pool: page/iterator buffers
├── WAL/WR/  # Writer: append, segment rotation
├── WAL/FL/  # Flusher: fsync, LSN counter
├── WAL/RP/  # Replay: WAL recovery
├── ENG/LS/  # LSM tree: skiplist, SST, manifest
├── ENG/ID/  # Index: primary key
├── ENG/TB/  # Table: create/drop, schema registry
├── ENG/SC/  # Schema: column types, constraints
├── ENG/DP/  # Deparser: row serialization
├── TXN/MV/  # MVCC: version chain, arena
├── TXN/LC/  # Lock: hazard pointers, epoch
├── TXN/SN/  # Snapshot: read view
├── TXN/VL/  # Validation: commit protocol, slots
├── SQL/LX/  # Lexer: tokenization
├── SQL/PS/  # Parser: recursive descent, AST
├── SQL/PL/  # Planner: cost, index select
├── SQL/EX/  # Executor: operator tree
├── SQL/RE/  # Rewriter: constant fold, pushdown
├── SYS/SY/  # System: init, shutdown
├── SYS/AP/  # API: Engine, Options, errors
├── SYS/SE/  # Session: lifecycle, deadline
├── SYS/TX/  # Transaction: context, savepoints
└── SYS/ST/  # Statement: prepare, bind
```

## Design Protection

All design documents in `design/subsystems/*.md` are authoritative. Updates to design must be applied by a human — AI agents must not auto-edit design files.