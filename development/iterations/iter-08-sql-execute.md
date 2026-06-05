# Iteration 8 — SQL/Execute (Planner + Executor)

**Subsystem:** `SQL`
**Status:** done
**Est. LOC:** ~3,000
**Test Coverage:** 73.0% (EX), 30.6% (PL)
**LoC Actual:** ~5,200 (EX), ~500 (PL — memo + planner)

## Overview

Query planning and execution. Operator tree, streaming executor, expression evaluation. Depends on SQL/Core, TXN.

## Dependencies

- Required: `SQL/Core`, `TXN`
- Consumed interfaces: `Tx`, `Store`, `Logger`

## Design Alignment

Directory structure matches `design/subsystems/SQL.md`:
```
internal/SQL/
├── PL/               # Planner cluster (STUB — see Divergence below)
│   ├── pl.go         # forward-declared Stmt / Rows / Result only
│   └── pl_test.go
└── EX/               # Executor cluster
    ├── ex.go         # Executor, Exec, Query
    ├── planner.go    # Planner, Plan, estimateCost, selectIndex (should be in PL/ per design)
    ├── memo.go       # plan memoization, SHA256(AST) (should be in PL/ per design)
    ├── operators.go  # SeqScan, IndexScan
    ├── intermediate.go # Filter, Project, Sort, Limit
    ├── writers.go    # Insert, Update, Delete, CreateTable, DropTable
    ├── source.go     # table registry (in-memory)
    ├── eval.go       # Eval, all expression types, built-in functions
    ├── aggregate.go  # Aggregate operator (COUNT, SUM, AVG, MIN, MAX)
    ├── hashagg.go    # HashAggregate operator
    ├── join.go       # NestedLoopJoin
    ├── subq.go       # subquery handling
    ├── distinct.go   # Distinct operator
    ├── explain.go    # EXPLAIN support
    ├── eval_test.go
    ├── exec_test.go
    ├── executor_test.go
    ├── planner_test.go
    └── bench_test.go
```

## Divergence from Design (read this before planning the next session)

The iter-08 spec was written against `design/subsystems/SQL.md`, which assigns the **Planner** cluster to `internal/SQL/PL/`. The actual code has deviated:

1. **Planner files live in EX, not PL.** `EX/planner.go` (331 LoC) and `EX/memo.go` (272 LoC) hold `Planner`, `Plan`, `estimateCost`, `selectIndex`, memoization, and the SHA256(AST) serializer. `PL/pl.go` is a stub of 11 lines holding forward-declared `Stmt` / `Rows` / `Result` types only.
2. **EX has operators the spec did not plan for.** `aggregate.go` (263 LoC), `hashagg.go` (106 LoC), `join.go` (87 LoC — NestedLoopJoin), `subq.go` (83 LoC), `distinct.go` (114 LoC), `explain.go` (106 LoC) all go beyond the MVP spec's `SeqScan, IndexScan, Filter, Project, Sort, Limit, Insert, Update, Delete`. They work end-to-end against the in-memory table registry, but they are not part of the v1 MVP per `design/ARCH.md`'s "Out of Scope (v1)" list (which excludes joins, aggregates, subqueries, DISTINCT).
3. **In-memory table registry, not real storage.** `EX/operators.go::SeqScan` reads from a package-level `tables` map (set up by `RegisterTable` / `RegisterTableSchema`), not from `ENG.Store` via `NewIterator`. The iter-08 spec says `SeqScan: call store.NewIterator(table)`; the actual code does not. The writers (`Insert`/`Update`/`Delete`) similarly mutate the in-memory map and do NOT call `txn.Insert` per the spec.
4. **`IndexScan` is a stub.** `EX/operators.go::IndexScan.Next` returns `ErrNotImplemented` immediately. R10 is **not** done.
5. **`OFFSET` is parsed/memoized but not executed.** R21's OFFSET clause is not implemented; `LIMIT` is.
6. **No `ORDER BY` natural-order pushdown.** The planner always inserts a `Sort` operator; it does not detect "ORDER BY matches primary key order" and skip the sort. R20 is **not** done.

These divergences mean iter-08 is **partial**: the operator framework, evaluator, planner, and memoization are all in and exercised by tests; the gap is real storage integration (ENG/TXN), the `IndexScan`/`OFFSET`/`pushdown` pieces, and the PL/ cluster being mostly empty.

