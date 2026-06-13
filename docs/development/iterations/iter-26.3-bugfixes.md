# Iteration 26.3 — v0.26.5 Bugfixes

**Subsystem:** `SQL/EX`, `SQL/PS`, `tests/sqlcmp/dual`

**Status:** done

**Est. LOC:** ~250 (5 REQs: 378, 379, 380, 381, 382)

**Target release:** v0.26.5

**Tags:** `quality`, `bugfix`, `correctness`, `parser`, `eval`

---

## Overview

A second probing sweep (wider SLT-style corpus + dual-runner
edge cases) surfaced five concrete parser/eval defects. The fixes
follow the v0.26.3/v0.26.4 cadence — pure bugfix, no features,
no API changes. Each defect already had a `REQ` row from the
TBD backlog; this iteration is the planned fix-up of
REQ000378-REQ000382.

**Deferred:** REQ000383 (compound SELECT: UNION, INTERSECT,
EXCEPT) is M-effort and was held back for v0.26.6 to keep this
release minimal.

---

## Goals

1. Close REQ000378 — `HAVING COUNT(*) > N` returns 0 rows.
2. Close REQ000379 — regression tests for SQLite-compatible
   `--` line comment behavior (the chained unary minus form).
3. Close REQ000380 / REQ000381 — `NOT LIKE` and `NOT IN`
   parser errors.
4. Close REQ000382 — add `ABS`, `HEX`, `ROUND` scalar
   functions to `evalFunction`.
5. Add dual-runner probe coverage for every fixed bug.

## Non-Goals

- REQ000383 UNION/INTERSECT/EXCEPT (deferred to v0.26.6).
- Core function matrix REQ000384-417 (planned as separate
  iterations).
- Performance work, REW layer, persistent probe corpus.

---

## REQ-by-REQ Notes

### REQ000378 — HAVING with `COUNT(*)` returns 0 rows

**Symptom:** `SELECT g, COUNT(*) FROM t GROUP BY g HAVING COUNT(*) > 1`
returned 0 rows.

**Root cause:** `evalAggregate` already supported `COUNT(col)` /
`SUM(col)` lookup in the row emitted by the Aggregate operator
(via the `Ident` arg path). It did not have a parallel `StarExpr`
arg path, so `COUNT(*)` (whose emitted column name is the literal
`COUNT(*)`, not `COUNT(col)`) was not found and the aggregate
function returned the default `int64(0)` — which then made the
HAVING filter `0 > 1` false for every group.

**Fix:** Add the `StarExpr` lookup path before the `Ident`
lookup, mirroring the existing pattern:

```go
if _, ok := e.Arg.(*PS.StarExpr); ok {
    name := e.Name + "(*)"
    if v, found := row.Lookup(name); found {
        return v, nil
    }
}
```

**Probe:** `having_count_star` in
`tests/sqlcmp/dual/probe_cases.go`.

**Touches:** `internal/SQL/EX/eval.go` (one new 6-line block).

### REQ000379 — Chained unary minus

**Symptom:** (per REQ description) `SELECT --5` returned
`ps: syntax error`.

**Analysis:** SQLite unconditionally treats `--` as a line
comment, even when it follows a value token. The RazorData
lexer already follows that rule. The REQ was originally framed
as "allow chained unary minus", but the SQLite standard
actually rejects `SELECT --5` (the `--5` is a comment) and
accepts `SELECT 5- -5` (chain with explicit space).

**Fix:** No code change — the existing `parseUnary` already
accepts the explicit-space form. Added 3 probe cases to lock
the behavior in:

- `chained_unary_minus_with_space` — `SELECT 5- -5` = 10
- `chained_unary_minus_three_with_space` — `SELECT 5- - -5` = 10
- `minus_minus_value_is_comment` — `SELECT 5-- this is a comment` = 5

**Touches:** `tests/sqlcmp/dual/probe_cases.go` only.

**Note:** A pre-fix attempt introduced a `prevToken` field on
the lexer to suppress `--` comment handling when the previous
token was a value. That change was reverted once the dual-runner
test against `modernc.org/sqlite` confirmed SQLite returns 5
for `SELECT 5--5` (treating `--5` as a comment), making the
proposed change non-SQLite-compatible.

### REQ000380 / REQ000381 — `NOT LIKE` and `NOT IN` parser errors

**Symptom:** `SELECT 'abc' NOT LIKE 'a%'` and `SELECT 5 NOT IN (1,2,3)`
returned `ps: syntax error at line 1 col N: expected expression, got NOT`.

