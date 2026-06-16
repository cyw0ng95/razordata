# SQL — SQL Processing Layer

## Overview

Receives raw SQL text, tokenizes it, builds an AST, rewrites and plans it, then executes the operator tree to return rows. It never touches the disk directly — it calls down into `TXN` and `ENG`. The executor is a direct tree traverser with no virtual machine or bytecode layer. Depends on `TXN`, `ENG`, and `LOG`.

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

// Operator is a node in the operator tree
type Operator interface {
    Next(ctx context.Context) (Row, error)
    Close() error
}

// Row is a single row of data
type Row struct {
    Cols  []string
    Types []TypeID
    Data  [][]byte
}
```

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

- Tokens are value types (`struct { Type TokenType; Lexeme string; Literal any; Line int; Col int }`) — no interface allocations in the hot path.
- `Literal` holds the parsed value: `int64` for `T_INT`, `string` for `T_STRING`, `nil` otherwise.

### Lexer (`LX`)

```go
type lexer struct {
    input   string
    pos     int
    line    int
    col     int
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
type CreateTable struct {
    Name    string
    Cols    []ColDef
    PK      *string // primary key column name, nil if none
}
type DropTable   struct{ Name string }
type Insert      struct{ Table string; Cols []string; Values [][]Expr }
type Update      struct{ Table string; Set []Pair; Where Expr }
type Delete      struct{ Table string; Where Expr }
type Select      struct{
    Cols   []Expr
    From   string
    Where  Expr
    OrderBy Expr
    Limit  Expr
    Offset Expr
}
type BeginTX      struct{}
type CommitTX     struct{}
type RollbackTX   struct{}

type ColDef struct {
    Name     string
    Type     TokenType
    Size     int     // for VARCHAR
    Nullable bool
    Default  Expr    // nil means no default
    PK       bool    // primary key
}

type Pair struct { Col string; Val Expr }
```

- Node types are concrete structs with no interface fields except in `Value` (which holds one of: `int64`, `float64`, `string`, `bool`, `nil`).
- Every node type implements `exprNode()` or `stmtNode()` — no shared mutable state.

### Rewriter (`RE`)

```go
func rewrite(n Stmt) Stmt
func constantFold(e Expr) Expr
func predicatePushdown(where, input Expr) (Expr, Expr)
func flattenSubquery(e *InExpr) (Expr, bool) // true if flattened
```

- **Constant folding:** evaluate binary/unary expressions where all operands are literals. E.g., `1 + 2 * 3` → `7`.
- **Predicate pushdown:** move `WHERE` conditions as close to the data source as possible. E.g., `SELECT * FROM t WHERE a > 10 AND b < 20` → the engine applies `a > 10` first (index), then `b < 20` as a filter.
- **Subquery flattening:** merge single-row subqueries in `WHERE IN` into a join or a list lookup.

### Built-in Functions (v1)

```go
const (
    FN_COUNT    = "COUNT"
    FN_SUM      = "SUM"
    FN_AVG      = "AVG"
    FN_MIN      = "MIN"
    FN_MAX      = "MAX"
    FN_NOW      = "NOW"
    FN_COALESCE = "COALESCE"
    FN_IFNULL   = "IFNULL"
    FN_LENGTH   = "LENGTH"
    FN_SUBSTR   = "SUBSTR"
)
```

- Aggregates (`COUNT`, `SUM`, `AVG`, `MIN`, `MAX`): handled by a dedicated `Aggregate` operator in the executor.
- Scalar functions (`NOW`, `COALESCE`, `IFNULL`, `LENGTH`, `SUBSTR`): evaluated at expression evaluation time in `EX/eval.go`.

### Planner (`PL`)

```go
type plan struct {
    root     Operator
    params   []string // parameter list from '?'
    cost     float64
    memoKey  string   // fingerprint of the AST for memoization
}

func plan(stmt Stmt) (*plan, error)
```

- **Plan memoization:** equivalent query shapes share sub-plans. Memo key = `SHA256(serialize(AST))` where `serialize` produces a canonical binary encoding of the AST (node type + field indices + string lengths). The binary format avoids a JSON overhead — Go struct tags drive the serialization.
- **Cost model:** estimates I/O cost based on key selectivity (from statistics, initially uniform distribution). Each operator has an estimated cost.
- **Index selection:** if a `WHERE` column has an index, consider `IndexScan`; otherwise `SeqScan`.
- **Sort ordering:** if `ORDER BY` matches the primary key order, avoid explicit sort; use the natural order from the LSM tree.
- **`LIMIT` pushdown:** `SeqScan` with `LIMIT` stops after N rows.

### Operator Tree (`EX`)

```go
type Operator interface {
    Next(ctx context.Context) (Row, error)
    Close() error
}

// Leaf operators
type SeqScan struct { table string; filter Expr; schema *TableSchema; iter Iterator }
type IndexScan struct { table string; idx string; rangeStart, rangeEnd []byte; schema *TableSchema }

// Intermediate operators
type Filter    struct{ child Operator; predicate Expr }
type Project   struct{ child Operator; cols []string }
type Sort      struct{ child Operator; keys []Expr; asc []bool }
type Limit     struct{ child Operator; n int64 }

// Leaf writer operators
type Insert struct { table string; values []Row }
type Update struct { table string; set []Pair; where Expr; iter Operator }
type Delete struct { table string; where Expr; iter Operator }

// Future (not in v1)
type NestedLoopJoin struct{ left, right Operator; cond Expr }
type HashJoin    struct{ left, right Operator; keys []string }
```

- Leaf nodes materialize rows by reading from `ENG` via the schema.
- Intermediate nodes are streaming (pull-based). Parent calls `child.Next()`, processes the row, yields to its parent.
- No fully materialized intermediate sets unless `Sort` requires it.
- `Sort` must collect all rows first (materialization), then sort and yield in order.
- No code generation — the executor is a plain Go struct interpreter. Performance comes from tight loops, not JIT.
- All operators accept `context.Context` for cancellation support.

## Function Clusters

| Cluster | Responsibility |
|---|---|
| `LX` | Lexer: tokenization, keyword lookup, error recovery |
| `PS` | Parser: recursive descent, AST construction, syntax error reporting, CTE/recursive CTE, window function, ALTER TABLE, subquery parsing |
| `PL` | Planner: query planning, cost estimation, index selection, plan memoization, selectivity estimation, hash agg planning (REQ000306), memo-optimized planning (memo.go) |
| `EX` | Executor: streaming operator tree with HashJoin (REQ000312), window functions, ALTER TABLE executor, FK validation (REQ000126), CTE/recursive CTE, views, triggers, JSON/datetime functions, PRAGMA, integrity, EXPLAIN, compound SELECT, parallel sort, pipeline parallelism, SIMD-dispatched scalars, decimal, hash agg, coerce |
| `RE` | Rewriter: AST normalization, constant folding, predicate pushdown, subquery flattening, join reorder |

## Clusters

### LX — Lexer

**Responsibility:** Tokenization, keyword lookup, error recovery at token level.

**Key behaviors:**
- `Next()`: return the next token. Handles whitespace, comments (`--` until end of line), string literals (`'...'`).
- `peek()`: look at the next byte without advancing.
- `advance()`: consume one byte, update `line`/`col`.
- On error: emit `T_EOF` with error, continue to allow parser to report multiple errors.
- `token.go` — token type enum and `Token` struct.

### PS — Parser

**Responsibility:** Grammar parsing (recursive descent), AST construction, syntax error reporting, CTE/recursive CTE, window functions, ALTER TABLE, subquery parsing.

**Key behaviors:**
- Grammar is LL(1). `parseSelect()`, `parseInsert()`, `parseUpdate()`, `parseDelete()`, `parseCreateTable()`, `parseDropTable()`.
- Expression parsing: `parseExpr()` uses operator precedence (comparison > add/sub > mul/div > unary > primary).
- **CTE/recursive CTE:** `ps.go` parses `WITH name AS (query), ...` and `WITH RECURSIVE` syntax.
- **Window functions:** parses `FUNC() OVER (PARTITION BY ... ORDER BY ...)` syntax.
- **ALTER TABLE:** parses `ALTER TABLE name ADD COLUMN / DROP COLUMN / RENAME TO`.
- **Subquery:** parses derived tables `(SELECT ...)` and scalar subqueries.
- **View parsing:** `view_test.go` validates CREATE VIEW syntax.
- **Error reporting:** each parse function returns `(node, error)`. Errors include `Line` and `Col` for IDE integration.

### PL — Planner

**Responsibility:** Query planning, cost estimation, index selection, sort ordering, plan memoization, selectivity estimation, hash agg planning, memo-optimized planning.

**Key behaviors:**
- `Plan(stmt Stmt) (*plan, error)`: build an operator tree from an AST.
- `memoize(key, plan)`: store the plan in a `map[string]*plan`.
- `estimateCost(op Operator) float64`: estimate based on row count (from statistics) and selectivity.
- `selectIndex(col string) bool`: check if an index exists for this column; if yes, use `IndexScan`.
- **Selectivity estimation (REQ000264):** `planner.go` estimates key range selectivity to choose between IndexScan and SeqScan.
- **Hash agg planning (REQ000306):** `hashagg_planner_test.go` validates planning of hash-based aggregation for GROUP BY queries.
- **Sort ordering:** if `ORDER BY` matches the primary key order, avoid explicit sort; use the natural order from the LSM tree.
- **`LIMIT` pushdown:** `SeqScan` with `LIMIT` stops after N rows.
- **Plan memoization (REQ000260):** `memo.go` implements SHA256-based plan fingerprinting for equivalent query shapes.

### RE — Rewriter

**Responsibility:** AST normalization, constant folding, predicate pushdown, subquery flattening.

**Key behaviors:**
- `Rewrite(stmt Stmt) Stmt`: walk the AST and apply transformations.
- `ConstantFold(e Expr) Expr`: if all operands are literals, evaluate and return the result literal.
- `PredicatePushdown(s *Select) *Select`: move `WHERE` conditions to the earliest possible operator (SeqScan/IndexScan).
- `FlattenSubquery(e *InExpr) (*InExpr, bool)`: replace `WHERE x IN (SELECT y FROM t)` with a list of values if the subquery is known small.

## Implementation Plan

1. **`internal/SQL/LX/lx.go`** — `Lexer`: `Next()`, `peek()`, `advance()`, keyword map. Full token type enum.
2. **`internal/SQL/LX/token.go`** — `Token` struct, token type constants.
3. **`internal/SQL/PS/ps.go`** — `Parser`: recursive descent for all statement types including CTE, recursive CTE, window functions, ALTER TABLE, subqueries.
4. **`internal/SQL/RE/re.go`** — `Rewrite`, `ConstantFold`, `PredicatePushdown`, `FlattenSubquery`.
5. **`internal/SQL/PL/pl.go`** — `Planner`: `Plan()`, `memoize()`, `estimateCost()`, `selectIndex()`, selectivity estimation, memo-optimized planning.
6. **`internal/SQL/EX/ex.go`** — `Executor`: `Exec()`, `Query()`. Core execution framework.
7. **`internal/SQL/EX/operators.go`** — basic operator structs: `SeqScan`, `IndexScan`, `Filter`, `Project`, `Sort`, `Limit`, `Insert`, `Update`, `Delete`.
8. **`internal/SQL/EX/operators_vec.go`** — SIMD-optimized operators: vectorized `SeqScan`, `Filter`, `Project`, `Aggregate`.
9. **`internal/SQL/EX/operators_parallel.go`** — parallel operators: parallel `SeqScan`, `IndexScan`, `Update`, `Delete`. Worker pool, fan-out/fan-in.
10. **`internal/SQL/EX/hashjoin.go`** — radix-partitioned hash join for INNER equi-joins.
11. **`internal/SQL/EX/window.go`** — window function operator (ROW_NUMBER, RANK, LAG/LEAD, SUM/AVG with OVER).
12. **`internal/SQL/EX/alter_table.go`** — online schema migration operator.
13. **`internal/SQL/EX/fk.go`** — foreign key validation and cascade.
14. **`internal/SQL/EX/subq.go`** — CTE / recursive CTE operator.
15. **`internal/SQL/EX/view.go`** — view resolution operator.
16. **`internal/SQL/EX/json.go`** — JSON functions.
17. **`internal/SQL/EX/datetime.go`** — datetime functions.
18. **`internal/SQL/EX/pragma.go`** — PRAGMA support.
19. **`internal/SQL/EX/integrity.go`** — integrity check operator.
20. **`internal/SQL/EX/explain.go`** — EXPLAIN operator.
21. **`internal/SQL/EX/compound.go`** — UNION/INTERSECT/EXCEPT set operations.
22. **`internal/SQL/EX/simd_dispatch.go`** — SIMD-dispatched scalar function evaluation.
23. **`internal/SQL/EX/aggregate_vec.go`** — vectorized aggregate operators.
24. **`internal/SQL/EX/hashagg.go`** — hash-based aggregation for GROUP BY.
25. **`internal/SQL/EX/coerce.go`** — type coercion for prepared statement parameters.
26. **`internal/SQL/EX/decimal.go`** — DECIMAL type support.
27. **Tests:** table-driven tests throughout, covering all operators and functions.
    - `ex_vec_test.go` (SIMD batch evaluation correctness)
    - `ex_parallel_test.go` (parallel execution, race detection)
    - `batch_test.go` (columnar batch management)
    - Use table-driven tests throughout.
16. **Benchmarks:**
    - `ex_bench.go`: single-threaded vs vectorized vs parallel for SeqScan, Filter, Aggregate.
    - Measure throughput (rows/s), latency (p50/p99), memory allocation (allocs/op).

## Shipped Requirements

The following requirements have been implemented and shipped; they are now part of the design baseline.

### LX — Lexer

| ID | Requirement | Iteration |
|---|---|---|
| REQ000356 | Unary `NOT` as logical prefix operator | iter-27 |

### PS — Parser

| ID | Requirement | Iteration |
|---|---|---|
| REQ000202 | Parser CASE/EXISTS tests | iter-20 |
| REQ000206 | Type tokens (NUMERIC/DATE/TIME/JSON/DECIMAL) | iter-20 |
| REQ000207 | Parameterized types VARCHAR(N)/DECIMAL(P,S) | iter-20 |
| REQ000209 | DEFAULT clause parsing tests | iter-20 |
| REQ000210 | CHECK constraint parsing | iter-20 |
| REQ000232 | Parse ON CONFLICT clause (`INSERT ... ON CONFLICT DO NOTHING/UPDATE`) | iter-21 |
| REQ000234 | Parse RETURNING clause (`INSERT/UPDATE/DELETE ... RETURNING col`) | iter-21 |
| REQ000236 | Parse window functions (`OVER`, `PARTITION BY`, `ROW_NUMBER`, `RANK`) | iter-23 |
| REQ000238 | Parse SAVEPOINT / RELEASE / ROLLBACK TO | iter-21 |
| REQ000240 | Parse CREATE VIEW | iter-24 |
| REQ000243 | Parse ALTER TABLE ADD/DROP COLUMN/RENAME | iter-24 |
| REQ000246 | Parse TRIGGER (CREATE TRIGGER, BEFORE/AFTER, FOR EACH ROW) | iter-07 |
| REQ000248 | Parse generated columns (AS (expr) STORED/VIRTUAL) | iter-27 |
| REQ000251 | Parse CREATE INDEX (UNIQUE, multi-column) | iter-22 |
| REQ000256 | Parse VACUUM / ANALYZE | iter-21 |
| REQ000262 | Add DATE / TIME / TIMESTAMP type tokens | iter-23 |
| REQ000264 | Add JSON type and parse `->`, `->>`, `json_extract` | iter-23 |
| REQ000270 | FETCH FIRST n ROWS ONLY | iter-24 |
| REQ000273 | Add `EXPLAIN` and `EXPLAIN QUERY PLAN` keyword tokens | iter-21 |
| REQ000274 | Parse `EXPLAIN [QUERY PLAN] <stmt>` prefix syntax | iter-21 |
| REQ000275 | `ExplainStmt` AST (`Mode` enum, `Inner` statement) | iter-21 |
| REQ000279 | `EXPLAIN` execution path: skip row execution, return plan as result-set | iter-21 |
| REQ000280 | EXPLAIN on DML (INSERT/UPDATE/DELETE) returns execution plan | iter-21 |
| REQ000281 | EXPLAIN QUERY PLAN formatter (tree-style, human-readable) | iter-21 |
| REQ000288 | `parseInterval` unit validation | iter-24 |
| REQ000291 | EXCLUDED.col reference in ON CONFLICT DO UPDATE | iter-24 |
| REQ000230 | Parse WITH clause (CTE: `WITH x AS (...) SELECT...`) | iter-21 |
| REQ000218 | HAVING filter (already implemented) | iter-20 |
| REQ000084 | SQL/PL+RE — Subquery planning (FROM-subquery parser support) | iter-27 |
| REQ000118 | DML — LIKE pattern matching | iter-07 |
| REQ000119 | DML — BETWEEN | iter-07 |
| REQ000120 | DML — IS NULL / IS NOT NULL | iter-07 |
| REQ000349 | Missing SQLite builtin scalar functions: `LENGTH`, `TYPEOF`, `UNICODE`, `QUOTE`, `ZEROBLOB`, `RANDOMBLOB`, `HEX`, `SOUNDEX`. Each emits `ps: syntax error` rather than a typed "unsupported" error, so the SLT classifier must fall back to substring matching on `syntax error` | iter-25 surfacing (edge probe `TestEdge_Expressions`) |
| REQ000355 | Aggregate function `GROUP_CONCAT(expr)` — route through AggregateFunc when name is a known aggregate | iter-26.2 (v0.26.4) |
| REQ000358 | XOR parser gap fix | iter-27 |
| REQ000368 | Parser comma-join `FROM a, b` — synthesize CROSS joins for trailing comma-separated tables | iter-26.2 (v0.26.4) |
| REQ000379 | Chained unary minus: `SELECT 5- -5` must equal 10 — regression test for SQLite-compatible `--` comment behavior | iter-26.3 (v0.26.5) |
| REQ000380 | `NOT LIKE` parser error — parsePostfix now peeks `T_NOT T_LIKE` and dispatches to parseNotLike | iter-26.3 (v0.26.5) |
| REQ000381 | `NOT IN (subquery)` parser error — same pattern, dispatches to parseNotIn; parseIn refactored to use parseInBody helper | iter-26.3 (v0.26.5) |

### PL — Planner

| ID | Requirement | Iteration |
|---|---|---|
| REQ000231 | CTE planner (materialization vs inline expansion) | iter-21 |
| REQ000241 | View resolution (inline expansion) | iter-24 |
| REQ000253 | Index selection in planner (col=lit equality → real seek) | iter-22 |
| REQ000276 | `PlanNode` tree wrapper (type, cost, rows, children) | iter-21 |
| REQ000277 | Visitor pattern: emit PlanNodes during planner tree construction | iter-21 |
| REQ000278 | Per-operator cost annotation (`cost=N rows=N width=N`) | iter-21 |
| REQ000196 | SQL/EX — HashAggregate in planner (1000-row threshold) | iter-20 |

### EX — Executor

| ID | Requirement | Iteration |
|---|---|---|
| REQ000074 | IndexScan real seek (replace prefix-scan fallback; range bounds `>`, `>=`, `BETWEEN`, inclusive/exclusive) | iter-27 (Phase 6) |
| REQ000116 | DML — JOIN (INNER, CROSS) | iter-08 |
| REQ000144 | SIMD vectorized execution (batch + 4-wide unrolling + selection vectors) | iter-19 (Phase 1) |
| REQ000145 | Parallel query execution (worker pool, fan-out/fan-in, channel merge) | iter-19 (Phase 2) |
| REQ000149 | Columnar batch memory management (sync.Pool for 1024-row batches) | iter-19 (Phase 1) |
| REQ000150 | Parallel Sort implementation (sample sort for top-k) | iter-19 (Phase 3) |
| REQ000157 | Expression evaluation SIMD acceleration (batch predicate) | iter-19 (Phase 1) |
| REQ000163 | Rewriter AST normalization (design mentions, verify completeness) | iter-17 (partial: 65.0%) |
| REQ000173 | SIMD vectorized operators (consolidated into REQ000144) | iter-19 (Phase 1) |
| REQ000182 | Parallel Sort implementation (sample sort for top-k, external merge for large datasets) | iter-08 |
| REQ000182 | Parallel Sort implementation (sample sort for top-k, external merge for large datasets) | iter-08 |
| REQ000183 | Expression evaluation SIMD (batch predicate EvalBatch function) | iter-27 |
| REQ000192 | Adaptive vectorization threshold (auto-fallback to row-at-a-time for tables <100K rows) | iter-19 |
| REQ000197 | OUTER JOIN executor (LEFT/RIGHT/FULL) | iter-20 |
| REQ000201 | QUAL — SQL/PL coverage 30.6% to 98.8% | iter-20 |
| REQ000208 | Type affinity system (SQLite-like 5 affinities) | iter-20 |
| REQ000211 | CHECK constraint enforcement | iter-20 |
| REQ000218 | HAVING filter (already implemented) | iter-20 |
| REQ000229 | DECIMAL type storage (big.Float) | iter-20 |
| REQ000233 | UPSERT executor (`INSERT...ON CONFLICT`) | iter-21 |
| REQ000235 | RETURNING executor (return rows from DML) | iter-21 |
| REQ000237 | Window function executor (`ROW_NUMBER`, `RANK`, `SUM OVER`, `LAG`, `LEAD`) | iter-23 |
| REQ000239 | TXN/VL — Savepoint implementation (nested transaction markers) | iter-21 |
| REQ000244 | ALTER TABLE executor (online schema migration) | iter-27 |
| REQ000247 | TRIGGER executor (fire on INSERT/UPDATE/DELETE) | iter-07 |
| REQ000249 | Generated column materialization on INSERT/UPDATE | iter-27 |
| REQ000252 | IndexScan operator (real seek via LSM index keyspace) | iter-22 |
| REQ000257 | VACUUM executor (reclaim tombstone space, rebuild SST) | iter-23 |
| REQ000258 | ANALYZE executor (collect column statistics) | iter-23 |
| REQ000265 | JSON value storage and `json_extract` executor | iter-23 |
| REQ000282 | Fix computeRank RANK for tied rows | iter-23 |
| REQ000286 | Window materialize context propagation — uses context.Background() instead of caller's ctx | iter-23 |
| REQ000287 | Window setOutput allocation optimization — allocates 2 new slices per call on hot path | iter-23 |
| REQ000290 | LAG/LEAD arbitrary offset support | iter-24 |
| REQ000310 | Real SIMD intrinsics for filter/projection (AVX2/AVX-512) | iter-27 |
| REQ000312 | Vector-aware hash join (Radix partition; SIMD probe) | iter-27 |
| REQ000345 | Empty-table aggregate returns 1 row (not 0) | iter-26 |
| REQ000346 | Test isolation: EX package-level maps reset | iter-26 |
| REQ000357 | SELECT without FROM returns 0 rows — wrap in Values op when `s.From==""` | iter-26.1 (v0.26.3) |
| REQ000359 | String concat NULL semantics — `'a' \\| NULL` returns NULL | iter-26.1 (v0.26.3) |
| REQ000360 | Arithmetic NULL semantics — `10 + NULL` returns NULL | iter-26.1 (v0.26.3) |
| REQ000361 | IS NULL / IS NOT NULL semantics — `NULL IS NULL` true, `NULL IS NOT NULL` false | iter-26.1 (v0.26.3) |
| REQ000362 | Comparison with NULL — `5 = NULL` returns NULL | iter-26.1 (v0.26.3) |
| REQ000369 | All aggregate functions (COUNT/SUM/AVG/MIN/MAX) return NULL — verified working | iter-27 |
| REQ000378 | HAVING with `COUNT(*)` returns 0 rows — evalAggregate resolves `COUNT(*)` (StarExpr arg) to the precomputed column | iter-26.3 (v0.26.5) |
| REQ000382 | Add `ABS`, `HEX`, `ROUND` scalar functions to evalFunction | iter-26.3 (v0.26.5) |
| REQ000383 | Compound SELECT: UNION, INTERSECT, EXCEPT with correct precedence (INTERSECT binds tighter), trailing ORDER BY/LIMIT/OFFSET apply to compound result; RE rewrite/format support | iter-26.4 (v0.26.6) |
| REQ000384 | Scalar function `abs(X)` — returns absolute value, NULL→NULL, string→0.0, MIN_INT64→error | iter-26 |
| REQ000385 | Scalar function `changes()` — last INSERT/UPDATE/DELETE row count; not yet wired to session state | iter-26 |
| REQ000386 | Scalar function `char(X1,...,XN)` — Unicode code point → character; variadic int args; any NULL → NULL | iter-26 |
| REQ000387 | Scalar function `concat(X,...)` — concatenate all args; any NULL → NULL (SQLite semantics) | iter-26 |
| REQ000388 | Scalar function `concat_ws(SEP,X,...)` — concat with separator; SEP=NULL → NULL, skips NULL values | iter-26 |
| REQ000389 | Scalar function `format(FORMAT,...)` — printf-style formatting via fmt.Sprintf | iter-26 |
| REQ000390 | Scalar function `glob(X,Y)` — filename glob match with * and ? wildcards | iter-26 |
| REQ000391 | Scalar function `hex(X)` — BLOB/text → uppercase hex; integer is converted via text first | iter-26 |
| REQ000392 | Scalar function `iif(B,V,...)` / `if()` alias — short-circuit CASE; NULL condition → false branch | iter-26 |
| REQ000393 | Scalar function `instr(X,Y)` — 1-based position of Y in X, 0 if not found; NULL → 0 | iter-26 |
| REQ000394 | Scalar function `last_insert_rowid()` — last successful INSERT rowid | iter-26 |
| REQ000395 | Scalar function `likelihood(X,Y)` — no-op pass-through; planner hint | iter-26 |
| REQ000396 | Scalar function `likely(X)` — no-op pass-through; planner hint | iter-26 |
| REQ000397 | Scalar function `ltrim(X[,Y])` — trim left whitespace or chars in Y | iter-26 |
| REQ000398 | Scalar function `max(X,Y,...)` — multi-arg scalar max, NULLs skipped, all-NULL → NULL | iter-26 |
| REQ000399 | Scalar function `min(X,Y,...)` — multi-arg scalar min, NULLs skipped, all-NULL → NULL | iter-26 |
| REQ000400 | Scalar function `octet_length(X)` — byte length (not code-point count) | iter-26 |
| REQ000401 | Scalar function `quote(X)` — SQL literal rendering; strings single-quoted with escape, BLOBs as X'hex' | iter-26 |
| REQ000402 | Scalar function `random()` — pseudo-random int64; exclude MIN_INT64 | iter-26 |
| REQ000403 | Scalar function `randomblob(N)` — N-byte random BLOB | iter-26 |
| REQ000404 | Scalar function `replace(X,Y,Z)` — string substitution; Y="" returns X unchanged | iter-26 |
| REQ000405 | Scalar function `round(X[,Y])` — round to Y decimal places; Y default 0; Y<0 → 0 | iter-26 |
| REQ000406 | Scalar function `rtrim(X[,Y])` — trim right; default Y=" " | iter-26 |
| REQ000407 | Scalar function `sign(X)` — -1/0/+1 or NULL for non-numeric | iter-26 |
| REQ000408 | Scalar function `soundex(X)` — soundex encoding; "?000" for non-ASCII / NULL | iter-26 |
| REQ000409 | Scalar function `sqlite_source_id()` — fixed string for v1 | iter-26 |
| REQ000410 | Scalar function `sqlite_version()` — fixed string for v1 | iter-26 |
| REQ000411 | Scalar function `total_changes()` — cumulative row-change count since connection open | iter-26 |
| REQ000413 | Scalar function `unhex(X[,Y])` — hex → BLOB; X invalid → NULL; Y ignored | iter-26 |
| REQ000414 | Scalar function `unicode(X)` — code point of first char; NULL → NULL | iter-26 |
| REQ000415 | Scalar function `unistr(X)` — backslash-escape decoder | iter-26 |
| REQ000416 | Scalar function `unlikely(X)` — no-op pass-through | iter-26 |
| REQ000417 | Scalar function `zeroblob(N)` — N-byte BLOB of 0x00 | iter-26 |
| REQ000418 | Scalar function `coalesce(X,Y,...)` — variadic NULL-skipping | iter-26 |
| REQ000419 | Scalar function `ifnull(X,Y)` — 2-arg NULL coalesce | iter-26 |
| REQ000420 | Scalar function `length(X)` — code-point count | iter-26 |
| REQ000421 | Scalar function `like(X,Y[,Z])` — 2-arg pattern match | iter-26 |
| REQ000422 | Scalar function `lower(X)` — ASCII lower-case | iter-26 |
| REQ000423 | Scalar function `nullif(X,Y)` — NULL-on-equal | iter-26 |
| REQ000424 | Scalar function `printf(FORMAT,...)` — alias for format | iter-26 |
| REQ000425 | Scalar function `substr(X,Y[,Z])` — 1-based, negative start | iter-26 |
| REQ000426 | Scalar function `substring(X,Y[,Z])` — substr alias | iter-26 |
| REQ000427 | Scalar function `trim(X[,Y])` — both-sides, default space | iter-26 |
| REQ000433 | Scalar function `upper(X)` — ASCII upper-case | iter-26 |
| REQ000437 | Full SQL aggregate DISTINCT support — `SUM/AVG/MIN/MAX/GROUP_CONCAT(DISTINCT col)` now dedup before aggregating (parity with `COUNT(DISTINCT col)`); NULLs are excluded from the distinct set per SQLite semantics; parser routes DISTINCT through the IDENT aggregate path for `GROUP_CONCAT` | iter-27 |
| REQ000438 | Scalar function eval error routing fix | iter-26 |
| REQ000439 | EXPLAIN statement support | iter-26 |
| REQ000440 | VACUUM and ANALYZE not routed — fixed in buildWriterOp | iter-26 |
| REQ000441 | ALTER TABLE ADD COLUMN not implemented — fixed in buildWriterOp | iter-26 |
| REQ000442 | SLT gap survey — 30/42 common patterns identified; gaps tracked in individual REQs | iter-26 |
| REQ000443b | Fix negative_literal eval pipeline bug (case-sensitive Lookup, UnaryExpr column extraction) | iter-27 |
| REQ000445 | NULL three-valued logic: `<`, `<=`, `>`, `>=`, `=`, `!=` comparisons with NULL operand → return NULL (UNKNOWN), not a boolean | iter-26 |
| REQ000447 | `count(DISTINCT x)`, `avg(DISTINCT x)`, `sum(DISTINCT x)` — DISTINCT aggregate semantics verified; NULLs skipped, dedup works | iter-26 |
| REQ000457 | Package-level EX state complete cleanup — `UnregisterAll()` clears `triggerReg` and `tableTriggers` maps | iter-28 |
| REQ000459 | NULL three-valued logic in IN/NOT IN — `evalIn` returns nil (UNKNOWN) when target is NULL; tracks NULL list elements and returns nil instead of false when no match found and any list element was NULL; `evalInSubquery` same NULL tracking for subquery results | iter-27 |
| REQ000311 | Operator codegen framework — `go generate` template driver (gen.go), PlanVisitor IR with 15 op types, ExprCompiler emitting Go source from PS expression trees, InlineCache (type-dispatch, LRU 256-entry), generated codegen_ops.go with all 15 registered stubs (SeqScan, IndexScan, Filter, Project, Sort, Limit, NestedLoopJoin, HashJoin, HashAggregate, Aggregate, Distinct, CompoundOp, WindowOperator, ExplainStmtOp, CreateViewOperator) | iter-28 |
| REQ000313 | Adaptive query compilation — AdaptiveOp wrapper with InvocationCounter (threshold=2, atomic), composite-key LRU cache (planHash@schemaVersion, 256-entry), FallbackOp + trySpecialized panic recovery, adqc_telemetry.go (slog.Debug events, AdqcMetrics counters), planner.go wraps SELECT/query roots | iter-28 |
| REQ000263 | DATE / TIME / TIMESTAMP value storage and arithmetic | iter-23 |
| REQ000363 | GROUP_CONCAT empty result — empty table returns NULL (after REQ000367 hidden-PK) | iter-26.2 (v0.26.4) |
| REQ000366 | Subquery planner store threading — `Row.planner` + `currentSubqueryPlanner`; `outerInjector` updates `outer` in place for memoized plans | iter-26.2 (v0.26.4) |
| REQ000367 | Hidden rowid for tables without PRIMARY KEY — `hiddenPK` flag, atomic `nextRowID` | iter-26.2 (v0.26.4) |

### Top-level SQL (cross-cluster)

| ID | Requirement | Iteration |
|---|---|---|
| REQ000065 | Lexer with keyword map | iter-07 |
| REQ000066 | Recursive-descent parser | iter-07 |
| REQ000067 | AST node types for DDL/DML/SELECT | iter-07 |
| REQ000068 | Constant folding | iter-07 |
| REQ000069 | Predicate pushdown | iter-07 |
| REQ000070 | Subquery flattening (IN/EXISTS) | iter-07 |
| REQ000071 | Plan memoization (SHA256 of AST) | iter-08 |
| REQ000072 | Cost estimation (uniform distribution) | iter-08 |
| REQ000073 | `SeqScan` operator | iter-08 |
| REQ000075 | `Filter` / `Project` / `Sort` / `Limit` operators | iter-08 |
| REQ000076 | `Insert` / `Update` / `Delete` operators | iter-08 |
| REQ000077 | `Aggregate` (COUNT/SUM/AVG/MIN/MAX) | iter-08 |
| REQ000078 | `HashAggregate` | iter-08 |
| REQ000079 | `NestedLoopJoin` (INNER/CROSS) | iter-08 |
| REQ000080 | `Distinct` operator | iter-08 |
| REQ000081 | `EXPLAIN` rendering | iter-08 |
| REQ000082 | Subquery operator (IN/EXISTS/scalar) | iter-08 |
| REQ000085 | Histogram-based selectivity (replace uniform distribution) | iter-23 |
| REQ000086 | Parallel query execution (operators in goroutines, merge via channel) | iter-08 |
| REQ000107 | `UNIQUE` constraint (in-memory path) | iter-11 |
| REQ000126 | Foreign keys (REFERENCES, ON DELETE/UPDATE) | iter-27 |
| REQ000127 | Catalog persistence across restarts (`CREATE TABLE` / `DROP TABLE` survive `Close`/`Open`) | iter-12 |
| REQ000144 | SIMD vectorized execution (batch + 4-wide unrolling + selection vectors) | iter-19 (Phase 1) |
| REQ000145 | Parallel query execution (worker pool, fan-out/fan-in, channel merge) | iter-19 (Phase 2) |
| REQ000156 | Cost-based scan selection in planner — `pickCheaperScan` | iter-27 |
| REQ000162 | Plan memoization with SHA256(AST binary encoding) | iter-08 |
| REQ000167 | Parameter binding type coercion (Go int -> BIGINT, string -> INT error) | iter-16 |
| REQ000185 | Plan memoization with SHA256 canonical AST binary encoding (not JSON) | iter-08 |
| REQ000204 | CREATE INDEX (no implementation, no parser support) | iter-21 |
| REQ000205 | EXPLAIN SQL syntax (currently only cost calc, not SQL statement) | iter-21 |
| REQ000310 | Real SIMD intrinsics for filter/projection (AVX2/AVX-512) | iter-27 |
| REQ000312 | Vector-aware hash join (Radix partition; SIMD probe) | iter-27 |
| REQ000442 | SLT gap survey — 30/42 common patterns identified | iter-26 |

## Open Issues

- Window function frame specs (ROWS vs RANGE vs GROUPS) — implemented basic ROWS, RANGE needs future work.
- Multi-column hash join keys — current HashJoin supports single key column only.
- LEFT/RIGHT/FULL OUTER JOIN — only INNER via HashJoin; OUTER via NestedLoopJoin (slower).
- Non-equi joins — require NestedLoopJoin with filter operator.
- Should parallel query execution be enabled by default or opt-in via query hint (e.g., `SELECT /*+ PARALLEL(4) */ ...`)?