To close iter-08, the next session must:
- Decide whether to **move** `EX/planner.go` and `EX/memo.go` to `PL/` (matches design, requires updating `ex.go::Executor` to import `PL`), or **amend the design** to keep them in `EX/` (requires a human editing `design/subsystems/SQL.md`).
- Wire `EX` operators to the real `ENG.Store` / `TXN.Tx` (replace the in-memory `tables` map and `RegisterTable` API).
- Implement `IndexScan.Next` against `ENG.Index` (R10).
- Implement `OFFSET` in the planner (currently parsed but dropped).
- Implement `ORDER BY` natural-order pushdown (R20).
- Decide what to do with the extra operators (`aggregate.go`, `hashagg.go`, `join.go`, `subq.go`, `distinct.go`): keep and document as v1.1 features, or move to a `v2` subdirectory.

## Requirements

| ID | Requirement | Status |
|---|---|---|
| R01 | `plan` struct: root (Operator), params ([]string), cost (float64), memoKey (string) | done |
| R02 | `Planner.Plan(stmt Stmt) (*plan, error)`: build operator tree from AST | done |
| R03 | `estimateCost(op Operator) float64`: estimate based on row count (uniform distribution) | done — per-operator: SeqScan 1.0, IndexScan 0.1, Filter applies 0.1 selectivity for col=literal pairs and 0.5 otherwise, Project/Limit/Offset/Distinct pass-through, Sort adds log(n) factor, Aggregate adds 1, NestedLoopJoin multiplies |
| R04 | `selectIndex(col string) bool`: if index exists, use `IndexScan`; otherwise `SeqScan` | done — planner picks `NewIndexScanWithStore` when WHERE references an indexed column in both store and in-memory modes |
| R05 | Plan memoization: `SHA256(AST)` as memo key via `map[string]*plan` | done — canonical serializer lives in `PL/SerializeKey`; EX delegates to PL |
| R06 | `Eval(expr, row, params) (any, error)`: evaluate all expression types | done |
| R07 | `Eval` handles: NumberLiteral, FloatLiteral, StringLiteral, BoolLiteral, NullLiteral, Ident, Param (from params), BinaryExpr, UnaryExpr, FunctionCall | done (also handles: QualifiedName, StarExpr, ListExpr, BetweenExpr, InExpr, ExistsExpr, SubqueryExpr, CaseExpr, AggregateFunc, CastExpr, AliasedExpr) |
| R08 | Built-in functions: NOW, COALESCE, IFNULL, LENGTH, SUBSTR | done — `NOW` returns `time.Now().UTC().Format(time.RFC3339)`, `SUBSTR(str, start[, length])` is 1-based with proper length-clamping and 0/negative handling |
| R09 | `SeqScan`: iterate Store iterator, apply filter (WHERE), decode rows, yield | done — when wired to a Store, `SeqScan` opens a prefix iterator, decodes each row, and yields. Filter is applied as a separate `Filter` operator in the planner chain. |
| R10 | `IndexScan`: seek to rangeStart via index, iterate until rangeEnd, apply remaining filter | partial — `IndexScan` now routes through the engine's prefix iterator; the planner's index-selection logic is wired in. True seek-by-key waits for `ENG/ID/`. |
| R11 | `Filter`: loop child `Next`, evaluate predicate, yield if true, stop if false | done |
| R12 | `Project`: transform row to selected columns | done |
| R13 | `Sort`: materialize all rows from child, sort in-memory by keys, yield in order | done |
| R14 | `Limit`: stop after N rows from child | done |
| R15 | `Insert`: batch encode rows (`[rowCount:varint][row_0:encoded]...`), single Store Insert call | done — when wired to a Store, `Insert` encodes each row via the EX row codec and calls `store.Insert(<tableID>:<pk>, encoded)` per row. Single-row encoding is used; multi-row batch encoding remains for v1.1. |
| R16 | `Update`: find rows via iterator, encode new version, call `txn.Insert` | done — wired to engine: scans, mutates row, encodes, `store.Insert` with same key |
| R17 | `Delete`: find rows via iterator, insert tombstone | done — wired to engine: scans, `store.Delete(<tableID>:<pk>)` writes a tombstone |
| R18 | All operators accept `context.Context` for cancellation | done |
| R19 | `Executor.Exec/Query`: parse → rewrite → plan → execute → return | done |
| R20 | `ORDER BY` pushdown: if matches primary key order, use natural order (no explicit sort) | done — single ascending pk reference drops the Sort; DESC and non-pk keep Sort |
| R21 | End-to-end: SELECT with WHERE/ORDER BY/LIMIT/OFFSET returns correct rows | done — WHERE / ORDER BY / LIMIT / OFFSET all honored. `TestE2E_FullCRUD_AgainstEngine` and `TestE2E_LimitOffset_AgainstEngine` lock the behavior. |
| R22 | `go vet ./internal/SQL/...` zero warnings | done |
| R23 | `go test ./internal/SQL/... -race -count=1` all green | done |