**Root cause:** `parsePostfix` only handled the postfix forms
`T_BETWEEN` and `T_IN`. When the current token was `T_NOT`, the
parser fell through to `parseBinary`, which rejected `T_NOT`
(it's not in the binary-op table).

**Fix:** Add a `T_NOT` block in `parsePostfix` that peeks the
next token. If it is `T_LIKE` or `T_IN`, dispatch to
`parseNotLike` / `parseNotIn`. Both helpers wrap the inner
`BinaryExpr{T_LIKE, ...}` or `InExpr{...}` in a
`UnaryExpr{T_NOT, ...}` — the `evalUnary` already negates the
result via `!truthy(operand)`, so no eval change was needed.

**Refactor:** `parseIn` was split into a wrapper that
consumes the `T_IN` token and a body helper `parseInBody` that
operates on the `( ... )` part. `parseNotIn` consumes the
`T_IN` and calls `parseInBody` directly.

**Probes:**

- `not_like_true` / `not_like_false`
- `not_in_list` / `not_in_subquery`

**Touches:** `internal/SQL/PS/ps.go` (parsePostfix, parseNotLike,
parseNotIn, parseInBody).

### REQ000382 — Add `ABS`, `HEX`, `ROUND` scalar functions

**Symptom:** `SELECT ABS(-5)`, `SELECT HEX(255)`,
`SELECT ROUND(3.14, 2)` all returned `ex: eval error`.

**Fix:** Add three new branches to `evalFunction`, each
delegating to a new helper (`evalAbs`, `evalHex`, `evalRound`).

- **ABS(X):** `int64 → int64`, `float64 → float64`,
  `MIN_INT64 → error`, `NULL → NULL`, non-numeric → 0.0.
- **HEX(X):** uppercase hex encoding. **Important**:
  integers are first converted to their decimal text form,
  then hex-encoded (SQLite behavior). `HEX(255)` = `"323535"`,
  not `"FF"`. Floats use `strconv.FormatFloat(_, 'g', -1, 64)`.
  Strings and byte slices are hex-encoded byte-for-byte. NULL → NULL.
- **ROUND(X[,Y]):** rounds to Y decimal places; Y default 0;
  Y < 0 is clamped to 0 (SQLite warning, we treat as 0 for v1).
  NULL → NULL, non-numeric → 0.0. Uses `math.Round`.

**Tests:** 19 unit-test cases in a new
`internal/SQL/EX/corefunc_test.go`, plus 7 dual-runner probe
cases.

**Touches:** `internal/SQL/EX/eval.go` (3 new helpers + 3 new
switch cases), `internal/SQL/EX/corefunc_test.go` (new file),
`tests/sqlcmp/dual/probe_cases.go`.

---

## Files Changed

- `internal/SQL/EX/eval.go` — REQ000378 (`evalAggregate`
  StarExpr path), REQ000382 (3 new helpers + switch cases,
  `encoding/hex` and `strconv` imports).
- `internal/SQL/PS/ps.go` — REQ000380/381 (`parsePostfix`
  NOT-LIKE/IN dispatch, `parseNotLike`, `parseNotIn`,
  `parseInBody` refactor).
- `internal/SQL/EX/corefunc_test.go` — new file, 19 unit
  tests for ABS / HEX / ROUND.
- `tests/sqlcmp/dual/probe_cases.go` — 12 new probe cases:
  - `chained_unary_minus_with_space`
  - `chained_unary_minus_three_with_space`
  - `minus_minus_value_is_comment`
  - `having_count_star`
  - `not_like_true`, `not_like_false`
  - `not_in_list`, `not_in_subquery`
  - `abs_negative`, `abs_positive`, `abs_null`
  - `hex_string`, `hex_integer`
  - `round_no_places`, `round_with_places`
- `docs/development/REQUIREMENTS.md` — REQ000378-382 moved
  from TBD to DONE; REQ000383 stays in TBD for v0.26.6.
- `docs/development/ROADMAP.md` — new `v0.26.5` row.
- `docs/development/iterations/iter-26.3-bugfixes.md` — this
  file.

## Test Results

- `go test ./... -race -count=1` green (1×; full suite).
- Dual-runner: 60 passed, 0 failed, 0 skipped (was 46/0/1 in
  v0.26.4).
- `go vet ./...` zero warnings.
- `gofmt -s -l .` no new drift (pre-existing alignment issue
  in `internal/SQL/PS/ps.go` `tokenNames` table; not from this
  iteration).

## Commit Sequence

1. `7dc486e` — `fix(SQL/EX/PS): REQ000378 HAVING COUNT(*) StarExpr
   lookup; REQ000379 chained unary minus probe cases`
2. `c3554ad` — `fix(SQL/PS): REQ000380/381 parse NOT LIKE and
   NOT IN prefix combinations`
3. `c9ee07a` — `fix(SQL/EX): REQ000382 add ABS, HEX, ROUND scalar
   functions`
4. (this commit) — `docs(REQ): close v0.26.5 bugs (REQ000378-382)
   move to DONE` (REQUIREMENTS, ROADMAP, iter-26.3 doc).

## Outcome

5 REQs closed in 4 commits, ~250 LoC added (eval helpers +
parser dispatch + tests + docs). The two-week v0.26.x bug
sweep is now complete: 7 + 5 + 5 = 17 bugs fixed across
v0.26.3, v0.26.4, and v0.26.5. The next candidate pool
(v0.26.6) is REQ000383 (UNION/INTERSECT/EXECT, M-effort) plus
the start of the core function matrix (REQ000384-417).

**Final tag:** `v0.26.5`
**Final commit:** (this iteration's docs commit)
**Final LoC delta:** 12 files changed, 318 insertions(+), 26 deletions(-)
