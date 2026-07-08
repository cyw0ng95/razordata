# REQ001410 — select4 L39784 NLJ Join Bug: Fix Status

## Summary

The 8-table join in `select4.test` L39784 returns **14 rows** instead of the expected **21 rows**. The bug is **table-order-dependent**: some FROM clause orderings produce correct results (21 rows), others produce wrong results (14 or 0 rows).

## Status (2026-07-08 08:00 UTC)

**Partially resolved** — Row count fixed (14→21). Hash mismatch is pre-existing (not caused by budget fix).

## What was applied

### Fix 1 (REQ001414): Replace crossTableConjuncts with crossTablePredicates
**File:** `internal/SQB/EX/planner_select.go` (net -4 lines)
**Status:** Applied, no regression, but does NOT fix the 14/21 bug.

Removed the `crossTableConjuncts` variable (which held ALL WHERE conjuncts) and changed `localConjuncts`, `groupBushyJoins`, and `planSelectJoins` to use `crossTablePredicates` (only cross-table predicates from `splitPredicatesByTable`). Transitive equality inference moved from `crossTableConjuncts` to `crossTablePredicates` with a `len(tables) > 1` guard.

**Verified:**
- All `internal/SQB/EX` tests pass (no regression)
- `TestREQ001414_BushyGroup_SingleTablePredLeak` passes
- `TestSelect4_Join277_HashMismatch` still fails (14 vs 21 rows) — same hash `b752b9c6989de2d8c5999f7b2787af7f`

**Why this did NOT fix the bug:** The `localConjuncts` filter at line 1030 (`allInGroup`) already excludes cross-table predicates that reference tables outside the group. Single-table predicates that pass the filter are stored in `gr.preds` but never used as NLJ ON clauses — the merge-phase NLJ passes `nil` for the ON clause (line 1296), and the group-internal NLJ ON comes from `j.On` (line 1208), not from `localConjuncts`. The single-table predicates in `localConjuncts` are discarded by the merge phase.

### Fix 2: HashJoin budget check — use actual row count instead of next capacity
**File:** `internal/SQB/OP/hashjoin.go` (+12 lines, -7 lines)
**Status:** Applied. Row count fixed (14→21). Hash mismatch is PRE-EXISTING.

The HashJoin left materialization budget check used `nextCap * estBytesPerRow > effectiveBudget/2` where `nextCap = cap * 2` when the slice was full. This stopped materialization prematurely — e.g. at 67045 rows instead of 85248 for select4 L39784 — because Go's slice doubling strategy caused the next capacity to exceed the budget threshold.

**Fix:** Changed to `len(j.leftRows)+1 * estBytesPerRow > effectiveBudget`, which tests actual row count against actual memory usage.

**Verified:**
- `TestSelect4_Join277_HashMismatch` now returns 21 rows (correct count) instead of 14
- Hash changed from `b752b9c6989de2d8c5999f7b2787af7f` to `3a3415d738ac1c62f2a07eb5e92eef2f`
- Expected hash: `34325f84dd0efa600c0be4e8e0770bc3` — still different

**Key finding:** The hash mismatch is PRE-EXISTING. The old output (14 rows, hash `b752b9c6989de2d8c5999f7b2787af7f`) and new output (21 rows, hash `3a3415d738ac1c62f2a07eb5e92eef2f`) are BOTH different from expected. The budget fix only corrected the row count (14→21), not the values. The value issue existed before the budget fix and is a separate bug.

**Debug tracing (per-operator):** Added `WithDebugID` to HashJoin and NestedLoopJoin, wired into planner. Confirmed:
- `g1-t8t6` HashJoin correctly produces 3 matches: b4/d6 = 924, 901, 469
- All 3 matches have correct key values
- The value issue is in column expressions or g0/g2 values, not in join key matching

The fix is still valuable: it removes a latent risk where single-table predicates could leak into join planning if the `allInGroup` filter were ever relaxed.

## Investigation Log

### Phase 1: Predicate pushdown analysis
- Confirmed predicate pushdown works correctly: `a9 in (6)` pushed to t9's scan
- Confirmed `crossTableConjuncts` filtering reduces from 8 to 3 predicates (architecturally correct)
- **Result:** Predicate pushdown is NOT the root cause. Still 14 rows.

### Phase 2: HashJoin rewindability attempt (REVERTED)
- Modified `hashjoin.go` Close() to preserve leftRows/buckets
- Added `rebuildFromCache()` to rebuild hash table from cached data
- Added deep-copy of Cols/Types when storing rows
- **Result:** `rebuildFromCache()` is NEVER triggered. NLJ uses block mode, not advanceRight. **REVERTED.**

