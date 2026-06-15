# Iteration 28 — Parser DDL Hardening + SQL Compliance + MV-OCC

Status: **planned**

## Scope

62 REQs across SQL parser/executor, DDL routing, and TXN subsystems. Five
categories: (A) parser DDL fixes — IF [NOT] EXISTS, RENAME COLUMN, REINDEX,
TRUNCATE, PRAGMA, AUTOINCREMENT; (B) executor routing + SQL compliance —
buildWriterOp routes, LIMIT/OFFSET, CAST, COALESCE, CASE, ORDER BY, HAVING,
DISTINCT, constraints, window functions, scalar IN; (C) MV-OCC — Silo-style
O(1) read-set validation rewrite; (D) executor DDL edge cases — idempotent
indexes, view cleanup, ALTER edge cases; (E) bugfixes — concrete bugs from
SLT corpus runs and code audit with file:line references.

## Requirements

### TXN (1 REQ)

| ID | Subsystem | Summary | Priority | Effort |
|----|-----------|---------|----------|--------|
| REQ000307 | TXN/MV | MV-OCC timestamp ordering — Silo-style O(1) read-set validation | critical | XL |

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

- [ ] 0.1 Add missing cases to `buildWriterOp` in `ex.go`:
  - VACUUM → `NewVacuum(s)` (already exists in writers.go)
  - ANALYZE → `NewAnalyze(s)` (already exists)
  - PRAGMA → new `Pragma` operator (read-only: return empty result)
  - EXPLAIN / EXPLAIN QUERY PLAN → route through planner, return plan as result
- [ ] 0.2 Add missing DDL routes:
  - CREATE INDEX → `NewCreateIndex(s)` (already exists)
  - CREATE VIEW → `NewCreateView(s)` (already exists)
  - DROP VIEW → new `DropView` operator
  - CREATE TRIGGER → `NewTrigger(s)` (already exists)
  - DROP TRIGGER → new `DropTrigger` operator
- [ ] 0.3 Add PRAGMA AST node (`PragmaStmt` already exists in ast.go)
- [ ] 0.4 Add `parsePragma` to parser — `PRAGMA name [= value]`
- [ ] 0.5 Add `parseTruncate` to parser — `TRUNCATE TABLE name`
- [ ] 0.6 Add `TruncateStmt` AST + `NewTruncate` operator (alias for DELETE without WHERE)
- [ ] 0.7 Add `parseReindex` to parser — `REINDEX [table]`
- [ ] 0.8 Add `ReindexStmt` AST + `NewReindex` operator (no-op)
- [ ] 0.9 Add T_REINDEX token to lexer
- [ ] * 0.10 Unit tests for each new route

- [ ] Checkpoint — `go test ./internal/SQL/EX/... -race` green

### Phase 1: Parser DDL fixes (REQ000479, 480, 497, 498, 473, 482, 461)

- [ ] 1. Add `IfExists` field to AST nodes
  - [ ] 1.1 `DropTable` — add `IfExists bool`
  - [ ] 1.2 `CreateIndexStmt` — add `IfExists bool`
  - [ ] 1.3 `DropIndexStmt` — add `IfExists bool`
  - [ ] 1.4 `AlterTableStmt` — add `NewName string` for RENAME COLUMN

- [ ] 2. Update parser for IF [NOT] EXISTS
  - [ ] 2.1 `parseDropTable` — record `IfExists` flag
  - [ ] 2.2 `parseCreateIndex` — add IF NOT EXISTS before index name
  - [ ] 2.3 `parseDropIndex` — add IF EXISTS after INDEX
  - [ ] 2.4 `parseAlterTable` RENAME — add COLUMN path: `RENAME COLUMN old TO new`

- [ ] 3. Parser: PRAGMA support
  - [ ] 3.1 Add `parsePragma` — `PRAGMA name [= value]`
  - [ ] 3.2 Wire into main `Parse()` switch

- [ ] 4. Parser: AUTOINCREMENT keyword
  - [ ] 4.1 In `parseCreateTable`, accept AUTOINCREMENT after PRIMARY KEY
  - [ ] 4.2 Add `Autoincrement bool` to `ColDef` or `CreateTable`

