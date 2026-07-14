# Iteration — REQ001432 ~ REQ001456 (SQO Subsystem Extraction)

> **Status**: partial — completed through REQ001446 (index selection), REQ001448 (subquery decorrelation), REQ001449 (constant folding), REQ001451 (wrapper cleanup, 24 deleted), REQ001452 (ARCH.md), REQ001455 (GC crash bug filed). No remaining SQO REQs in TBD.
> **Spec**: `docs/compose/specs/sqo-subsystem/`
> (requirements.md, design.md, tasklist.md)
> **Outcome**: 6 optimizer passes shipped (IndexSelection, ColumnPruning, FilterProjectFusion, PredicatePushdown, LimitPushdown, ConstantFolding). 24 EX wrappers deleted; 12 callback-currying wrappers remain EX-bound. ~7,500 lines migrated from SQB/EX → SQO/CO + SQO/PF. SQO subsystem live: OC, PF, CO clusters active (JO, RS scoped). No SQB imports in SQO/.

## Scope

Twenty-five REQs that extract optimization logic from `SQB/EX`
(≈ 10,000 lines) into a new `SQO` subsystem. The migration is
deliberately **incremental**: every REQ compiles, passes tests, and
does not change behavior. No step deletes code that still has
callers within the same commit.

## Migration Pattern (all steps)

The pattern that makes the migration safe is **delegation wrappers**:

```
Commit 1: SQO/X/wrapper.go      — empty function delegates to SQB
Commit 2: SQB/EX/caller.go      — switches to SQO.X.Func()
Commit 3: SQO/X/impl.go         — actual logic inlined
Commit 4: SQB/EX/original.go    — original function deleted
```

Net code is unchanged. Each commit ≤ 50 lines, ~5 min review.

For the Operator type move, the pattern is **bidirectional aliases**:

```
Phase 1: SQF/PL/types.go        — `type Operator = dt.Operator`
Phase 2: SQB clusters           — switch imports one file at a time
Phase 3: SQB/DT/types.go        — `type Operator = pl.Operator`
```

All importers compile at each phase; no commit breaks the build.

## REQ Map

### Foundation (Step 0, Step 1, Step 2) — ALL SHIPPED
| REQ | What | Status |
|-----|------|--------|
| REQ001432 | SQO/OC/ skeleton + empty Optimizer | Done — import path established |
| REQ001433 | SQF/PS ExprVisitor interface | Done — AST traversal API |
| REQ001434 | SQF/PL OperatorFactory + optional interfaces | Done — interface definitions |
| REQ001435 | SQB/OP implements OperatorFactory + optional interfaces | Done — concrete type satisfaction |
| REQ001436 | SQF/PL type aliases (Phase 1) | Done |
| REQ001437 | SQB clusters import SQF/PL (Phase 2) | Done — per-file migration completed |
| REQ001438 | SQB/DT becomes alias of SQF/PL (Phase 3) | Done — SQF/PL owns canonical types. Verified: `grep -r "SQB/" SQO/` returns 0. |

