# Iteration 10 — SQL/Execute (Planner + Executor)

**Subsystem:** `SQL`
**Status:** pending
**Est. LOC:** ~3,000

## Overview

Query planning and execution. Operator tree, streaming executor, expression evaluation. Depends on SQL/Core, TXN.

## Requirements

| ID | Requirement | Status |
|---|---|---|
| R01 | `Plan` struct: root (Operator), params ([]string), cost (float64), memoKey (string) | pending |
| R02 | `Planner.Plan(stmt Stmt)`: build operator tree from AST | pending |
| R03 | `estimateCost`: estimate based on row count (uniform distribution initially) | pending |
| R04 | `selectIndex(col)`: if index exists for column, use `IndexScan`; otherwise `SeqScan` | pending |
| R05 | Plan memoization: `SHA256(AST)` as memo key, `map[string]*plan` | pending |
| R06 | `Eval(expr, row, params) (any, error)`: evaluate all expression types | pending |
| R07 | `Eval` handles: NumberLiteral, FloatLiteral, StringLiteral, BoolLiteral, NullLiteral, Ident, Param (from params), BinaryExpr, UnaryExpr, FunctionCall | pending |
| R08 | Built-in functions: NOW, COALESCE, IFNULL, LENGTH, SUBSTR | pending |
| R09 | `SeqScan`: iterate ENG iterator, apply filter (WHERE), decode rows, yield | pending |
| R10 | `IndexScan`: seek to rangeStart via index, iterate until rangeEnd, apply remaining filter | pending |
| R11 | `Filter`: loop child `Next`, evaluate predicate, yield if true, stop if false | pending |
| R12 | `Project`: transform row to selected columns | pending |
| R13 | `Sort`: materialize all rows from child, sort in-memory by keys, yield in order | pending |
| R14 | `Limit`: stop after N rows from child | pending |
| R15 | `Insert`: batch encode rows (`[rowCount:varint][row_0:encoded]...`), single ENG `Insert` call | pending |
| R16 | `Update`: find rows via iterator, encode new version, call `txn.Insert` | pending |
| R17 | `Delete`: find rows via iterator, insert tombstone | pending |
| R18 | All operators accept `context.Context` for cancellation | pending |
| R19 | `Executor.Exec/Query`: parse → rewrite → plan → execute → return | pending |
| R20 | `ORDER BY` pushdown: if matches primary key order, use natural order (no explicit sort) | pending |
| R21 | End-to-end: SELECT with WHERE/ORDER BY/LIMIT/OFFSET returns correct rows | pending |
| R22 | `go vet ./internal/SQL/...` zero warnings | pending |
| R23 | `go test ./internal/SQL/... -race -count=1` all green | pending |

## Implementation

```
internal/SQL/
├── plan.go        # Planner, Plan, estimateCost, selectIndex
├── memo.go        # plan memoization, SHA256(AST)
├── eval.go        # Eval, all expression types, built-in functions
├── operators.go   # SeqScan, IndexScan, Filter, Project, Sort, Limit, Insert, Update, Delete
└── executor.go    # Executor, Exec, Query
```

## Deferred

- Parallel query execution (goroutine-per-operator)
- Additional built-in functions beyond the core set