# Iteration 7 — SQL/Core (Lexer + Parser + Rewriter)

**Subsystem:** `SQL`
**Dialect:** SQLite-compatible
**Status:** in_progress
**Est. LOC:** ~2,500

## Dialect Alignment

SQLite-compatible subset for embedded/embedded-testing use case.

**Supported features:**
- DDL: CREATE TABLE, DROP TABLE
- DML: INSERT, UPDATE, DELETE, SELECT
- Clauses: WHERE, ORDER BY, LIMIT, OFFSET
- Constraints: PRIMARY KEY, NOT NULL, DEFAULT
- Types: INTEGER, TEXT, REAL, BLOB (SQLite storage classes)
- Comments: `--` until EOL, `/* */` block comments
- String literals: single quotes only `'...'`
- Parameter placeholder: `?`
- Operators: `=`, `!=`, `<`, `<=`, `>`, `>=`, `+`, `-`, `*`, `/`, `AND`, `OR`, `NOT`, `IN`, `BETWEEN`, `LIKE`, `IS NULL`, `IS NOT NULL`

**Not in v1 scope (deferred):**
- AUTOINCREMENT, REPLACE/INSERT OR
- GLOB, GLOB patterns
- CASE expressions
- JOINs (CROSS, INNER, LEFT)
- GROUP BY, HAVING, aggregates (COUNT, SUM, etc.)
- CREATE INDEX, DROP INDEX
- ALTER TABLE
- Transactions keywords (BEGIN/COMMIT/ROLLBACK handled at TXN layer)
- Subqueries in FROM clause

## Overview

SQL parsing. Tokenization, recursive-descent parser, AST normalization. Depends on LOG.

## Dependencies

- Required: `LOG`
- Consumed interfaces: `Logger`

## Design Alignment

Directory structure matches `design/subsystems/SQL.md`:
```
internal/SQL/
├── LX/               # Lexer cluster
│   ├── token.go      # Token struct, token type constants
│   ├── token_test.go
│   ├── lx.go         # Lexer, Next/peek/advance, keyword map
│   └── lx_test.go
├── PS/               # Parser cluster
│   ├── ps.go         # recursive descent: parseSelect/parseInsert/parseUpdate/parseDelete/parseCreateTable/parseDropTable
│   ├── ps_test.go
│   ├── expr.go       # expression parser with precedence
│   └── ast.go        # AST node types (shared across LX, PS, RE)
├── RE/               # Rewriter cluster
└── tests/
    └── sqlcmp/       # SQLite comparison test framework
        ├── sqlcmp.go       # Compare framework
        ├── sqlcmp_test.go # Table-driven comparison tests
        └── README.md      # Usage documentation
```

## SQL Compare Framework (`tests/sqlcmp`)

SQLite-compatible test harness that runs identical SQL against both our parser and SQLite, then compares AST/result equivalence.

### Purpose

- **Parser validation**: Compare parsed AST structure with SQLite's interpretation
- **Round-trip test**: Parse SQL → AST → (reconstruct SQL) → compare with original
- **Semantic equivalence**: For DML (INSERT/UPDATE/DELETE), verify row counts and final values match SQLite
- **Error compatibility**: SQLite syntax errors should map to our parse errors

### Requirements

| ID | Requirement | Status |
|---|---|---|
| C01 | `Compare(query string)` — parse query, return AST or error | pending |
| C02 | `CompareParse(query string) (AST, error)` — parse to AST, compare with SQLite AST | pending |
| C03 | `CompareDML(query string) (rows int64, err error)` — execute DML, return affected rows | pending |
| C04 | `CompareQuery(query string) ([]Row, error)` — execute SELECT, return rows | pending |
| C05 | Table-driven test format: `TestSQL{Name, Input, ExpectedAST, ExpectError, SkipSQLite}` | pending |
| C06 | `sqlite3` exec as subprocess for reference results | pending |
| C07 | Skipped tests when SQLite unavailable (`-short` flag or no `sqlite3` binary) | pending |
| C08 | `go test ./tests/sqlcmp/... -v` shows pass/fail with SQLite diff | pending |
| C09 | Test coverage report: statements parsed, expressions covered | pending |

### Test Format

