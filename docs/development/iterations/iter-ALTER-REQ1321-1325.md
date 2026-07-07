# Iteration — REQ001321 ~ REQ001325 (ALTER TABLE 故事组)

> **Status**: done
> **Outcome**: shipped — commit `b5abea4` on `develop`.
> **Lines of code**: +641 / -42 across 4 files (`SQB/WT/alter_table.go`,
> `SQB/EX/alter_table_test.go`, `SQF/PS/ast.go`, `SQF/PS/ddl.go`).

## Scope

Five REQs that all live in the `ALTER TABLE` executor and share the same
entry point (`internal/SQB/WT/alter_table.go`). Treated as a single
deliverable because splitting them produced unstable intermediate states
(any one of the new actions references helpers shared with the others).

## Delivered

| REQ       | Action                                                  | Test coverage                                                              |
|-----------|---------------------------------------------------------|----------------------------------------------------------------------------|
| REQ001321 | `ALTER TABLE t RENAME TO new` cascades FK RefTable, view FROM/JOIN, and trigger OnTable | `TestAlterTable_RenameCascadesFK` / `View` / `Trigger` |
| REQ001322 | `ALTER TABLE t ALTER [COLUMN] c SET DEFAULT expr` with semantic check (rejects unknown column refs) | `TestAlterColumn_SetDefault`, `TestAlterColumn_SetDefault_UnknownColumn` |
| REQ001323 | `ALTER TABLE t ALTER [COLUMN] c DROP DEFAULT`           | `TestAlterColumn_DropDefault`                                              |
| REQ001324 | `DROP COLUMN` cascades any FK constraint that references the dropped column | `TestDropColumn_CascadesFK`                          |
| REQ001325 | `DROP COLUMN` cascades any generated column whose expression references the dropped column | `TestDropColumn_CascadesGenerated`            |

## Deviations

1. **Helper re-entrancy.** `renameFKReferencesInSchemas` takes `DT.StoreMu`
   itself. The store-backed `execRename` path previously held `StoreMu`
   through a `defer Unlock`; calling the helper from inside that scope
   would have self-deadlocked. The fix was to release `StoreMu`
   explicitly before the cascade calls instead of via `defer`.

2. **FK list over-write at empty.** `RegisterStoreSchemaWithFKLocked`
   only writes `ss.ForeignKeys` when `fks != nil`. `execDropColumn`
   originally built `newFKs` as `var ... []DT.ForeignKeyConstraint` —
   a nil slice even when all FKs were dropped. Changed to
   `make([]..., 0, ...)` so a cascade that removes every FK still
   replaces the slice with an empty one. No schema.go changes needed.

3. **Parser extension.** `ALTER COLUMN` was not parsed at all. Added
   the `LX.T_ALTER` branch to `parseAlterTable` and a `NewExpr` field
   on `AlterTableStmt`. Touched `SQF/PS/ast.go` and `SQF/PS/ddl.go`.

## Verification

```
go test ./internal/SQB/EX/ ./internal/SQF/PS/ ./internal/SQB/WT/... \
        ./internal/SQB/DT/ ./internal/ENG/CT/ -race -count=1
```

All 6 affected packages green. `go test ./... -race -count=1` for the
broader regression has two pre-existing failures unrelated to this
iteration (`tests/sqlcmp/dual` correlated-subquery cases;
`internal/SQB/UT` `HandleDebugPragma` undefined) — verified by
`git stash` + re-run.

`gofmt -s -l` clean on all four touched files.