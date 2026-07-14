# SQO Subsystem — Design

## 0. Subsystem-Cluster Rule Compliance

The project follows a **Subsystem-Cluster** architecture:

- **Subsystem** = top-level directory under `internal/` (e.g., `SQB`, `SQF`, `ENG`)
- **Cluster** = second-level directory (e.g., `SQB/EX`, `SQB/OP`, `SQF/PL`)
- **Subsystems are standalone**: each subsystem is a cohesive unit. Clusters within a subsystem may freely import each other. Cross-subsystem imports follow the dependency order.
- **Dependency order**: `LOG → FIL → MEM → WAL → ENG → TXN → SQF → SQB → SYS → DBG`

SQO is a **new subsystem** inserted between SQF and SQB:

```
LOG → FIL → MEM → WAL → ENG → TXN → SQF → SQO → SQB → SYS → DBG
```

This placement is correct because:
- SQO imports SQF (shared types: AST, Row, Operator interfaces)
- SQB imports SQO (to get optimized plan trees)
- SQO never imports SQB (enforced by grep gate)

### 1.1 Package Layout

```
internal/
├── SQF/                          # Shared Foundations (no SQB/SQO dependency)
│   ├── LX/                       # Lexer, TokenType (unchanged)
│   ├── PS/                       # Parser, AST types (unchanged)
│   │   ├── ast.go                # Expr, Stmt interfaces, all node types
│   │   └── visitor.go            # ExprVisitor interface (NEW)
│   └── PL/                       # Plan/Root types, operator interfaces
│       ├── types.go              # Row, Value (unchanged)
│       ├── operator.go           # Operator interface MOVED FROM SQB/DT + optional interfaces (ENHANCED)
│       └── factory.go            # OperatorFactory interface (NEW)
│
├── SQO/                          # Optimization (imports SQF only)
│   ├── OC/                       # Optimization Core: chain, Plan, Context, Transform
│   │   ├── optimizer.go          # Optimizer struct, pass chain orchestration
│   │   └── transform.go          # Operator tree walker + replacer
│   ├── PF/                       # Passes: individual optimization transformations
│   │   ├── column_pruning.go     # Projection pushdown pass
│   │   ├── constant_folding.go   # Constant folding pass (moved from SQF/RE)
│   │   ├── predicate_pushdown.go # Predicate pushdown pass (moved from SQF/RE)
│   │   ├── subquery_flatten.go   # Subquery flattening (moved from SQF/RE)
│   │   ├── index_selection.go    # Index scan selection pass
│   │   ├── subquery_decorrelation.go  # EXISTS → semi-join pass
│   │   ├── limit_pushdown.go     # LIMIT pushdown pass
│   │   └── filter_project_fusion.go   # Filter+Project merge pass
│   ├── CO/                       # Cost: model, selectivity, predicate analysis
│   │   ├── cost.go               # Cost model (moved from SQB/EX + SQF/PL)
│   │   ├── selectivity.go        # Selectivity estimation (moved from SQF/PL)
│   │   └── predicate.go          # Predicate analysis (moved from SQB/EX + SQF/RE)
│   ├── JO/                       # Join Ordering + stats propagation
│   │   ├── join_order.go         # Join ordering (moved from SQB/EX + SQF/PL)
│   │   └── stats_propagation.go  # Stats-based range derivation (moved)
│   └── RS/                       # Resolve Slots
│       └── resolve_slots.go      # Slot index resolution (moved from SQB/EX)
│
├── SQB/                          # Execution (imports SQF + SQO)
│   ├── DT/                       # Data types — Operator iface MOVED TO SQF/PL
│   ├── OP/                       # Concrete operators (unchanged, adds interface methods)
│   ├── EV/                       # Expression evaluator (unchanged)
│   ├── EX/                       # Execution engine (dramatically smaller)
│   ├── WT/                       # Writers (unchanged)
│   ├── AD/                       # Adaptive execution (unchanged)
│   └── AG/                       # Aggregate functions (unchanged)
```

Cluster name uniqueness (cross-subsystem):