- [ ] 5. Parser: LAG/LEAD offset
  - [ ] 5.1 In window function parsing, accept optional 3rd argument for offset
  - [ ] 5.2 Store offset in window function AST

- [ ] 6. Verify all parser changes
  - [ ] 6.1 Table-driven tests for each new parser path
  - [ ] 6.2 `go test ./internal/SQL/PS/...` green

- [ ] Checkpoint — all parser tests green

### Phase 2: Executor SQL compliance (17 REQs)

- [ ] 7. IF EXISTS/IF NOT EXISTS in executor
  - [ ] 7.1 `DropTable.Next` — if `IfExists` and missing, return OK
  - [ ] 7.2 `DropIndex.Next` — if `IfExists` and missing, return OK
  - [ ] 7.3 `CreateIndex.Next` — if `IfExists` and exists, return OK

- [ ] 8. Scalar functions
  - [ ] 8.1 Add `typeof(X)` to `evalFunction` — return type name string
  - [ ] 8.2 Fix `COALESCE` variadic — loop over all args, skip NULLs

- [ ] 9. Expression evaluation fixes
  - [ ] 9.1 `CASE WHEN` — evaluate expression arms (simple value CASE)
  - [ ] 9.2 `ORDER BY` expressions — eval expression before sort
  - [ ] 9.3 `CAST` — implement type coercion for INTEGER/REAL/TEXT/BLOB

- [ ] 10. LIMIT/OFFSET operator
  - [ ] 10.1 Add `Limit` operator — count rows, stop at limit, skip offset
  - [ ] 10.2 Wire into planner for SELECT with LIMIT/OFFSET clause

- [ ] 11. Aggregate fixes
  - [ ] 11.1 `HAVING` — apply filter after aggregation in pipeline
  - [ ] 11.2 `DISTINCT` — deduplicate rows on non-PK columns
  - [ ] 11.3 Scalar `IN (literal-list)` — produce 1 row with boolean result

- [ ] 12. Constraint enforcement
  - [ ] 12.1 `DEFAULT` values — apply on INSERT when column omitted
  - [ ] 12.2 `CHECK` constraints — validate on INSERT/UPDATE
  - [ ] 12.3 `UNIQUE` constraints — check on INSERT

- [ ] 13. DML fixes
  - [ ] 13.1 `DELETE` with ORDER BY/LIMIT — add to Delete operator
  - [ ] 13.2 `DELETE` row count — fix `RowsAffected()` in Delete.Next
  - [ ] 13.3 `ON CONFLICT` — INSERT OR IGNORE/ABORT/ROLLBACK

- [ ] 14. Window functions
  - [ ] 14.1 `ROW_NUMBER()` — fix rank counter to advance per row
  - [ ] 14.2 `LAG/LEAD` offset — apply offset in window evaluation

- [ ] 15. DDL edge cases
  - [ ] 15.1 `CREATE INDEX` — idempotent (skip if exists, no error)
  - [ ] 15.2 `DROP VIEW` — clean up EX registry (UnregisterAllViews)
  - [ ] 15.3 `ALTER TABLE DROP COLUMN` — verify with edge cases

- [ ] 16. Integration tests
  - [ ] 16.1 Each new operator: table-driven tests
  - [ ] 16.2 `go test ./internal/SQL/... -race` green

- [ ] Checkpoint — all SQL tests green

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

- [ ] 23. INSERT/UPDATE/DELETE RETURNING fix (REQ000512, 518)
  - [ ] 23.1 `writers.go` `Insert.Next` — return full `resultRows` iterator, not just first
  - [ ] 23.2 `writers.go` `Update.Next` — same fix
  - [ ] 23.3 `writers.go` `Delete.Next` — same fix
  - [ ] 23.4 Add `RETURNING *` expansion in `parseReturning`

- [ ] 24. ON CONFLICT DO UPDATE (REQ000511)
  - [ ] 24.1 `writers.go:120-122` — implement actual update of conflicting row
  - [ ] 24.2 Resolve column references in `SET` clause
  - [ ] 24.3 Test: INSERT OR REPLACE with conflict on PK

