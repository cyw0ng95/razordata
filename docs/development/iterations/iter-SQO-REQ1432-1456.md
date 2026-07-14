# Iteration — REQ001432 ~ REQ001456 (SQO Subsystem Extraction)

> **Status**: planned
> **Spec**: `docs/compose/specs/sqo-subsystem/`
> (requirements.md, design.md, tasklist.md)
> **Outcome**: pending — no commits yet.

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

### Foundation (Step 0, Step 1, Step 2)
| REQ | What | Why |
|-----|------|-----|
| REQ001432 | SQO/OC/ skeleton + empty Optimizer | Establish import path |
| REQ001433 | SQF/PS ExprVisitor interface | AST traversal API for passes |
| REQ001434 | SQF/PL OperatorFactory + optional interfaces | SQO manipulates operators via interfaces |
| REQ001435 | SQB/OP implements OperatorFactory + optional interfaces | Concrete types satisfy interfaces |
| REQ001436 | SQF/PL type aliases (Phase 1 of bridge) | Allow SQO imports without moving definitions |
| REQ001437 | SQB clusters import SQF/PL instead of SQB/DT | Per-file migration, ~20 commits |
| REQ001438 | SQB/DT becomes alias of SQF/PL (Phase 3 of bridge) | SQF/PL owns the canonical types |

### Function Migration via Delegation (Step 3, Step 4)
| REQ | What | Source | Destination |
|-----|------|--------|-------------|
| REQ001439 | `splitAnd` + `walkExpr` + helpers (~14 functions) | SQB/EX/predicate.go | SQO/CO/predicate.go |
| REQ001440 | cost estimation (~8 functions) | SQB/EX/cost.go | SQO/CO/cost.go |
| REQ001441 | join ordering (~12 functions) | SQB/EX/join_order.go | SQO/JO/join_order.go |
| REQ001442 | resolve_slots (~8 functions) | SQB/EX/resolve_slots.go | SQO/RS/resolve_slots.go |

### Optimizer Passes (Step 5)
| REQ | Pass | Destination |
|-----|------|-------------|
| REQ001443 | Column pruning | SQO/PF/column_pruning.go |
| REQ001444 | FilterProject fusion | SQO/PF/filter_project_fusion.go |
| REQ001445 | Predicate pushdown | SQO/PF/predicate_pushdown.go |
| REQ001446 | Index selection | SQO/PF/index_selection.go |
| REQ001447 | Limit pushdown (TopN) | SQO/PF/limit_pushdown.go |
| REQ001448 | Subquery decorrelation | SQO/PF/subquery_decorrelation.go |
| REQ001449 | Constant folding | SQO/PF/constant_folding.go (from SQF/RE) |
| REQ001450 | planSelect shrinks from 316 → ~50 lines | SQB/EX/planner_select.go |

### Cleanup (Step 6)
| REQ | What |
|-----|------|
| REQ001451 | Delete SQB/EX/predicate.go, cost.go, join_order.go, resolve_slots.go, planner_stats_propagation.go |
| REQ001452 | Update docs/design/ARCH.md with SQO entry; new dep order: SQF → SQO → SQB |

## Gate

```
./before-commit-cases.sh   # core tests + SLT select1-4
go test ./... -race -count=1
grep -r "SQB/" SQO/       # must be 0 after REQ001438
```

## Verification

After all REQs ship:
- `SQO/` contains zero `import "internal/SQB/..."` lines
- SLT select1-4 still pass
- Benchmark numbers within ±5% of pre-migration baseline
- SQB/EX drops from ~14,000 lines to ~4,000 lines

## Commits

Each REQ = 1-4 commits. Total: ~50-60 commits over ~15 working days.