| Abbr | Subsystem | Meaning | Conflict? |
|------|-----------|---------|-----------|
| OC | SQO | Optimization Core | unique |
| PF | SQO | Passes | unique |
| CO | SQO | Cost | unique |
| JO | SQO | Join Ordering | unique |
| RS | SQO | Resolve Slots | unique |

Note: `CT` appears in both `ENG/CT` (catalog) and `DBG/CT` (counters) — pre-existing conflict, not introduced by SQO.

## 1.2 Key Interface Move: Operator belongs in SQF/PL

Currently `Operator` lives in `SQB/DT` (per ARCH.md line 65). This is a dependency problem for SQO — SQO cannot import SQB/DT.

**Change**: Move `Operator`, `Row`, `Value` interface + type definitions from `SQB/DT` to `SQF/PL`.

- `SQB/DT` re-exports them as type aliases for backward compatibility
- `SQF/PL` becomes the single source of truth for the Operator contract
- `SQO` can now import `SQF/PL.Operator` without touching SQB

```go
// SQF/PL/operator.go — single source of truth
type Operator interface {
    Next(ctx context.Context) (Row, error)
    Close() error
    WithParams([]any) Operator
}

// SQB/DT/types.go — backward-compatible alias
import pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
type Operator = pl.Operator
type Row = pl.Row
type Value = pl.Value
```

## 1.3 SQF/RE shrinks — optimization passes move to SQO

Currently `SQF/RE` handles "constant folding, predicate pushdown, subquery flattening, join reorder" (ARCH.md line 15). These are optimization passes, not frontend responsibilities.

**Change**: Move optimization logic from `SQF/RE` to `SQO/passes/`. `SQF/RE` keeps only the AST-level expression rewriting that must happen before planning (e.g., `x + 0 → x`). All query-level optimization (predicate pushdown, subquery flattening, join reorder) goes to SQO.

## 1.4 SQF/PL shrinks — cost/join logic moves to SQO

Currently `SQF/PL` handles "cost estimation, index selection, plan memoization, selectivity estimation, N3 join ordering" (ARCH.md line 15). These are optimization concerns.

**Change**: Move cost model, join ordering, and selectivity estimation to `SQO/`. `SQF/PL` keeps: plan memoization (cache), catalog management, initial plan construction from AST.

## 2. Interface Definitions (in SQF/PL)

### 2.1 Operator (MOVED from SQB/DT, enhanced with optional interfaces)

```go
// MOVED from SQB/DT to SQF/PL — single source of truth.
// SQB/DT re-exports as type alias.
type Operator interface {
    Next(ctx context.Context) (Row, error)
    Close() error
    WithParams([]any) Operator
}

// NEW — tree traversal support (required for all composite operators)
type Parent interface {
    Children() []Operator
    SetChild(idx int, child Operator)
}

// NEW — column pruning support (implemented by SeqScan, IndexScan)
type ColPrunable interface {
    SetUsedCols(cols []string)
    UsedCols() []string
    RequestedCols() []int
    SetRequestedCols(indices []int)
}

// NEW — predicate carrier (implemented by Filter, FilterProject)
type PredicateCarrier interface {
    Predicate() PS.Expr
    SetPredicate(e PS.Expr)
}

// NEW — index metadata (implemented by IndexScan)
type IndexInfo interface {
    IndexName() string
    IndexColumns() []string
    IndexTableID() uint64
}

// NEW — relation source (implemented by SeqScan, IndexScan)
type RelationSource interface {
    Table() string
    Schema() ColumnSchema
}

// NEW — aggregate info (implemented by Aggregate, HashAggregate)
type AggregateInfo interface {
    GroupCols() []PS.Expr
    Aggregates() []*PS.AggregateFunc
}

// NEW — sort info (implemented by Sort)
type SortInfo interface {
    OrderBy() []PS.OrderItem
}

// NEW — limit info (implemented by Limit)
type LimitInfo interface {
    Limit() int64
    Offset() int64
}

// NEW — column schema (returned by RelationSource.Schema)
type ColumnSchema interface {
    Columns() []string
    ColumnIndex(name string) int
}
```

