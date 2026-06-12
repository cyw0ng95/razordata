# Iteration 10 — NOT NULL / DEFAULT Constraints

**Subsystem:** `SQL` (`PS`, `EX`)
**Status:** done
**Est. LOC:** ~600
**Actual LoC:** ~480 (impl ~280, tests ~200)
**Requirements:** REQ000105, REQ000106
**Target release:** v0.7.0

## Outcome

Shipped in v0.7.0. All requirements met; no deviations from the plan.

**What shipped:**
- `ColInfo` extended with `Nullable`, `Default`, `PK` fields
- `storeSchema` extended with `nullable` and `defaults` parallel slices
- New `registerStoreSchemaWithConstraints` registration function
- `AP.ErrConstraint` sentinel, classified as fatal
- `SQL/EX/constraints.go` with `fillDefaults` and `validateRow`
- Wired into Insert (both in-memory and engine paths) and Update (both paths)
- `CreateTable` propagates `ColDef.Nullable` / `ColDef.Default`; PRIMARY KEY
  implies NOT NULL
- `UnregisterAll` now also clears `storeSchemas` / `tableIDs` (test isolation fix)

**Tests:** 16 new (12 EX + 3 PS + 1 AP regression); all pass with `-race`.

**Benchmark:** `BenchmarkConstraintsInsert` ~1µs/op (no regression).

**Final commit/tag:** commit 4c48713, tag v0.7.0.

## Overview

Wire up `NOT NULL` and `DEFAULT` column constraints end-to-end. The
parser already captures both (`PS/ps.go:812-857`) and the AST carries
them (`ColDef.Nullable`, `ColDef.Default`), but the executor never
consults them. INSERT/UPDATE accept nulls for non-nullable columns and
silently drop columns that should be filled with a default.

## Dependencies

- Required: `SQL/PS`, `SQL/EX`, `SQL/RE` (already present)
- Touches: `SQL/EX/store.go`, `SQL/EX/writers.go`, `SQL/PS/ast.go`,
  `SQL/EX/ex.go`, `SQL/EX/planner.go`

## Current State

**Parsing (already works):**
- `NOT NULL` / `NOT NULL PRIMARY KEY` → `col.Nullable = false`
- `DEFAULT <expr>` → `col.Default = <expr>` (untyped `PS.Expr`)
- `PRIMARY KEY` implies `NOT NULL` (PS sets `Nullable = false`)

**Execution (missing):**
- `buildInsertRow` (writers.go) maps supplied columns to schema order.
  Omitted columns are silently left as `nil` in the output row.
- `Insert.nextFromStore` calls `buildInsertRow` → `extractPK` →
  `encodeRow` → `store.Insert`. No NOT NULL check, no default fill.
- `ColInfo` (ex.go:50) is `{Name, Typ}` only — no `Nullable`, no `Default`.
- `storeSchema` (store.go:27) is `{cols, pk}` only — no constraint info.
- `registerStoreSchema` / `Executor.RegisterTable*` lose constraint
  information when registering a table.
- No sentinel error for constraint violation. `AP.ErrTypeMismatch` is
  not a fit; needs `ErrConstraint` (or reuse `ErrTypeMismatch` with a
  clear wrapped message).

## Requirements

| ID | Requirement | Status |
|---|---|---|
| REQ000105 | `NOT NULL` constraint: INSERT/UPDATE that produces NULL for a non-nullable column returns `ErrConstraint` (or equivalent) | planned |
| REQ000106 | `DEFAULT <expr>` substitution: omitted column with a default is filled with the evaluated default; expression must be evaluable without a source row (literals, `NOW()`, etc.) | planned |
| R30-1 | Wire `Nullable` and `Default` from `PS.ColDef` through `CreateTableStmt` handling into `ColInfo` and `storeSchema` | planned |
| R30-2 | Add `ErrConstraint` sentinel to `SYS/AP` and re-export from `SQL/EX` | planned |
| R30-3 | `buildInsertRow` fills omitted columns with evaluated `Default` when present | planned |
| R30-4 | `buildInsertRow` errors on NULL result for non-nullable column | planned |
| R30-5 | `Update.Next` validates new values against the schema (NOT NULL only — old values may have been NULL) | planned |
| R30-6 | `CreateTable` planning path populates the constraint fields; existing `RegisterTable*` helper signatures remain backward-compatible via a new `RegisterTableWithConstraints` variant | planned |
| R30-7 | `go test ./internal/SQL/... -race -count=1` all green | planned |
| R30-8 | Coverage for `SQL/EX` constraint paths ≥ 80% | planned |