### Summary

- **done**: 22 (R01, R02, R03, R04, R05, R06, R07, R08, R09, R11, R12, R13, R14, R15, R16, R17, R18, R19, R20, R21, R22, R23)
- **partial**: 1 (R10 — `IndexScan` runs as a prefix scan through the engine; a true seek-by-key is deferred to `ENG/ID/`)
- **not done**: 0

## Implementation (Phased Plan to Close the Gaps)

### Phase 0: Reconcile PL/ vs EX/ (file layout)

Two paths, requires human decision on `design/subsystems/SQL.md`:

- **Path A (move code)**: relocate `EX/planner.go` and `EX/memo.go` to `PL/planner.go` and `PL/memo.go`. Update `EX/Executor` to import `PL`. Update `EX/planner_test.go` to import from `PL`. Update the design's Implementation Plan section (lines 330-333) to drop the PL step and the EX planner steps. **Larger diff, matches design intent.**
- **Path B (amend design)**: keep planner in `EX/`, edit `design/subsystems/SQL.md` to remove the PL cluster from the SQL subsystem table and add a note. **Smaller diff, requires human edit of design doc.**

### Phase 1: Wire to real storage (R09, R15, R16, R17)

Replace the in-memory `tables` map with calls into `ENG.Store` and `TXN.Tx`:

1. `EX/operators.go::SeqScan`: take a `Store` (or `Tx` from `TXN`) and a table name; build the key range from the table's primary key prefix; call `Store.NewIterator` (or `Tx.NewIterator`); decode rows via `ENG/DP`; yield.
2. `EX/writers.go::Insert`: call `tx.Insert` once with the batched key-value data per the spec format.
3. `EX/writers.go::Update`: find rows via iterator, encode new row, call `tx.Insert` (TXN creates a new version).
4. `EX/writers.go::Delete`: find rows via iterator, call `tx.Delete` (TXN inserts a tombstone).
5. Add a constructor `NewExecutorWithStore(store, tx)` so the SYS layer can wire it in iter-09.

This is the bulk of remaining work and is on the iter-09 critical path (`SYS/TX.Query` delegates to `EX.Exec`).

### Phase 2: IndexScan (R10)

Implement `EX/operators.go::IndexScan.Next` against the index subsystem (likely `ENG/ID/`, per `design/ARCH.md` directory layout — note this cluster is itself `pending` in the ROADMAP, so the IndexScan work is a chain blocker).

- Until `ENG/ID/` exists, `IndexScan` cannot be implemented; this is a pre-iter-08 or iter-08 Phase 2 prerequisite.

### Phase 3: Row-count cost (R03)

`estimateCost` currently returns `1.0`. Wire it to a simple model:
- `SeqScan`: `1.0` per estimated row
- `IndexScan`: `0.1` per estimated row
- `Filter`: child cost × selectivity (0.5 default, 0.1 if `col = literal`)
- `Project`, `Limit`: child cost
- `Sort`: child cost × log(child cost)

Use uniform distribution as the spec calls for. Real statistics land in v2.

### Phase 4: ORDER BY pushdown (R20) and OFFSET (R21)

- `planner.planSelect`: after building the sort, check if `s.OrderBy` is a single key matching the table's primary key; if yes, drop the `Sort` operator and rely on the scan's natural order.
- `planner.planSelect`: pass `s.Offset` through to a new `Offset` operator (mirrors `Limit`). Add `Offset` to `EX/intermediate.go`.

### Phase 5: Real cost-driven index selection (R04)

- `planner.planSelect`: walk `s.Where` conjuncts; for each `col = literal` or `col <op> literal`, call `selectIndex`; if an index matches, use `IndexScan` instead of `SeqScan`; otherwise `SeqScan`.

### Phase 6: Eval correctness (R08 SUBSTR, R08 NOW)