### 2.2 OperatorFactory

```go
// NEW — abstract operator creation (implemented by SQB/OP)
type OperatorFactory interface {
    NewSeqScan(table string, schema ColumnSchema) Operator
    NewIndexScan(table, idx string, opts ...IndexOption) Operator
    NewFilter(child Operator, pred PS.Expr) Operator
    NewProject(child Operator, cols []PS.Expr) Operator
    NewFilterProject(child Operator, pred PS.Expr, cols []PS.Expr) Operator
    NewSort(child Operator, order []PS.OrderItem) Operator
    NewLimit(child Operator, limit, offset int64) Operator
    NewHashJoin(left, right Operator, leftKeys, rightKeys []string) Operator
    NewNestedLoopJoin(left, right Operator, pred PS.Expr) Operator
    NewAggregate(child Operator, groupCols []PS.Expr, aggExprs []PS.Expr) Operator
    NewDistinct(child Operator) Operator
    NewValues(rows [][]PS.Expr) Operator
}

type IndexOption func(any) // implementation-specific, kept opaque
```

### 2.3 ExprVisitor (in SQF/PS)

```go
// NEW — expression visitor interface
type ExprVisitor interface {
    VisitNumberLiteral(*NumberLiteral) error
    VisitFloatLiteral(*FloatLiteral) error
    VisitStringLiteral(*StringLiteral) error
    VisitBoolLiteral(*BoolLiteral) error
    VisitNullLiteral(*NullLiteral) error
    VisitIdent(*Ident) error
    VisitQualifiedName(*QualifiedName) error
    VisitBinaryExpr(*BinaryExpr) error
    VisitUnaryExpr(*UnaryExpr) error
    VisitFunctionCall(*FunctionCall) error
    VisitAggregateFunc(*AggregateFunc) error
    VisitCaseExpr(*CaseExpr) error
    VisitBetweenExpr(*BetweenExpr) error
    VisitInExpr(*InExpr) error
    VisitSubqueryExpr(*SubqueryExpr) error
    VisitExistsExpr(*ExistsExpr) error
    VisitCastExpr(*CastExpr) error
    VisitAliasedExpr(*AliasedExpr) error
    VisitStarExpr(*StarExpr) error
    VisitListExpr(*ListExpr) error
    VisitParam(*Param) error
    VisitIntervalLiteral(*IntervalLiteral) error
    VisitWindowFunc(*WindowFunc) error
}

// Each Expr node gains an Accept method.
func (e *BinaryExpr) Accept(v ExprVisitor) error {
    if err := v.VisitBinaryExpr(e); err != nil {
        return err
    }
    if err := e.Left.Accept(v); err != nil {
        return err
    }
    return e.Right.Accept(v)
}
```

## 3. Optimizer Chain Design

### 3.1 Core Types

```go
package SQO

// Plan is an operator tree + metadata for optimization.
type Plan struct {
    Root  Operator
    Stmt  *PS.Select  // original statement for reference
}

// Context carries shared state through the optimization pipeline.
type Context struct {
    Factory OperatorFactory
    Catalog CatalogReader  // table metadata, index info
    Stats   StatsReader    // NDV, row counts
}

// OptimizerPass is a single optimization phase.
type OptimizerPass interface {
    Name() string
    Apply(plan *Plan, ctx *Context) (*Plan, error)
}

// Optimizer runs passes in sequence.
type Optimizer struct {
    passes []OptimizerPass
}

func (o *Optimizer) AddPass(p OptimizerPass) {
    o.passes = append(o.passes, p)
}

func (o *Optimizer) Optimize(plan *Plan, ctx *Context) (*Plan, error) {
    var err error
    for _, p := range o.passes {
        plan, err = p.Apply(plan, ctx)
        if err != nil {
            return nil, fmt.Errorf("SQO pass %q: %w", p.Name(), err)
        }
    }
    return plan, nil
}
```

### 3.2 CatalogReader Interface

