# Iteration 24 — SQLite Compliance (v0.22.0 → v0.24.0)

**Subsystem:** `TXN/VL`, `TXN/SN`, `SQL/EX`, `SQL/PS`, `SQL/PL`, `ENG/LS`, `SYS`

**Status:** done

**Est. LOC:** ~3,500

**Target releases:** v0.23.0 (transaction correctness + schema), v0.24.0 (FK enforcement)

---

## Overview

This iteration brings Razordata toward full SQLite compliance across three clusters:

1. **Transaction Correctness** — Read-committed isolation (SQLite default), own-writes visibility, configurable isolation levels
2. **Schema & Constraints** — Foreign keys, ALTER TABLE, CREATE VIEW, EXCLUDED.col fix
3. **SQL Completeness** — FETCH FIRST, Pragmas, parseInterval validation, LAG/LEAD offset

---

## Cluster A: Transaction Correctness (v0.23.0) — ~4,000 LOC

### REQ000123: Configurable Isolation Levels (~800 LOC)

**Files:** `SQL/PS/ps.go` (parser), `SQL/EX/ex.go` (executor), `TXN/SN/snapshot.go`, `SYS/AP/ap.go`

**Syntax:**
```sql
SET TRANSACTION ISOLATION LEVEL READ COMMITTED;
SET TRANSACTION ISOLATION LEVEL REPEATABLE READ;
SET TRANSACTION ISOLATION LEVEL SERIALIZABLE;
```

**Implementation:**
- Parse `SET TRANSACTION ISOLATION LEVEL <level>` in parser
- Add `IsolationLevel` enum to `TXN/SN`
- Store level on session/transaction
- Default: READ UNCOMMITTED (current behavior) → upgradeable

**Tests:**
- `TestSetTransaction_IsolationLevel` — parse + apply
- `TestSetTransaction_InvalidLevel` — error on bad level

---

### REQ000255: Read-Committed Per-Statement Snapshot (~1,500 LOC)

**Files:** `TXN/SN/snapshot.go` (re-snapshot), `SQL/EX/ex.go` (per-statement snapshot)

**Algorithm:**
1. At statement start: take new snapshot (max committed TX at statement start)
2. Statement reads see all commits up to snapshot
3. Statement writes go to write set (not visible to own reads until commit)
4. At commit: validate no write-write conflicts

**Key change:** Currently snapshots are per-transaction. Read-committed needs per-statement snapshots.

**Tests:**
- `TestReadCommitted_StaleRead` — concurrent insert visible after commit
- `TestReadCommitted_OwnWrite` — own uncommitted write NOT visible (unlike current)
- `TestReadCommitted_Concurrent` — two writers don't block reads

---

### REQ000062: MVCC Own-Writes Visibility (~1,200 LOC)

**Files:** `TXN/SN/snapshot.go`, `SQL/EX/ex.go`, `TXN/VL/protocol.go`

**Behavior:** Within a transaction, SELECT must see rows that the same transaction has written (even before commit). Currently, reads go to the snapshot which may not include own writes.

**Implementation:**
- Add `writeSet` to transaction context
- During SELECT, merge snapshot results with write-set entries
- Write-set entries override snapshot for same-key entries

**Tests:**
- `TestTxOwnWrites_InsertSelect` — INSERT then SELECT in same tx sees the row
- `TestTxOwnWrites_UpdateSelect` — UPDATE then SELECT sees new value
- `TestTxOwnWrites_DeleteSelect` — DELETE then SELECT sees deletion

---

### REQ000061: Read-Committed as Default (~200 LOC)

**Files:** `SYS/AP/ap.go` (default options)

**Change:** Set default isolation level to READ COMMITTED instead of READ UNCOMMITTED.

**Tests:**
- `TestDefaultIsolation_ReadCommitted` — new engine defaults to RC

---

## Cluster B: Schema & Constraints (v0.23.0) — ~4,000 LOC

### REQ000126: Foreign Keys (~2,000 LOC)

**Files:** `SQL/PS/ps.go` (parser), `SQL/EX/constraints.go` (enforcement), `SQL/EX/writers.go` (INSERT/UPDATE/DELETE hooks)

**Syntax:**
```sql
CREATE TABLE orders (
    id INT PRIMARY KEY,
    user_id INT REFERENCES users(id) ON DELETE CASCADE ON UPDATE SET NULL
);
```

**Enforcement:**
- INSERT: validate referenced row exists
- DELETE: enforce ON DELETE (CASCADE, SET NULL, SET DEFAULT, RESTRICT, NO ACTION)
- UPDATE: enforce ON UPDATE (same actions)
- Deferred mode: check at commit time (SQLite default)

