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

| # | Name | Subsystem | Clusters | Est. LOC | Tests | Cov | Benches | Status |
|---|---|---|---|---|---|---|---|---|
| 0 | LOG | Structured logging | `LG`, `HK` | ~1,500 | 108 | 78 / 98.0 | 0 / 0 | done (LG pre-existing fail in v0.4; see Notes) |
| 1 | FIL | File I/O | `DF`, `MF`, `LF`, `FS` | ~2,500 | 187 | 83.9 / 80.0 / 74.4 / 76.0 | 5 / 0 / 0 / 0 | done |
| 2 | MEM | Buffer pool | `BF`, `SP` | ~2,000 | 82 | 74.7 / 95.5 | 6 / 4 | done |
| 3 | WAL | Write-Ahead Log | `WR`, `FL`, `RP` | ~2,000 | 132 | 85.8 / 90.3 / 71.1 | 4 / 0 / 0 | done |
| 4 | ENG/Memtable+SST | Lock-free skiplist + memtable + SST + manifest + flush | `LS` | ~8,000 | 203 | 74.9 | 0 | done |
| 5 | TXN/MVCC | Version chain + per-thread arena | `MV`, `LC`, `SN` | ~3,000 | 66 | 88.9 / 98.0 / 82.4 | 6 / 3 / 0 | done |
| 6 | TXN/Protocol | Transaction slot + commit + WAL | `VL` | ~2,000 | 93 | 98.1 | 5 | done |
| 7 | SQL/Core | Lexer + parser + rewriter | `LX`, `PS`, `RE` | ~2,500 | 102 | 89.5 / 66.6 / 49.0 | 2 / 4 / 0 | done (RE 49% is the project low) |
| 8 | SQL/Execute | Planner + executor | `PL`, `EX` | ~3,000 | 4 / 63 | 30.6 / 72.8 | 0 / 8 | **done** (v0.5.0; PL/ now owns memo + planner entry, EX/ has full operator tree) |
| 9 | SYS+Integration | Public API + end-to-end | `AP`, `SY`, `SE`, `TX`, `ST` | ~3,000 | 57 | 100.0 / 100.0 / 100.0 / 100.0 / 100.0 | 2 / 0 / 0 / 0 / 0 | **done** (v0.6.0; tests reorganized into function domains in v0.6.3) |

**Coverage / Benchmark legend:** each iter's cluster columns are listed in
the order the clusters appear in the design dir tree. For example
`74.4 / 76.0` for iter-01 = "DF=83.9, MF=80.0, LF=74.4, FS=76.0".

**Iteration-by-iteration status notes:**

- **iter-04 (ENG/LS)** — 0 benchmarks, which violates the AGENTS.md rule
  that every storage component must have a benchmark. The skiplist,
  memtable, and SST writer are exercised by integration tests but no
  `Benchmark*` exists. **Open for v1.1**.
- **iter-05 (TXN/SN)** — 0 benchmarks, but SN is small and read-only;
  arguably exempt. MVCC version chain and arena each have benchmarks.
- **iter-07 (SQL/RE)** — 49.0% coverage is the lowest in the project.
  Constant folding and predicate pushdown paths work but error
  branches are under-tested. **Open for round-2 coverage**.
- **iter-08 (SQL/Execute)** — **done as of v0.5.0**. 22/23 requirements
  complete; R10 (IndexScan) is partial — the operator exists and is
  selected by the planner, but the seek-by-key waits for `ENG/ID/`.
  Current behavior degrades to a prefix scan through the engine.
- **iter-09 (SYS+Integration)** — **done as of v0.6.0**. All 29
  requirements complete, 57 tests, 100.0% statement coverage,
  BenchmarkEngineSelect ~4.3µs/op. R29 documents the v1 isolation
  model: read-uncommitted between transactions, read-your-own-writes
  within, shadow writeSet ROLLBACK. **v0.6.3 reorganized tests into
  function-domain subdirectories (AP/, SE/, ST/, SY/, TX/) per ARCH.md
  design; top-level SYS directory now contains no source code.**

## What's Remaining (post-v0.6.0)

The v1 chain LOG → SYS is closed. Remaining work splits into
**v1.1 (in-process refinements)** and **v2 (architectural upgrades)**.

### v1.1 — in-process refinements (small, low-risk)

| # | Item | Subsystem | Owner | Notes |
|---|---|---|---|---|
| 1 | Round-2 coverage for `SQL/RE` | RE | SQL | 49% → 80%+; tests for constant-fold branches, pushdown, subquery flatten. |
| 2 | Add `Benchmark*` for `ENG/LS` | LS | ENG | Skiplist insert/get, SST write/read, flush. AGENTS.md rule. |
| 3 | IndexScan real seek | EX/ID | ENG+SQL | Once `ENG/ID/` lands, swap the prefix-scan fallback for a true index seek. iter-08 R10 partial becomes done. |
| 4 | Full MVCC reads inside transactions | TX/SYS | TXN+SYS | Current R29 is shadow-writeSet; v1.1 adds MVCC-aware iterator so SELECT in tx sees own writes through Tx. |
| 5 | NOT NULL + DEFAULT constraints | PS/EX | SQL | Parser already accepts; validator/evaluator paths need wiring. |
| 6 | LOG/LG `TestListRotatedFiles_DirMissing` fix | LG | LOG | Fixed in v0.6.x: pre-existing test bug (hardcoded path) and the underlying race in ENG/LS flushManager close. |
| 7 | Catalog persistence | EX/TX | SYS | Reopen loses the in-memory table schema; ship a catalog LSM in `ENG/ID/`. |