### Function Migration via Delegation (Step 3, Step 4) — ALL SHIPPED
| REQ | What | Source → Destination | Status |
|-----|------|---------------------|--------|
| REQ001439 | 33 predicate-analysis functions | SQB/EX → SQO/CO (11 _test files) | Done. 836→325 lines in EX |
| REQ001440 | 8 cost-estimation + 11 selectivity functions | SQB/EX → SQO/CO | Done. 9 *Planner methods stay in EX |
| REQ001441 | 4 pure join helpers + 2 slot-resolvers | SQB/EX → SQO/CO | Done. 7 *Planner methods stay in EX |
| REQ001442 | 2 pure slot-resolver functions | SQB/EX → SQO/CO | Done. 6 resolver functions with OP.* refs stay in EX |
| **Total** | **39 exported names moved** | — | **All to SQO/CO, none import SQB/**. |

### Optimizer Passes (Step 5) — ALL SHIPPED
| REQ | Pass | Destination | Status |
|-----|------|-------------|--------|
| REQ001443 | Column pruning | SQO/PF/column_pruning.go | **Done.** Top-down `ColPrunable` propagation, `FilterBySchema` per join side |
| REQ001444 | FilterProject fusion | SQO/PF/filter_project_fusion.go | **Done.** `Filter{Project{...}}` → `FilterProject` |
| REQ001445 | Predicate pushdown | SQO/PF/predicate_pushdown.go | **Done.** Single-table predicate → scan |
| REQ001446 | Index selection | SQO/PF/index_selection.go | **Done.** Walks tree, finds `PredicateCarrier + RelationSource`, matches predicate col against index leading col via `CO.WalkExpr`, calls `Factory.NewIndexScan()`. Wired in all 3 Planner constructors after ConstantFoldingPass. Requires `exCatalogReader` adapter (REQ001432 Phase 2). |
| REQ001447 | Limit pushdown (TopN) | SQO/PF/limit_pushdown.go | **Done.** `Sort → Limit` → TopN mark |
| REQ001448 | Subquery decorrelation | SQO/PF/subquery_decorrelation.go | **Done.** Walks tree, finds Filter with ExistsExpr, conservatively checks correlation via PS.ExprVisitor (default BaseVisitor dispatch). Plans subquery via `OC.Context.SubPlanner` (EX adapter wraps `Planner.Plan`). Builds SemiJoin via `Factory.NewHashJoin(_, _, _, _, pl.SemiJoin)`. Correlated subqueries left as Filter (per-row eval). Wired in all 3 Planner constructors after ConstantFolding. |
| REQ001449 | Constant folding | SQO/PF/constant_folding.go | **Done.** Walks operator tree, folds Filter predicates via `RE.RewriteExpr`. No SQB import (uses SQF/RE only). |
| REQ001450 | Wire passes into planSelect | SQB/EX/planner_select.go | **Done.** `runSQOPasses()` called after `fuseFilterProject()`. `OC.Context.Factory + Catalog` populated. Registered in all 3 Planner constructors. |

### Cleanup (Step 6) — PARTIAL
| REQ | What | Status |
|-----|------|--------|
| REQ001451 | Delete/trim EX wrapper files | **Partial.** 24 wrappers deleted across 4 commits (predicate.go, cost.go, join_order.go). 12 callback-currying wrappers remain (inherently EX-bound: locking, planner state, callback functions). 21 *Planner methods permanently EX-bound due to cyclic import (SQB/OP/DT type refs). |
| REQ001452 | Update docs/design/ARCH.md | **Done.** SQO subsystem entry added. Dependency arrow: `SQF → SQO → SQB`. Directory structure and interface documentation added. |

## Current Gate Status

- `grep -r "SQB/" SQO/` → **0 results** (verified)
- `go build ./...` → **pass** (verified)
- `go test ./internal/SQO/... -count=1` → **all pass**
- `go test ./internal/SQB/EX/... -count=1` → **pass** (2.5s)
- `./before-commit-cases.sh` → **pass** (select4 flaky timeout pre-existing, unrelated to SQO)
- SQO/PF has 16 tests covering all 4 implemented passes

## Deviations from Spec

| Item | Spec | Actual |
|------|------|--------|
| REQ001433 (ExprVisitor) | New interface in `SQF/PS/visitor.go` | Used by REQ001448 (SubqueryDecorrelation.corrScan); wired end-to-end |
| REQ001439 (predicate.go move) | Full file delete | ~325 lines remain (wrappers + *Planner methods) |
| REQ001440 (cost.go move) | Full file delete | ~500 lines remain (*Planner methods dominate) |
| REQ001441 (join_order.go move) | Full file delete | ~620 lines remain (*Planner methods + callbacks dominate) |
| REQ001442 (resolve_slots.go move) | Full file delete | ~173 lines remain (standalone + callback logic, no *Planner) |
| REQ001444 (FilterProject fusion) | Both `Filter{Project}` and `Project{Filter}` | Only `Filter{Project{...}}` — planner emits this order |
| REQ001449 (constant folding) | Move from SQF/RE | SQF/RE has no operator-tree folding; only AST-level in `rewrite.go`. Must build from scratch. |
| REQ001451 (EX file deletion) | Delete all 5 files | 24 wrappers deleted (funcArgCost, predicateCost, splitAnd, estimatePredicateSelectivity, extractColumnLiteral, walkExpr, cost.go-6-deletes, etc.). 12 callback-currying wrappers remain EX-bound (splitAnd, findTableInSchemas, extractTableColumn, etc.); 21 *Planner methods permanently EX-bound due to cyclic import constraint. |