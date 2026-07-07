# Iteration — REQ001363 ~ REQ001365 (UPSERT story)

> **Status**: done
> **Outcome**: shipped — 3 REQs closed. Parser extensions in
>   `internal/SQF/PS/ast.go` + `insert.go`, executor extensions in
>   `internal/SQB/WT/writers_dml.go`, tests in
>   `internal/SQB/EX/iter28_bugfix_test.go`. All three REQ rows deleted
>   from `docs/development/REQUIREMENTS.md` TBD.
> **Lines of code**: +~280 LoC across 4 files.
> **Verification**: `go test ./internal/SQB/EX/... -run 'TestBugfix_InsertOnConflict|TestUpsert_' -race -count=1` — 7/7 PASS.
>   `go test ./internal/SQF/PS/... -race -count=1` — PASS.
>   `go vet` clean (only pre-existing `subq.go:96` unreachable code).

## Scope

Three REQs extending SQL UPSERT (`ON CONFLICT … DO UPDATE SET …`)
with three features required by commercial workloads: `EXCLUDED.col`
reference resolution, partial-index conflict-target WHERE clause,
and a conditional WHERE clause on the DO UPDATE branch itself.

Pre-iteration discovery revealed that the UPSERT parser and a basic
literal-value DO UPDATE (`SET v = 99`) were already shipped via
REQ000511. The three acceptance items missing were:

1. **REQ001363** — `EXCLUDED.col` dereference in the SET clause
2. **REQ001364** — `ON CONFLICT (cols) WHERE <pred>` on the conflict target
3. **REQ001365** — `DO UPDATE SET … WHERE <pred>` on the UPDATE

## Delivered

### REQ001363 — EXCLUDED.col

- **Parser**: already accepted `EXCLUDED.col` as `QualifiedName{Table:
  "excluded", Name: col}` (REQ000291). No parser changes needed.
- **Executor**: added `evalUpsertValue` in `writers_dml.go`. When the
  SET clause RHS is a `QualifiedName` with `Table == "excluded"`,
  resolves by bare column name lookup against the *new* (would-be-
  inserted) row instead of routing through the generic EV.EvalValue
  path (which would fail to match `"excluded.v"` against `"v"`).
- **Test**: `TestUpsert_DoUpdate_ExcludedCol` — seed `(1, 10)`,
  UPSERT `(1, 99) SET v = EXCLUDED.v`, verify `v = 99`.

### REQ001364 — partial-index conflict-target WHERE

- **Parser**: added `TargetWhere Expr` field to `ast.go:OnConflict`;
  extended `parseOnConflict` in `insert.go:132-140` to consume
  `WHERE <expr>` after the optional column-list conflict target.
- **Executor**: added `ErrTargetWhereFalse` sentinel error. In
  `applyConflictUpdate`, after the Mutate callback evaluates the
  predicate against the existing target row; if `false`, the callback
  returns the target unchanged and sets a `targetWhereFalse` flag.
  The caller catches `ErrTargetWhereFalse`, removes the conflicting
  row via `RemoveConflicting`, and falls through to `doInsertReplace`
  (normal insert).
- **Tests**: `TestUpsert_PartialIndex_Target` — WHERE predicate true
  (v < 50 against v=10), conflict honoured (no change).
  `TestUpsert_PartialIndex_PredicateMismatch_NoConflict` — WHERE
  predicate false (v > 50 against v=10), conflict skipped, row
  inserted alongside existing.

### REQ001365 — DO UPDATE WHERE

- **Parser**: added `UpdateWhere Expr` field to `ast.go:OnConflict`;
  extended `parseOnConflict` in `insert.go:241-248` to consume
  `WHERE <expr>` after the SET clause list.
- **Executor**: in `applyConflictUpdate`'s Mutate callback, evaluates
  `UpdateWhere` against the existing target row before applying SET
  mutations. If `false`, returns target unchanged (no-op mutation).
  No sentinel needed — unlike TargetWhere (which changes the
  conflict/no-conflict decision), UpdateWhere only controls whether
  the update applies; the row remains in place either way.