```go
type SQLTestCase struct {
    Name       string   // e.g., "simple_select"
    Input      string   // SQL query
    ExpectAST  bool     // true if should produce AST
    ExpectErr  bool     // true if should error
    SkipSQLite bool      // skip SQLite comparison
    Notes      string   // explanation
}

var testCases = []SQLTestCase{
    {"simple_select", "SELECT * FROM t", true, false, false, "basic SELECT"},
    {"insert_values", "INSERT INTO t VALUES (1, 'a')", true, false, false, "INSERT"},
    {"invalid_syntax", "SELECT * FROM", false, true, false, "expected table name"},
}
```

### SQLite Integration

- Uses `sqlite3` CLI via `exec.Command` for reference
- Temp in-memory database: `sqlite3 :memory: "sql"`
- Captures stdout/stderr for comparison
- Falls back gracefully if sqlite3 not installed

### Design

```go
// sqlcmp.go
type Runner struct {
    sqlitePath string
    parser     *Parser  // our SQL parser
}

func NewRunner() (*Runner, error) {
    r := &Runner{}
    if path, err := exec.LookPath("sqlite3"); err == nil {
        r.sqlitePath = path
    }
    return r, nil
}

func (r *Runner) Parse(query string) (*AST, error) { ... }
func (r *Runner) ParseAndCompare(query string) error { ... }
func (r *Runner) ExecDML(ctx context.Context, query string) (int64, error) { ... }
func (r *Runner) Query(ctx context.Context, query string) ([]Row, error) { ... }
```

### Test Categories

1. **Lexer tests**: Token round-trip, keyword recognition
2. **Parser tests**: All statement types, expression precedence
3. **Rewriter tests**: Constant fold, predicate pushdown
4. **集成 tests**: End-to-end SQL against SQLite reference

## Requirements

| ID | Requirement | Status |
|---|---|---|
| R01 | `Token` struct (value type, no interface): Type (TokenType), Lexeme (string), Literal (any), Line (int), Col (int) | pending |
| R02 | Token types: T_EOF, T_IDENT, T_STRING, T_INT, T_FLOAT, T_BIND (`?`) | pending |
| R03 | Token types: comparison (T_EQ, T_NE, T_LT, T_LE, T_GT, T_GE), arithmetic (T_PLUS, T_MINUS, T_STAR, T_SLASH) | pending |
| R04 | Token types: punctuation (T_LPAREN, T_RPAREN, T_COMMA, T_DOT, T_SEMICOLON, T_COLON) | pending |
| R05 | Keyword tokens: T_CREATE, T_DROP, T_INSERT, T_UPDATE, T_DELETE, T_SELECT, T_FROM, T_WHERE, T_AND, T_OR, T_NOT, T_IN, T_BETWEEN, T_LIKE, T_IS, T_NULL, T_BEGIN, T_COMMIT, T_ROLLBACK, T_AS, T_BY, T_ASC, T_DESC, T_LIMIT, T_OFFSET, T_TABLE, T_INDEX, T_PRIMARY, T_KEY, T_NOTNULL, T_DEFAULT, T_UNIQUE, T_INT_KW, T_BIGINT, T_FLOAT_KW, T_BOOL, T_TEXT, T_BLOB, T_VARCHAR, T_TIMESTAMP | pending |
| R06 | `Lexer`: `Next() Token`, `peek() byte`, `advance() byte` | pending |
| R07 | Static keyword map for O(1) lookup | pending |
| R08 | String literal parsing (`'...'`), `--` comment skip, whitespace skip | pending |
| R09 | `?` recognized as `T_BIND` (parameter placeholder) | pending |
| R10 | Error recovery: emit `T_EOF` with error on malformed input, continue to collect all errors in one pass | pending |
| R11 | Token round-trip test: `a + 1` → T_IDENT, T_PLUS, T_INT | pending |
| R12 | Expression parser precedence: comparison > add/sub > mul/div > unary > primary | pending |
| R13 | `parseExpr`: handles binary expr, unary expr, parenthesized expr, primary | pending |
| R14 | `parseSelect`: SELECT columns FROM table WHERE expr ORDER BY expr LIMIT expr OFFSET expr | pending |
| R15 | `parseInsert`: INSERT INTO table (cols) VALUES (rows) | pending |
| R16 | `parseUpdate`: UPDATE table SET col=expr WHERE expr | pending |
| R17 | `parseDelete`: DELETE FROM table WHERE expr | pending |
| R18 | `parseCreateTable`: CREATE TABLE name (col_defs) PRIMARY KEY (col) | pending |
| R19 | `parseDropTable`: DROP TABLE name | pending |
| R20 | AST expression nodes: NumberLiteral, FloatLiteral, StringLiteral, BoolLiteral, NullLiteral, Ident, Param, BinaryExpr, UnaryExpr, FunctionCall, StarExpr, ListExpr, BetweenExpr, InExpr | pending |
| R21 | AST statement nodes: CreateTable, DropTable, Insert, Update, Delete, Select, BeginTX, CommitTX, RollbackTX | pending |
| R22 | `ColDef` struct: Name, Type (TokenType), Size (int), Nullable, Default (Expr), PK (bool) | pending |
| R23 | `ConstantFold(e Expr) (Expr, bool)`: evaluate literal expressions, return new expr or unchanged | pending |
| R24 | `PredicatePushdown(s *Select) *Select`: move WHERE conditions closer to data source | pending |
| R25 | `FlattenSubquery(e *InExpr) (*InExpr, bool)`: stub — returns false, no-op | pending |
| R26 | `go vet ./internal/SQL/...` zero warnings | pending |
| R27 | `go test ./internal/SQL/... -race -count=1` all green | pending |
| R28 | Table-driven parse tests for all statement types | pending |
| C01 | `Compare(query string)` — parse query, return AST or error | pending |
| C02 | `CompareParse(query string) (AST, error)` — parse to AST, compare with SQLite AST | pending |
| C03 | `CompareDML(query string) (rows int64, err error)` — execute DML, return affected rows | pending |
| C04 | `CompareQuery(query string) ([]Row, error)` — execute SELECT, return rows | pending |
| C05 | Table-driven test format: `TestSQL{Name, Input, ExpectedAST, ExpectError, SkipSQLite}` | pending |
| C06 | `sqlite3` exec as subprocess for reference results | pending |
| C07 | Skipped tests when SQLite unavailable | pending |
| C08 | `go test ./tests/sqlcmp/... -v` shows pass/fail with SQLite diff | pending |
| C09 | Test coverage report: statements parsed, expressions covered | pending |

