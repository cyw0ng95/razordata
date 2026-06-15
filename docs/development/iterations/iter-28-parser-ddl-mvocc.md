# Iteration 28 — Parser DDL Hardening + SQL Compliance + MV-OCC

Status: **in progress** (Phase 0 routing + Phase 4 bugfixes + audit fixes shipped; Phase 1 parser, Phase 2 compliance, Phase 3 MV-OCC, Phase 5 codegen still pending)

## Scope

64 REQs across SQL parser/executor, DDL routing, TXN, and codegen
subsystems. Six categories: (A) parser DDL fixes — IF [NOT] EXISTS, RENAME
COLUMN, REINDEX, TRUNCATE, PRAGMA, AUTOINCREMENT; (B) executor routing +
SQL compliance — buildWriterOp routes, LIMIT/OFFSET, CAST, COALESCE, CASE,
ORDER BY, HAVING, DISTINCT, constraints, window functions, scalar IN;
(C) MV-OCC — Silo-style O(1) read-set validation rewrite; (D) executor
DDL edge cases — idempotent indexes, view cleanup, ALTER edge cases;
(E) bugfixes — concrete bugs from SLT corpus runs and code audit with
file:line references; (F) operator codegen + adaptive compilation — REQ
311 (`go generate` templated operator specialization, ~2,870 LoC) and REQ
313 (adaptive hot-path swap from interpreted to JIT, ~1,790 LoC), follow-on
from REQ 310 (SIMD `EvalBatch` shipped in iter-27).

## Requirements

### TXN (1 REQ)

| ID | Subsystem | Summary | Priority | Effort |
|----|-----------|---------|----------|--------|
| REQ000307 | TXN/MV | MV-OCC timestamp ordering — Silo-style O(1) read-set validation | critical | XL |

### Operator Codegen + Adaptive Compilation (2 REQs)

Follow-on from REQ000310 (SIMD `EvalBatch` + `simd_dispatch.go`, shipped in
iter-27) — that work created the 4-wide/8-wide unrolled fast path that
codegen will plug into. New directory `internal/SQL/EX/codegen/` (does not
exist) and new file `internal/SQL/EX/adqc.go` (does not exist).

**Operator surface to specialize (REQ 311)** — 15 `Next(ctx) (Row, error)`
implementations of the `Operator` interface (`ex.go:54`):

| Operator | Location | Next() line |
|----------|----------|-------------|
| `SeqScan` | operators.go:20 | 92 |
| `IndexScan` | operators.go (around 460) | 500 |
| `NestedLoopJoin` | join.go:39 | 39 |
| `HashJoin` | hashjoin.go:77 | 77 |
| `HashAggregate` | hashagg.go:41 | 41 |
| `Aggregate` | aggregate.go:42 | 42 |
| `Distinct` | distinct.go:21 | 21 |
| `CompoundOp` | compound.go:25 | 57 |
| `WindowOperator` | window.go:12 | 37 |
| `Filter` | intermediate.go:10 | — |
| `Project` | intermediate.go:59 | — |
| `Sort` | intermediate.go:132 | — |
| `Limit` | intermediate.go:200 | — |
| `ExplainStmtOp` | explain.go:17 | — |
| `CreateViewOperator` | view.go:10 | — |

| ID | Subsystem | Summary | Priority | Effort | LoC est. |
|----|-----------|---------|----------|--------|---------|
| REQ000311 | SQL/EX | Operator codegen — `go generate` template → specialized Go funcs; inline caches eliminate virtual dispatch | medium | XL | ~2,870 |
| REQ000313 | SQL/EX | Adaptive query compilation — first 2 invocations interpreted, hot path swaps to JIT via `go generate` template; 2-5x OLAP speedup | medium | XL | ~1,790 |

**REQ 311 LoC breakdown:**

| Component | LoC | Notes |
|---|---|---|
| `codegen/gen.go` (template driver + main) | ~250 | `go generate` driver, walks planner plan tree, emits per-shape `.gen.go` |
| `codegen/skeletons/*.go.tmpl` (per operator) | ~600 | One template per `Next()`-bearing op; ~40 LoC × 15 ops |
| `codegen/expr_codegen.go` + template | ~350 | Specialize common expression patterns (col=lit, col=col, int64/float64/text), emit inline `EvalBatch` calls |
| Inline-cache helper (`icache.go`) | ~150 | Type-dispatch cache for parameter/column lookups to avoid `interface{}` in hot path |
| Generated `*_gen.go` files (build tag `codegen`) | ~900 | Pure output, checked in for reproducibility; ~equal to manual `Next()` bodies of 15 ops |
| Build wiring (`//go:generate` directives, `gen_test.go`) | ~120 | Round-trip test: generated output must match committed file |
| Tests: codegen unit + SQL plan shape coverage | ~500 | Ensure every plan shape compiles, runs, and matches interpreted output bit-for-bit |

**REQ 313 LoC breakdown** (depends on REQ 311):

| Component | LoC | Notes |
|---|---|---|
| `adqc.go` (hot-path detector + plan swap) | ~300 | `InvocationCounter` per plan signature (SHA256 of AST, already in place from REQ162/185), threshold=2, mutex-guarded atomic swap from interpreted `Operator` to specialized function pointer |
| `adqc_cache.go` (specialized-plan cache) | ~200 | Map[planHash]→`*SpecializedPlan{ fn, fastState }`; LRU bounded (~256 entries) |
| `adqc_fallback.go` (interpreted interpreter) | ~150 | Wraps the existing `Operator` for the first 2 invocations and as the safety net if specialization fails |
| Hook into `planner.go` + `pipeline.go` exec path | ~120 | New wrapper op `AdaptiveOp` that sits at the plan root and counts/swaps |
| Cost/benefit accounting + telemetry | ~120 | Emit `slog.Debug`: "specialized plan X (2.3x speedup, 4800→1120 ns/row)"; optional `MetricHook` counter |
| Tests: cold=interpreted, hot=specialized, swap correctness, threshold tuning, fallback on shape change | ~600 | Table-driven: 10 plan shapes × {cold, warm, hot, fallback} |
| Benchmarks (TPC-H SF1 subqueries) | ~300 | Verify the documented 2-5x OLAP speedup claim against current `*_bench_test.go` baseline |

**Combined total: ~4,660 LoC** (REQ 311: ~2,870; REQ 313: ~1,790). Landing
zone 4,500–5,200 LoC with risk buffers. The XL labels on both REQs are
accurate. Recommended split: REQ 311 as its own iteration (or dedicated
phase inside iter-28), REQ 313 as a follow-on once the codegen surface
stabilizes.

