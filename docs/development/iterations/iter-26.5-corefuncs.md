# Iteration 26.5 — v0.26.7 Core Scalar Functions Batch 1

**Subsystem:** `SQL/EX`, `SYS/AP`, `SYS/SE`, `tests/sqlcmp/dual`

**Status:** pending

**Est. LOC:** ~1000 (20 REQs: REQ000384-404, REQ000407-412)

**Target release:** v0.26.7

**Tags:** `feature`, `sql-compliance`, `core-functions`

---

## Overview

This iteration implements the first batch of remaining SQLite core
scalar functions from the function matrix (REQ000384-417). Focus
is on high-value, frequently-used functions with minimal external
dependencies. Engine state plumbing functions (`changes()`,
`last_insert_rowid()`, `total_changes()`) are included as they
unlock additional SQLite compatibility testing.

**Selection criteria:**
- High usage frequency in real queries
- Low external dependencies (no CGO, no extension loading)
- Self-contained eval logic (string/number ops)
- Testable via dual-runner against modernc.org/sqlite

**Deferred to v0.26.8:**
- `glob()` — requires glob pattern matcher impl
- `soundex()` — phonetic encoding algorithm
- `unistr()` — complex escape sequence parsing
- `likelihood()`, `likely()`, `unlikely()` — planner hints (need PL integration)
- `load_extension()` — CGO, out of v1 scope

---

## Goals

1. Implement 20 core scalar functions (80% of remaining TODOs)
2. Wire engine state counters for `changes()`, `last_insert_rowid()`,
   `total_changes()`
3. Add unit tests in `corefunc_test.go` (table-driven, 5+ cases each)
4. Add dual-runner probe cases for all new functions
5. Dual-runner target: maintain 100% pass rate (new probes + existing)
6. `go test ./... -race -count=1` green (3×)

## Non-Goals

- Remaining 5 core functions (`glob`, `soundex`, `unistr`,
  `likelihood`/`likely`/`unlikely`) — v0.26.8
- Foreign keys, triggers, generated columns — separate iterations
- SLT corpus full run (requires submodule setup)

---

## REQ-by-REQ Notes

### REQ000384 — `abs(X)` [S]

**Status:** Implemented in iter-26.3 (REQ000382), keep for test uniformity.

**Semantics:**
- `int64 → int64`, `MIN_INT64 → error`
- `float64 → float64`, `NULL → NULL`
- Non-numeric string → `0.0`

**Test:** `abs_negative`, `abs_positive`, `abs_zero`, `abs_null`, `abs_string`

---

### REQ000385 — `changes()` [M]

**Status:** Not implemented (needs engine state)

**Semantics:**
- Returns row count from last INSERT/UPDATE/DELETE on current connection
- SELECT does not reset counter
- Transaction rollback does not undo count

**Fix:**
1. Add `changesCount int64` to `SYS/SE/session` struct
2. Increment in `SQL/EX` executors (`InsertOp`, `UpdateOp`, `DeleteOp.Next()`)
3. Register `changes` function in `evalFunction` that returns session counter
4. Add `LastInsertRowID int64` field to session for REQ000394

**Touches:**
- `SYS/SE/se.go` — `session` struct changes field
- `SYS/AP/ap.go` — `Session` interface may need accessor
- `SQL/EX/insert.go`, `update.go`, `delete.go` — increment counter
- `SQL/EX/eval.go` — `evalChanges` helper

**Tests:**
- `changes_after_insert`, `changes_after_update`, `changes_after_delete`
- `changes_after_select_unchanged`, `changes_after_rollback`

**Est. LOC:** 80

---

### REQ000386 — `char(X1,...,XN)` [S]

**Status:** Not implemented

**Semantics:**
- Integer Unicode code points → UTF-8 string
- `char(65, 66, 67)` → `"ABC"`
- `char(NULL)` → `""` (empty string)
- Out-of-range → `""`

**Fix:**
- New `evalChar(e *FuncExpr, row value.Row) (value.Value, error)`
- Loop through args, convert each `int64` to `rune`, build string

