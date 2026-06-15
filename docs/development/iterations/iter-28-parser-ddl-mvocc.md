# Iteration 28 — Parser DDL Hardening + MV-OCC

Status: **planned**

## Scope

7 REQs across SQL parser/executor and TXN subsystems. Mix of small parser
fixes (IF [NOT] EXISTS, RENAME COLUMN, REINDEX) and one critical TXN
rewrite (MV-OCC read-set validation).

## Requirements

| ID | Subsystem | Summary | Priority | Effort |
|----|-----------|---------|----------|--------|
| REQ000307 | TXN/MV | MV-OCC timestamp ordering — Silo-style O(1) read-set validation | critical | XL |
| REQ000478 | SQL/EX | `REINDEX` — no-op routing (rebuild index not yet supported; accept statement, return OK) | low | S |
| REQ000479 | SQL/PS | `CREATE INDEX IF NOT EXISTS` — parser accepts clause, executor skips if index exists | low | S |
| REQ000480 | SQL/PS+EX | `DROP INDEX IF EXISTS` — parser accepts clause, executor no-ops if index missing | low | S |
| REQ000497 | SQL/PS | `DROP TABLE IF EXISTS` — parser accepts clause, executor ignores missing table | low | S |
| REQ000498 | SQL/PS+EX | `ALTER TABLE t RENAME COLUMN a TO b` — parser + executor for column rename | low | M |
| REQ000499 | SQL/PS+EX | `ALTER TABLE t DROP COLUMN c` — already partially implemented; verify end-to-end with IF EXISTS edge cases | low | S |

## Gap Analysis

### Parser gaps

1. **`parseCreateIndex`** — goes straight from `INDEX` to name. Needs:
   - Check for `IF` (T_IDENT) → `NOT` (T_NOT) → `EXISTS` (T_EXISTS) before name
   - Store `IfExists bool` in `CreateIndexStmt`

2. **`parseDropIndex`** — same issue. Needs:
   - Check for `IF` → `EXISTS` after `INDEX`
   - Store `IfExists bool` in `DropIndexStmt`

3. **`parseDropTable`** — silently skips `IF EXISTS` (lines 1772-1777) but
   doesn't store it. Needs:
   - Add `IfExists bool` to `DropTable` AST
   - Actually record the flag instead of skipping

4. **`parseAlterTable`** — has `RENAME TO newname` (table rename) but
   NOT `RENAME COLUMN a TO b`. Needs:
   - After `RENAME`, check for `COLUMN` token
   - If COLUMN present: parse `old_name TO new_name`
   - Action = "RENAME COLUMN", Column = old, add `NewName` field to AST

### Executor gaps

5. **REINDEX** — no executor handler. Need:
   - Parse `REINDEX` in parser (new token or IDENT check)
   - Add `ReindexStmt` AST node
   - Route in `buildWriterOp` → `NewReindex()` (no-op for v1, returns 0 rows affected)

6. **DROP INDEX IF EXISTS** — `DropIndex.Next` currently errors on
   missing index. With `IfExists`, should return OK (0 affected).

7. **DROP TABLE IF EXISTS** — `DropTable.Next` currently succeeds
   regardless (table lookup returns silently). Verify edge case.

8. **ALTER TABLE RENAME COLUMN** — `AlterTable.Next` needs new action
   handler: update `storeSchemas` column name in-place, update catalog.

### MV-OCC (REQ000307)

9. Current validation (`VL/validate.go`) checks write-write overlap with
   O(N*M) key-range comparison. Silo-style OCC replaces this with:
   - **Read-set tracking**: per-txn read-set stored as flat key list
   - **O(1) validation**: at commit time, check if any committed writer
     since our beginTS wrote to any key in our read-set
   - **Version-chain integration**: version nodes carry commitTS; reader
     validates read-set against committed versions
   - This is a rewrite of `Validate()` + `tx.Get()` paths

## Implementation Plan

### Phase 1: Parser DDL fixes (REQ000479, REQ000480, REQ000497, REQ000498)