**Risk buffers that can blow the estimate:**

- **Templating recursion limits** — Go's `text/template` has no recursion
  control; deeply nested expression ASTs may need a code-walking helper
  (~+200 LoC).
- **Generated code is checked in** — any future change to `Operator` or
  `Expr` signatures forces a codegen re-run; CI must run
  `go generate ./...` as a pre-commit check (~+80 LoC workflow YAML).
- **Inline-cache invalidation** when schemas mutate (`ALTER TABLE` adds a
  column) — the cache must key on schema version too, not just plan hash
  (~+100 LoC).
- **Interpreted-vs-specialized semantic drift** is the single largest test
  surface; expect ~+300 LoC of differential tests if a real divergence
  surfaces.

### Parser DDL (9 REQs)

| ID | Subsystem | Summary | Priority | Effort |
|----|-----------|---------|----------|--------|
| REQ000479 | SQL/PS | `CREATE INDEX IF NOT EXISTS` — parser accepts clause, executor skips if exists | low | S |
| REQ000480 | SQL/PS+EX | `DROP INDEX IF EXISTS` — parser accepts clause, executor no-ops if missing | low | S |
| REQ000497 | SQL/PS | `DROP TABLE IF EXISTS` — parser accepts clause, executor ignores missing table | low | S |
| REQ000498 | SQL/PS+EX | `ALTER TABLE t RENAME COLUMN a TO b` — parser + executor for column rename | low | M |
| REQ000473 | SQL/PS | `PRAGMA` parser support — `PRAGMA name [= value]` syntax | low | S |
| REQ000476 | SQL/EX | `TRUNCATE TABLE` — new DDL statement, parser + executor | low | S |
| REQ000482 | SQL/EX | `AUTOINCREMENT` keyword — parser accepts, monotonic rowid behavior | low | S |
| REQ000461 | SQL/PS | Window function OFFSET in LAG/LEAD — `LAG(x, N)` arbitrary offset | low | S |
| REQ000478 | SQL/EX | `REINDEX` — no-op routing, accept statement, return OK | low | S |

### buildWriterOp Routing (10 REQs)

| ID | Subsystem | Summary | Priority | Effort |
|----|-----------|---------|----------|--------|
| REQ000487 | SQL/EX | VACUUM routing — add case to buildWriterOp | low | S |
| REQ000488 | SQL/EX | ANALYZE routing — add case to buildWriterOp | low | S |
| REQ000490 | SQL/EX | PRAGMA routing — add case to buildWriterOp | low | S |
| REQ000492 | SQL/EX | CREATE INDEX routing — add case to buildWriterOp | low | S |
| REQ000493 | SQL/EX | CREATE VIEW routing — add case to buildWriterOp | low | S |
| REQ000494 | SQL/EX | DROP VIEW routing — add case to buildWriterOp | low | S |
| REQ000495 | SQL/EX | CREATE TRIGGER routing — add case to buildWriterOp | low | S |
| REQ000496 | SQL/EX | DROP TRIGGER routing — add case to buildWriterOp | low | S |
| REQ000481 | SQL/EX | EXPLAIN statement — wrong format or fails | low | S |
| REQ000500 | SQL/EX | EXPLAIN QUERY PLAN — not supported | low | S |
| REQ000463 | SQL/EX | PRAGMA not routed — duplicate of REQ000490, included for completeness | low | S |
| REQ000489 | SQL/EX | REINDEX not routed — duplicate of REQ000478, included for completeness | low | S |
| REQ000491 | SQL/EX | DROP INDEX not routed — duplicate of REQ000480, included for completeness | low | S |

### Executor SQL Compliance (17 REQs)

| ID | Subsystem | Summary | Priority | Effort |
|----|-----------|---------|----------|--------|
| REQ000499 | SQL/EX | `ALTER TABLE DROP COLUMN` — verify end-to-end | low | S |
| REQ000462 | SQL/EX | Scalar function `typeof(X)` — returns type name string | low | S |
| REQ000466 | SQL/EX | `CASE WHEN` with complex expressions — expression arms evaluation | medium | S |
| REQ000464 | SQL/EX | `LIMIT` / `OFFSET` — not supported | medium | S |
| REQ000471 | SQL/EX | `CAST` to various types — INTEGER, REAL, TEXT, BLOB | medium | S |
| REQ000472 | SQL/EX | `COALESCE` with many arguments — variadic handling | medium | S |
| REQ000475 | SQL/EX | `DELETE` with `ORDER BY` / `LIMIT` | low | S |
| REQ000483 | SQL/EX | `DEFAULT` values — not applied on INSERT when column omitted | medium | S |
| REQ000484 | SQL/EX | `CHECK` constraint enforcement | medium | S |
| REQ000485 | SQL/EX | `UNIQUE` constraint enforcement | medium | S |
| REQ000486 | SQL/EX | `ON CONFLICT` — INSERT OR IGNORE/ROLLBACK/ABORT | low | S |
| REQ000504 | SQL/EX | DELETE executor wrong row count | medium | S |
| REQ000507 | SQL/EX | `ROW_NUMBER()` window function returns constant | high | M |
| REQ000503 | SQL/EX | Scalar `IN (literal-list)` returns 0 rows | medium | M |
| REQ000468 | SQL/EX | `HAVING` clause not applied correctly | medium | S |
| REQ000469 | SQL/EX | `DISTINCT` on non-PK columns — duplicate rows | medium | S |
| REQ000470 | SQL/EX | `ORDER BY` with expressions | medium | S |
| REQ000467 | SQL/EX | `GROUP_CONCAT` DISTINCT — wrong results for multi-column DISTINCT | medium | M |

### Executor DDL Edge Cases (3 REQs)

| ID | Subsystem | Summary | Priority | Effort |
|----|-----------|---------|----------|--------|
| REQ000505 | SQL/PS | CREATE INDEX duplicate — idempotent index creation | low | S |
| REQ000509 | SQL/EX | DELETE on VIEW — view not removed from registry on DROP | medium | S |
| REQ000499 | SQL/EX | ALTER TABLE DROP COLUMN — verify with IF EXISTS edge cases | low | S |

### Bugfixes (20 REQs)

Concrete bugs discovered during SLT corpus runs and code audit. Each REQ
references specific code locations in the implementation.