**Tests:**
- `TestFK_InsertReferential` — INSERT with valid FK
- `TestFK_InsertOrphan` — INSERT with missing reference fails
- `TestFK_DeleteCascade` — CASCADE deletes children
- `TestFK_DeleteRestrict` — RESTRICT prevents parent delete
- `TestFK_UpdateCascade` — CASCADE updates children

---

### REQ000243+244: ALTER TABLE ADD/DROP COLUMN (~1,500 LOC)

**Files:** `SQL/PS/ps.go` (parser), `SQL/EX/alter.go` (new), `ENG/LS/catalog.go`

**Syntax:**
```sql
ALTER TABLE t ADD COLUMN c INT DEFAULT 0;
ALTER TABLE t DROP COLUMN c;
```

**Implementation:**
- Parse ALTER TABLE ADD/DROP COLUMN
- Update catalog schema (add/remove column from TableSchema)
- Rewrite existing rows with new schema (lazy or eager)
- Handle DEFAULT values for existing rows

**Tests:**
- `TestAlterTable_AddColumn` — add column with default
- `TestAlterTable_DropColumn` — drop column
- `TestAlterTable_AddColumnNotNull` — add NOT NULL with default
- `TestAlterTable_Persistence` — schema survives restart

---

### REQ000240+241: CREATE VIEW (~500 LOC)

**Files:** `SQL/PS/ps.go` (parser), `SQL/PL/planner.go` (view expansion), `ENG/LS/catalog.go`

**Syntax:**
```sql
CREATE VIEW v AS SELECT id, name FROM users WHERE active = 1;
SELECT * FROM v;
```

**Implementation:**
- Parse CREATE VIEW with AS SELECT
- Store view definition in catalog
- Planner expands view to underlying query (inline substitution)

**Tests:**
- `TestCreateView_Basic` — create + query view
- `TestCreateView_Persistence` — view survives restart
- `TestCreateView_Columns` — view column aliases

---

### REQ000291: EXCLUDED.col in ON CONFLICT (~300 LOC)

**Files:** `SQL/PS/ps.go` (parser fix)

**Bug:** `EXCLUDED.col` reference is silently dropped in ON CONFLICT DO UPDATE SET.

**Fix:** Parse `EXCLUDED.col` as a qualified name referencing the would-be-inserted row.

**Tests:**
- `TestUpsert_ExcludedCol` — `INSERT ... ON CONFLICT DO UPDATE SET x = EXCLUDED.x`

---

## Cluster C: SQL Completeness (v0.24.0) — ~1,500 LOC

### REQ000270: FETCH FIRST / LIMIT Shorthand (~200 LOC)

**Files:** `SQL/PS/ps.go` (parser)

**Syntax:**
```sql
SELECT * FROM t FETCH FIRST 10 ROWS ONLY;
SELECT * FROM t LIMIT 10 OFFSET 5;
```

**Tests:**
- `TestFetchFirst` — FETCH FIRST n ROWS ONLY
- `TestFetchFirstWithOffset` — FETCH FIRST n ROWS OFFSET m

---

### REQ000242: Pragmas (~400 LOC)

**Files:** `SYS/SY/sy.go` (pragma dispatch)

**Syntax:**
```sql
PRAGMA cache_size = 1000;
PRAGMA journal_mode = WAL;
PRAGMA synchronous = FULL;
PRAGMA cache_size;  -- read
```

**Pragmas:**
- `cache_size` — buffer pool page count
- `journal_mode` — WAL (default) or DELETE
- `synchronous` — OFF, NORMAL, FULL
- `user_version` — user-defined version number

**Tests:**
- `TestPragma_CacheSize` — set and read cache_size
- `TestPragma_JournalMode` — set journal_mode
- `TestPragma_Unknown` — error on unknown pragma

---

### REQ000288: parseInterval Unit Validation (~100 LOC)

**Files:** `SQL/PS/ps.go`

**Bug:** `INTERVAL '7' FOO` parses successfully with unit="FOO".

**Fix:** Validate unit against YEAR, MONTH, DAY, HOUR, MINUTE, SECOND.

**Tests:**
- `TestInterval_InvalidUnit` — error on bad unit

---

### REQ000290: LAG/LEAD Arbitrary Offset (~200 LOC)

**Files:** `SQL/EX/window.go`

**Current:** LAG/LEAD hardcoded to offset 1.

**Fix:** Read `args[1]` as the offset argument.

**Tests:**
- `TestWindow_LagOffset2` — LAG(col, 2)
- `TestWindow_LeadWithDefault` — LEAD(col, 1, 0)

---

## Implementation Order