- [ ] 1. Add `IfExists` field to `DropTable`, `CreateIndexStmt`, `DropIndexStmt` AST nodes
  - [ ] 1.1 Modify `DropTable` struct — add `IfExists bool`
  - [ ] 1.2 Modify `CreateIndexStmt` struct — add `IfExists bool`
  - [ ] 1.3 Modify `DropIndexStmt` struct — add `IfExists bool`
  - [ ] 1.4 Add `NewName string` field to `AlterTableStmt` for RENAME COLUMN

- [ ] 2. Update parser to capture IF [NOT] EXISTS flags
  - [ ] 2.1 `parseDropTable` — record `IfExists` instead of silently skipping
  - [ ] 2.2 `parseCreateIndex` — add IF NOT EXISTS check before index name
  - [ ] 2.3 `parseDropIndex` — add IF EXISTS check after INDEX keyword
  - [ ] 2.4 `parseAlterTable` RENAME — add COLUMN path: `RENAME COLUMN old TO new`

- [ ] 3. Verify parser changes with unit tests
  - [ ] 3.1 Table-driven tests for each new parser path
  - [ ] 3.2 Ensure existing tests still pass (`go test ./internal/SQL/PS/...`)

- [ ] Checkpoint — all parser tests green

### Phase 2: Executor DDL fixes (REQ000478, REQ000480, REQ000497, REQ000499)

- [ ] 4. Handle IF EXISTS/IF NOT EXISTS in executor operators
  - [ ] 4.1 `DropTable.Next` — if `IfExists` and table missing, return OK (0 affected)
  - [ ] 4.2 `DropIndex.Next` — if `IfExists` and index missing, return OK (0 affected)
  - [ ] 4.3 `CreateIndex.Next` — if `IfExists` and index exists, return OK (0 affected)

- [ ] 5. Implement REINDEX (REQ000478)
  - [ ] 5.1 Add `T_REINDEX` token to lexer keyword map
  - [ ] 5.2 Add `ReindexStmt` AST node
  - [ ] 5.3 Add `parseReindex` to parser — accept `REINDEX` or `REINDEX <table>`
  - [ ] 5.4 Add `NewReindex` operator — no-op, returns 0 rows affected
  - [ ] 5.5 Route in `buildWriterOp` and planner

- [ ] 6. Implement ALTER TABLE RENAME COLUMN (REQ000498)
  - [ ] 6.1 In `AlterTable.Next`, handle "RENAME COLUMN" action:
    - Look up table in storeSchemas
    - Find column index by old name
    - Replace column name in schema cols slice
    - Update catalog entry if persistent catalog is wired
  - [ ] 6.2 Update EX package-level `schemas` map (in-memory fallback)

- [ ] 7. Add integration tests for executor changes
  - [ ] 7.1 DROP TABLE IF EXISTS (existing + missing table)
  - [ ] 7.2 CREATE INDEX IF NOT EXISTS (existing + new index)
  - [ ] 7.3 DROP INDEX IF EXISTS (existing + missing index)
  - [ ] 7.4 REINDEX (basic no-op)
  - [ ] 7.5 ALTER TABLE RENAME COLUMN (rename, verify SELECT works with new name)
  - [ ] 7.6 ALTER TABLE DROP COLUMN (verify existing still works)

- [ ] Checkpoint — `go test ./internal/SQL/... -race` green

### Phase 3: MV-OCC (REQ000307)

- [ ] 8. Design read-set tracking structure
  - [ ] 8.1 Add `readSet [][]byte` field to `transactionSlot` in `slot.go`
  - [ ] 8.2 Add `trackRead(key []byte)` method on `tx` — appends to slot.readSet during Get
  - [ ] 8.3 Ensure read-set is cleared on slot release (reuse)

- [ ] 9. Implement O(1) validation
  - [ ] 9.1 Rewrite `slotManager.Validate`:
    - For each committed slot with `commitTS > mySlot.beginTS`:
      - Check if slot.writeSet overlaps with mySlot.readSet (set intersection)
      - If overlap → abort (read-write conflict)
    - Use hash-set on mySlot.readSet for O(1) lookup per write key
  - [ ] 9.2 Add `readSetKeys` helper — flatten readSet for hash-set construction
  - [ ] 9.3 Ensure validation holds the slot mutex for consistent snapshot

