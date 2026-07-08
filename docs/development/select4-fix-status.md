# REQ001410 — select4 L39784 NLJ Join Bug: Fix Status

## Summary

The 8-table join in `select4.test` L39784 returns **14 rows** instead of the expected **21 rows**. The bug is **table-order-dependent**: some FROM clause orderings produce correct results (21 rows), others produce wrong results (14 or 0 rows).

## Status (2026-07-08 07:15 UTC)

**Open** — REQ001414 applied (architectural refactor). 14/21 bug **not yet fixed**.

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

## Root cause hypothesis (REVISED)

The row loss occurs within the group 1 internal join tree `{t4,t9,t1,t8,t6}`. The group builder creates a left-deep join tree:
1. NLJ(t4, t9) — cross join (no equi-join keys between t4 and t9)
2. NLJ(result, t1) — cross join
3. HashJoin(result, t8) on e8=c9, a1=d8
4. HashJoin(result, t6) on b4=d6

The 3 expected t4/t6 pairs are: d6=924, d6=901, d6=469. Only 2 appear in the result. The `d6=469` pair is the dropped one.

**The bug is in step 4 (HashJoin on b4=d6) or the interaction between steps 1-3 and step 4.** Possible causes:
- HashJoin key extraction for the 5-column left row (t4, t9, t1, t8) fails for the d6=469 case
- NLJ block mode in steps 1-2 produces rows with corrupted data that HashJoin in step 3-4 cannot match
- The left-deep construction order causes the d6=469 pair to be consumed before HashJoin step 4 runs

**This is an operator-level bug, not a planner-level bug.** REQ001414's refactor is architecturally correct but addresses the wrong layer.

## Files modified (REQ001414)

- `internal/SQB/EX/planner_select.go` — removed `crossTableConjuncts`, updated `planSelectJoins` signature and call sites, updated `groupBushyJoins` and `localConjuncts` to use `crossTablePredicates`
- `internal/SQB/EX/req001414_test.go` — new regression test for single-table predicate leak
- `docs/development/REQUIREMENTS.md` — REQ001414 added
- `docs/development/select4-fix-status.md` — this document updated

## Files for future investigation

- `internal/SQB/EX/join_order.go` — `groupBushyJoins()` and `isConnectedGraph()` may need improvement
- `internal/SQB/OP/hashjoin.go` — HashJoin `nextMatched()` correctness during reuse; key extraction for multi-column left rows from NLJ block mode
- `internal/SQB/OP/join.go` — NLJ block mode `nextBlock()` interaction with downstream HashJoin; `cachedRightRows` reuse
- `internal/SQB/EX/planner_select.go` lines 1186-1216 — `extractEquiJoinKeys` and HashJoin creation within group builder; consider alternative join tree shapes (bushy within group, not just left-deep)

## Current test status

- `TestSelect4_Join277_HashMismatch` still fails (14 vs 21 rows) — expected, bug not fully fixed
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
Actual: 14 rows, hash `b752b9c6989de2d8c5999f7b2787af7f`
