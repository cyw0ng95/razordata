# SQF — SQL Frontend Processing Layer

## Overview

Receives raw SQL text, tokenizes it, builds an AST, rewrites and plans it, then produces an operator tree for the backend (`SQB/EX`) to execute. Never touches the disk directly — calls down into `TXN` and `ENG`. Depends on `TXN`, `ENG`, and `LOG`. The `Operator` interface it produces is defined in `SQB/EX` (soft split; a future iteration will move it to `SQF/PL`).

## Dependencies

- Required: `TXN`, `ENG`, `LOG`
- Consumed interfaces: `Tx`, `Store`, `Iterator`, `Logger`

## Exposed Interfaces

```go
// Rows is the result of a query
type Rows struct {
    Cols  []string
    Types []TypeID
}

// Stmt is a prepared statement
type Stmt interface {
    Query(ctx context.Context, args ...any) (*Rows, error)
    Exec(ctx context.Context, args ...any) (Result, error)
    Close() error
}

// Result is the result of an INSERT/UPDATE/DELETE
type Result struct {
    RowsAffected int64
    LastInsertID uint64
}
```

The `Operator` interface and `Row`/`Value` types are defined in `SQB/EX`
(soft split). `SQF/PL` imports them from `SQB/EX` when constructing
operator trees.

## Data Structures

### Token Types

```go
const (
    T_EOF TokenType = iota

    // Literals
    T_IDENT
    T_STRING
    T_INT
    T_FLOAT
    T_BIND // '?'

    // Operators
    T_EQ T_NE T_LT T_LE T_GT T_GE
    T_PLUS T_MINUS T_STAR T_SLASH

    // Punctuation
    T_LPAREN T_RPAREN T_COMMA T_DOT T_SEMICOLON T_COLON

    // Keywords
    T_CREATE T_DROP T_INSERT T_UPDATE T_DELETE T_SELECT T_FROM T_WHERE
    T_AND T_OR T_NOT T_IN T_BETWEEN T_LIKE T_IS T_NULL
    T_BEGIN T_COMMIT T_ROLLBACK T_AS T_BY T_ASC T_DESC T_LIMIT T_OFFSET
    T_TABLE T_INDEX T_PRIMARY T_KEY T_NOTNULL T_DEFAULT T_UNIQUE
    T_INT_KW T_BIGINT T_FLOAT_KW T_BOOL T_TEXT T_BLOB T_VARCHAR T_TIMESTAMP
)

type Token struct {
    Type    TokenType
    Lexeme  string
    Literal any
    Line    int
    Col     int
}
```

- Tokens are value types — no interface allocations in the hot path.
- `Literal` holds the parsed value: `int64` for `T_INT`, `string` for `T_STRING`, `nil` otherwise.

### Lexer (`LX`)

```go
type lexer struct {
    input    string
    pos      int
    line     int
    col      int
    keywords map[string]TokenType
}

func (l *lexer) Next() Token { ... }
func (l *lexer) peek() byte { ... }
func (l *lexer) advance() byte { ... }
```

- Keyword lookup via a static `map[string]TokenType` for O(1) recognition.
- String literals and identifiers use `string` directly; no heap allocation beyond the input itself.
- Error recovery: on malformed input, the lexer advances to the next whitespace or delimiter and emits `T_EOF` with an error, allowing the parser to continue and collect all errors in one pass.
- `Bind` parameters (`?`) are recognized as `T_BIND` tokens.

### AST Node Types (`PS`)

```go
// Expression nodes
type Expr interface { exprNode() }
type NumberLiteral  struct { Val int64 }
type FloatLiteral   struct { Val float64 }
type StringLiteral  struct { Val string }
type BoolLiteral    struct { Val bool }
type NullLiteral    struct{}
type Ident          struct{ Name string }
type Param          struct{ Index int } // '?'
type BinaryExpr     struct{ Op TokenType; Left, Right Expr }
type UnaryExpr      struct{ Op TokenType; Operand Expr }
type FunctionCall   struct{ Name string; Args []Expr }
type StarExpr       struct{}
type ListExpr       struct{ Items []Expr }
type BetweenExpr    struct{ Expr, Low, High Expr }
type InExpr        struct{ Expr Expr; List []Expr; Subquery Stmt }

// Statement nodes
type Stmt interface{ stmtNode() }
type CreateTable struct { Name string; Cols []ColDef; PK *string }
type DropTable   struct{ Name string }
type Insert      struct{ Table string; Cols []string; Values [][]Expr }
type Update      struct{ Table string; Set []Pair; Where Expr }
type Delete      struct{ Table string; Where Expr }
type Select      struct{ Cols []Expr; From string; Where, OrderBy, Limit, Offset Expr }
type BeginTX    struct{}
type CommitTX   struct{}
type RollbackTX struct{}
```

- Node types are concrete structs with no interface fields.
- Every node type implements `exprNode()` or `stmtNode()` — no shared mutable state.
- Visitor pattern: `Visitor` interface with `Visit*` methods for all Expr and Stmt types.

### Rewriter (`RE`)

```go
func rewrite(n Stmt) Stmt
func constantFold(e Expr) Expr
func predicatePushdown(where, input Expr) (Expr, Expr)
func flattenSubquery(e *InExpr) (Expr, bool)
```

- **Constant folding:** evaluate binary/unary expressions where all operands are literals.
- **Predicate pushdown:** move `WHERE` conditions as close to the data source as possible.
- **Subquery flattening:** merge single-row subqueries in `WHERE IN` into a join or a list lookup.