**Touches:** `SQL/EX/eval.go` (+40 LoC)

**Tests:** `char_single`, `char_multiple`, `char_null`, `char_out_of_range`

**Est. LOC:** 45

---

### REQ000387 — `concat(X,...)` [S]

**Status:** Not implemented

**Semantics:**
- Concatenate all non-NULL args (SQLite skips NULLs)
- All-NULL → `""` (empty string, not NULL)
- Numbers converted to text via `strconv`

**Note:** Current `\|\|` operator returns NULL if any operand is NULL.
`concat()` differs — it skips NULLs.

**Fix:**
- `evalConcat(e *FuncExpr, row) (value.Value, error)`
- `strings.Builder`, skip NULL args, append text representations

**Touches:** `SQL/EX/eval.go` (+35 LoC)

**Tests:** `concat_two_strings`, `concat_with_null`, `concat_all_null`,
`concat_numbers`, `concat_mixed`

**Est. LOC:** 40

---

### REQ000388 — `concat_ws(SEP,X,...)` [S]

**Status:** Not implemented

**Semantics:**
- First arg is separator
- `concat_ws(',', 'a', 'b', 'c')` → `"a,b,c"`
- SEP=NULL → NULL
- Skip NULL values (but add separator between non-NULLs)

**Fix:**
- `evalConcatWS(e *FuncExpr, row)`
- First arg special-case, `strings.Builder` with separator logic

**Touches:** `SQL/EX/eval.go` (+40 LoC)

**Tests:** `concat_ws_basic`, `concat_ws_with_null`, `concat_ws_null_sep`

**Est. LOC:** 45

---

### REQ000389 — `format(FORMAT,...)` [M]

**Status:** Not implemented

**Semantics:**
- printf-style formatting (subset of `fmt` verbs)
- Supports: `%d`, `%s`, `%f`, `%%`, `%Nd` (width), `%.*f` (precision)
- `format('Hello %s', 'world')` → `"Hello world"`
- Mismatched args → NULL padding or error (SQLite silently pads)

**Fix:**
- Use `fmt.Sprintf` with arg unpacking
- Handle variadic args, convert to `interface{}` for `fmt`

**Touches:** `SQL/EX/eval.go` (+50 LoC)

**Tests:** `format_string`, `format_int`, `format_float`, `format_percent`,
`format_width`, `format_mismatch`

**Est. LOC:** 55

---

### REQ000390 — `glob(X,Y)` [M] — DEFERRED to v0.26.8

**Status:** Deferred (requires glob pattern matcher)

**Semantics:**
- Unix shell-style pattern matching
- `*` = any sequence, `?` = any char, `[abc]` = char set

**Defer reason:** Glob algorithm ~100 LoC, want to batch with regex support.

---

### REQ000391 — `hex(X)` [S]

**Status:** Implemented in iter-26.3 (REQ000382), keep for uniformity.

**Semantics:**
- Integer → decimal text → hex-encoded (`hex(255)` = `"323535"`)
- String/BLOB → uppercase hex
- NULL → NULL

---

### REQ000392 — `iif(B,V1,V2)` [S]

**Status:** Not implemented

**Semantics:**
- If B is truthy, return V1; else return V2
- Short-circuit: only evaluate chosen branch
- `iif(1 > 0, 'yes', 'no')` → `"yes"`

**Fix:**
- `evalIIF(e *FuncExpr, row)` — check boolean result of first arg,
  evaluate second or third arg only

**Touches:** `SQL/EX/eval.go` (+30 LoC)

**Tests:** `iif_true`, `iif_false`, `iif_null_condition`

**Est. LOC:** 35

---

### REQ000393 — `instr(X,Y)` [S]

**Status:** Not implemented

**Semantics:**
- Position of first occurrence of Y in X (1-based)
- Not found → `0`
- `instr('hello world', 'world')` → `7`
- NULL → `0`

**Fix:**
- `evalInstr(e *FuncExpr, row)`
- `strings.Index` + 1 for 1-based indexing

