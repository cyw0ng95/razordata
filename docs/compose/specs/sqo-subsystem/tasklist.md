# SQO Subsystem — Task List

## Design Principle

Every step compiles, passes tests, and does not change behavior. No step deletes code
that still has callers within the same commit. The migration uses **bidirectional type
aliases** and **delegation wrappers** to break big moves into tiny, verifiable steps.

## Step 0: SQO Skeleton (REQ001431-a) — 1 commit, ~100 lines

Create SQO package with an empty Optimizer chain. SQB/EX imports SQO but the
optimizer currently does nothing — SQB/EX keeps all existing optimization logic.
This establishes the import path without behavior change.

### 0.1 Create SQO/OC/ with empty Optimizer

```go
// SQO/OC/optimizer.go
package oc

type Optimizer struct{}
func New() *Optimizer { return &Optimizer{} }

// Optimize returns the plan unchanged. Passes will be added incrementally.
func (o *Optimizer) Optimize(plan *Plan, ctx *Context) (*Plan, error) {
    return plan, nil
}
```

- [ ] Create `internal/SQO/OC/optimizer.go` with Optimizer, Plan, Context types
- [ ] Create `internal/SQO/OC/catalog.go` with CatalogReader, StatsReader interfaces
- [ ] Update `internal/SQB/EX/planner.go` — import SQO/OC, create Optimizer instance, store on Planner
- Gate: `grep -r "SQB/" SQO/` returns 0

## Step 1: Interface Definitions (REQ001431-b) — 3 commits, additive only

All new files, no code moves. Every commit compiles independently.

### 1.1 SQF/PS: ExprVisitor

- [ ] Define `ExprVisitor` interface in `internal/SQF/PS/visitor.go`
- [ ] Add `Accept(visitor) error` method to every Expr node type in `internal/SQF/PS/ast.go`
- No callers yet — additive only
- Test: `TestExprVisitor_CoversAllNodeTypes` (compile check)

### 1.2 SQF/PL: OperatorFactory + optional interfaces

- [ ] Define `OperatorFactory` interface in `internal/SQF/PL/factory.go`
- [ ] Define `Parent`, `ColPrunable`, `PredicateCarrier`, `IndexInfo`, `RelationSource`, `ColumnSchema` in `internal/SQF/PL/operator.go`
- Test: compile check only

### 1.3 SQB/OP: Implement OperatorFactory + optional interfaces

- [ ] Create `internal/SQB/OP/factory.go` implementing `OperatorFactory`
- [ ] Add `Children()`, `SetChild()`, `Predicate()`, `SetPredicate()`, `SetUsedCols()` etc.
  to each operator type. One file per operator group is fine.
- Test: `TestOperatorImplementsOptionalInterfaces` — type-assert each operator

## Step 2: Type Alias Bridge (REQ001431-c) — 2 commits

Instead of moving Operator in one shot, use bidirectional aliases. Each commit
compiles and tests pass.

### 2.1 SQF/PL defines Operator alias pointing to SQB/DT

```go
// SQF/PL/types.go — NEW FILE
package pl
import dt "github.com/cyw0ng95/razordata/internal/SQB/DT"
type Operator = dt.Operator
type Row = dt.Row
type Value = dt.Value
```

- [ ] Create `internal/SQF/PL/types.go` with type aliases to SQB/DT
- [ ] SQO's imports now resolve through SQF/PL without touching SQB/DT
- [ ] SQO/OC/optimizer.go imports `pl.Operator` instead of `dt.Operator`
- Test: `go build ./...` passes

### 2.2 SQB clusters start importing SQF/PL instead of SQB/DT for interfaces

- [ ] `SQB/OP/operators.go`: change `import dt "SQB/DT"` to `import pl "SQF/PL"` for Operator/Row/Value
- [ ] `SQB/EV/eval.go`: same change
- [ ] `SQB/EX/ex.go`: same change
- [ ] `SQB/AG/aggregate.go`: same change
- [ ] One file per commit. Each commit: `go build ./...` passes, `go test ./...` passes.
- [ ] After all clusters converted: SQB/DT/types.go still defines the canonical types,
  SQF/PL aliases them, SQB clusters import SQF/PL.
- Files: ~20 files, each changed individually (~5 lines per file)

### 2.3 Swap alias direction (final step of bridge)

```go
// SQF/PL/types.go — now owns the definition
package pl
type Operator struct { ... }
type Row struct { ... }

// SQB/DT/types.go — now aliases to SQF/PL
package dt
import pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
type Operator = pl.Operator
type Row = pl.Row
type Value = pl.Value
```

- [ ] Copy Operator/Row/Value definitions from SQB/DT to SQF/PL
- [ ] Swap SQB/DT to alias SQF/PL
- [ ] Verify: `grep -r '"SQB/DT"' SQF/` returns 0 (SQF no longer imports SQB)
- Test: `go build ./... && go test ./...` passes