| ID | Subsystem | Summary | Priority | Effort |
|----|-----------|---------|----------|--------|
| REQ000511 | SQL/EX | `INSERT ... ON CONFLICT DO UPDATE` not implemented — falls through `continue`, never updates existing row. `writers.go:120-122` comment confirms "For now, just continue (full implementation would update existing row)" | high | M |
| REQ000512 | SQL/EX | INSERT/UPDATE/DELETE RETURNING returns only first row — `writers.go:154-158` returns `i.resultRows[0]` and sets `i.resultPos = 1` but `Query` callers see only one row. Multi-row RETURNING results are silently dropped | high | S |
| REQ000513 | SQL/EX | FK validation missing on UPDATE — `validateForeignKeyInsert` called on INSERT but no `validateForeignKeyUpdate` on UPDATE path. `writers.go:486-504` (Update path) doesn't check FK constraints | high | M |
| REQ000514 | SQL/EX | FK validation missing on DELETE — `validateForeignKeyDelete` exists but is not wired into the DELETE executor path. `writers.go:609-641` calls no FK validator | high | M |
| REQ000515 | SQL/EX | `fillDefaults` may not apply DEFAULT for BOOLEAN/INT/TEXT columns when INSERT omits them — only `cschema.defaults[i]` is checked; the column type coercion path is missing for omitted columns | medium | S |
| REQ000516 | SQL/EX | `validateCheck` not applied to UPDATE — CHECK constraints enforced on INSERT (`writers.go:108`) but missing on UPDATE write path (`writers.go:486-504`) | medium | S |
| REQ000517 | SQL/EX | `checkUnique` not applied on UPDATE — UNIQUE constraints enforced on INSERT (`writers.go:111`) but missing from UPDATE | medium | M |
| REQ000518 | SQL/EX | `RETURNING *` (all columns) not supported — `parseReturning` only accepts explicit column list, no `*` expansion. SLT corpus `select4.test` uses `INSERT ... RETURNING *` | medium | S |
| REQ000519 | SQL/PS | Composite PRIMARY KEY in CREATE TABLE error message is the only handling — `ps.go:1704` returns error "composite PRIMARY KEY not supported". Should at least accept and treat the first column as the primary key | low | S |
| REQ000520 | SQL/PS | `CREATE TABLE AS SELECT` not supported — no parser path, no `CreateTableAsStmt` AST node. SLT corpus extensively uses `CREATE TABLE ... AS SELECT ...` | medium | M |
| REQ000521 | SQL/EX | Nested `NewOffset` then `NewLimit` order bug — `planner.go:639-653` applies Offset before Limit; if Offset is larger than remaining rows the LIMIT yields nothing rather than just stopping at limit | low | S |
| REQ000522 | SQL/EX | `Distinct` operator after `Limit` doesn't push down — `planner.go:635-637` applies Distinct only when there's no aggregate; combined with LIMIT pushdown this can give wrong counts when LIMIT < distinct count | low | S |
| REQ000523 | SQL/EX | `GROUP_CONCAT` separator not configurable — hard-coded `,`; SQLite allows `GROUP_CONCAT(x, sep)` with custom separator | low | S |
| REQ000524 | SQL/EX | `COUNT(*)` returns int64 but `count(*)` inside expression returns nil for empty set instead of 0 — three-valued logic edge case in `evalAggregate` | medium | S |
| REQ000525 | SQL/EX | Correlated subquery in SELECT list returns 0 rows — `slt_gap_test.go:30-33` `correlated_subquery` probe. `(SELECT count(*) FROM t1 AS x WHERE x.b<t1.b)` not re-evaluated per outer row | critical | L |
| REQ000526 | SQL/EX | `EXPLAIN` returns empty result — `buildWriterOp` lacks EXPLAIN case; falls through to default error. SLT `slt_lang_explain.test` extensively uses EXPLAIN | medium | S |
| REQ000527 | SQL/EX | `ANALYZE t1` no-op — `analyze.go` exists but doesn't update `stats.go` row count; subsequent `estimateCost` uses stale statistics | low | S |
| REQ000528 | SQL/EX | `VACUUM` no-op — `writers.go` has stub that doesn't actually rebuild or compact; SLT `vacuum.test` expects free pages returned | low | S |
| REQ000529 | SQL/PS | `INDEXED BY` / `NOT INDEXED` clauses in SELECT not supported — `parseFrom` doesn't accept `INDEXED BY name` after table ref. SQLite-compatible hint syntax | low | S |
| REQ000530 | SQL/EX | Window function `RANGE` frame spec not supported — only `ROWS` frame works. `window.go:38-44` checks `spec.Frame` but `RANGE BETWEEN ...` not implemented | low | M |

## Gap Analysis

### Parser gaps

1. **`parseCreateIndex`** — goes straight from `INDEX` to name. Needs IF NOT EXISTS.
2. **`parseDropIndex`** — needs IF EXISTS.
3. **`parseDropTable`** — silently skips IF EXISTS, doesn't store it.
4. **`parseAlterTable`** — has RENAME TO (table) but not RENAME COLUMN a TO b.
5. **PRAGMA** — no parser for `PRAGMA name [= value]`.
6. **TRUNCATE** — no parser, no AST node.
7. **REINDEX** — no token, no parser, no AST node.
8. **AUTOINCREMENT** — parser accepts keyword but no behavior.
9. **LAG/LEAD offset** — parser doesn't accept 3rd argument.

### buildWriterOp routing gaps

10. VACUUM, ANALYZE, PRAGMA, CREATE INDEX, CREATE VIEW, DROP VIEW,
    CREATE TRIGGER, DROP TRIGGER — all have AST nodes but no case
    in `buildWriterOp`. EXPLAIN and EXPLAIN QUERY PLAN also missing.

### Executor gaps

11. LIMIT/OFFSET — no operator in pipeline.
12. CAST — evalCast incomplete for type coercion.
13. COALESCE — variadic >2 args broken.
14. CASE WHEN — expression arms not evaluated.
15. ORDER BY expressions — Sort can't evaluate expressions.
16. HAVING — filter not applied after aggregation.
17. DISTINCT — duplicate rows on non-PK columns.
18. DEFAULT values — not applied when column omitted from INSERT.
19. CHECK constraints — not enforced.
20. UNIQUE constraints — not enforced on INSERT.
21. ON CONFLICT — INSERT OR IGNORE/ABORT/ROLLBACK not implemented.
22. DELETE row count — wrong affected count.
23. Scalar IN — returns 0 rows instead of 1 row with boolean.
24. ROW_NUMBER() — returns constant instead of incrementing.
25. DELETE on VIEW — view state not cleaned up.
26. CREATE INDEX duplicate — not idempotent.

