# Iteration 10 — SQL/Execute (Planner + Executor)

**Subsystem:** `SQL`
**Status:** pending
**Est. LOC:** ~3,000

## Overview

Query planning and execution. Operator tree, streaming executor, expression evaluation. Depends on SQL/Core, TXN.

## Dependencies

- Required: `SQL/Core`, `TXN`
- Consumed interfaces: `Tx`, `Store`, `Logger`

## Design Alignment

Directory structure matches `design/subsystems/SQL.md`:
```
internal/SQL/
├── PL/               # Planner cluster
│   ├── pl.go         # Planner, Plan, estimateCost, selectIndex
│   ├── pl_test.go
│   └── memo.go       # plan memoization, SHA256(AST)
└── EX/               # Executor cluster
    ├── ex.go         # Executor, Exec, Query
    ├── ex_test.go
    ├── operators.go  # SeqScan, IndexScan, Filter, Project, Sort, Limit, Insert, Update, Delete
    ├── operators_test.go
    └── eval.go       # Eval, all expression types, built-in functions
```

## Requirements

| ID | Requirement | Status |
|---|---|---|
| R01 | `plan` struct: root (Operator), params ([]string), cost (float64), memoKey (string) | pending |
| R02 | `Planner.Plan(stmt Stmt) (*plan, error)`: build operator tree from AST | pending |
| R03 | `estimateCost(op Operator) float64`: estimate based on row count (uniform distribution) | pending |
| R04 | `selectIndex(col string) bool`: if index exists, use `IndexScan`; otherwise `SeqScan` | pending |
| R05 | Plan memoization: `SHA256(AST)` as memo key via `map[string]*plan` | pending |
| R06 | `Eval(expr, row, params) (any, error)`: evaluate all expression types | pending |
| R07 | `Eval` handles: NumberLiteral, FloatLiteral, StringLiteral, BoolLiteral, NullLiteral, Ident, Param (from params), BinaryExpr, UnaryExpr, FunctionCall | pending |
| R08 | Built-in functions: NOW, COALESCE, IFNULL, LENGTH, SUBSTR | pending |
| R09 | `SeqScan`: iterate Store iterator, apply filter (WHERE), decode rows, yield | pending |
| R10 | `IndexScan`: seek to rangeStart via index, iterate until rangeEnd, apply remaining filter | pending |
| R11 | `Filter`: loop child `Next`, evaluate predicate, yield if true, stop if false | pending |
| R12 | `Project`: transform row to selected columns | pending |
| R13 | `Sort`: materialize all rows from child, sort in-memory by keys, yield in order | pending |
| R14 | `Limit`: stop after N rows from child | pending |
| R15 | `Insert`: batch encode rows (`[rowCount:varint][row_0:encoded]...`), single Store Insert call | pending |
| R16 | `Update`: find rows via iterator, encode new version, call `txn.Insert` | pending |
| R17 | `Delete`: find rows via iterator, insert tombstone | pending |
| R18 | All operators accept `context.Context` for cancellation | pending |
| R19 | `Executor.Exec/Query`: parse → rewrite → plan → execute → return | pending |
| R20 | `ORDER BY` pushdown: if matches primary key order, use natural order (no explicit sort) | pending |
| R21 | End-to-end: SELECT with WHERE/ORDER BY/LIMIT/OFFSET returns correct rows | pending |
| R22 | `go vet ./internal/SQL/...` zero warnings | pending |
| R23 | `go test ./internal/SQL/... -race -count=1` all green | pending |

## Implementation

### Phase 1: Eval (`EX/eval.go`)

1. `Eval(expr Expr, row Row, params []any) (any, error)`: switch on expr type
2. Literals: return Literal.Val directly
3. `Ident`: look up column name in row.Cols, return matching column data
4. `Param`: return params[expr.Index]
5. `BinaryExpr`: eval left, eval right, apply Op (switch on TokenType)
6. `UnaryExpr`: eval operand, apply Op
7. `FunctionCall`: switch on Name, apply built-in function logic

### Phase 2: Operators (`EX/operators.go`)

1. `Operator` interface: `Next(ctx) (Row, error)`, `Close() error`
2. `SeqScan`: call `store.NewIterator(table)`, loop, apply filter, decode via ENG/DP, yield
3. `IndexScan`: (stub for now) seek to rangeStart, iterate, apply filter
4. `Filter`: for { row, err := child.Next(); if err != nil { return nil, err }; if eval(predicate, row, params) { yield row } }
5. `Sort`: collect all rows from child into slice, sort via `sort.Slice`, yield in order
6. `Limit`: count rows from child, stop after N
7. `Insert`: batch encode all rows, call `txn.Insert` once
8. `Update`/`Delete`: find matching rows via iterator, encode/insert

### Phase 3: Planner (`PL/pl.go` + `memo.go`)

1. `Plan(stmt)`: switch on statement type, build appropriate operator tree
2. `estimateCost`: base cost per operator type, multiplied by estimated row count
3. `selectIndex`: check if column has index (deferred to ENG for now — check schema)
4. `memoize(key, plan)`: store in `map[string]*plan`
5. `memoKey = SHA256(canonicalEncoding(AST))`: struct tags drive serialization, binary format

### Phase 4: Executor (`EX/ex.go`)

1. `Executor` struct: store, logger
2. `Exec(ctx, sql, args)`: parse → rewrite → plan (memoized) → execute → return Result
3. `Query(ctx, sql, args)`: same flow, return *Rows

## Deferred to v2

- Parallel query execution (goroutine-per-operator)
- Additional built-in functions beyond core set