- [ ] 25. FK and constraint enforcement on UPDATE/DELETE (REQ000513, 514, 516, 517)
  - [ ] 25.1 Add `validateForeignKeyUpdate` in `fk.go`
  - [ ] 25.2 Wire `validateForeignKeyUpdate` into `Update.Next`
  - [ ] 25.3 Wire `validateForeignKeyDelete` into `Delete.Next`
  - [ ] 25.4 Wire `validateCheck` into `Update.Next`
  - [ ] 25.5 Wire `checkUnique` into `Update.Next`

- [ ] 26. DEFAULT values on omitted columns (REQ000515)
  - [ ] 26.1 `fillDefaults` in `writers.go` — apply DEFAULT for BOOLEAN/INT/TEXT when INSERT omits column
  - [ ] 26.2 Test: `INSERT INTO t (a) VALUES (1)` with `b INTEGER DEFAULT 0`

- [ ] 27. CREATE TABLE AS SELECT (REQ000520)
  - [ ] 27.1 `SQL/PS/ast.go` — add `CreateTableAsStmt`
  - [ ] 27.2 `parseCreateTableAs` in `ps.go`
  - [ ] 27.3 `NewCreateTableAs` operator in `writers.go`
  - [ ] 27.4 Infer column types from SELECT result schema

- [ ] 28. Composite PK (REQ000519)
  - [ ] 28.1 `ps.go:1704` — instead of error, accept and use first column as PK with warning
  - [ ] 28.2 Test: `CREATE TABLE t (a INT, b INT, PRIMARY KEY (a, b))`

- [ ] 29. Window function frame spec (REQ000530)
  - [ ] 29.1 `window.go` — implement `RANGE BETWEEN ...` frame
  - [ ] 29.2 Test: `RANGE BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW`

- [ ] 30. ANALYZE / VACUUM stubs (REQ000527, 528)
  - [ ] 30.1 `analyze.go` — update `stats.go` with row count
  - [ ] 30.2 `vacuum.go` — at minimum log "VACUUM: no-op" rather than error
  - [ ] 30.3 Both: add proper AST routing via `buildWriterOp`

- [ ] 31. Correlated subquery in SELECT list (REQ000525)
  - [ ] 31.1 `eval.go` `evalScalarSubquery` — re-evaluate per outer row, threading outer row context
  - [ ] 31.2 Use LATERAL-style execution for correlated subqueries
  - [ ] 31.3 Test: `SELECT (SELECT count(*) FROM t1 AS x WHERE x.b<t1.b) FROM t1`

- [ ] 32. EXPLAIN statement (REQ000526)
  - [ ] 32.1 `buildWriterOp` — add EXPLAIN case
  - [ ] 32.2 `NewExplain` operator that runs the inner plan and returns plan text as single column
  - [ ] 32.3 Test: `EXPLAIN SELECT * FROM t1`

- [ ] 33. Miscellaneous cleanup (REQ000521-524, 529)
  - [ ] 33.1 `Offset` then `Limit` order fix in `planner.go`
  - [ ] 33.2 `Distinct` after `Limit` pushdown
  - [ ] 33.3 `GROUP_CONCAT` separator support
  - [ ] 33.4 `COUNT(*)` empty-set zero handling
  - [ ] 33.5 `INDEXED BY` / `NOT INDEXED` parser support

- [ ] 34. Bugfix tests
  - [ ] 34.1 Each new behavior: table-driven test in `internal/SQL/EX/`
  - [ ] 34.2 SLT corpus per-file regressions stay green
  - [ ] 34.3 `go test ./internal/SQL/... -race` green

- [ ] Checkpoint — all bugfix tests green

### Phase 5: Integration & polish

- [ ] 35. End-to-end verification
  - [ ] 35.1 `go test ./... -race -count=1`
  - [ ] 35.2 SLT corpus subset
  - [ ] 35.3 `go vet ./...` and `gofmt -s -l .`

- [ ] 36. Update docs
  - [ ] 36.1 Move all 62 REQs from TBD to DONE in REQUIREMENTS.md
  - [ ] 36.2 Add iter-28 row to ROADMAP.md

## Execution Order

```
Phase 0 (Routing) → Phase 1 (Parser) → Phase 2 (Executor) → Phase 3 (MV-OCC) → Phase 4 (Bugfixes) → Phase 5 (Integration)
```

Phases 0-2 are independent of Phase 3. Can be parallelized.
Phase 4 depends on all prior phases.
Phase 5 depends on all prior phases.

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