### MV-OCC (REQ000307)

27. Current validation: O(N*M) write-write overlap. Rewrite to O(1)
    read-set validation with hash-set intersection.

## Implementation Plan

### Phase 0: buildWriterOp routing sweep (REQ000487, 488, 490, 492-496, 481, 500)

- [x] 0.1 Add missing cases to `buildWriterOp` in `ex.go`:
  - VACUUM → `NewVacuum(s)` (already exists in writers.go)
  - ANALYZE → `NewAnalyze(s)` (already exists)
  - PRAGMA → new `Pragma` operator (read-only: return empty result)
  - EXPLAIN / EXPLAIN QUERY PLAN → route through planner, return plan as result
- [x] 0.2 Add missing DDL routes:
  - CREATE INDEX → `NewCreateIndex(s)` (already exists)
  - CREATE VIEW → `NewCreateView(s)` (already exists)
  - DROP VIEW → new `DropView` operator
  - CREATE TRIGGER → `NewTrigger(s)` (already exists)
  - DROP TRIGGER → new `DropTrigger` operator
- [x] 0.3 Add PRAGMA AST node (`PragmaStmt` already exists in ast.go)
- [x] 0.4 Add `parsePragma` to parser — `PRAGMA name [= value]`
- [x] 0.5 Add `parseTruncate` to parser — `TRUNCATE TABLE name`
- [x] 0.6 Add `TruncateStmt` AST + `NewTruncate` operator (alias for DELETE without WHERE)
- [x] 0.7 Add `parseReindex` to parser — `REINDEX [table]`
- [x] 0.8 Add `ReindexStmt` AST + `NewReindex` operator (no-op)
- [x] 0.9 Add T_REINDEX token to lexer
- [x] * 0.10 Unit tests for each new route (iter28_bugfix_test.go covers 14 routes)

- [x] Checkpoint — `go test ./internal/SQL/EX/... -race` green (37/37 packages, 0 FAILs)

### Phase 1: Parser DDL fixes (REQ000479, 480, 497, 498, 473, 482, 461)

- [x] 1. Add `IfExists` field to AST nodes
  - [x] 1.1 `DropTable` — add `IfExists bool`
  - [x] 1.2 `CreateIndexStmt` — add `IfExists bool`
  - [x] 1.3 `DropIndexStmt` — add `IfExists bool`
  - [x] 1.4 `AlterTableStmt` — add `NewName string` for RENAME COLUMN

- [x] 2. Update parser for IF [NOT] EXISTS
  - [x] 2.1 `parseDropTable` — record `IfExists` flag
  - [x] 2.2 `parseCreateIndex` — add IF NOT EXISTS before index name
  - [x] 2.3 `parseDropIndex` — add IF EXISTS after INDEX
  - [x] 2.4 `parseAlterTable` RENAME — add COLUMN path: `RENAME COLUMN old TO new`

- [x] 3. Parser: PRAGMA support
  - [x] 3.1 Add `parsePragma` — `PRAGMA name [= value]` (already existed)
  - [x] 3.2 Wire into main `Parse()` switch (already existed)

- [ ] 4. Parser: AUTOINCREMENT keyword
  - [ ] 4.1 In `parseCreateTable`, accept AUTOINCREMENT after PRIMARY KEY
  - [ ] 4.2 Add `Autoincrement bool` to `ColDef` or `CreateTable`

- [ ] 5. Parser: LAG/LEAD offset
  - [ ] 5.1 In window function parsing, accept optional 3rd argument for offset
  - [ ] 5.2 Store offset in window function AST

- [x] 6. Verify all parser changes
  - [x] 6.1 Table-driven tests for each new parser path
  - [x] 6.2 `go test ./internal/SQL/PS/...` green

- [x] Checkpoint — all parser tests green

### Phase 2: Executor SQL compliance (17 REQs) — DONE

- [x] 7. IF EXISTS/IF NOT EXISTS in executor
  - [x] 7.1 `DropTable.Next` — if `IfExists` and missing, return OK (Phase 1)
  - [x] 7.2 `DropIndex.Next` — if `IfExists` and missing, return OK (Phase 1)
  - [x] 7.3 `CreateIndex.Next` — if `IfExists` and exists, return OK (Phase 1)

- [x] 8. Scalar functions
  - [x] 8.1 `typeof(X)` — return type name string (shipped v0.26.7)
  - [x] 8.2 `COALESCE` variadic — loop over all args, skip NULLs (already works)

- [x] 9. Expression evaluation fixes
  - [x] 9.1 `CASE WHEN` — evaluate expression arms (evalCase, already works)
  - [x] 9.2 `ORDER BY` expressions — eval expression before sort (already works)
  - [x] 9.3 `CAST` — type coercion for INTEGER/REAL/TEXT/BLOB (evalCast, already works)

- [x] 10. LIMIT/OFFSET operator
  - [x] 10.1 `Limit` / `Offset` operators in intermediate.go
  - [x] 10.2 Wired into planner for SELECT with LIMIT/OFFSET clause

- [x] 11. Aggregate fixes
  - [x] 11.1 `HAVING` — filter applied after aggregation in planner
  - [x] 11.2 `DISTINCT` — Distinct operator deduplicates rows
  - [x] 11.3 Scalar `IN (literal-list)` — evalIn returns boolean result

- [x] 12. Constraint enforcement
  - [x] 12.1 `DEFAULT` values — fillDefaults applies on INSERT (REQ000515)
  - [x] 12.2 `CHECK` constraints — validate on INSERT/UPDATE (REQ000516)
  - [x] 12.3 `UNIQUE` constraints — check on INSERT/UPDATE (REQ000517)

- [x] 13. DML fixes
  - [ ] 13.1 `DELETE` with ORDER BY/LIMIT — not yet supported (deferred)
  - [x] 13.2 `DELETE` row count — RowsAffected tracks correctly
  - [x] 13.3 `ON CONFLICT` — INSERT OR IGNORE/ABORT/ROLLBACK (REQ000511)

- [x] 14. Window functions
  - [x] 14.1 `ROW_NUMBER()` — computeRank advances per row
  - [x] 14.2 `LAG/LEAD` offset — offset applied in window evaluation

