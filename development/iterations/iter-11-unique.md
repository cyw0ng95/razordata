# Iteration 11 — UNIQUE Constraint

**Subsystem:** `SQL` (`PS`, `EX`)
**Status:** planned
**Est. LOC:** ~700
**Requirements:** REQ000107
**Target release:** v0.8.0

## Overview

Enforce column-level and table-level `UNIQUE` constraints. On
INSERT/UPDATE that would produce a duplicate value in any UNIQUE
column or composite key, return `ErrConstraint` (same sentinel used
for NOT NULL violations in iter-10).

## Dependencies

- Required: iter-10 (NOT NULL plumbing — same `validateRow` /
  `fillDefaults` / `registerStoreSchemaWithConstraints` path)
- Required: iter-09 (in-memory catalog; switch path is best-effort
  for v1 since catalog persistence is iter-12)
- Touches: `SQL/PS/ast.go`, `SQL/PS/ps.go`, `SQL/EX/store.go`,
  `SQL/EX/writers.go`, `SQL/EX/constraints.go`, `SQL/EX/operators.go`

## Current State

**Parsing (partly works):**
- `CREATE TABLE t (a INTEGER UNIQUE)` → `col.Unique = true`
- `CREATE TABLE t (a INTEGER UNIQUE NOT NULL)` → both flags
- `CREATE TABLE t (a INTEGER, b TEXT, UNIQUE (a))` → parsed but
  the constraint is **discarded** — `parseCreateTable` only captures
  `PK` from the trailing `PRIMARY KEY (a)` / `UNIQUE (a)` block
  (PS/ps.go:875-899). Composite `UNIQUE (a, b)` would overwrite the
  single `*string` PK.
- No `UniqueConstraints` field on `CreateTable` AST node.

**Storage (missing):**
- `storeSchema` has no `unique` field. No way to look up an existing
  value to check for duplicates.
- `EncodeRow` produces row bytes; there's no keyspace index to scan
  for "does this value already exist?".
- In-memory `tables` map is in-process only; engine path uses LSM
  key range scan.

**Error type (ready):**
- `AP.ErrConstraint` exists (iter-10) and is in `FatalErrors`.

## Requirements