```go
type CatalogReader interface {
    TableSchema(table string) ColumnSchema
    TableIndexes(table string) []IndexDef
    TablePK(table string) string
    TableRowCount(table string) int64
}

type IndexDef struct {
    Name    string
    Columns []string
    Unique  bool
    Partial PS.Expr  // nil for non-partial
}

type StatsReader interface {
    ColumnNDV(table, col string) int64
    ColumnNullCount(table, col string) int64
    ColumnMinMax(table, col string) (min, max any, ok bool)
}
```

## 4. Transform Utility

```go
// Transform walks the operator tree and applies fn to each node.
// Children are transformed before parents (bottom-up).
func Transform(root Operator, fn func(Operator) Operator) Operator {
    if parent, ok := root.(Parent); ok {
        children := parent.Children()
        for i, child := range children {
            children[i] = Transform(child, fn)
        }
        parent.SetChildren(children)
    }
    return fn(root)
}

// TransformTopDown walks the tree top-down.
func TransformTopDown(root Operator, fn func(Operator) (Operator, bool)) Operator {
    result, descend := fn(root)
    if !descend {
        return result
    }
    if parent, ok := result.(Parent); ok {
        children := parent.Children()
        for i, child := range children {
            children[i] = TransformTopDown(child, fn)
        }
        parent.SetChildren(children)
    }
    return result
}
```

## 5. Migration Plan

The migration is incremental. Each step preserves all existing tests.

### Phase 1: Foundation (REQ001431)

1. Add `ExprVisitor` interface to SQF/PS with `Accept` methods on all node types.
2. Add optional interfaces (`ColPrunable`, `PredicateCarrier`, `Parent`, etc.) to SQF/PL.
3. Implement optional interfaces on all SQB/OP operator types.
4. Add `OperatorFactory` interface to SQF/PL and implement in SQB/OP.
5. Create `SQO/optimizer.go` with `Optimizer` chain + `Transform` utility.
6. Create `SQO/resolve_slots.go` (pure move from SQB/EX — no SQB dependency).
7. Verify: `grep -r "SQB/" SQO/` is empty.

### Phase 2: Move predicate analysis (REQ001432)

1. Move `predicate.go` from SQB/EX to SQO. All functions use SQF/PS types only.
2. Replace all SQB/EX callers with SQO imports.
3. Update SQB/EX tests to use SQO for predicate analysis.

### Phase 3: Move cost model (REQ001433)

1. Move `cost.go` from SQB/EX to SQO.
2. Cost functions use Operator interface + optional interfaces for type inference.
3. No SQB/OP concrete types referenced.

### Phase 4: Move join ordering (REQ001434)

1. Move `join_order.go` from SQB/EX to SQO.
2. Join ordering works on operator trees via Transform + optional interfaces.
3. SQB/EX join planning delegates to SQO.

### Phase 5: Extract passes (REQ001435)

1. Each optimization phase in `planSelect` becomes a standalone `SQO/passes/*.go` file.
2. `planSelect` shrinks from 316 lines to ~50 lines of pass orchestration.
3. Each pass has its own unit test file.

### Phase 6: Cleanup (REQ001436)

1. Delete all moved code from SQB/EX.
2. Remove now-unused imports from SQB/EX files.
3. Update docs/development/ARCH.md with new package structure.

## 6. Testing Strategy

### Unit Tests (in SQO/*_test.go)

Each optimizer pass has a standalone test using mock operators:

```go
func TestColumnPruning_RemovesUnusedCols(t *testing.T) {
    // Build a mock operator tree using test doubles, not real SQB/OP types.
    // Factory creates test operators that implement ColPrunable + Parent.
    plan := &Plan{Root: newMockSeqScan("t", []string{"a", "b", "c"})}
    ctx := &Context{/* mock catalog */}
    
    result, err := ColumnPruningPass{}.Apply(plan, ctx)
    // assert result.Root.(ColPrunable).UsedCols() == ["a"]
}
```

### Integration Tests (in SQB/EX)

Existing planner tests continue to verify end-to-end correctness:

