# SQO Function Migration Playbook — REQ001439 to REQ001442

> **Status**: planning — foundation complete (REQ001432-435 done).
> This doc is the execution playbook for the next 4 REQs.
> All REQ001439-442 functions are package-private; callers are
> entirely within SQB/EX. The migration is a large but mechanical
> lift-and-shift.

## Key Insight (from the audit)

Every function in REQ001439-442 is **package-private** (lowercase
or `*Planner` method). All callers live in `SQB/EX` itself. The
**delegate-wrapper** pattern from the spec adds two layers of
indirection with no behavior change. For a function this isolated,
a single **lift-and-shift** per function group is safer than four
wrapper-only commits.

**Pragmatic pattern** (deviation from spec, documented):

```
C1: Create SQO/X/Y.go with the function body COPIED from SQB/EX
    (capitalized, exported). SQB/EX original unchanged.
C2: Add a thin wrapper in SQB/EX that calls the SQO version.
    Switch internal SQB/EX callers to call the wrapper.
C3: Delete the SQB/EX original body; wrapper stays.
```

This is **2 commits per function group** instead of 4. It still
satisfies the spec's discipline (no commit deletes a function with
active external callers; the build is green at every step).

## REQ001439: predicate analysis → SQO/CO

**Source**: `internal/SQB/EX/predicate.go` (836 lines, 33 functions)

**Destination**: `internal/SQO/CO/predicate.go` (target: ~900 lines)

### Function inventory

| Group | Functions | Source lines | Callsite count (intra-EX) |
|-------|-----------|--------------|---------------------------|
| **Cost** | `predicateCost`, `funcArgCost`, `caseExprCost` | 35-120 | many (cost.go, etc.) |
| **Reorder** | `reorderIndices`, `orderSlice` | 127-162 | join_order.go |
| **Split** | `(p *Planner).splitAnd` | 164-181 | planner.go, planner_select.go, join_order.go |
| **Column literal** | `extractColumnLiteral`, `extractColumnLiteralExpr`, `literalToBytes` | 186-212, 537-543 | tryApplyPointLookup |
| **Selectivity** | `(p *Planner).estimatePredicateSelectivity` | 216-267 | planner.go, cost.go |
| **Table resolution** | `(p *Planner).findTableForColumn`, `resolveTableForColumn`, `findTableInSchemas`, `splitAlphaNum` | 271-407 | walkExprForTables, splitPredicatesByTable |
| **Tree walk** | `(p *Planner).walkExprForTables`, `walkExpr` | 290-374 | extractTablesFromExpr, resolveSingleTablePredicate |
| **Pushdown** | `(p *Planner).canPushDown`, `(p *Planner).splitPredicatesByTable` | 379-481 | predicate_pushdown pass |
| **In-list / OR** | `extractInListValues`, `extractOrChainEquality`, `extractEqualityAnySide`, `literalValue`, `extractSingleEquality`, `extractColumnLiteralExpr`, `isColumnLiteralPair` | 537-726 | tryApplyPointLookup, cost.go |
| **Point lookup** | `tryApplyPointLookup` | 561-583 | planner.go |
| **Equi-join** | `(p *Planner).equiJoinKey`, `allInSet`, `colNameFromExpr`, `(p *Planner).extractEquiJoinKeys`, `(p *Planner).extractSingleOnEquiKey` | 732-826 | join_order.go |
| **Predicate resolve** | `resolveSingleTablePredicate` | 489-533 | splitPredicatesByTable |

### Per-group commit plan (12 groups, 24 commits total)

