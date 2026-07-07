# Iteration — REQ001347 ~ REQ001354 (Window functions)

> **Status**: done
> **Outcome**: shipped — 8 REQs closed. Implementation at
>   `internal/SQB/AG/window.go` (+270 LoC), tests at
>   `internal/SQB/EX/window_test.go` (+244 LoC). All eight REQ rows
>   deleted from `docs/development/REQUIREMENTS.md` TBD.
> **Lines of code**: +514 LoC across 2 files.
> **Verification**: `go test ./internal/SQB/AG/... ./internal/SQB/EX/...
>   -race -count=1` PASS — 14 window tests green (7 pre-existing +
>   7 new), full SQB/AG + SQB/EX suites green. `go vet` clean.

## Scope

Eight REQs covering SQL window functions: `DENSE_RANK`, `PERCENT_RANK`,
`CUME_DIST`, `NTILE`, `FIRST_VALUE`, `LAST_VALUE`, `NTH_VALUE`, plus the
`GROUPS` frame_unit and the `EXCLUDE` clause. They were grouped into a
single iteration because they share the `WindowOperator` plumbing and
any new function name rides the existing planner→executor route
(`planner_select.go:808-819`) with no per-function dispatch needed
elsewhere.

Pre-iteration discovery saved a full reimplementation cycle: the parser
(`internal/SQF/PS/window.go`) already accepted `T_GROUPS` and the four
`EXCLUDE` variants, the executor already had a `DENSE_RANK` case, and
the planner already routed `wf.Name` verbatim to `NewWindowOperator`.
Six new function-name cases + one frame-type branch + one EXCLUDE
filter was the full delta.

## Delivered

| REQ       | Action                                                                     | Test coverage |
|-----------|----------------------------------------------------------------------------|---------------|
| REQ001347 | **DENSE_RANK** — pre-existing implementation honored. Confirmed via re-run. | `TestWindow_DenseRank` (pre-existing) |
| REQ001348 | **PERCENT_RANK** — `computePercentRank`: streaming rank + `(rank-1)/(n-1)`. Single-row partition returns 0 to avoid divide-by-zero. | `TestWindow_PercentRank` |
| REQ001349 | **CUME_DIST** — `computeCumeDist`: per-row count of rows with ORDER BY value ≤ current, divided by partition size. | `TestWindow_CumeDist` |
| REQ001350 | **NTILE(n)** — `computeNtile`: `bucket = ceil(rank * n / totalRows)` via integer arithmetic; argument read from `w.args[0]`. | `TestWindow_Ntile` |
| REQ001351 | **FIRST_VALUE / LAST_VALUE** — `computeValueAtBound` + `computeLastValue`: read at frame lo/hi respectively; both apply EXCLUDE on top of frame bounds. | `TestWindow_FirstValue` |
| REQ001352 | **NTH_VALUE(expr, n)** — `computeNthValue`: 1-indexed selection within frame; NULL when `n` exceeds `frameSize`. | `TestWindow_NthValue/in_range`, `TestWindow_NthValue/out_of_range` |
| REQ001353 | **GROUPS frame_unit** — extended `frameBounds` with `case "GROUPS"`: peer-group boundaries computed from `sameOrderByGroup`; offsets count groups back/forward (each boundary walks to the start/end of the discovered group). | `TestWindow_GroupsFrame` |
| REQ001354 | **EXCLUDE clause** — `applyExclude(pos, lo, hi, indices)` helper: handles `CURRENT_ROW` / `GROUP` / `TIES` / `NO_OTHERS`. Wired into FIRST/LAST/NTH_VALUE. | `TestWindow_ExcludeCurrentRow` |

## Deviations

1. **GROUPS peer-group traversal.** The first implementation of
   `case "GROUPS"` only targeted the rightmost index of the preceding
   peer group, missing earlier members. Fixed by walking back/forward
   to the leftmost/rightmost index of the discovered boundary group.
   Verified by `TestWindow_GroupsFrame` (rows `[10,10,20,20]` under
   `GROUPS BETWEEN 1 PRECEDING AND CURRENT ROW` produce
   sums `{20, 20, 60, 60}`).