## Implementation

### Phase 1: Token + Lexer (`LX/token.go` + `lx.go`)

1. Token type constants as described
2. `Token` struct as value type (no allocations in hot path)
3. `Lexer` struct: input (string), pos (int), line (int), col (int), keywords (static map)
4. `Next()`: skip whitespace/comments, match identifier (check keywords), match string/int/float, match operators/punctuation
5. Keyword map: static `map[string]TokenType` initialized once

### Phase 2: Parser (`PS/expr.go` + `ps.go`)

1. Expression precedence table: comparison (==, !=, <, <=, >, >=) → add/sub (+, -) → mul/div (*, /) → unary (+, -, NOT) → primary
2. `parsePrimary`: number, string, identifier, `?` (param), `*` (star), parenthesized expr, function call
3. `parseUnary`: if next token is unary operator, parse unary expr; else parsePrimary
4. `parseBinary`: parseUnary, then while next token is binary operator of >= precedence, parse right side
5. `parseSelect`: parse SELECT → columns (list or `*`) → FROM → table → WHERE (optional) → ORDER BY (optional) → LIMIT (optional) → OFFSET (optional)
6. `parseCreateTable`: CREATE TABLE → name → `(` → col_defs → `)` → PRIMARY KEY (optional)

### Phase 3: AST (`ast.go`)

1. All expression nodes implement `exprNode()` (empty interface method for type switching)
2. All statement nodes implement `stmtNode()`
3. `Expr` interface: `exprNode()` method
4. `Stmt` interface: `stmtNode()` method
5. `ColDef` and `Pair` (for UPDATE SET) structs

### Phase 4: Rewriter (`RE/re.go`)

1. `ConstantFold`: walk expression tree, if all children are literals, evaluate and return literal
2. `PredicatePushdown`: in SELECT, if WHERE has conditions that could use an index (future), mark them
3. `FlattenSubquery`: stub — return input unchanged, false

## Deferred to v2

- Full subquery planning
- Histogram-based selectivity
- `IN` list with subquery flatten (full implementation)