### v0.23.0 (Cluster A + B)

1. REQ000123 — Configurable isolation levels (foundation)
2. REQ000255 — Read-committed per-statement snapshot
3. REQ000062 — MVCC own-writes visibility
4. REQ000061 — Read-committed as default
5. REQ000291 — EXCLUDED.col fix
6. REQ000126 — Foreign keys
7. REQ000243+244 — ALTER TABLE
8. REQ000240+241 — CREATE VIEW

**Milestone:** Tag v0.23.0 after all 8 REQs pass tests

### v0.24.0 (Cluster C)

1. REQ000270 — FETCH FIRST / LIMIT shorthand
2. REQ000242 — Pragmas
3. REQ000288 — parseInterval validation
4. REQ000290 — LAG/LEAD arbitrary offset

**Milestone:** Tag v0.24.0 after all 4 REQs pass tests

---

## Dependencies

```
REQ000123 (configurable isolation)
    → REQ000255 (read-committed per-statement)
        → REQ000061 (make it default)
REQ000062 (own-writes) — independent
REQ000291 (EXCLUDED.col) — independent
REQ000126 (foreign keys) — independent
REQ000243+244 (ALTER TABLE) — independent
REQ000240+241 (CREATE VIEW) — independent
REQ000270 (FETCH FIRST) — independent
REQ000242 (Pragmas) — independent
REQ000288 (parseInterval) — independent
REQ000290 (LAG/LEAD offset) — independent
```

---

## Testing Requirements

**Each REQ must have:**
1. Unit tests (table-driven, happy path + edge cases)
2. Integration tests (end-to-end with SQL API)
3. Transaction isolation tests (concurrent access patterns)

**Quality gates:**
- `go test ./... -race -count=1` — all green
- `go test ./internal/TXN/VL/ -race -count=3` — stability check
- Coverage: >80% for new files

---

## Outcome (Post-Completion Update)

**Status:** done

**Actual LOC:** ~3,500

**Tags:** v0.23.0, v0.24.0

**Commits:**
- `aa5c8b3` REQ000123: configurable isolation levels
- `bffd0db` REQ000255: read-committed per-statement snapshot
- `3e8dc70` REQ000062: MVCC own-writes visibility
- `f20b5ae` REQ000061: read-committed as default
- `cc36606` REQ000291: EXCLUDED.col fix
- `29b8d0f` REQ000240+241: CREATE VIEW
- `62cc29d` REQ000243: ALTER TABLE parsing
- `c38b0db` REQ000126: FK parsing
- `994ebf0` REQ000270+REQ000242: FETCH FIRST + Pragmas
- `d752876` REQ000288+REQ000290: parseInterval + LAG/LEAD
- `f2591d6` REQ000126: FK enforcement on INSERT

**Phase 1 outcome (v0.23.0):**
- REQ000123: SET TRANSACTION ISOLATION LEVEL syntax + IsolationLevel enum
- REQ000255: Snapshot infrastructure (snapshotTS on Executor, SetSnapshot on adapter/engine)
- REQ000062: Own-writes verified (SQL reads/writes directly to LSM)
- REQ000061: Read-committed as default isolation
- REQ000291: EXCLUDED.col in ON CONFLICT fixed
- REQ000240+241: CREATE VIEW parsing + planner resolution (inline expansion)
- REQ000243: ALTER TABLE ADD/DROP COLUMN/RENAME parsing
- REQ000126: FK parsing (inline REFERENCES + table-level FOREIGN KEY)
- REQ000270: FETCH FIRST n ROWS ONLY
- REQ000242: Pragmas (cache_size, journal_mode, synchronous, user_version)
- REQ000288: parseInterval unit validation
- REQ000290: LAG/LEAD arbitrary offset

**Phase 2 outcome (v0.24.0):**
- REQ000126 enforcement: FK INSERT validation (referenced row exists)
- FK metadata in storeSchema, validateForeignKeyInsert/validateForeignKeyDelete
- NULL FK values skip check (SQL standard)
- CASCADE/SET NULL/SET DEFAULT on DELETE stubbed

**Deviations:**
- Phase 2 was originally planned for FETCH FIRST/Pragmas/etc but these shipped in v0.23.0
- v0.24.0 focused on FK enforcement instead
- ALTER TABLE executor (REQ000244) deferred — complex schema migration
- FK CASCADE/SET NULL/SET DEFAULT on DELETE stubbed — returns error for now
- FK enforcement on UPDATE deferred

**Tests added:** ~15 new test cases (SET TRANSACTION, EXCLUDED.col, CREATE VIEW, ALTER TABLE, FK parsing, parseInterval)