## Step 3: Delegation Wrappers (REQ001431-d) — N commits, one function per commit

For each optimization function in SQB/EX, create a thin wrapper in SQO that
delegates to the SQB/EX original. Then SQB/EX callers use the SQO wrapper.
Finally, inline the function body into SQO and delete from SQB/EX.

Pattern for each function:

```
Commit 1: // SQO/CO/predicate.go — wrapper
func SplitAnd(e PS.Expr) []PS.Expr { return ex.SplitAnd(e) }

Commit 2: // SQB/EX/planner_select.go — caller
import "SQO/CO"
conjuncts := co.SplitAnd(whereExpr)  // was: p.splitAnd(whereExpr)

Commit 3: // SQO/CO/predicate.go — inlined
func SplitAnd(e PS.Expr) []PS.Expr { ... actual implementation ... }

Commit 4: // SQB/EX/predicate.go — delete splitAnd function
```

### 3.1 Move splitAnd
- [ ] SQO/CO/predicate.go: wrapper for splitAnd
- [ ] SQB/EX callers switch to SQO/CO.SplitAnd
- [ ] Inline into SQO/CO, delete from SQB/EX/predicate.go
- 4 commits, ~30 lines each

### 3.2 Move walkExpr
- [ ] SQO/PF/walk.go: wrapper for walkExpr
- [ ] Callers switch
- [ ] Inline + delete
- 4 commits, ~50 lines each

### 3.3 Move predicateCost, reorderIndices
- [ ] Same pattern
- 4 commits each

... repeat for all functions in predicate.go, cost.go, join_order.go ...

Total: ~40 small commits, each ~20-50 lines changed. Never more than 100 lines per commit.

## Step 4: SQO/RS/resolve_slots via delegation (REQ001431-e) — 4 commits

Same delegation pattern as Step 3:
- [ ] SQO/RS/resolve_slots.go: wrapper for ResolvePlanSlots
- [ ] SQB/EX callers switch to SQO/RS.ResolvePlanSlots
- [ ] Inline into SQO/RS, delete from SQB/EX
- 4 commits, ~60 lines each

## Step 5: SQO/PF/ passes via extraction (REQ001435) — 8 small commits

For each pass, extract from planSelect into SQO/PF/. Each pass is a file with
one function. This is the same delegation pattern:

```
planSelect line 50-70 (column pruning logic) → SQO/PF/column_pruning.go
planSelect calls SQO/PF.ColumnPruning instead of inline code
Original inline code deleted from planSelect
```

Each commit shrinks planSelect by ~30 lines and adds one file to SQO/PF/.

### 5.1 Column pruning pass
### 5.2 FilterProject fusion pass
### 5.3 Predicate pushdown pass
### 5.4 Index selection pass
### 5.5 Limit pushdown pass
### 5.6 Subquery decorrelation pass
### 5.7 planSelect shrinks from 316 to ~50 lines
### 5.8 SQF/RE constant folding → SQO/PF/constant_folding.go

## Step 6: Delete Originals (REQ001436) — 1 commit

After all functions are delegated and inlined into SQO:

- [ ] Delete `internal/SQB/EX/predicate.go` (all functions moved)
- [ ] Delete `internal/SQB/EX/cost.go` (all functions moved)
- [ ] Delete `internal/SQB/EX/join_order.go` (all functions moved)
- [ ] Delete `internal/SQB/EX/resolve_slots.go` (all functions moved)
- [ ] Delete `internal/SQB/EX/planner_stats_propagation.go` (all functions moved)
- [ ] Delete `internal/SQB/EX/planner_minmax.go` portions (if moved)
- [ ] Delete `internal/SQB/EX/fold_cse.go` (if moved)
- [ ] Remove no-op fallback from Optimizer (Step 0)
- [ ] Update `docs/design/ARCH.md`

## Summary

| Step | Pattern | Commits | Δ Lines | Risk |
|------|---------|---------|---------|------|
| 0. SQO skeleton | New package, no code moved | 1 | +100 | None |
| 1. Interfaces | Additive, no callers | 3 | +300 | None |
| 2. Type alias bridge | Aliases, no definition moved | 20+ | +40 | Low (per-file change) |
| 3. Delegation wrappers | One function at a time | 40 | +0 net | Very low |
| 4. Resolve slots | Same delegation | 4 | +0 | Very low |
| 5. Pass extraction | Extract from planSelect | 8 | +0 | Low |
| 6. Delete originals | No code change | 1 | -4000 | None (dead code) |
| **Total** | | **~77** | **+0 net** | |

Key insight: net code is unchanged. We're moving code, not rewriting it. Each
commit is small enough to review in under 5 minutes. Rollback is per-function,
not per-file.