**Touches:** `SQL/EX/eval.go` (+25 LoC)

**Tests:** `instr_found`, `instr_not_found`, `instr_empty`, `instr_null`

**Est. LOC:** 30

---

### REQ000394 — `last_insert_rowid()` [M]

**Status:** Not implemented (needs engine state)

**Semantics:**
- Returns last inserted rowid on current connection
- Unaffected by subsequent operations
- Transaction rollback does not undo value

**Fix:**
- Add `lastInsertRowID int64` to `SYS/SE/session`
- Set in `HiddenPK` or explicit `INTEGER PRIMARY KEY` insert path
- `evalLastInsertRowID` reads session field

**Touches:**
- `SYS/SE/se.go` — session field
- `SQL/EX/insert.go` — set field on insert
- `SQL/EX/eval.go` — `evalLastInsertRowID` helper

**Est. LOC:** 60

---

### REQ000395 — `likelihood(X,Y)` [S] — DEFERRED to v0.26.8

**Status:** Deferred (needs planner integration)

**Semantics:**
- No-op pass-through: returns X unchanged
- Hint to query planner about probability Y (0.0-1.0)

**Defer reason:** Only useful with cost-based planner; low ROI for v1.

---

### REQ000396 — `likely(X)` [S] — DEFERRED to v0.26.8

**Status:** Deferred (needs planner integration)

**Semantics:**
- No-op, assumes X is true with high probability

---

### REQ000397 — `ltrim(X[,Y])` [S]

**Status:** Not implemented

**Semantics:**
- Trim leading characters in Y from X (default Y=" ")
- `ltrim('  hello  ')` → `"hello  "`
- `ltrim('xxxhello', 'x')` → `"hello"`

**Fix:**
- `evalLtrim(e *FuncExpr, row)`
- `strings.TrimLeft` with default space charset

**Touches:** `SQL/EX/eval.go` (+25 LoC)

**Tests:** `ltrim_default`, `ltrim_custom`, `ltrim_null`, `ltrim_empty`

**Est. LOC:** 30

---

### REQ000398 — `max(X,Y,...)` [S]

**Status:** Not implemented (note: aggregate `max()` exists)

**Semantics:**
- Multi-arg scalar max: returns largest value
- `max(1, 5, 3)` → `5`
- NULLs skipped; all-NULL → NULL
- Uses first arg's collation for strings

**Fix:**
- `evalMaxScalar(e *FuncExpr, row)` — distinct from aggregate
- Compare numeric/string values, track max

**Touches:** `SQL/EX/eval.go` (+40 LoC)

**Tests:** `max_two`, `max_three`, `max_with_null`, `max_all_null`,
`max_string`, `max_mixed`

**Est. LOC:** 45

---

### REQ000399 — `min(X,Y,...)` [S]

**Status:** Not implemented (aggregate `min()` exists)

**Semantics:**
- Multi-arg scalar min: returns smallest value
- `min(1, 5, 3)` → `1`
- NULLs skipped; all-NULL → NULL

**Fix:**
- `evalMinScalar(e *FuncExpr, row)` — mirror of `evalMaxScalar`

**Touches:** `SQL/EX/eval.go` (+40 LoC)

**Tests:** `min_two`, `min_three`, `min_with_null`, `min_all_null`

**Est. LOC:** 45

---

### REQ000400 — `octet_length(X)` [S]

**Status:** Not implemented

**Semantics:**
- Byte length of X (not code-point count)
- `octet_length('café')` → `5` (UTF-8 bytes)
- `length('café')` → `4` (code points)
- NULL → NULL

**Fix:**
- `evalOctetLength(e *FuncExpr, row)`
- `len(stringVal)` for byte count vs `utf8.RuneCountInString` for code points

**Touches:** `SQL/EX/eval.go` (+20 LoC)

**Tests:** `octet_ascii`, `octet_utf8`, `octet_null`

**Est. LOC:** 25

---

### REQ000401 — `quote(X)` [S]