## Design

### Data model

```go
// SQL/EX/ex.go
type ColInfo struct {
    Name     string
    Typ      int
    Nullable bool          // default true; false means NOT NULL
    Default  PS.Expr       // nil means no default
    PK       bool          // moved from separate param
}

// SQL/EX/store.go
type storeSchema struct {
    cols      []string
    pk        string
    nullable  []bool   // parallel to cols
    defaults  []PS.Expr // parallel to cols; nil entry means no default
}
```

### Error type

```go
// SYS/AP/ap.go
var ErrConstraint = errors.New("razordata: constraint violation")
```

Wrapped with column name and rule: `fmt.Errorf("%w: column %q %s: %v",
AP.ErrConstraint, col, "is NOT NULL", val)`.

### Constraint validation entry point

```go
// SQL/EX/constraints.go (new file)
func validateRow(schema *storeSchema, row Row) error {
    for i, col := range schema.cols {
        v := row.Data[i]
        if v == nil {
            if schema.defaults[i] != nil {
                continue // filled in by fillDefaults
            }
            if !schema.nullable[i] {
                return fmt.Errorf("%w: column %q is NOT NULL", AP.ErrConstraint, col)
            }
        }
    }
    return nil
}

func fillDefaults(schema *storeSchema, row Row) (Row, error) {
    for i, col := range schema.cols {
        if row.Data[i] == nil && schema.defaults[i] != nil {
            v, err := Eval(schema.defaults[i], nil, nil)
            if err != nil {
                return row, fmt.Errorf("%w: default for %q: %v", AP.ErrConstraint, col, err)
            }
            row.Data[i] = v
        }
    }
    return row, nil
}
```

### Wiring

`buildInsertRow` (writers.go) currently:
1. Resolves supplied columns against schema.
2. Returns a `Row` with `nil` for omitted columns.

After iter-10:
1. Same resolution.
2. **NEW**: call `fillDefaults` to fill omitted columns with their default.
3. **NEW**: call `validateRow` to enforce NOT NULL.

`Update.Next` (writers.go:128-215):
1. Find rows via iterator.
2. Build new row from `set` clauses.
3. **NEW**: call `fillDefaults` and `validateRow` for the merged row.

### Registration path

```go
// SQL/EX/ex.go — new public function
func (e *Executor) RegisterTableWithConstraints(
    name string,
    cols []ColInfo,           // now carries Nullable + Default
    pk string,
) {
    e.planner.RegisterTable(name, cols, pk)
    RegisterTableSchema(name, colNames(cols))
    if e.store != nil {
        registerStoreSchemaWithConstraints(name, cols, pk)
    }
}
```

Existing `RegisterTable` / `RegisterTableWithPK` remain unchanged for
backward compatibility. New code paths use the constraint-aware
variant.

## Implementation

### Phase 1: Data model

1. `SQL/EX/ex.go` — add `Nullable` and `Default` to `ColInfo`. Add
   `RegisterTableWithConstraints`.
2. `SQL/EX/store.go` — extend `storeSchema` with `nullable` and
   `defaults` slices. Add `registerStoreSchemaWithConstraints` next to
   existing `registerStoreSchema`. `schemaFor` returns the new fields.
3. `SQL/EX/planner.go` — `tableInfo` mirrors `ColInfo` (no change to
   planner logic, just the schema carrier).

### Phase 2: Error type