| ID | Requirement | Status |
|---|---|---|
| REQ000107 | `UNIQUE` constraint: column-level and table-level (composite); reject INSERT/UPDATE that produces a duplicate value | planned |
| R11-1 | Parser emits `CreateTable.UniqueConstraints []UniqueKey` from `UNIQUE (a, b, ...)` clauses; column-level `UNIQUE` is folded into the same slice | planned |
| R11-2 | `ColDef.Unique` retained for round-trip; planner normalizes both forms into `UniqueConstraints` | planned |
| R11-3 | `storeSchema` carries `unique []UniqueKey` (parallel to `cols`); composite keys are sub-slices | planned |
| R11-4 | `registerStoreSchemaWithConstraints` propagates unique info from `CreateTable` | planned |
| R11-5 | New `checkUnique(schema, row, existingLookup) error` in `constraints.go`; runs after `validateRow` in Insert and Update paths | planned |
| R11-6 | In-memory path: `existingLookup(cols)` scans the in-memory `tables` map for matching values; matches `ErrConstraint` with `UNIQUE violation` message | planned |
| R11-7 | Engine path: `existingLookup(cols)` uses `Store.NewIterator` with a prefix derived from the table prefix; reads the row by composite key, returns first hit | planned |
| R11-8 | Composite unique (multi-column): for `UNIQUE (a, b)`, encode the values into a key and look up; pre-existing rows in the same statement are also checked (deferred constraint to end of statement) | planned |
| R11-9 | Within-statement duplicates: track the pending row batch in `Insert`; check pending set before scanning the store | planned |
| R11-10 | `Update` only checks new values against existing rows whose PK is different (the row's pre-update key is excluded to allow no-op updates) | planned |
| R11-11 | `go test ./internal/SQL/... -race -count=1` all green | planned |
| R11-12 | Coverage for unique-check paths ≥ 80% | planned |

## Design

### Data model

```go
// SQL/PS/ast.go
// UniqueKey is a unique constraint over one or more columns. The
// resolved field holds column indices into the table's column list,
// populated by the planner (parser only has column names).
type UniqueKey struct {
    Cols     []string // column names; empty before resolution
    resolved []int    // column indices; populated at registration time
}

// CreateTable now also carries:
type CreateTable struct {
    Name              string
    Cols              []ColDef
    PK                *string
    UniqueConstraints []UniqueKey
}

// ColDef.Unique (already exists) becomes the single-column form:
// resolved into UniqueKey{{Col: name}} at registration.
```

```go
// SQL/EX/store.go
type UniqueKey struct {
    Cols []int // indices into schema.cols
}

type storeSchema struct {
    cols     []string
    pk       string
    nullable []bool
    defaults []PS.Expr
    unique   []UniqueKey // each entry: 1+ columns
}
```

Note: `PS.UniqueKey` and `EX.UniqueKey` are different types. The
planner converts PS form to EX form at registration. This keeps the
parser AST free of engine internals.

### Existing-value lookup

```go
// SQL/EX/constraints.go
type uniqueLookup func(cols []int, vals []interface{}) (bool, error)
// returns (true, nil) if the value already exists in another row

// For the in-memory path:
func inMemoryLookup(tableName string) uniqueLookup {
    return func(cols []int, vals []interface{}) (bool, error) {
        tablesMu.RLock()
        defer tablesMu.RUnlock()
        for _, row := range tables[tableName] {
            if rowMatches(row.Data, cols, vals) {
                return true, nil
            }
        }
        return false, nil
    }
}

// For the engine path: use the LSM key range iterator
func engineLookup(store Store, tableID uint64) uniqueLookup {
    return func(cols []int, vals []interface{}) (bool, error) {
        // Encode vals into a lookup key (composite). Walk the
        // table's prefix; on the first row with matching values,
        // return true. Limit the scan to the table's key range.
    }
}
```

### Composite key encoding

```go
// SQL/EX/constraints.go
func encodeUniqueKey(cols []int, vals []interface{}) []byte {
    // Format: [n:varint][col_0:varint][val_0:8bytes-or-hash]...
    // Hash values to 8 bytes for fixed-size comparison.
}
```

For v1, use `crc32` of the concatenated value bytes — exact equality
isn't required since we filter the iterator by prefix first.

### Validation order

```
1. buildInsertRow       (resolve cols → schema order)
2. fillDefaults         (eval DEFAULT for nil)
3. validateRow          (NOT NULL on non-nullable)
4. checkUnique          (UNIQUE — NEW)
   a. check pending-batch duplicates
   b. check existing-store duplicates
5. encodeRow → store.Insert
```

### Within-statement duplicates

For multi-row INSERT, the same UNIQUE value can appear twice in
the batch. Maintain a `pending map[string]struct{}` of encoded
unique keys already seen in the current `Insert.Next` call.

```go
// SQL/EX/writers.go (Insert.nextFromStore)
pending := make(map[string]struct{}, len(i.values))
for _, row := range i.values {
    out, err := buildInsertRow(...)
    out, err = fillDefaults(i.schema, out)
    if err := validateRow(i.schema, out); err != nil { return Row{}, err }
    if err := checkUnique(i.schema, out, pending, engineLookup); err != nil {
        return Row{}, err
    }
    pending[encodeUniqueKey(...)] = struct{}{}
    // ... encode + store.Insert
}
```

### Update path

```go
// SQL/EX/writers.go (Update.nextFromStore)
for {
    row := iter.Next() // existing row
    newRow := applyUpdate(row, set)
    if err := checkUnique(i.schema, newRow, nil, engineLookup); err != nil {
        return Row{}, err
    }
    // The current row itself is "existing" — exclude its old PK
    // from the scan by short-circuiting if the new row is identical
    // to the existing one for the unique key.
}
```

## Implementation

### Phase 1: AST + Parser

1. `SQL/PS/ast.go` — add `UniqueKey` struct + `UniqueConstraints`
   field to `CreateTable`. Add helper `UniqueKeyFromName(string)` for
   single-column form.
2. `SQL/PS/ps.go:875-899` — the trailing `UNIQUE (a, b, ...)` loop
   currently stores only single-column PK. Refactor to support
   multi-column UNIQUE:
   - Single `UNIQUE (a)` → add `UniqueKey{Cols: [a]}` to
     `UniqueConstraints`.
   - Multi `UNIQUE (a, b)` → `UniqueKey{Cols: [a, b]}`.
3. `SQL/PS/ps_test.go` — add round-trip tests:
   - `UNIQUE (a, b)` composite
   - Multi UNIQUE: `(a)`, `(b, c)`
   - `UNIQUE KEY (a)` (existing keyword form)

### Phase 2: Storage

1. `SQL/EX/store.go` — add `UniqueKey` (different from PS) and
   `unique []UniqueKey` to `storeSchema`.
2. `SQL/EX/store.go` — extend `registerStoreSchemaWithConstraints` to
   take `unique []EX.UniqueKey` and copy it into the schema.
3. `SQL/EX/store.go` — back-compat: `registerStoreSchema` with no
   unique info (existing callers) still works — sets `unique = nil`.

### Phase 3: Constraint engine

1. `SQL/EX/constraints.go` — add:
   - `UniqueKey` type (with `Cols []int`)
   - `uniqueLookup` callback type
   - `checkUnique(schema, row, pending map, lookup uniqueLookup) error`
   - `encodeUniqueKey(cols []int, vals []interface{}) []byte`
   - `inMemoryLookup(tableName string) uniqueLookup`
2. `SQL/EX/constraints.go` — add tests:
   - Single-column match returns ErrConstraint
   - Composite (a, b) match returns ErrConstraint
   - Partial-match columns (only `a` matches, `b` differs) → not unique
   - Empty lookup (table empty) → no violation
   - Pending-batch duplicate → ErrConstraint

### Phase 4: Wiring

1. `SQL/EX/writers.go` — `Insert.Next` (in-memory path): call
   `checkUnique` after `validateRow`, with `inMemoryLookup` + pending
   set.
2. `SQL/EX/writers.go` — `Insert.nextFromStore` (engine path): call
   `checkUnique` with `engineLookup` + pending.
3. `SQL/EX/writers.go` — `Update.Next` / `nextFromStore`: call
   `checkUnique` on the merged row, excluding the current row's
   pre-update value (the row IS in the unique set already, and a
   no-op update should not self-conflict).
4. `SQL/EX/writers.go` — `CreateTable.Next`: when
   `registerStoreSchemaWithConstraints` is called, also pass the
   resolved unique constraints (column names → indices).

### Phase 5: CreateTable path

1. `SQL/EX/writers.go` — `CreateTable.Next`:
   - Build `[]EX.UniqueKey` from `ColDef.Unique` flags (single-col).
   - Build `[]EX.UniqueKey` from `c.stmt.UniqueConstraints` (composite
     or single).
   - Resolve column names to indices.
   - Pass to `registerStoreSchemaWithConstraints`.

### Phase 6: Tests

1. `SQL/EX/constraints_test.go` (extend) — UNIQUE-specific tests:
   - Column-level UNIQUE: duplicate insert → ErrConstraint
   - Column-level UNIQUE: distinct values → ok
   - Composite UNIQUE (a, b): (1, 'x') then (1, 'y') → ok
   - Composite UNIQUE: (1, 'x') then (1, 'x') → ErrConstraint
   - UPDATE setting unique value to existing → ErrConstraint
   - Within-statement duplicate in multi-row INSERT → ErrConstraint
   - PRIMARY KEY is implicitly UNIQUE — duplicate PK → ErrConstraint
2. `SQL/PS/ps_test.go` (extend) — parser tests:
   - `UNIQUE (a, b)` composite
   - Multiple `UNIQUE` clauses
3. `SYS/AP/ap_test.go` — already includes `ErrConstraint` in
   retry/fatal table from iter-10; no change needed.
4. `SQL/EX/e2e_test.go` — full pipeline: CREATE TABLE with UNIQUE,
   INSERT duplicate, verify error, INSERT distinct, verify success.

### Phase 7: Benchmarks

1. `SQL/EX/bench_test.go` — add `BenchmarkUniqueInsert` to verify the
   unique-check overhead is bounded. Target: ≤ 1.5x of
   `BenchmarkConstraintsInsert` baseline (no-unique).

## Open Questions

- **Q1**: Composite UNIQUE on TEXT columns — exact match or
  normalize? Decision: exact match for v1 (case-sensitive, no
  trimming).
- **Q2**: Should UNIQUE also be enforced on `UPDATE` even if the
  value didn't change? Decision: yes, to keep semantics simple.
- **Q3**: Within-statement duplicates — fail-fast on first conflict
  or report all? Decision: fail-fast (simpler; same as standard SQL
  with default error handling).
- **Q4**: `NULL` in UNIQUE columns — SQL standard says multiple NULLs
  are allowed. Decision: out of scope for v1; treat NULL as equal
  to itself (reject duplicates including NULL). Document as a
  known deviation.

## Deferred to v2

- Per-row `DEFAULT` expressions with column references
- `CHECK` constraints
- Partial / functional unique indexes (`UNIQUE WHERE deleted = false`)
- Index-backed unique checks (currently O(n) table scan; fine for v1)
- Auto-create index on UNIQUE columns (REQ000045 territory)

## Acceptance Criteria

- `go test ./... -race -count=1` green
- `go vet ./...` zero warnings
- `gofmt -s -l .` no drift
- `SQL/EX` coverage for unique paths ≥ 80%
- New `tests/sqlcmp` cases: single + composite UNIQUE
- Tag `v0.8.0` on completion

## Migration Note (for release notes)

- `CreateTable` AST now has `UniqueConstraints` field. External code
  that constructs `CreateTable` directly must populate it (or leave
  nil — backward-compat path uses `ColDef.Unique` flags).
- `registerStoreSchemaWithConstraints` signature changes: new
  `unique []EX.UniqueKey` parameter. Update any direct callers.
- `AP.ErrConstraint` now also covers UNIQUE violations (was NOT NULL
  only in iter-10).