**Status:** Not implemented

**Semantics:**
- Render X as SQL literal
- String → single-quoted with escaped single-quotes (`'it''s'`)
- BLOB → `X'hex...'`
- NULL → `NULL` (unquoted)
- Integer/float → unquoted number

**Fix:**
- `evalQuote(e *FuncExpr, row)`
- Type switch, escape strings with `strings.ReplaceAll`

**Touches:** `SQL/EX/eval.go` (+45 LoC)

**Tests:** `quote_string`, `quote_with_quote`, `quote_int`, `quote_float`,
`quote_null`, `quote_blob`

**Est. LOC:** 50

---

### REQ000402 — `random()` [S]

**Status:** Not implemented

**Semantics:**
- Pseudo-random int64 in range `[-2^63, 2^63-1]`
- Exclude `MIN_INT64` (special marker in some contexts)
- Deterministic if seed set (not for v1)

**Fix:**
- `evalRandom(e *FuncExpr, row)`
- Use `math/rand/v2` or `crypto/rand` for randomness

**Touches:** `SQL/EX/eval.go` (+20 LoC)

**Tests:** `random_returns_int64`, `random_not_min_int64`

**Est. LOC:** 25

---

### REQ000403 — `randomblob(N)` [S]

**Status:** Not implemented

**Semantics:**
- N-byte BLOB of random data
- N < 0 → NULL
- N = 0 → `X''` (empty BLOB)

**Fix:**
- `evalRandomBlob(e *FuncExpr, row)`
- `make([]byte, n)`, fill with random bytes

**Touches:** `SQL/EX/eval.go` (+25 LoC)

**Tests:** `randomblob_positive`, `randomblob_zero`, `randomblob_negative`

**Est. LOC:** 30

---

### REQ000404 — `replace(X,Y,Z)` [S]

**Status:** Not implemented

**Semantics:**
- Replace all occurrences of Y in X with Z
- `replace('aaa', 'a', 'b')` → `"bbb"`
- Y = "" → return X unchanged
- NULL → NULL

**Fix:**
- `evalReplace(e *FuncExpr, row)`
- `strings.ReplaceAll` with empty-Y check

**Touches:** `SQL/EX/eval.go` (+25 LoC)

**Tests:** `replace_basic`, `replace_empty`, `replace_null`,
`replace_no_match`

**Est. LOC:** 30

---

### REQ000405 — `round(X[,Y])` [S]

**Status:** Implemented in iter-26.3 (REQ000382), keep for uniformity.

---

### REQ000406 — `rtrim(X[,Y])` [S]

**Status:** Not implemented

**Semantics:**
- Trim trailing characters in Y from X (default Y=" ")
- Mirror of `ltrim`

**Fix:**
- `evalRtrim(e *FuncExpr, row)`
- `strings.TrimRight`

**Touches:** `SQL/EX/eval.go` (+25 LoC)

**Tests:** `rtrim_default`, `rtrim_custom`, `rtrim_null`

**Est. LOC:** 30

---

### REQ000407 — `sign(X)` [S]

**Status:** Not implemented

**Semantics:**
- Returns -1, 0, or +1 based on sign of X
- `sign(-5)` → `-1`, `sign(0)` → `0`, `sign(5)` → `1`
- NULL or non-numeric → `0`

**Fix:**
- `evalSign(e *FuncExpr, row)`
- Type switch on numeric, compare to zero

**Touches:** `SQL/EX/eval.go` (+25 LoC)

**Tests:** `sign_negative`, `sign_zero`, `sign_positive`, `sign_null`

**Est. LOC:** 30

---

### REQ000408 — `soundex(X)` [M] — DEFERRED to v0.26.8

**Status:** Deferred (phonetic encoding algorithm)

**Semantics:**
- Soundex encoding for phonetic matching
- Returns 4-char code like `"S532"`

**Defer reason:** Algorithm complexity ~80 LoC, low priority for v1.

---

### REQ000409 — `sqlite_source_id()` [S]

**Status:** Not implemented