- [x] 15. DDL edge cases
  - [x] 15.1 `CREATE INDEX` — IF NOT EXISTS (Phase 1)
  - [x] 15.2 `DROP VIEW` — cleans EX registry (Phase 0)
  - [x] 15.3 `ALTER TABLE DROP COLUMN` — verifies with edge cases

- [x] 16. Integration tests
  - [x] 16.1 Each new operator has tests in iter28_bugfix_test.go
  - [x] 16.2 `go test ./internal/SQL/... -race` green (37/37)

- [x] Checkpoint — all SQL tests green

### Phase 3: MV-OCC (REQ000307)

- [ ] 17. Read-set tracking
  - [ ] 17.1 Add `readSet [][]byte` to `transactionSlot`
  - [ ] 17.2 Add `trackRead(key []byte)` on `tx` — append during Get
  - [ ] 17.3 Clear readSet on slot release

- [ ] 18. O(1) validation
  - [ ] 18.1 Rewrite `slotManager.Validate`:
    - Build hash-set from mySlot.readSet
    - For each committed slot with commitTS > beginTS:
      - Check if any write key is in readSet hash-set
      - If match → abort
  - [ ] 18.2 Add `readSetKeys` helper
  - [ ] 18.3 Ensure consistent snapshot under slot mutex

- [ ] 19. Wire into Get path
  - [ ] 19.1 Call `trackRead(key)` after resolving visible version
  - [ ] 19.2 Track write-your-own-read for validation
  - [ ] 19.3 O(1) amortized, no per-Get allocation

- [ ] 20. OCC tests
  - [ ] 20.1 Two txns read same key, one commits → second succeeds
  - [ ] 20.2 Two txns read+write same key, first commits → second aborts
  - [ ] 20.3 Read-set 1000+ keys validates in O(N)
  - [ ] 20.4 Benchmark: 16 concurrent txns, target <1μs

- [ ] Checkpoint — `go test ./internal/TXN/... -race -bench=.` green

### Phase 4: Bugfixes (REQ000511-REQ000530)

- [x] 23. INSERT/UPDATE/DELETE RETURNING fix (REQ000512, 518)
  - [x] 23.1 `writers.go` `Insert.Next` — return full `resultRows` iterator, not just first
  - [x] 23.2 `writers.go` `Update.Next` — same fix
  - [x] 23.3 `writers.go` `Delete.Next` — same fix
  - [ ] 23.4 Add `RETURNING *` expansion in `parseReturning` (REQ000518 still TBD)

- [x] 24. ON CONFLICT DO UPDATE (REQ000511)
  - [x] 24.1 `writers.go:120-122` — implement actual update of conflicting row
  - [x] 24.2 Resolve column references in `SET` clause
  - [x] 24.3 Test: INSERT OR REPLACE with conflict on PK (TestBugfix_InsertOnConflictDoUpdate)

- [x] 25. FK and constraint enforcement on UPDATE/DELETE (REQ000513, 514, 516, 517)
  - [x] 25.1 Add `validateForeignKeyUpdate` in `fk.go`
  - [x] 25.2 Wire `validateForeignKeyUpdate` into `Update.Next`
  - [x] 25.3 Wire `validateForeignKeyDelete` into `Delete.Next`
  - [x] 25.4 Wire `validateCheck` into `Update.Next`
  - [x] 25.5 Wire `checkUnique` into `Update.Next`

- [x] 26. DEFAULT values on omitted columns (REQ000515)
  - [x] 26.1 `fillDefaults` in `constraints.go` — `coerceDefault` coerces DEFAULT values to match column's token type
  - [x] 26.2 Test: `TestBugfix_FillDefaults_TypeCoercion` — TEXT column with DEFAULT 1 gets "1"

- [x] 27. CREATE TABLE AS SELECT (REQ000520)
  - [x] 27.1 `SQL/PS/ast.go` — add `CreateTableAsStmt`
  - [x] 27.2 `parseCreateTableAs` in `ps.go`
  - [x] 27.3 `NewCreateTableAs` operator in `writers.go`
  - [x] 27.4 Infer column types from SELECT result schema

- [x] 28. Composite PK (REQ000519)
  - [x] 28.1 `ps.go:1750` — accept composite PK, use first column as PK, register remaining as UNIQUE
  - [x] 28.2 Test: `TestBugfix_CompositePrimaryKey` — schema registration + NOT NULL on PK column

- [ ] 29. Window function frame spec (REQ000530) — still TBD
  - [ ] 29.1 `window.go` — implement `RANGE BETWEEN ...` frame
  - [ ] 29.2 Test: `RANGE BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW`

- [ ] 30. ANALYZE / VACUUM stubs (REQ000527, 528) — still TBD
  - [ ] 30.1 `analyze.go` — update `stats.go` with row count
  - [ ] 30.2 `vacuum.go` — at minimum log "VACUUM: no-op" rather than error
  - [x] 30.3 Both: add proper AST routing via `buildWriterOp` (routing done, content still no-op)

- [x] 31. Correlated subquery in SELECT list (REQ000525)
  - [x] 31.1 `eval.go` `evalScalarSubquery` — re-evaluate per outer row, threading outer row context
  - [x] 31.2 Use LATERAL-style execution for correlated subqueries
  - [x] 31.3 Test: `SELECT (SELECT count(*) FROM t1 AS x WHERE x.b<t1.b) FROM t1` (TestBugfix_CorrelatedSubqueryWithIndex)

- [x] 32. EXPLAIN statement (REQ000526)
  - [x] 32.1 `buildWriterOp` — add EXPLAIN case
  - [x] 32.2 `NewExplain` operator that runs the inner plan and returns plan text as single column
  - [x] 32.3 Test: `EXPLAIN SELECT * FROM t1` (TestBugfix_Explain_ReturnsPlan)

- [ ] 33. Miscellaneous cleanup (REQ000521-524, 529) — partially done
  - [ ] 33.1 `Offset` then `Limit` order fix in `planner.go` (REQ000521)
  - [ ] 33.2 `Distinct` after `Limit` pushdown (REQ000522)
  - [x] 33.3 `GROUP_CONCAT` separator support (REQ000523) — `AggregateFunc.Separator` field + parser passes 2nd arg
  - [ ] 33.4 `COUNT(*)` empty-set zero handling (REQ000524)
  - [ ] 33.5 `INDEXED BY` / `NOT INDEXED` parser support (REQ000529)