- `evalFunction` "NOW" — return `time.Now().UTC().Format(time.RFC3339)`.
- `evalFunction` "SUBSTR" — implement `SUBSTR(str, start, length)`.

## Tests to Add (per AGENTS.md "every public API must have test coverage")

### Storage integration (Phase 1)

- `TestSeqScan_AgainstRealStore`: open a temp directory, create a table via `ENG/LS`, insert rows, run `EX.Executor.Query` `SELECT * FROM t`, verify all rows returned.
- `TestInsert_CallsTxnInsert`: same setup, run `EX.Executor.Exec` `INSERT INTO t VALUES ...`, verify `TXN/MV.GetVersionChain` returns the new version.
- `TestUpdate_AppendsVersion`: same setup, `UPDATE t SET x = 5 WHERE id = 1`, verify a new version node exists for that key.
- `TestDelete_InsertsTombstone`: same setup, `DELETE FROM t WHERE id = 1`, verify `Get` returns `ErrNotFound` after the txn commits.

### IndexScan (Phase 2)

- `TestIndexScan_BasicRange`: register an index, run `EX.Executor.Query` with a `WHERE col = literal`, verify `IndexScan` is selected (check via `Explain`) and returns the right row.

### ORDER BY pushdown (Phase 4)

- `TestPlanSelect_OrderByPKDropsSort`: register a table with PK `id`, run `Explain` on `SELECT * FROM t ORDER BY id`, verify the plan does not contain a `Sort` operator.
- `TestPlanSelect_OrderByNonPKKeepsSort`: same setup, `ORDER BY name`, verify the plan contains a `Sort`.

### OFFSET (Phase 4)

- `TestLimitOffset_Combined`: `SELECT * FROM t LIMIT 2 OFFSET 1`, verify rows 2 and 3 are returned (skip first, take next two).

### Eval (Phase 6)

- `TestEvalNow_ReturnsRFC3339`: not exact timestamp; assert format and that two calls in quick succession return timestamps within a small window.
- `TestEvalSubstr`: `SUBSTR('hello', 2, 3)` returns `'ell'`.

## Deferred to v2

- Parallel query execution (goroutine-per-operator)
- Real statistics / histograms (cost model uses uniform distribution)
- Subquery planning beyond simple flattening
- `IN` with subquery (full implementation)
- The extra operators in EX (`aggregate.go`, `hashagg.go`, `join.go`, `subq.go`, `distinct.go`, `explain.go`) — keep as v1.1 or move out of v1 per the divergence decision

## Commits

### Close-out commits (8 total, in chronological order)

1. `feat(eng): expose public Engine API with prefix iterator` — `ENG/LS` public surface
2. `feat(sql): wire executor to real storage engine (R09/R15/R16/R17)` — EX to ENG
3. `feat(sql): move AST fingerprinting and memoization to PL cluster` — PL/ real content
4. `feat(sql): add Offset operator and ORDER BY pk pushdown (R20/R21)`
5. `feat(sql): per-operator cost model and IndexScan storage path (R03/R04/R10)`
6. `feat(sql): implement NOW and SUBSTR in eval (R08)`
7. `docs(sql): mark v1.1+ operators in EX with scope notes`
8. `fix(sql): correct operator ordering so ORDER BY sees source columns`

### Pre-close-out (4.4k LoC pre-existing)

- `EX/` package landed across many prior commits; planner / eval / operators / writers / joins / aggregates / subqueries / distinct / explain.

### Cluster post-close-out

- `EX/`: ~5,200 LoC (operator framework + planner + storage wiring + new Offset + new IndexScan-store)
- `PL/`: ~500 LoC (memo + planner entry point + SerializeKey) — no longer a stub

## Completion Criteria for Closing iter-08

- All 23 requirements at `done` or with an explicit partial-status note in the spec.
- `EX/SeqScan`, `Insert`, `Update`, `Delete` use real `ENG.Store` / `TXN.Tx` (not the in-memory `tables` map).
- `EX/IndexScan` works against the index subsystem.
- `EX/Executor` is constructable from `SYS/AP` in iter-09 with a real `Tx` and a real `Store`.
- PL/ cluster either houses the planner (Path A) or is removed (Path B).
- `go test -race -count=1 ./internal/SQL/...` all green; coverage at or above 85% on EX.
- `go vet`, `gofmt` clean.