- **Tests**: `TestUpsert_DoUpdate_Where_SkipsUpdate` — WHERE false
  (v > 50 against v=10), update skipped, v stays 10.
  `TestUpsert_DoUpdate_Where_AppliesUpdate` — WHERE true (v < 50
  against v=10), update applied, v becomes 99.

## Deviations

1. **Partial-index WHERE uses the existing row as context, not the
   proposed row.** SQLite evaluates the conflict-target WHERE against
   the *existing* (conflicting) row, which is what we do. The test
   `PredicateMismatch_NoConflict` exercises the "false → no conflict
   → normal insert" path. However, duplicate PK insertion succeeds
   in the in-memory executor (no physical PK constraint enforced at
   storage level), so the test verifies the *control flow* but not a
   real storage-layer uniqueness guarantee.

2. **Multi-column SET (`SET a = EXCLUDED.a, b = EXCLUDED.b`) not
   separately tested.** The per-clause `evalUpsertValue` is called
   independently for each SET clause; `TestUpsert_DoUpdate_ExcludedCol`
   covers the resolver with one column, and the existing
   `TestParse_Upsert_ExcludedColMultiple` (parser test) confirms the
   AST shape for multi-column. Adding a multi-column runtime test
   would produce no new coverage. (This is a "three similar lines vs
   abstraction" call — decide at review time.)

3. **TargetWhere is evaluated in the mutation callback, not in the
   conflict-detection path.** Ideally the predicate would be evaluated
   *before* locking the row (`FindAndLock`). The current approach
   locks, evaluates, and either mutates or leaves untouched. For the
   in-memory executor this is correct (no concurrent writers). A
   storage-engine path that cared about lock granularity would move
   the check before `FindAndLock`.

## Verification (evidence)

```
$ go test ./internal/SQB/EX/... -run 'TestBugfix_InsertOnConflict|TestUpsert_' -race -count=1 -v
TestBugfix_InsertOnConflictDoUpdate      PASS
TestBugfix_InsertOnConflictDoNothing     PASS
TestUpsert_DoUpdate_ExcludedCol          PASS
TestUpsert_PartialIndex_Target           PASS
TestUpsert_PartialIndex_PredicateMismatch_NoConflict PASS
TestUpsert_DoUpdate_Where_SkipsUpdate    PASS
TestUpsert_DoUpdate_Where_AppliesUpdate  PASS
--- PASS: 7/7

$ go test ./internal/SQF/PS/... -race -count=1
ok  github.com/cyw0ng95/razordata/internal/SQF/PS  1.041s
--- exit=0

$ go test ./internal/SQB/WT/... -race -count=1
ok  github.com/cyw0ng95/razordata/internal/SQB/WT  0.045s [no test files]

$ go vet ./internal/SQF/PS/... ./internal/SQB/WT/... ./internal/SQB/EX/... 2>&1 | grep -v 'subq.go:96'
(no output besides pre-existing subq.go:96 unreachable code)
```

## Files touched

- `internal/SQF/PS/ast.go` — added `TargetWhere`, `UpdateWhere`
  fields to `OnConflict` struct.
- `internal/SQF/PS/insert.go` — `parseOnConflict` extended to consume
  optional `WHERE` on the conflict target and on the DO UPDATE branch.
- `internal/SQB/WT/writers_dml.go` — added `ErrTargetWhereFalse`
  sentinel, `applyConflictUpdate` rewritten to accept `*PS.OnConflict`
  and evaluate both WHERE predicates; added `evalUpsertValue` helper;
  caller catches `ErrTargetWhereFalse` and routes to `doInsertReplace`.
- `internal/SQB/EX/iter28_bugfix_test.go` — 5 new test functions
  (~110 lines).
- `docs/development/REQUIREMENTS.md` — rows for REQ001363, REQ001364,
  REQ001365 removed (93 rows remaining).
- `docs/development/iterations/iter-UPSERT-REQ1363-1365.md` — this
  iteration doc.

## Pre-existing (unrelated)

`internal/SQB/WT/subq.go:96` unreachable code warning predates this
iteration. Not introduced by this change.