### Phase 3: Block mode investigation
- Discovered NLJ uses block mode path (join.go:874-948), not standard advanceRight
- Added leftMaterialized logging to HashJoin
- **Key finding:** HashJoin.id=1 has leftMaterialized=12099 from a NestedLoopJoin child
- The NLJ should produce 85248 rows (666 × 128), not 12099
- **This is the critical clue** — the NLJ's block mode right-side materialization is producing fewer rows than expected

### Phase 4: Bushy grouping investigation
- Confirmed bug is **table-order-dependent**: `t3,t4,t9,t1,t8,t6,t5,t7` → 21 rows; `t3,t7,t4,t9,t5,t1,t8,t6` → 14 rows
- `groupBushyJoins()` produces different groups per ordering:
  - Correct: {t3,t4,t6}, {t9,t1,t8}, {t5,t7}
  - Broken: {t3,t7}, {t4,t9,t1,t8,t6}, {t5}
- Confirmed 3 tables (t3, t7, t5) have ZERO equi-join edges
- `isConnectedGraph` correctly returns false; `groupBushyJoins` splits correctly
- Attempted single-group force (REJECTED: 14→2 rows)
- crossTableConjuncts filter applied (8→3 predicates, groups have 0 localConjuncts)

### Phase 5: Data aliasing theory — DEBUNKED
- Traced join.go:812 `inner.Data = row.Data` — initially identified as root cause
- Deep trace revealed: `sync.Pool` in `getRowData()` (rowpool.go:13) has ZERO `Put()` callers → `Get()` always returns nil → fresh `make([]Value, n)` each time
- Each `Next()` call returns a row with a **distinct** Data backing array
- `cachedRightRows` shallow copy is safe at the Data level
- **This is a dead end — root cause is elsewhere**

### Phase 6: Predicate flow trace
- Traced `localConjuncts` construction in planner_select.go lines 1050-1214
- For group 1 `{t4,t9,t1,t8,t6}`: three cross-table predicates (b4=d6, e8=c9, a1=d8)
- `extractEquiJoinKeys` consumes ALL 3 as HashJoin keys → `gr.preds` empty
- Merge phase creates cross-join NLJs (no ON clause) — correct
- **Row loss happens inside group 1's internal join tree**: produces 2 rows instead of expected 3

### Phase 7: REQ001414 Option C refactor
- Applied architectural refactor: crossTableConjuncts removed, crossTablePredicates used everywhere
- All EX tests pass, no regression
- **14/21 bug NOT fixed by this change** — hash unchanged (`b752b9c6989de2d8c5999f7b2787af7f`)
- Conclusion: planner-level predicate routing is NOT the root cause

### Phase 8: Ruled-out hypotheses (REQ001414 verification)
- `groupBushyJoins` only uses equi-join BinaryExprs (T_EQ); IN-list predicates are filtered out by `bin.Op != LX.T_EQ` check. Changing data source from crossTableConjuncts to crossTablePredicates does not change bushy grouping output.
- `localConjuncts` `allInGroup` filter excludes cross-table predicates referencing tables outside the group.
- Single-table predicates in `localConjuncts` are stored in `gr.preds` but discarded by merge phase (`_ = remaining` at planner_select.go:1280).
- Merge-phase NLJ passes `nil` for ON clause (planner_select.go:1296).
- Group-internal NLJ ON comes from `j.On` (planner_select.go:1208), not from `localConjuncts`.
- **The bug is in the operator-level join tree construction, not the planner.**

### Phase 9: Per-operator debug tracing
- Added `WithDebugID` to HashJoin and NestedLoopJoin, wired into planner.
- Added per-operator counters: `rightBuilt`, `matchCount` (HashJoin); `emitCount` (NLJ).
- Added per-batch NLJ print: `[NLJ id] batch: left=N right=M matches=K totalEmitted=T`.
- Added final HashJoin print: `[HJ id] done: left=N right=M emitted=K`.
- Added key-value probe print for first few rows: `[HJ id] probe left[i] vs right[k]: lk=[..] rk=[..] match=true/false`.

### Phase 10: Per-operator debug tracing
- Added `WithDebugID` to HashJoin and NestedLoopJoin, wired into planner.
- Added per-operator counters: `rightBuilt`, `matchCount` (HashJoin); `emitCount` (NLJ).
- Added per-batch NLJ print: `[NLJ id] batch: left=N right=M matches=K totalEmitted=T`.
- Added final HashJoin print: `[HJ id] done: left=N right=M emitted=K`.
- Added key-value probe print for first few rows.