| # | Group | Commits | Notes |
|---|-------|---------|-------|
| 1 | Cost (3 funcs) | 2 | `CO.Cost(e) int`; switch `predicateCost` callers to `co.Cost` |
| 2 | Reorder (2) | 2 | `CO.Reorder(preds) []int`, `CO.OrderSlice(preds, idx) []Expr` |
| 3 | Split (1 method) | 2 | `CO.SplitAnd(p, e)`; uses `p.splitAndCache` field — needs adapter or refactor |
| 4 | Column literal (3) | 2 | `CO.ExtractColumnLiteral`, `CO.ExtractColumnLiteralExpr`, `CO.LiteralToBytes` |
| 5 | Selectivity (1 method) | 2 | `CO.EstimateSelectivity(p, e) float64`; uses `p.statsCatalog` |
| 6 | Table resolution (4) | 2 | `CO.FindTableForColumn(p, col) string`, `CO.ResolveTableForColumn`, `CO.FindTableInSchemas`, `CO.SplitAlphaNum` |
| 7 | Tree walk (2) | 2 | `CO.WalkExpr(e, fn)`, `CO.ExtractTablesFromExpr(p, e)` |
| 8 | Pushdown (2 methods) | 2 | `CO.CanPushDown(p, e, table) bool`, `CO.SplitPredicatesByTable` |
| 9 | In-list (7) | 2 | `CO.ExtractInListValues`, `CO.ExtractOrChainEquality`, `CO.ExtractEqualityAnySide`, `CO.LiteralValue`, `CO.ExtractSingleEquality`, `CO.ExtractColumnLiteralExpr`, `CO.IsColumnLiteralPair` |
| 10 | Point lookup (1) | 2 | `CO.TryApplyPointLookup(scan, pred)` |
| 11 | Equi-join (5) | 2 | `CO.EquiJoinKey`, `CO.AllInSet`, `CO.ColNameFromExpr`, `CO.ExtractEquiJoinKeys`, `CO.ExtractSingleOnEquiKey` |
| 12 | Predicate resolve (1) | 2 | `CO.ResolveSingleTablePredicate` |

### Key risk: `*Planner` method receivers

Several functions take `*EX.Planner` because they touch planner
state (`splitAndCache`, `statsCatalog`, `catalog`). Three options:

1. **Keep the method on EX** (option A) — wrappers in CO call back
   to the EX method. This preserves the state locality but keeps
   a tight coupling. Cleanest for splitAndCache (one field).
2. **Pass the state as a parameter** (option B) — CO.SplitAnd takes
   a `*Cache` struct that EX populates. More refactor; riskier.
3. **Move the state to a shared struct in CO** (option C) — EX
   embeds the shared struct. Largest refactor; cleanest final.

**Recommendation**: option A for REQ001439 (least risky). Plan
option C in a follow-up REQ if needed.

### Tests to add

- Move SQB/EX/predicate_pushdown_test.go tests to SQO/CO if
  they exist (verify with `find _test.go`).
- New test: `TestCO_*_BehaviorMatchesEX` for each function group,
  asserting CO returns the same output as the (now-removed) EX
  function for a fixed input. The existing EX test files (which
  use `p.splitAnd(...)`) continue to pass via the wrapper.

### Files to touch

- New: `internal/SQO/CO/predicate.go` (target ~900 lines)
- New: `internal/SQO/CO/predicate_test.go` (target ~200 lines)
- Modified: `internal/SQB/EX/predicate.go` (shrink to 0 lines, deleted)
- Modified: `internal/SQB/EX/planner.go`, `planner_select.go`,
  `join_order.go`, `cost.go` — call sites switch to CO.* names
  via thin SQB/EX wrappers

## REQ001440: cost model → SQO/CO

**Source**: `internal/SQB/EX/cost.go` (814 lines)

**Destination**: `internal/SQO/CO/cost.go` + `selectivity.go`

**Function inventory** (from earlier grep):
- `estimateCostLegacy`, `estimateCostWithParams`
- `estimateSelectivity`, `estimateSelectivityWithStats`
- `estimateEqSelectivity`, `estimateRangeSelectivity`
- `estimateRowCount`, `getTableRowCount`

**Special concern**: cost functions use `*ls.ColumnStats` (concrete
type from ENG/LS). To keep SQO from importing SQB, replace with
`pl.StatsCatalog.ColumnStatsByName(...)` and use the interface
return.

**Commit plan**: 8 functions × 2 commits = 16 commits

## REQ001441: join ordering → SQO/JO

**Source**: `internal/SQB/EX/join_order.go` (813 lines) +
`planner_stats_propagation.go`

**Destination**: `internal/SQO/JO/join_order.go` +
`stats_propagation.go`