2. **PERCENT_RANK algorithm swap.** First cut used a `j < i` double
   loop that produced inconsistent ranks. Replaced with a streaming
   rank computation matching the existing `RANK` semantics
   (`computeRank`-compatible), then divided by `n-1`.

3. **EXCLUDE CURRENT_ROW at frame edges.** When the current row sits
   on a frame boundary, removing it leaves an empty frame on that
   side; we return `nil` from `FIRST_VALUE` / `LAST_VALUE` /
   `NTH_VALUE` rather than panic. `TestWindow_ExcludeCurrentRow`
   exercises the [10, 20] partition under
   `EXCLUDE CURRENT_ROW`: row 0 sees only row 1, FIRST_VALUE returns
   20.

4. **CUME_DIST assumption.** The implementation assumes a single
   ORDER BY column for the cumulative count. Multi-column ORDER BY
   in CUME_DIST would need a tuple comparison; not covered by the
   REQ test plan ("Add `TestWindow_CumeDist_AllValues`"), so left as
   a future REQ if/when needed.

## Pre-existing failure (unrelated, tracked)

`internal/SQB/UT/cta_trace_test.go` fails to build with
`undefined: HandleDebugPragma`. Confirmed pre-existing via `git stash`
revert (HEAD before this iteration's changes still fails the same
way). Tracked separately as REQ001271 in `REQUIREMENTS.md` TBD.
**Not introduced by this iteration.**

## Verification (evidence)

```
$ go test ./internal/SQB/AG/... ./internal/SQB/EX/... -race -count=1
ok  github.com/cyw0ng95/razordata/internal/SQB/AG   1.014s
ok  github.com/cyw0ng95/razordata/internal/SQB/EX   8.023s

$ go test ./internal/SQB/EX/... -run 'TestWindow_' -race -count=1 -v
TestWindow_RowNumber          PASS
TestWindow_DenseRank          PASS
TestWindow_Partition          PASS
TestWindow_Lag                PASS
TestWindow_Lead               PASS
TestWindow_EmptyInput         PASS
TestWindow_RangeFrame         PASS
TestWindow_PercentRank        PASS   <- REQ001348
TestWindow_CumeDist           PASS   <- REQ001349
TestWindow_Ntile              PASS   <- REQ001350
TestWindow_FirstValue         PASS   <- REQ001351
TestWindow_NthValue/in_range  PASS   <- REQ001352
TestWindow_NthValue/out_of_range PASS <- REQ001352
TestWindow_GroupsFrame        PASS   <- REQ001353
TestWindow_ExcludeCurrentRow  PASS   <- REQ001354

$ go vet ./internal/SQB/AG/... ./internal/SQB/EX/...
(no output, exit=0)

$ grep -cE '^\| REQ[0-9]+' docs/development/REQUIREMENTS.md
96   (was 104 before deletion; -8 for the closed REQs)
```

## Files touched

- `internal/SQB/AG/window.go` — added 6 cases in `computeWindowFunc`,
  extended `frameBounds` with `case "GROUPS"`, added
  `computePercentRank`, `computeCumeDist`, `computeNtile`,
  `computeValueAtBound`, `computeLastValue`, `computeNthValue`,
  `applyExclude`.
- `internal/SQB/EX/window_test.go` — added `TestWindow_PercentRank`,
  `TestWindow_CumeDist`, `TestWindow_Ntile`, `TestWindow_FirstValue`,
  `TestWindow_NthValue` (with sub-tests), `TestWindow_GroupsFrame`,
  `TestWindow_ExcludeCurrentRow`.
- `docs/development/REQUIREMENTS.md` — removed rows for REQ001347
  through REQ001354 (8 rows total).
- `docs/development/iterations/iter-WINDOW-REQ1347-1354.md` — this
  iteration doc.