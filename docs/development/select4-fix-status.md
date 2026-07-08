# REQ001410 — select4 L39784 NLJ Join Bug: Fix Status

## Summary

The 8-table join in `select4.test` L39784 returns **14 rows** instead of the expected **21 rows**. The bug is **table-order-dependent**: some FROM clause orderings produce correct results (21 rows), others produce wrong results (14 or 0 rows).

## Status (2026-07-08 05:50 UTC)

**Partially resolved** — Planner fix applied (architecturally correct). NLJ merge bug root cause identified but not yet fixed.

## What was applied

### Fix 1 (applied): crossTableConjuncts single-table filter
**File:** `internal/SQB/EX/planner_select.go` (+31 lines)

The `crossTableConjuncts` slice (used by the bushy join group builder) was previously populated with ALL WHERE conjuncts, including single-table predicates that had already been pushed to individual table scans via `splitPredicatesByTable`. These single-table predicates cannot be extracted as equi-join keys, so they became unconsumed residuals in the `localConjuncts` of each group, causing the merge-phase NLJ to apply them incorrectly.

**Fix:** Added a filter that removes single-table predicates (those that `canPushDown` to a single table) from `crossTableConjuncts`. After the filter, `crossTableConjuncts` contains only truly cross-table predicates.

**Verified:** CrossTableConjuncts now correctly holds 3 predicates (instead of 8). Group `localConjuncts` are now 0 (instead of 2-5). No regression in `internal/SQB/EX` or `internal/SQB/OP` test suites.

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

### Phase 6: Predicate flow trace (current)
- Traced `localConjuncts` construction in planner_select.go lines 1050-1214
- For group 1 `{t4,t9,t1,t8,t6}`: three cross-table predicates (b4=d6, e8=c9, a1=d8)
- `extractEquiJoinKeys` consumes ALL 3 as HashJoin keys → `gr.preds` empty
- Merge phase creates cross-join NLJs (no ON clause) — correct
- **Row loss happens inside group 1's internal join tree**: produces 2 rows instead of expected 3
- Next: run plan tree diagnostic test to pinpoint which operator drops rows

## Root cause hypothesis

The row loss occurs within the group 1 internal join tree `{t4,t9,t1,t8,t6}`. The group builder creates a left-deep join tree:
1. NLJ(t4, t9) — cross join (no equi-join keys between t4 and t9)
2. NLJ(result, t1) — cross join
3. HashJoin(result, t8) on e8=c9, a1=d8
4. HashJoin(result, t6) on b4=d6

The 3 expected t4/t6 pairs are: d6=924, d6=901, d6=469. Only 2 appear in the result. The `d6=469` pair is the dropped one. This suggests the HashJoin on b4=d6 (step 4) loses 1 of 3 matches, likely due to incorrect equi-join key extraction or predicate evaluation within the group.

## Files modified

- `internal/SQB/EX/planner_select.go` — added single-table filter to `crossTableConjuncts`
- `doc/development/select4-fix-status.md` — this document

## Files for future investigation

- `internal/SQB/EX/join_order.go` — `groupBushyJoins()` and `isConnectedGraph()` may need improvement
- `internal/SQB/OP/hashjoin.go` — HashJoin `nextMatched()` correctness during reuse
- `internal/SQB/OP/join.go` — NLJ block mode `nextBlock()` `cachedRightRows` reuse
- `internal/SQB/EX/planner_select.go` lines 1186-1216 — `extractEquiJoinKeys` and HashJoin creation within group builder

## Current test status

- `TestSelect4_Join277_HashMismatch` still fails (14 vs 21 rows) — expected, bug not fully fixed
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