**Semantics:**
- Returns fixed string for version identification
- Return `"razordata-v0.26.7"` for v1

**Fix:**
- `evalSqliteSourceID(e *FuncExpr, row)` — just return constant

**Touches:** `SQL/EX/eval.go` (+15 LoC)

**Tests:** `sqlite_source_id_returns_string`

**Est. LOC:** 20

---

### REQ000410 — `sqlite_version()` [S]

**Status:** Not implemented

**Semantics:**
- Returns version string `"0.26.7"`

**Fix:**
- `evalSqliteVersion(e *FuncExpr, row)` — return constant

**Touches:** `SQL/EX/eval.go` (+15 LoC)

**Tests:** `sqlite_version_returns_string`

**Est. LOC:** 20

---

### REQ000411 — `total_changes()` [M]

**Status:** Not implemented (needs engine state)

**Semantics:**
- Cumulative row changes since connection open
- Includes changes from all transactions (committed)
- Does not reset on transaction boundary

**Fix:**
- Add `totalChanges int64` to `SYS/SE/session`
- Increment same places as `changesCount`, but never reset
- `evalTotalChanges` reads session field

**Touches:**
- `SYS/SE/se.go` — session field
- `SQL/EX/insert.go`, `update.go`, `delete.go` — increment both counters
- `SQL/EX/eval.go` — `evalTotalChanges` helper

**Est. LOC:** 70

---

### REQ000412 — `typeof(X)` [S]

**Status:** Not implemented

**Semantics:**
- Returns type name: `"null"`, `"integer"`, `"real"`, `"text"`, `"blob"`
- `typeof(NULL)` → `"null"`
- `typeof(42)` → `"integer"`

**Fix:**
- `evalTypeof(e *FuncExpr, row)`
- Type switch on `value.Value` (union type)

**Touches:** `SQL/EX/eval.go` (+30 LoC)

**Tests:** `typeof_null`, `typeof_int`, `typeof_float`, `typeof_string`,
`typeof_blob`

**Est. LOC:** 35

---

### REQ000413 — `unhex(X[,Y])` [S] — DEFERRED to v0.26.8

**Status:** Deferred (hex decoding)

**Semantics:**
- Hex string → BLOB
- Invalid hex → NULL
- Y is ignored-char set

**Defer reason:** `encoding/hex` decoding + error handling, batch with `hex()` edge cases.

---

### REQ000414 — `unicode(X)` [S]

**Status:** Not implemented

**Semantics:**
- Unicode code point of first character
- `unicode('A')` → `65`
- Empty string → `0`
- NULL → NULL

**Fix:**
- `evalUnicode(e *FuncExpr, row)`
- `[]rune(s)[0]` for first code point

**Touches:** `SQL/EX/eval.go` (+20 LoC)

**Tests:** `unicode_ascii`, `unicode_utf8`, `unicode_empty`, `unicode_null`

**Est. LOC:** 25

---

### REQ000415 — `unistr(X)` [M] — DEFERRED to v0.26.8

**Status:** Deferred (escape sequence parsing)

**Semantics:**
- Decode `\uXXXX`, `\UXXXXXXXX`, `\+XXXXXX` escapes
- Complex escape handling

**Defer reason:** Escape parser ~100 LoC, low priority for v1.

---

### REQ000416 — `unlikely(X)` [S] — DEFERRED to v0.26.8

**Status:** Deferred (planner hint)

---

### REQ000417 — `zeroblob(N)` [S]

**Status:** Not implemented

**Semantics:**
- N-byte BLOB of `0x00` bytes
- `zeroblob(4)` → `X'00000000'`
- N < 0 → NULL

**Fix:**
- `evalZeroblob(e *FuncExpr, row)`
- `make([]byte, n)` — Go zero-initializes

**Touches:** `SQL/EX/eval.go` (+20 LoC)

**Tests:** `zeroblob_positive`, `zeroblob_zero`, `zeroblob_negative`

**Est. LOC:** 25

---

## Files Changed