1. `SYS/AP/ap.go` — add `ErrConstraint` sentinel to the error block.
2. `SYS/AP/ap.go` — add `ErrConstraint` to the `fatal` list
   (constraint violations are application-level, not retryable).
3. `SQL/EX/errors.go` (or top of writers.go) — alias
   `ErrConstraint = AP.ErrConstraint` for in-package use.

### Phase 3: Constraint engine

1. `SQL/EX/constraints.go` (new) — `validateRow`, `fillDefaults`.
2. `SQL/EX/writers.go:64` — `Insert.Next` calls `fillDefaults` then
   `validateRow` after `buildInsertRow`. Map `ErrConstraint` through
   unchanged.
3. `SQL/EX/writers.go:78` — `Insert.nextFromStore` same hook after
   `buildInsertRow`, before `extractPK`.
4. `SQL/EX/writers.go:128+` — `Update.Next` calls
   `fillDefaults` + `validateRow` on the merged row.

### Phase 4: CreateTable path

1. `SQL/EX/ex.go` or `SQL/EX/planner.go` — `Plan` for
   `*PS.CreateTable` populates `ColInfo` from `PS.ColDef` and calls
   `RegisterTableWithConstraints` (or stores the constraint data
   internally; the in-memory `tables` map path still uses
   `RegisterTable`).
2. The `CREATE TABLE` executor path: re-examine `planner.go:CreateTable`
   handling and the `Executor.handleCreate` path. Make sure the
   constraint info flows from the AST into the in-memory catalog.

### Phase 5: Tests

1. `SQL/EX/constraints_test.go` (new) — table-driven:
   - INSERT with `NOT NULL` column, value supplied → ok
   - INSERT with `NOT NULL` column, value omitted, no default → `ErrConstraint`
   - INSERT with `NOT NULL` column, value omitted, default present → filled
   - INSERT with `DEFAULT 42`, omitted → value is 42
   - INSERT with `DEFAULT NOW()`, omitted → non-zero time
   - UPDATE setting non-nullable to NULL → `ErrConstraint`
   - `PRIMARY KEY` column is implicitly NOT NULL (regression)
2. `SQL/PS/ps_test.go` — extend with NOT NULL / DEFAULT round-trip:
   parsed AST has `Nullable = false` and `Default = <Expr>`.
3. `SYS/AP/ap_test.go` — add `ErrConstraint` to the
   `TestAP_RetryableAndFatal` table as fatal.
4. End-to-end: `tests/sqlcmp/...` or `SQL/EX/e2e_test.go` — run a
   full CREATE → INSERT → SELECT with `NOT NULL` and `DEFAULT` against
   the engine path.

### Phase 6: Benchmarks

1. `SQL/EX/bench_test.go` — extend to include a constraint-heavy
   INSERT throughput case (proves no regression on the hot path).

## Open Questions

- **Q1**: When `DEFAULT` is an expression involving column references
  (e.g., `DEFAULT other_col + 1`), should we evaluate per-row or treat
  as a static value? Decision: **out of scope for iter-10** — only
  literal/default-function expressions. Per-row evaluation requires a
  future iteration.
- **Q2**: `ErrConstraint` vs reusing `ErrTypeMismatch`. Decision: new
  sentinel `ErrConstraint`. Wrap with column name + rule.
- **Q3**: Backward compat for `RegisterTable` / `RegisterTableWithPK`.
  Decision: keep them; new `RegisterTableWithConstraints` is the
  preferred path. Document migration in iter-10 release notes.

## Deferred to v2

- Per-row `DEFAULT` expressions with column references
- `CHECK` constraints
- `UNIQUE` constraint (iter-11)
- Foreign keys (iter-20)
- Constraint violation → trigger / log hooks

## Acceptance Criteria

- `go test ./... -race -count=1` green
- `go vet ./...` zero warnings
- `gofmt -s -l .` no drift
- `SQL/EX` coverage for constraint paths ≥ 80%
- New `tests/sqlcmp` cases: `NOT NULL` violation, `DEFAULT` fill
- Tag `v0.7.0` on completion
