# Razordata Development Roadmap

## MVP Feature Scope (v1 — complete)

- **DDL:** CREATE TABLE, DROP TABLE
- **DML:** INSERT, UPDATE, DELETE, SELECT
- **Clauses:** WHERE, ORDER BY, LIMIT, OFFSET
- **Constraints:** PRIMARY KEY, NOT NULL, DEFAULT (PK exercised; NOT NULL/DEFAULT deferred to v1.1)
- **Types:** INTEGER, TEXT, BOOLEAN
- **Transactions:** BEGIN, COMMIT, ROLLBACK

The v1 dependency chain **LOG → FIL → MEM → WAL → ENG → TXN → SQL → SYS** is
complete as of v0.6.0. All ten iterations (0-9) are done; iter-08 and
iter-09 both shipped as fully-tested milestones. iter-10 (v0.7.0) is the
next planned milestone and adds the v2 ENG/ID + ENG/TB + ENG/SC + ENG/DP
clusters.

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
| 9 | SYS+Integration | Public API + end-to-end | `AP`, `SY`, `SE`, `TX`, `ST` | ~3,000 | 57 | 100.0 | 2 | **done** (v0.6.0) |
| 10 | ENG/Index+Catalog | v2 ENG/ID + ENG/TB (+ DP/SC extraction) + EX IndexScan real seek | `ID`, `TB`, `SC`, `DP`, `EX`, `SY` | ~5,000 | ~25 new | ID ≥ 80, TB ≥ 85, DP/SC ≥ 90 | 3 (PKIndex) + 2 (Catalog) | **pending** (v0.7.0) |

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
  within, shadow writeSet ROLLBACK.

## What's Remaining (post-v0.6.0; v0.7.0 in flight via iter-10)

The v1 chain LOG → SYS is closed. iter-10 (v0.7.0) ships the
`ENG/ID/` and `ENG/TB/` architectural pieces and extracts the
`ENG/SC/` and `ENG/DP/` clusters. After iter-10 lands, remaining
work splits into **v1.1 (in-process refinements)** and **v2
(architectural upgrades)**.

### v1.1 — in-process refinements (small, low-risk)

| # | Item | Subsystem | Owner | Notes |
|---|---|---|---|---|
| 1 | Round-2 coverage for `SQL/RE` | RE | SQL | 49% → 80%+; tests for constant-fold branches, pushdown, subquery flatten. |
| 2 | Add `Benchmark*` for `ENG/LS` skiplist/memtable/SST | LS | ENG | Skiplist insert/get, SST write/read, flush. AGENTS.md rule. iter-10 ships `pkindex_bench.go` and `catalog_bench.go`; the skiplist/memtable/SST benchmarks remain. |
| 3 | Full MVCC reads inside transactions | TX/SYS | TXN+SYS | Current R29 is shadow-writeSet; v1.1 adds MVCC-aware iterator so SELECT in tx sees own writes through Tx. |
| 4 | NOT NULL + DEFAULT constraints | PS/EX/SC | SQL+ENG | Parser accepts; validator/evaluator paths need wiring. **Blocked** by iter-10's row codec unification (EX-side and SC-side formats must converge before enforcement). |
| 5 | LOG/LG `TestListRotatedFiles_DirMissing` fix | LG | LOG | Fixed in v0.6.x: pre-existing test bug (hardcoded path) and the underlying race in ENG/LS flushManager close. |

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

iter-10 (v0.7.0) closes **items 1–4** of this list: `ENG/ID/`, `ENG/TB/`,
`ENG/SC/`, `ENG/DP/` all ship in iter-10. The remaining items are:

| # | Item | Notes |
|---|---|---|
| 5 | `MEM/PC/` — page cluster | Page-level operations as a separate concern from BF. |
| 6 | Read-committed isolation | Bring inter-transaction isolation up from read-uncommitted (R29 v1) to read-committed. |
| 7 | Network server | TCP/gRPC listener; SYS.Serve() entry point. |
| 8 | Prometheus metrics | Wire `BufferPool.Stats`, `WALStats`, `TxnStats` to a metrics endpoint. |
| 9 | Session pooling | `sync.Pool` for sessions; v1 allocates per call. |
| 10 | Read-only mode | Honor `Options.ReadOnly = true`; skip WAL writes. |
| 11 | Admin interface | Operational tooling: schema dump, vacuum, manual compaction. |
| 12 | Secondary indexes | Build on the `ENG/ID/` PK index surface; same `Insert/Search/Delete` API extended to non-PK columns. **Pre-req:** iter-10 done. |
| 13 | `EX/planner.go` → `PL/` reconcile | iter-08 unresolved divergence: planner code lives in `EX/`, design says `PL/`. Requires human decision on `design/subsystems/SQL.md`. |

## Completion Criteria (All Iterations)

| Rule | Command | State |
|---|---|---|
| Lint | `go vet ./...` — zero warnings | green |
| Format | `gofmt -s -l .` — no drift | green |
| Test | `go test ./... -race -count=1` — all green | green (race-stable across 5+ runs as of v0.6.x) |
| Benchmark | At least one `Benchmark*` per storage component | **partial** — `ENG/LS` has none (see iter-04 note). |

## Iteration Detail

Each iteration is documented in `development/iterations/iter-XX-<name>.md`.
iter-00 through iter-09 are now in `done` state; **iter-10 is `pending`**
and is the next unit of work.

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
├── ENG/LS/  # LSM tree: skiplist, SST, manifest  (v1; iter-10 keeps as the storage driver)
├── ENG/ID/  # Index: primary key                  (v0.7.0 — iter-10)
├── ENG/TB/  # Table: persistent catalog            (v0.7.0 — iter-10)
├── ENG/SC/  # Schema: column types, constraints   (v0.7.0 — iter-10, extracted from LS)
├── ENG/DP/  # Deparser: row serialization          (v0.7.0 — iter-10, extracted from LS)
├── TXN/MV/  # MVCC: version chain, arena
├── TXN/LC/  # Lock: hazard pointers, epoch
├── TXN/SN/  # Snapshot: read view
├── TXN/VL/  # Validation: commit protocol, slots
├── SQL/LX/  # Lexer: tokenization
├── SQL/PS/  # Parser: recursive descent, AST
├── SQL/PL/  # Planner: cost, index select, memo
├── SQL/EX/  # Executor: operator tree
├── SQL/RE/  # Rewriter: constant fold, pushdown
├── SYS/AP/  # API: Engine, Options, errors
├── SYS/SY/  # System: init, shutdown
├── SYS/SE/  # Session: lifecycle, deadline
├── SYS/TX/  # Transaction: context, savepoints
└── SYS/ST/  # Statement: prepare, bind
```

## Release Tags

| Tag | Commit | Iterations |
|---|---|---|
| v0.1.0–v0.1.2 | LOG era | iter-00 |
| v0.2.0–v0.2.1 | FIL era | iter-01 |
| v0.3.0 | MEM era | iter-02 |
| v0.4.0–v0.4.1 | WAL + TXN/MV fix | iter-03, iter-05/06 |
| v0.5.0 | SQL/Execute close-out | iter-08 |
| **v0.6.0** | **SYS + Integration** | **iter-09** |
| **v0.6.1** | Test-race + LG fix (post-v0.6.0 close-out) | — |
| **v0.7.0** (planned) | **Real PK Index + Persistent Catalog** | **iter-10** |

## Design Protection

All design documents in `design/subsystems/*.md` are authoritative. Updates
to design must be applied by a human — AI agents must not auto-edit
design files.