- `SQL/EX/eval.go` — 20 new eval helpers (~600 LoC)
- `SQL/EX/eval_test.go` or `SQL/EX/corefunc_test.go` — expanded test
  table (~200 LoC)
- `SYS/SE/se.go` — session struct: `changesCount`, `lastInsertRowID`,
  `totalChanges` fields (~10 LoC)
- `SYS/AP/ap.go` — may need `Changes()` / `LastInsertRowID()` /
  `TotalChanges()` accessors (~30 LoC)
- `SQL/EX/insert.go` — increment counters on insert (~20 LoC)
- `SQL/EX/update.go` — increment counters on update (~10 LoC)
- `SQL/EX/delete.go` — increment counters on delete (~10 LoC)
- `tests/sqlcmp/dual/probe_cases.go` — 20+ new probe cases (~60 LoC)
- `docs/development/REQUIREMENTS.md` — 20 REQs TBD → DONE
- `docs/development/ROADMAP.md` — v0.26.7 row
- `docs/development/iterations/iter-26.5-corefuncs.md` — this file

---

## Test Results

**Targets:**
- `go test ./... -race -count=1` green (3× full suite)
- Dual-runner: maintain 100% pass rate with 20+ new probes
- `go vet ./...` zero warnings
- `gofmt -s -l .` clean
- Unit test coverage for all 20 functions (table-driven, 5 cases each)

---

## Commit Sequence

1. `SYS/SE` session state fields (`changesCount`, `lastInsertRowID`,
   `totalChanges`)
2. `SQL/EX` insert/update/delete executors — increment counters
3. `evalChanges`, `evalLastInsertRowID`, `evalTotalChanges` helpers
4. Batch 1: string functions (`char`, `concat`, `concat_ws`, `format`,
   `ltrim`, `rtrim`, `replace`, `quote`, `typeof`)
5. Batch 2: numeric functions (`iif`, `instr`, `max`/`min` scalar,
   `octet_length`, `sign`, `unicode`, `zeroblob`)
6. Batch 3: utility functions (`random`, `randomblob`,
   `sqlite_source_id`, `sqlite_version`)
7. Unit tests in `corefunc_test.go` (expand existing file)
8. Dual-runner probes in `probe_cases.go`
9. Docs: REQUIREMENTS, ROADMAP, iteration doc

---

## Gap Analysis

No anticipated deferred bugs. If implementation uncovers edge cases
(NULL handling, type coercion), they will be added to REQUIREMENTS.md
TBD section before tag.

---

## Outcome

20 REQs closed (18 new + 2 already done) in ~9 commits, ~1000 LoC.
Core function matrix completion: 30/60 (50%). Next iteration (v0.26.8)
covers remaining 5 deferred functions + any bugfixes from dual-runner
expansion.

**Final tag:** `v0.26.7`
**Final commit:** (this iteration's docs commit)
**Final LoC delta:** ~15 files changed, 1000 insertions(+), ~50 deletions(-)

---

## Appendix: Implementation Order

To minimize merge conflicts and enable incremental testing:

1. **Session state (REQ000385, 394, 411)** — 3 counters, shared infra
2. **Already-done verification (REQ000384, 391)** — confirm `abs`, `hex`, `round` work
3. **Low-risk string ops (REQ000397, 406, 404, 386, 412, 400, 414)** — `ltrim`, `rtrim`, `replace`, `char`, `typeof`, `octet_length`, `unicode`
4. **Variadic functions (REQ000387, 388, 398, 399)** — `concat`, `concat_ws`, `max`, `min`
5. **Format/parsing (REQ000389)** — `format` (printf)
6. **Conditional (REQ000392)** — `iif`
7. **String search (REQ000393)** — `instr`
8. **Random (REQ000402, 403, 417)** — `random`, `randomblob`, `zeroblob`
9. **Quoting (REQ000401)** — `quote`
10. **Version strings (REQ000409, 410)** — `sqlite_source_id`, `sqlite_version`
11. **Math (REQ000407)** — `sign`