- [ ] 10. Wire read-set tracking into Get path
  - [ ] 10.1 In `tx.Get`, after resolving visible version, call `trackRead(key)`
  - [ ] 10.2 In `tx.Get`, if version node's `commitTS > mySlot.beginTS`, this is
    a write-your-own-read — track it for validation
  - [ ] 10.3 Ensure read-set tracking is O(1) amortized (no per-Get allocation)

- [ ] 11. Add OCC-specific tests
  - [ ] 11.1 Test: two txns read same key, one commits first → second should succeed
    (read-write non-conflict)
  - [ ] 11.2 Test: two txns read+write same key, first commits → second should abort
    (read-write conflict)
  - [ ] 11.3 Test: read-set with 1000+ keys validates in O(N) not O(N^2)
  - [ ] 11.4 Benchmark: Validate with 16 concurrent txns, target <1μs

- [ ] Checkpoint — `go test ./internal/TXN/... -race -bench=. -benchtime=3s` green

### Phase 4: Integration & polish

- [ ] 12. End-to-end verification
  - [ ] 12.1 Run full test suite: `go test ./... -race -count=1`
  - [ ] 12.2 Run SLT corpus subset: `go test -tags slt_corpus ./tests/sqlcmp/slt/... -run TestSLT_Each -v`
  - [ ] 12.3 Verify no regression in existing tests
  - [ ] 12.4 Run `go vet ./...` and `gofmt -s -l .`

- [ ] 13. Update REQUIREMENTS.md
  - [ ] 13.1 Move REQ000307, 478, 479, 480, 497, 498, 499 from TBD to DONE
  - [ ] 13.2 Update iteration column to iter-28

- [ ] 14. Update ROADMAP.md
  - [ ] 14.1 Add iter-28 row to Iterations Overview
  - [ ] 14.2 Mark as done (v0.28.0)

## Execution Order

```
Phase 1 (Parser) → Phase 2 (Executor) → Phase 3 (MV-OCC) → Phase 4 (Integration)
```

Phases 1-2 are independent of Phase 3. Can be parallelized if desired.
Phase 4 depends on all prior phases.

## Files to modify

| File | Changes |
|------|---------|
| `internal/SQL/PS/ast.go` | Add `IfExists` to DropTable/CreateIndexStmt/DropIndexStmt; add `NewName` to AlterTableStmt; add ReindexStmt |
| `internal/SQL/PS/ps.go` | Update parseDropTable, parseCreateIndex, parseDropIndex, parseAlterTable; add parseReindex |
| `internal/SQL/LX/lx.go` | Add "REINDEX" keyword |
| `internal/SQL/LX/token.go` | Add T_REINDEX token |
| `internal/SQL/EX/writers.go` | Update DropTable/DropIndex/CreateIndex for IF EXISTS/IF NOT EXISTS; add Reindex operator; add RENAME COLUMN to AlterTable |
| `internal/SQL/EX/ex.go` | Route ReindexStmt in buildWriterOp |
| `internal/SQL/EX/planner.go` | Route ReindexStmt in planner |
| `internal/TXN/VL/slot.go` | Add readSet field to transactionSlot |
| `internal/TXN/VL/validate.go` | Rewrite Validate with O(1) read-set check |
| `internal/TXN/VL/protocol.go` | Wire trackRead into tx.Get |
| `internal/SQL/PS/ps_test.go` | Parser tests for new syntax |
| `internal/SQL/EX/alter_table_test.go` | Executor tests for RENAME COLUMN |
| `internal/SQL/EX/index_ddl_test.go` | Tests for IF NOT EXISTS / IF EXISTS |
| `internal/TXN/VL/validate_test.go` | OCC read-set validation tests |
| `internal/TXN/VL/protocol_test.go` | Integration tests for read-write conflict |
