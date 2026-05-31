# Iteration 9 — SQL/Core (Lexer + Parser + Rewriter)

**Subsystem:** `SQL`
**Status:** pending
**Est. LOC:** ~2,500

## Overview

SQL parsing. Tokenization, recursive-descent parser, AST normalization. Depends on LOG.

## Requirements

| ID | Requirement | Status |
|---|---|---|
| R01 | `Token` struct (value type, no interface): Type, Lexeme, Literal (any), Line, Col | pending |
| R02 | Token types: T_EOF, T_IDENT, T_STRING, T_INT, T_FLOAT, T_BIND (`?`) | pending |
| R03 | Token types: T_EQ, T_NE, T_LT, T_LE, T_GT, T_GE, T_PLUS, T_MINUS, T_STAR, T_SLASH | pending |
| R04 | Token types: T_LPAREN, T_RPAREN, T_COMMA, T_DOT, T_SEMICOLON, T_COLON | pending |
| R05 | Token types: keywords (T_CREATE, T_DROP, T_INSERT, T_UPDATE, T_DELETE, T_SELECT, T_FROM, T_WHERE, T_AND, T_OR, T_NOT, T_IN, T_BETWEEN, T_LIKE, T_IS, T_NULL, T_BEGIN, T_COMMIT, T_ROLLBACK, T_AS, T_BY, T_ASC, T_DESC, T_LIMIT, T_OFFSET, T_TABLE, T_INDEX, T_PRIMARY, T_KEY, T_NOTNULL, T_DEFAULT, T_UNIQUE, T_INT_KW, T_BIGINT, T_FLOAT_KW, T_BOOL, T_TEXT, T_BLOB, T_VARCHAR, T_TIMESTAMP) | pending |
| R06 | `Lexer`: `Next/peek/advance`, static keyword map for O(1) lookup | pending |
| R07 | String literal parsing (`'...'`), `--` comment skip, whitespace skip | pending |
| R08 | `?` recognized as `T_BIND` (parameter placeholder) | pending |
| R09 | Error recovery: emit `T_EOF` with error on malformed input, continue to collect all errors | pending |
| R10 | Token round-trip test: `a + 1` → T_IDENT, T_PLUS, T_INT | pending |
| R11 | Expression parser: operator precedence (comparison > add/sub > mul/div > unary > primary) | pending |
| R12 | `parseExpr`: handles binary expr, unary expr, parenthesized expr, primary | pending |
| R13 | `parseSelect`: SELECT columns FROM table WHERE expr ORDER BY expr LIMIT expr OFFSET expr | pending |
| R14 | `parseInsert`: INSERT INTO table (cols) VALUES (exprs) | pending |
| R15 | `parseUpdate`: UPDATE table SET col=expr WHERE expr | pending |
| R16 | `parseDelete`: DELETE FROM table WHERE expr | pending |
| R17 | `parseCreateTable`: CREATE TABLE name (col_defs) PRIMARY KEY (col) | pending |
| R18 | `parseDropTable`: DROP TABLE name | pending |
| R19 | AST node types: NumberLiteral, FloatLiteral, StringLiteral, BoolLiteral, NullLiteral, Ident, Param, BinaryExpr, UnaryExpr, FunctionCall, StarExpr, ListExpr, BetweenExpr, InExpr | pending |
| R20 | AST statement types: CreateTable, DropTable, Insert, Update, Delete, Select, BeginTX, CommitTX, RollbackTX | pending |
| R21 | `ConstantFold`: evaluate literal expressions (`1 + 2 * 3` → T_INT(7)) | pending |
| R22 | `PredicatePushdown`: move WHERE conditions closer to data source in SELECT | pending |
| R23 | `FlattenSubquery` stub (returns false, no-op) | pending |
| R24 | `go vet ./internal/SQL/...` zero warnings | pending |
| R25 | `go test ./internal/SQL/... -race -count=1` all green | pending |
| R26 | Table-driven parse tests for all statement types | pending |

## Implementation

```
internal/SQL/
├── token.go       # Token struct, token type constants
├── lexer.go       # Lexer, Next/peek/advance, keyword map
├── expr.go        # expression parser with precedence
├── parser.go      # recursive descent: parseSelect/parseInsert/parseUpdate/parseDelete/parseCreateTable/parseDropTable
├── ast.go         # AST node types (expr/stmt)
└── rewriter.go    # ConstantFold, PredicatePushdown, FlattenSubquery
```

## Deferred

- Full subquery planning
- Histogram-based selectivity
- `IN` list with subquery flatten (full implementation)