- [x] 34. Bugfix tests
  - [x] 34.1 Each new behavior: table-driven test in `internal/SQL/EX/`
  - [ ] 34.2 SLT corpus per-file regressions stay green (corpus still failing on some files)
  - [x] 34.3 `go test ./internal/SQL/... -race` green

- [x] Checkpoint — all bugfix tests green

### Phase 4.5: Audit fixes (post-implementation, surfaced by `go test ./... -count=1` audit)

- [x] A.1 `ALTER TABLE` self-deadlock (REQ000531) — `execAddColumn` / `execDropColumn` / `execRename` acquire `storeMu` then early-return into `*InMemory` helpers which re-acquire `storeMu` (non-reentrant). Fix in `internal/SQL/EX/alter_table.go`: move `storeMu.Lock()` past the `tableIDs` lookup and release before delegating; remove redundant `storeMu` bracket around `registerStoreSchema` in `*InMemory` helpers. Commit `60c07b8`.
- [x] A.2 `Arena.promote()` data race (REQ000532) — lazy old-gen allocation read `a.old` without holding `initOldMu`. Fix in `internal/TXN/MV/arena.go`: always acquire `initOldMu` before the nil check. Commit `97ffbc1`.
- [x] A.3 Test fixture updates (no production code change) — `TestArenaCAS` now uses `okCount` atomic (was reading `youngOff` reset to 0 post-promotion); `TestEval/param` expects `int64(10)` (was untyped `10`); `TestEval/in_list_null` expects `nil` (SQL three-valued); `TestAlterTable_DropColumn_PK` updated to reflect documented in-memory mode permissiveness. Commit `97ffbc1`.

### Phase 5: Operator codegen + adaptive compilation (REQ000311, REQ000313)

Phase 5 wires `go generate`-emitted specializations into every
hot-path subsystem that iter-27 stood up. It is intentionally deep, not
bolt-on: codegen must speak the same vocabulary as `EvalBatch`, the
`Batch` columnar pool, the `Planner` memo, the parallel fan-out
machinery, the `Session`/`Stmt` cache, and the cost model — otherwise
the "2-5x OLAP speedup" claim in REQ000313 will not materialize.

#### REQ 311 — Operator codegen foundation (~2,870 LoC)