### Planner (`PL`)

```go
type plan struct {
    root    Operator
    params  []string
    cost    float64
    memoKey string
}

func plan(stmt Stmt) (*plan, error)
```

- **Plan memoization:** equivalent query shapes share sub-plans. Memo key = SHA256 of canonical AST binary encoding.
- **Cost model:** estimates I/O cost based on key selectivity (from statistics, initially uniform distribution).
- **Index selection:** if a `WHERE` column has an index, prefer `IndexScan`; otherwise `SeqScan`.
- **N3 join ordering:** heap-based N3 algorithm inspired by SQLite's NGQP. Multi-start variant tries each FROM-list table as the candidate base (limited to K≤4 for performance). Prune threshold: `bestCost × 2` (MySQL's `optimizer_prune_level` heuristic).
- **Selectivity estimation:** NDV-based for equi-joins (`1/max(ndv_left, ndv_right)`), range predicates use `(1 - null_frac) / 3`. Falls back to hardcoded constants (0.1/0.3/0.5) when stats unavailable.
- **Hash agg planning:** hash-based aggregation for GROUP BY queries (1000-row threshold).
- **`LIMIT` pushdown,** sort ordering, cost-based scan selection.

## Function Clusters

| Cluster | Responsibility |
|---|---|
| `LX` | Lexer: tokenization, keyword lookup, error recovery |
| `PS` | Parser: recursive descent, AST construction with visitor pattern, syntax error reporting, CTE/recursive CTE, window function, ALTER TABLE, subquery parsing |
| `PL` | Planner: query planning, cost estimation, N3 join ordering, index selection, plan memoization, selectivity estimation, hash agg planning |
| `RE` | Rewriter: AST normalization, constant folding, predicate pushdown, subquery flattening, join reorder |

## Clusters

### LX — Lexer

**Responsibility:** Tokenization, keyword lookup, error recovery at token level.

**Key behaviors:**
- `Next()`: return the next token. Handles whitespace, comments (`--` until end of line), string literals (`'...'`).
- `peek()`: look at the next byte without advancing.
- `advance()`: consume one byte, update `line`/`col`.
- On error: emit `T_EOF` with error, continue to allow parser to report multiple errors.

### PS — Parser

**Responsibility:** Grammar parsing (recursive descent), AST construction, syntax error reporting, CTE/recursive CTE, window functions, ALTER TABLE, subquery parsing.

**Key behaviors:**
- Grammar is LL(1). `parseSelect()`, `parseInsert()`, `parseUpdate()`, `parseDelete()`, `parseCreateTable()`, `parseDropTable()`.
- Expression parsing: `parseExpr()` uses operator precedence.
- **CTE/recursive CTE:** `WITH name AS (query), ...` and `WITH RECURSIVE`.
- **Window functions:** `FUNC() OVER (PARTITION BY ... ORDER BY ...)`.
- **ALTER TABLE:** ADD/DROP COLUMN, RENAME TO, RENAME COLUMN.
- **Subquery:** derived tables `(SELECT ...)` and scalar subqueries.
- **View parsing:** `CREATE VIEW` and `CREATE TEMP VIEW`.
- **Error reporting:** each parse function returns `(node, error)` with Line/Col.
- **SQLite compatibility:** full coverage for SQLite dialect features.

### PL — Planner

**Responsibility:** Query planning, cost estimation, N3 join ordering, NDV-based selectivity, index selection, plan memoization, hash agg planning.

**Key behaviors:**
- `Plan(stmt Stmt) (*plan, error)`: build an operator tree from an AST.
- `n3JoinOrdering` / `n3JoinOrderingMultiStart`: N3 algorithm for join ordering.
- `joinPredSel`: NDV-based predicate selectivity estimation.
- `estimateJoinCost`: cost model for join ordering.
- Plan memoization: SHA256-based plan fingerprinting.
- Cost-based scan selection: `pickCheaperScan`.

### RE — Rewriter

**Responsibility:** AST normalization, constant folding, predicate pushdown, subquery flattening.

**Key behaviors:**
- `Rewrite(stmt Stmt) Stmt`: walk the AST and apply transformations.
- `ConstantFold(e Expr) Expr`: if all operands are literals, evaluate and return the result literal.
- `PredicatePushdown(s *Select) *Select`: move `WHERE` conditions to the earliest possible operator.
- `FlattenSubquery(e *InExpr) (*InExpr, bool)`: replace `WHERE x IN (SELECT y FROM t)` with a list of values.

## Implementation Plan

1. **`internal/SQF/LX/lx.go`** — Lexer: `Next()`, `peek()`, `advance()`, keyword map.
2. **`internal/SQF/LX/token.go`** — Token struct, token type constants.
3. **`internal/SQF/PS/ps.go`** — Parser: recursive descent for all statement types.
4. **`internal/SQF/PS/visitor.go`** — Visitor pattern for AST traversal.
5. **`internal/SQF/RE/re.go`** — Rewrite, ConstantFold, PredicatePushdown, FlattenSubquery.
6. **`internal/SQF/PL/pl.go`** — Planner: Plan, memoize, estimateCost, selectIndex, selectivity.

## Open Issues

- Operator/Row types live in SQB/EX (soft split). Moving them to SQF/PL would
  break the import cycle between SQB clusters and is tracked for a follow-up.
- PRAGMA configuration lives in SQB/EX/pragma_config.go (see SQB.md).
- Open issues from the original SQL.md (RANGE window, multi-key hash join,
  LEFT OUTER JOIN) are tracked in the issue tracker.