```go
func TestPlanner_EndToEnd(t *testing.T) {
    ex := NewExecutorWithEngine(store)
    result := ex.QueryAll("SELECT a FROM t WHERE a > 150")
    // Same test body as today, just the planner internally uses SQO
}
```

### Verification Gate

Every commit must pass:
```bash
go vet ./SQO/...                # No SQB imports leaked
grep -r "SQB/" SQO/ | wc -l    # Must be 0
grep -r "SQO" SQB/OP/ | wc -l  # Must be 0 (SQO is invisible to operators)
```

## 7. Dependency Rules (Hard Constraints)

```
Dependency order:
LOG → FIL → MEM → WAL → ENG → TXN → SQF → SQO → SQB → SYS → DBG

Allowed:
  SQO → SQF/PS  ✅ (AST types, ExprVisitor)
  SQO → SQF/PL  ✅ (Operator, Row, Value, Factory interfaces)
  SQO → SQF/LX  ✅ (TokenType)

  SQB → SQF/*   ✅
  SQB → SQO     ✅ (SQB/EX imports SQO for optimization)

  SQF → SQB/DT  ❌ REMOVED — Operator moved to SQF/PL, so SQF no longer needs SQB/DT
  
Forbidden:
  SQO → SQB/*   ❌ (enforced by grep gate; violates dependency order)
  SQB/OP → SQO  ❌ (operators are optimization-unaware; violates one-way dependency)
  SQO → SQF/RE   ❌ (SQF/RE optimization code should have moved to SQO; if SQO still needs it, the move is incomplete)
```

## 8. ARCH.md Update

After migration, `docs/design/ARCH.md` must be updated:

### Subsystems table — add SQO entry

```markdown
| `SQO` | passes/, cost.go, ... | Optimization: cost model, join ordering, predicate analysis, plan transformation passes. Imports SQF only. No SQB dependency. |
```

### Dependency order — insert SQO

```diff
- LOG → FIL → MEM → WAL → ENG → TXN → SQF → SQB → SYS → DBG
+ LOG → FIL → MEM → WAL → ENG → TXN → SQF → SQO → SQB → SYS → DBG
```

### SQF/PL description — remove optimization responsibilities

```diff
- `PL` — query planning, cost estimation, index selection, plan memoization, selectivity estimation, hash agg planning, N3 join ordering, NDV-based selectivity.
+ `PL` — plan memoization (cache), catalog management, initial plan construction from AST. Operator/Row/Value type definitions. Optimization delegated to SQO.
```

### SQF/RE description — remove query-level optimization

```diff
- `RE` — constant folding, predicate pushdown, subquery flattening, join reorder.
+ `RE` — AST-level expression rewriting (x + 0 → x). Query-level optimization (predicate pushdown, subquery flattening, join reorder) moved to SQO/passes/.
```

### Operator interface location — move from SQB/DT to SQF/PL

```diff
- // SQB/DT — Operator interface (defined in SQB/DT, consumed by SQF/PL and every SQB cluster)
+ // SQF/PL — Operator interface (single source of truth; SQB/DT re-exports as alias)
```

### Directory structure — add SQO

```diff
 internal/
 ├── SQF/   # SQL Frontend (LX, PS, RE, PL)
+├── SQO/   # SQL Optimizer (passes, cost, join_order)
 ├── SQB/   # SQL Backend  (EX, OP, EV, AG, AD, WT, UT, DT)
```

## 9. Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Optional interfaces miss methods needed by SQO | Medium | High | Add interface methods proactively during Phase 1 before any optimizer pass is moved |
| Transform utility causes nil pointer panics on malformed trees | Low | Medium | Defensive nil checks in Transform, test with edge-case operator trees (single node, deep chain, wide fan-out) |
| Performance regression from interface dispatch | Low | Low | Transform is used only at optimization time (not execution), so overhead is negligible |
| SQB/EX tests accidentally depend on SQO internals | Medium | Low | Keep existing integration tests in SQB/EX; add SQO unit tests in SQO package |
| Migration takes multiple iterations | High | Low | Each Phase is independently releasable; the project can stop after any Phase and still be better off |