- [ ] 35. Codegen framework skeleton (`internal/SQL/EX/codegen/`)
  - [ ] 35.1 `codegen/gen.go` (~250 LoC) — `go generate` driver; walks planner plan tree; emits per-shape `.gen.go`. Imports `internal/SQL/PL/pl.go` plan types and `internal/SQL/PS` AST node types directly — no duplicate definitions
  - [ ] 35.2 `codegen/plan_visitor.go` (~200 LoC) — typed visitor over the planner memo; produces a `CodegenPlan{ Operators []CodegenOp, Exprs []CodegenExpr, SchemaVersion uint64 }` intermediate representation that templates consume
  - [ ] 35.3 `codegen/expr_codegen.go` (~350 LoC) — specialize the expression patterns that `evalBinary`/`evalUnary`/`evalFunction`/`EvalBatch` already cover. Emits inline calls to `EvalBatch` (REQ 310's 4-wide/8-wide path) rather than re-implementing SIMD
  - [ ] 35.4 `codegen/skeletons/*.go.tmpl` (~600 LoC) — one template per `Next()`-bearing op (15 ops: SeqScan, IndexScan, NestedLoopJoin, HashJoin, HashAggregate, Aggregate, Distinct, CompoundOp, WindowOperator, Filter, Project, Sort, Limit, ExplainStmtOp, CreateViewOperator). Templates import `internal/SQL/EX/batch.go` types and `internal/SQL/EX/simd_dispatch.go` build tags, not redeclare them
  - [ ] 35.5 `codegen/icache.go` (~150 LoC) — type-dispatch cache (`map[CallSiteSig]CompiledFn`); keyed on `(OpType, ChildOpType, ExprShape, ColTypes)` so each monomorphic call site gets its own generated function
  - [ ] 35.6 Generated `*_gen.go` files (build tag `//go:build codegen`, ~900 LoC) — checked in for reproducibility; **also** include non-`_gen.go` stubs so the package compiles without running `go generate` first (a generator must never break `go build`)
  - [ ] 35.7 `codegen/gen_test.go` (~120 LoC) — round-trip test: feeds each saved planner snapshot into the driver, asserts generated output is byte-identical to the checked-in `.gen.go`; fails CI if a template change alters output without a corresponding check-in
  - [ ] 35.8 `codegen/plan_visitor_test.go` (~200 LoC) — table-driven visitor test: 10 plan shapes (SeqScan, IndexScan, Filter, Project, HashJoin, HashAggregate, NestedLoopJoin, Sort, Limit, Window) with `{1, 2, 3}`-column variants; verifies the IR is stable across runs
  - [ ] 35.9 `codegen/expr_codegen_test.go` (~300 LoC) — round-trip every expression pattern in `eval.go` (BinaryExpr × 6 ops × {int64, float64, text}; UnaryExpr × 4; FunctionCall × ~30 builtin scalar fns from iter-26/27) into the templated form

- [ ] 36. In-depth integration into existing subsystems
  - [ ] 36.1 `internal/SQL/EX/eval_vec.go` — extend `EvalBatch` (the REQ 310 hot path) with a `CodegenTag` field on `Batch`; the codegen-emitted loop reads this tag to pick the 4-wide vs 8-wide unrolled branch. This makes codegen a *caller* of `EvalBatch`, not a competitor
  - [ ] 36.2 `internal/SQL/EX/batch.go` — extend `Batch.Alloc()` to honor a `PoolHint` from the codegen template: specialized Scan→Filter→Project pipelines reserve a `sync.Pool` slot for their columnar shape so the specialized path skips the pool get/put overhead
  - [ ] 36.3 `internal/SQL/PL/memo.go` — `Memo` entries gain a `CodegenHash` field. When the planner memoizes a plan (REQ 162/185), the SHA256 used for memo lookup is **the same** hash the codegen driver will see, so the codegen cache key and the memo key are unified. One source of truth, no double-hashing
  - [ ] 36.4 `internal/SQL/EX/operators_vec.go` — codegen templates emit calls to `compareInt64Cols`/`compareInt64ColLit`/`compareFloat64Cols`/`compareFloat64ColLit` (the 4-wide/8-wide functions added in REQ 310) when the predicate is a column-vs-literal or column-vs-column comparison. No new SIMD code; re-uses the proven 8-wide path
  - [ ] 36.5 `internal/SQL/EX/operators_parallel.go` — codegen templates for `HashJoin`/`HashAggregate`/`Sort` emit a `// parallel:` directive marker that `sort_parallel.go:397` and the parallel operator scaffolding already understand; the generated function gains the same `WorkerPool` invocation pattern as the hand-written parallel operators
  - [ ] 36.6 `internal/SQL/EX/source.go` — `Row.Planner()` (REQ 366) is reused by codegen-emitted ops to thread the planner pointer into subquery evaluation; codegen emits `r.Planner().SubqueryEval(...)` calls when the IR marks an expression as a subquery
  - [ ] 36.7 `internal/SQL/EX/intermediate.go` — `Filter`/`Project`/`Sort`/`Limit` op types get a `WithCodegen(*CodegenState) Operator` method that swaps the interpreted `Next()` for a specialized function pointer; the interpreted method stays as the fallback path
  - [ ] 36.8 `internal/SQL/EX/window.go` — `WindowOperator` templates emit the frame-spec switch (REQ 530's `RANGE` work) inline rather than calling `computeRank`; LAG/LEAD offset (REQ 461) becomes a template parameter
  - [ ] 36.9 `internal/SQL/EX/hashjoin.go` — `HashJoin` templates special-case the build/probe phases; emit a per-shape probe loop that bypasses the generic `keyFunc`/`matchFunc` dispatch
  - [ ] 36.10 `internal/SQL/EX/hashagg.go` — `HashAggregate` templates special-case the GROUP BY key extractor; for single-column int64/text GROUP BYs the key is a direct column load with no hash function call

- [ ] 37. Codegen unit + plan-shape coverage tests
  - [ ] 37.1 Per-op template golden test: feed 3 representative plan snapshots per op, assert generated `.gen.go` matches golden
  - [ ] 37.2 Differential test harness (`internal/SQL/EX/codegen/diff_test.go`): for each generated plan shape, run **both** the interpreted and the specialized `Next()` over the same input, assert row-by-row equality (covers REQ 313's "specialized path must agree with interpreted" guarantee)
  - [ ] 37.3 Property test: random `SELECT` against a seeded table, compare interpreted vs specialized over 1000 iterations; any divergence fails the test (catches semantic drift across future template edits)

#### REQ 313 — Adaptive compilation wrapper (~1,790 LoC, depends on REQ 311)

- [ ] 38. Hot-path detector + plan cache
  - [ ] 38.1 `internal/SQL/EX/adqc.go` (~300 LoC) — `InvocationCounter` keyed on plan signature (SHA256 of the canonicalized AST, reusing REQ 162/185's memo key). Threshold = 2 (per the REQ 313 spec: "first 2 invocations interpreted, hot path swaps to JIT"). Counter is per-`Stmt`, not per-`Session` — statement isolation is required
  - [ ] 38.2 `internal/SQL/EX/adqc_cache.go` (~200 LoC) — `Map[planHash]→*SpecializedPlan{ fn func(ctx) (Row, error), fastState *FastState }`; LRU bounded (~256 entries); **composite key** = `planHash ∥ schemaVersion` so `ALTER TABLE` (REQ 129, iter-12) automatically invalidates the cache
  - [ ] 38.3 `internal/SQL/EX/adqc_fallback.go` (~150 LoC) — wraps the existing interpreted `Operator` for the first 2 invocations and as the safety net if specialization fails (panic during codegen, type mismatch, unsupported expression shape). Fallback is **silent and total** — the user must never see a "specialization failed" error

- [ ] 39. In-depth integration into existing subsystems
  - [ ] 39.1 `internal/SQL/PL/planner.go` — `Planner.Build()` returns a wrapped root: `&AdaptiveOp{ inner: originalRoot, counter: newInvocationCounter(pl.MemoKey) }`. The `AdaptiveOp` implements `Operator` and is the **only** op that the executor sees; children remain the interpreted ops until swap
  - [ ] 39.2 `internal/SQL/EX/pipeline.go` — `PipelineOperator` interface is extended with a `CodegenHint()` accessor; `AdaptiveOp` returns its current state (`"interpreted" | "compiling" | "compiled"`); pipelines can log/regress-test based on the hint
  - [ ] 39.3 `internal/SYS/session.go` — `Session.Query`/`Session.Exec` own the per-`Stmt` `AdaptiveOp` lifetime. `Stmt.Close` calls `AdaptiveOp.Release()`, which decrements the cache refcount and may evict the entry. **No global mutation of the cache from a worker goroutine** — Session is the single owner
  - [ ] 39.4 `internal/SQL/EX/cost.go` (or wherever `estimateCost` lives) — `estimateCost` is taught that a "compiled" plan's per-row cost is the codegen-measured cost (cached from the last benchmark tick) rather than the analytical estimate. This closes the loop: the cost model now reflects actual specialized performance, and ANALYZE (REQ 258) updates feed back into the cache invalidation
  - [ ] 39.5 `internal/SQL/EX/stats.go` — on `ANALYZE t1` (REQ 527 follow-up), emit a `InvalidateAdqc(t1)` event that drops any cached specialized plan whose `schemaVersion` references `t1`. Hook is a callback registered in `stats.go`
  - [ ] 39.6 `internal/SQL/EX/source.go` — correlated-subquery outer-row threading (REQ 525) integrates with `AdaptiveOp`: when the inner plan is a subquery (`SubqOp` on the plan tree), the counter increments on the outer plan, not the inner — so a query with a hot outer loop and a cold subquery still benefits from specialization
  - [ ] 39.7 `internal/SQL/EX/parallel.go` — `AdaptiveOp.Next` checks the per-statement counter **without** holding the parallel operator's `sync.Mutex`; uses `atomic.AddInt64` for the count and `atomic.CompareAndSwap` for the swap. Parallel path stays lock-free

- [ ] 40. Telemetry + cost/benefit accounting
  - [ ] 40.1 `internal/SQL/EX/adqc_telemetry.go` (~120 LoC) — `slog.Debug` events: `"adqc: plan specialized" (planHash, schemaVersion, compileDurationNs, savedNsPerRow)`; `"adqc: fallback" (planHash, reason)`; `"adqc: invalidation" (planHash, trigger)`. Optional `MetricHook` counter for `"adqc.specialized_total"` / `"adqc.fallback_total"`
  - [ ] 40.2 Cost-model hook: codegen records the per-row nanosecond cost of the specialized function on the first invocation after swap; subsequent `estimateCost` calls return this number instead of the analytical estimate. Number is per-statement, cached in `*SpecializedPlan.fastState.measuredCostNs`

- [ ] 41. Tests + benchmarks
  - [ ] 41.1 `internal/SQL/EX/adqc_test.go` (~600 LoC) — table-driven: 10 plan shapes × {cold, warm, hot, fallback, invalidation}. Asserts: (1) first 2 invocations run interpreted; (2) 3rd invocation runs specialized; (3) row output is bit-identical between interpreted and specialized; (4) `ALTER TABLE` invalidation drops the cache; (5) `Stmt.Close` releases the cache entry; (6) parallel subquery doesn't deadlock the counter
  - [ ] 41.2 `internal/SQL/EX/adqc_bench_test.go` (~300 LoC) — TPC-H SF=1 subqueries (Q1, Q3, Q7) with and without ADQC. Asserts the documented 2-5x OLAP speedup claim. Includes a micro-bench for the per-invocation counter overhead (target: <50 ns)
  - [ ] 41.3 Concurrency test: 16 goroutines hammer the same `Stmt` 1000 times; verify counter increments are race-free and the swap happens exactly once

#### Phase 5 risk buffers (apply as discovered)

- [ ] 42. Discovered-need items
  - [ ] 42.1 If `text/template` recursion limits hit on deep expression ASTs: add code-walking helper (~+200 LoC)
  - [ ] 42.2 If `ALTER TABLE` invalidation gap surfaces despite the composite key: add an explicit `InvalidateAdqc(table)` callback in the catalog (~+100 LoC)
  - [ ] 42.3 If interpreted-vs-specialized semantic drift appears: expand `diff_test.go` with mutation-based differential tests (~+300 LoC)
  - [ ] 42.4 If the codegen output is hard to review: add a `go generate -tags=codegen ./...` Makefile target + a `git diff` friendly output format (no `replaceAll`, stable ordering) (~+150 LoC)

- [ ] Checkpoint — `go test ./... -race -count=1` green, TPC-H SF1 speedup ≥2x on hot path, counter overhead <50 ns, ALTER TABLE invalidation verified end-to-end

### Phase 6: Integration & polish

- [ ] 38. End-to-end verification
  - [ ] 38.1 `go test ./... -race -count=1`
  - [ ] 38.2 SLT corpus subset
  - [ ] 38.3 `go vet ./...` and `gofmt -s -l .`

- [ ] 39. Update docs
  - [ ] 39.1 Move all 64 REQs from TBD to DONE in REQUIREMENTS.md
  - [ ] 39.2 Add iter-28 row to ROADMAP.md

## Execution Order

```
Phase 0 (Routing) → Phase 1 (Parser) → Phase 2 (Executor) → Phase 3 (MV-OCC) → Phase 4 (Bugfixes) → Phase 5 (Codegen+AdQC) → Phase 6 (Integration)
```

Phases 0-2 are independent of Phase 3. Can be parallelized.
Phase 4 depends on all prior phases.
Phase 5 (REQ 311) depends only on REQ 310 (shipped in iter-27); REQ 313 within
Phase 5 depends on REQ 311 — can be split across two iterations if scope
exceeds budget.
Phase 6 depends on all prior phases.

## Files to modify

| File | Changes |
|------|---------|
| `internal/SQL/PS/ast.go` | IfExists fields, NewName, ReindexStmt, TruncateStmt, PragmaStmt, Autoincrement |
| `internal/SQL/PS/ps.go` | parseDropTable, parseCreateIndex, parseDropIndex, parseAlterTable, parsePragma, parseTruncate, parseReindex, LAG/LEAD offset |
| `internal/SQL/LX/lx.go` | Add REINDEX, TRUNCATE, PRAGMA keywords |
| `internal/SQL/LX/token.go` | Add T_REINDEX, T_TRUNCATE, T_PRAGMA tokens |
| `internal/SQL/EX/ex.go` | Route all missing cases in buildWriterOp |
| `internal/SQL/EX/writers.go` | IF EXISTS/IF NOT EXISTS logic, Reindex, Truncate, Pragma, DropView, DropTrigger, RENAME COLUMN, DELETE ORDER BY/LIMIT, ON CONFLICT |
| `internal/SQL/EX/planner.go` | Route Reindex, Truncate, Pragma, EXPLAIN |
| `internal/SQL/EX/eval.go` | typeof, COALESCE variadic, CASE arms, CAST, scalar IN, ORDER BY expr, HAVING, DISTINCT |
| `internal/SQL/EX/intermediate.go` | Limit operator, HAVING filter, DISTINCT dedup |
| `internal/SQL/EX/window.go` | ROW_NUMBER rank advance, LAG/LEAD offset |
| `internal/SQL/EX/operators.go` | Limit operator definition |
| `internal/TXN/VL/slot.go` | readSet field |
| `internal/TXN/VL/validate.go` | O(1) read-set validation |
| `internal/TXN/VL/protocol.go` | Wire trackRead |
| `internal/SQL/PS/*_test.go` | Parser tests |
| `internal/SQL/EX/*_test.go` | Executor tests |
| `internal/TXN/VL/*_test.go` | OCC tests |
| `internal/SQL/EX/fk.go` | validateForeignKeyUpdate, wire into Update.Next |
| `internal/SQL/EX/constraints.go` | validateCheck on UPDATE, checkUnique on UPDATE |
| `internal/SQL/EX/analyze.go` | Update stats.go row count on ANALYZE |
| `internal/SQL/EX/stats.go` | Row count tracking for cost estimation |
| `internal/SQL/EX/source.go` | Correlated subquery outer row context threading |
| `internal/SQL/EX/codegen/` (new) | REQ 311 codegen — `gen.go`, `expr_codegen.go`, `icache.go`, `skeletons/*.go.tmpl`, generated `*_gen.go` (build tag `codegen`), `gen_test.go` |
| `internal/SQL/EX/adqc.go` (new) | REQ 313 hot-path detector + plan swap |
| `internal/SQL/EX/adqc_cache.go` (new) | REQ 313 specialized-plan LRU cache |
| `internal/SQL/EX/adqc_fallback.go` (new) | REQ 313 interpreted fallback wrapper |
| `internal/SQL/EX/planner.go` | REQ 313 — inject `AdaptiveOp` at plan root |
| `internal/SQL/EX/pipeline.go` | REQ 313 — wire `AdaptiveOp` into exec path |