### v1.1+ operators already in EX/ (work is shipping+documenting, not building)

The executor already implements these — they pass tests and ship with
v0.5/v0.6 but are tagged as v1.1+ in source. Document them in
`design/ARCH.md` and decide which to promote to v1:

| Operator | File | Status | Notes |
|---|---|---|---|
| `Aggregate` (COUNT/SUM/AVG/MIN/MAX) | `EX/aggregate.go` | working | Grouped + non-grouped; tested in `TestE2E_FullCRUD_AgainstEngine`. |
| `HashAggregate` | `EX/hashagg.go` | working | v1.1 alternative; kept for future perf work. |
| `NestedLoopJoin` (INNER/CROSS) | `EX/join.go` | working | OUTER joins are not in v1. |
| `Distinct` | `EX/distinct.go` | working | Hash-based; tested. |
| `Subquery` (IN/EXISTS/scalar) | `EX/subq.go` | working | Tested via `TestExecutorInSubquery`. |
| `EXPLAIN` | `EX/explain.go` | working | Renders the operator tree as text; used by `Executor.Explain`. |

### v2 — architectural upgrades (large, defer)

| # | Item | Notes |
|---|---|---|
| 1 | `ENG/ID/` — primary key index cluster | Replace `ENG/ID` placeholder; secondary indexes; replace `IndexScan` fallback with real seek. |
| 2 | `ENG/TB/` — table registry | Persist CREATE TABLE / DROP TABLE across restarts; currently in-memory only. |
| 3 | `ENG/SC/` — schema cluster | Column types, constraints, version check; currently folded into LS schema. |
| 4 | `ENG/DP/` — deparser cluster | Split row serialization out of LS; expose `EncodeRow`/`DecodeRow` as a separate package. |
| 5 | `MEM/PC/` — page cluster | Page-level operations as a separate concern from BF. |
| 6 | Read-committed isolation | Bring inter-transaction isolation up from read-uncommitted (R29 v1) to read-committed. |
| 7 | Network server | TCP/gRPC listener; SYS.Serve() entry point. |
| 8 | Prometheus metrics | Wire `BufferPool.Stats`, `WALStats`, `TxnStats` to a metrics endpoint. |
| 9 | Session pooling | `sync.Pool` for sessions; v1 allocates per call. |
| 10 | Read-only mode | Honor `Options.ReadOnly = true`; skip WAL writes. |
| 11 | Admin interface | Operational tooling: schema dump, vacuum, manual compaction. |

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
├── ENG/LS/  # LSM tree: skiplist, SST, manifest  (v1)
├── ENG/ID/  # Index: primary key                  (v2 — placeholder only)
├── ENG/TB/  # Table: create/drop, schema registry (v2 — folded into EX for v1)
├── ENG/SC/  # Schema: column types, constraints   (v2 — folded into LS for v1)
├── ENG/DP/  # Deparser: row serialization          (v2 — folded into LS for v1)
├── TXN/MV/  # MVCC: version chain, arena
├── TXN/LC/  # Lock: hazard pointers, epoch
├── TXN/SN/  # Snapshot: read view
├── TXN/VL/  # Validation: commit protocol, slots
├── SQL/LX/  # Lexer: tokenization
├── SQL/PS/  # Parser: recursive descent, AST
├── SQL/PL/  # Planner: cost, index select, memo
├── SQL/EX/  # Executor: operator tree
├── SQL/RE/  # Rewriter: constant fold, pushdown
└── SYS/  # System layer (top-level directory; see function domains below)
    ├── AP/  # API: Engine, Session, Transaction, Stmt, Options, errors
    ├── SY/  # System: init, shutdown, stats aggregation
    ├── SE/  # Session: lifecycle, deadline, session stats
    ├── TX/  # Transaction: context, commit/rollback, savepoints
    └── ST/  # Statement: prepare, bind, type coercion
```

Note: As of v0.6.3, the top-level `SYS` directory contains no source code
files directly — all code resides in function-domain subdirectories per
`design/ARCH.md` structure.

## Release Tags

| Tag | Commit | Iterations |
|---|---|---|
| v0.1.0–v0.1.2 | LOG era | iter-00 |
| v0.2.0–v0.2.1 | FIL era | iter-01 |
| v0.3.0 | MEM era | iter-02 |
| v0.4.0–v0.4.1 | WAL + TXN/MV fix | iter-03, iter-05/06 |
| v0.5.0 | SQL/Execute close-out | iter-08 |
| **v0.6.0** | **SYS + Integration** | **iter-09** |
| v0.6.1 | Pre-existing test failure fixes (LOG/LG + ENG/LS flush race) |
| v0.6.2 | Code-quality pass: staticcheck warnings, dead code removal, perf (sync.Pool boxing), deduplication (firstLogger / table-not-registered sentinel / replaySegment-forEachRecord / decodeVarint), hint-file atomic write |
| **v0.6.3** | **SYS test reorganization**: move tests to function-domain subdirectories (AP/, SE/, ST/, SY/, TX/); SYS is now a pure directory container |

## Design Protection

All design documents in `design/subsystems/*.md` are authoritative. Updates
to design must be applied by a human — AI agents must not auto-edit
design files.
