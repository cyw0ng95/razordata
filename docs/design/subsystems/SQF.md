# SQF — SQL Frontend Processing Layer

## Overview

Receives raw SQL text, tokenizes it, builds an AST, rewrites and plans it, then produces an operator tree for the backend (`SQB/EX`) to execute. Never touches the disk directly — calls down into `TXN` and `ENG`. Depends on `TXN`, `ENG`, and `LOG`. Core types (`Operator` interface, `Row` struct) are defined in `SQF/PL` and aliased by each SQB cluster (`SQB/DT` uses `type Operator = pl.Operator`). `Value` is defined in `SYS/AP` and aliased through `SQF/PL` → `SQB/DT` → cluster-specific type aliases.

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

The `Operator` interface and `Row`/`Value` types are defined in `SQF/PL` and
`SYS/AP` (not `SQB/EX`). Each SQB cluster aliases them through
`SQB/DT` → `SQF/PL` and `SQF/PL` → `SYS/AP`. This layering is forced by
import cycles — `Operator`/`Row`/`Value` cannot move into any SQB or SQO
package because they are imported by 45+ files across DT, OP, EX, EV, AG, UT, WT, AD and SQO.

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

### Planner (`PL` — slimmed)

```go
// Core types only — planner logic migrated to SQO/CO.
type Operator interface { ... }
type Row struct { ... }
// Value defined in SYS/AP, aliased here
```

PL no longer contains the query planner. All planning logic (cost estimation,
join ordering, index selection, memoization) moved to the `SQO/CO` optimizer
subsystem in the post-SQO migration. PL now hosts only:
- Core executor types (`Operator`, `Row`, `ExecContext`, `QueryPlanner`, `TxWriter`,
  `ColInfo`, `StatsCatalog`, `WorkerPool`)
- `CompareValue` / `EqualValueValue` (used by EV, OP, AG)
- `LearnedModel` (used by SQB/UT/analyze.go)
- `SerializeKey` / `NormalizeForMemo` / AST encoding helpers (shared with SQO/MM)
- `PRAGMA` type constants

PL cannot be fully deleted: its core types are imported by 45+ files across all
SQB clusters and SQO, and moving them would create import cycles between
DT/OP/EX/EV/AG/UT/WT/AD.

## Function Clusters

| Cluster | Responsibility |
|---|---|
| `LX` | Lexer: tokenization, keyword lookup, error recovery |
| `PS` | Parser: recursive descent, AST construction, syntax error reporting, CTE/recursive CTE, window function, ALTER TABLE, subquery parsing |
| `PL` | Core types: Operator/Row/Value aliases, ExecContext, QueryPlanner, TxWriter, ColInfo, StatsCatalog, CompareValue, LearnedModel, memo encoding helpers (planning logic moved to SQO/CO) |
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

### PL — Core Types (slimmed, planner migrated to SQO/CO)

**Responsibility:** Core executor types shared across all SQB clusters and SQO.
Planning logic migrated to `SQO/CO/optimizer.go`.

**Key behaviors:**
- `Operator` interface, `Row` struct, `ExecContext`, `QueryPlanner`, `TxWriter`
- `ColInfo`, `StatsCatalog`, `WorkerPool`
- `CompareValue` / `EqualValueValue`: row-value comparison for EV, OP, AG
- `LearnedModel`: stats-cache used by SQB/UT/analyze.go
- `SerializeKey` / `NormalizeForMemo`: AST encoding shared with SQO/MM/memo.go
- `PRAGMA` type constants

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
4. **`internal/SQF/RE/re.go`** — Rewrite, ConstantFold, PredicatePushdown, FlattenSubquery.
5. **`internal/SQF/PL/types.go`** — Core types: Operator, Row, Value, ExecContext,
   QueryPlanner, TxWriter, ColInfo, StatsCatalog, WorkerPool, CompareValue,
   LearnedModel, memo encoding helpers.

## Open Issues

- Operator/Row types live in SQF/PL, Value in SYS/AP aliased through PL. This
  layering is forced by import cycles and is the final destination — not a "soft
  split" to be resolved later. PL's planner logic was migrated to SQO/CO in
  2026Q1; core types remain in PL permanently.
- SQF/RE remains fully alive (RE.Rewrite/RE.FormatExpr/RE.SplitAnd used by SQB/EX).
- PS visitor pattern (`SQF/PS/visitor.go`) was deleted in 2026Q1 post-SQO
  cleanup (REQ001493) — zero external consumers.
- PRAGMA configuration lives in SQB/EX/pragma_config.go (see SQB.md).
- Open issues from the original SQL.md (RANGE window, multi-key hash join,
  LEFT OUTER JOIN) are tracked in the issue tracker.