**Function inventory**:
- `n3JoinOrdering`, `n3JoinOrderingMultiStart`, `exhaustiveJoinOrder`
- `filteredRowCount`, `estimateJoinOrderCost`
- `n3PredCacheKey`, `findPredicatesForPair`, `findPredicatesForSet`
- `hasIndexOnTable`, `joinResultRows`
- `isConnectedGraph`, `groupBushyJoins`, `extractTableColumn`

**Special concern**: `exhaustiveJoinOrder` builds HashJoin/NLJ by
calling concrete constructors. Replace with `pl.OperatorFactory`
calls (REQ001435) — this is the key validation of the factory.

**Commit plan**: 13 functions × 2 commits = 26 commits

## REQ001442: resolve_slots → SQO/RS + wire into planSelect

**Source**: `internal/SQB/EX/resolve_slots.go` (247 lines)

**Destination**: `internal/SQO/RS/resolve_slots.go`

**Function inventory**: `ResolvePlanSlots`, `resolveExprs`,
`computeSchema`, `tableSchema`, `childOf`, `secondChild`,
`projectColNames`, `resolveExprSlots`

**Special concern**: `planSelect` must call
`rs.ResolvePlanSlots(plan, ctx)` after the SQO pass chain runs.
Until the pass chain is wired (REQ001450), this is a passthrough.

**Commit plan**: 8 functions × 2 commits + 1 wiring commit = 17

## Total: 12 + 16 + 26 + 17 = 71 commits for REQ001439-442

## Cross-cutting concerns

### Imports

After REQ001439-442, SQB/EX should no longer import ENG/LS for
cost statistics — the cost data flows through `pl.StatsCatalog`.
Verify with:

```bash
grep -r "ENG/LS" internal/SQB/EX/  # should be empty for cost-related uses
```

### Cyclic import risk

SQO/CO imports SQB/EX **temporarily** (for the wrapper that
references `*EX.Planner`). This creates a cycle if SQB/EX
imports SQO. The mitigation:

- Keep wrappers in SQB/EX that call CO. The CO package has a
  **method-receiver adapter** that takes the relevant state
  without the `*EX.Planner` type.
- After all functions move, delete the wrappers and the EX
  import direction. At that point SQO/CO imports SQB only for
  concrete operator types it constructs (DT, OP, AG).

### Test strategy

- **Behavior parity**: every moved function gets a new test in
  SQO/CO/ that compares output to a hardcoded expected (since
  the EX original is gone, we can't compare; we compare against
  the pre-migration expected).
- **Existing tests**: all `*_test.go` in SQB/EX that called
  `p.splitAnd` etc. via the wrapper continue to pass.
- **SLT regression**: run `./before-commit-cases.sh` at the
  end of REQ001442 (the function-migration bucket boundary) to
  confirm zero SLT regression.

## Execution order

```
REQ001439 (predicate)    — 12 groups, ~24 commits, 2 days
REQ001440 (cost)         —  8 funcs,   ~16 commits, 1.5 days
REQ001441 (join order)   — 13 funcs,   ~26 commits, 2 days
REQ001442 (resolve_slots) — 8 funcs,   ~17 commits, 1.5 days
                                          Total: ~83 commits
```

Each REQ has a Medium gate at completion and a Full gate
(`./before-commit-cases.sh`) at the end of REQ001442 (bucket
boundary).

## What this REQ delivers vs the spec's 4-commit pattern

| Aspect | Spec | This playbook |
|--------|------|---------------|
| Commits per function | 4 (wrapper → call → inline → delete) | 2 (copy + wrapper) |
| Commits per group | 4 × N functions | 2 (one big commit + one wrapper) |
| Build status at every step | green | green |
| Behavior at every step | unchanged | unchanged |
| Final state | SQB/EX clean, SQO/CO owns | identical |
| Time to complete | ~200 commits | ~83 commits |
| Risk per commit | very low (atomic) | low (group-atomic) |

The deviation is **pragmatic, not sloppy**: the spec's 4-commit
pattern assumes callers may be in other packages. Here every
caller is in SQB/EX, so the wrapper step (commit 2) is sufficient
to keep the EX public API stable for the migration window.