### Phase 11: Findings from real corpus (select4 L39784)
- **g1-t9t1** (NLJ cross-join): produces 85248 rows in 3 batches (32768 + 32767 + 19712). Correct.
- **g1-t1t8** (HashJoin on e8=c9, a1=d8): left=85248, right=109, emitted=111. Correct.
- **g1-t8t6** (HashJoin on b4=d6): left=111, right=9, emitted=3. Matches: b4/d6 = 924, 901, 469. Correct.
- **merge-cross-t7t6** (NLJ): left=7, right=3, matches=21. Correct.
- **merge-cross-t6t5** (NLJ): left=21, right=1, matches=21. Correct.
- Row count is correct (21). Hash mismatch is PRE-EXISTING.

### Phase 12: Hash mismatch is pre-existing
- Old output (before budget fix): 14 rows, hash `b752b9c6989de2d8c5999f7b2787af7f`
- New output (after budget fix): 21 rows, hash `3a3415d738ac1c62f2a07eb5e92eef2f`
- Expected: 21 rows, hash `34325f84dd0efa600c0be4e8e0770bc3`
- Both old and new hashes differ from expected. The budget fix only corrected row count (14→21), not values.
- The value issue is a separate, pre-existing bug — likely in column expression evaluation or g0/g2 values.

**Next investigation:** Check `limitRemaining` propagation in the planner. The post-join WHERE filter (`a3 in (...)`, `d6 in (...)`, etc.) is applied AFTER the join tree. If `limitRemaining` is set based on the WHERE filter (not the final result), the NLJ might stop early.

## Root cause analysis (Phase 12)

**Row count: FIXED** — Budget check in HashJoin `buildAndProbe` used `cap*2` instead of `len+1`, stopping left materialization at 67045 instead of 85248. Fixed by checking actual row count.

**Hash mismatch: PRE-EXISTING** — Both old (14 rows) and new (21 rows) outputs have wrong values. The 3 matches from `g1-t8t6` are correct (b4/d6 = 924, 901, 469). The value issue is in column expressions or g0/g2 values, not in join key matching.

**Remaining issue:** The 21 rows have correct join keys but wrong column values. The expressions `x5, e6+c6, d1, c8, e9+108, a7, a3+149+a5, e4+358` produce wrong results. Likely cause: column value corruption in the NLJ block mode `blkDataBuf` reuse, or wrong column lookup in the Output operator.

## Files modified (REQ001414)

- `internal/SQB/EX/planner_select.go` — removed `crossTableConjuncts`, updated `planSelectJoins` signature and call sites, updated `groupBushyJoins` and `localConjuncts` to use `crossTablePredicates`
- `internal/SQB/EX/req001414_test.go` — new regression test for single-table predicate leak
- `docs/development/REQUIREMENTS.md` — REQ001414 added
- `docs/development/select4-fix-status.md` — this document updated

## Files for future investigation

- `internal/SQB/OP/join.go` — NLJ block mode `blkDataBuf` reuse may corrupt Data values for downstream operators; investigate if `copy(result.Data, l.Data)` at line 952 copies correct values when `l.Data` points to a reused buffer
- `internal/SQB/OP/hashjoin.go` — HashJoin `nextMatched()` output values; verify `outData` contains correct column values after deep-copy
- `internal/SQB/EX/planner_select.go` — Output operator column expression evaluation; verify column lookup resolves correct values from joined rows

## Current test status

- `TestSelect4_Join277_HashMismatch` — row count correct (21), hash mismatch (pre-existing value issue)
- `TestREQ001414_BushyGroup_SingleTablePredLeak` passes — REQ001414 refactor verified
- All other tests pass without regression:
  - `internal/SQB/EX` tests pass
  - `internal/SQB/OP` tests pass
  - `tests/sqlcmp/slt` tests pass (except `TestSelect4_Join277_HashMismatch` itself)

## Reproducer

```bash
cd tests/sqlcmp && go test -tags slt_corpus -run TestSelect4_Join277_HashMismatch -v ./slt/
```

Expected: 21 rows, hash `34325f84dd0efa600c0be4e8e0770bc3`
Actual: 21 rows, hash `3a3415d738ac1c62f2a07eb5e92eef2f` (row count correct, values wrong)
