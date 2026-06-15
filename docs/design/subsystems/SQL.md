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
| `PS` | Parser: recursive descent, AST construction, syntax error reporting |
| `PL` | Planner: query planning, cost estimation, index selection, plan memoization |
| `EX` | Executor: operator tree construction, SeqScan, IndexScan, Filter, Project, Sort, Limit, Insert, Update, Delete |
| `RE` | Rewriter: AST normalization, constant folding, predicate pushdown, subquery flattening |

## Clusters

### LX — Lexer

**Responsibility:** Tokenization, keyword lookup, error recovery at token level.

**Key behaviors:**
- `Next()`: return the next token. Handles whitespace, comments ( `--` until end of line), string literals (`'...'`).
- `peek()`: look at the next byte without advancing.
- `advance()`: consume one byte, update `line`/`col`.
- On error: emit `T_EOF` with error, continue to allow parser to report multiple errors.

### PS — Parser

**Responsibility:** Grammar parsing (recursive descent), AST construction, syntax error reporting.

**Key behaviors:**
- Grammar is LL(1). `parseSelect()`, `parseInsert()`, `parseUpdate()`, `parseDelete()`, `parseCreateTable()`, `parseDropTable()`.
- Expression parsing: `parseExpr()` uses operator precedence (comparison > add/sub > mul/div > unary > primary).
- Error reporting: each parse function returns `(node, error)`. Errors include `Line` and `Col` for IDE integration.

### PL — Planner

**Responsibility:** Query planning, cost estimation, index selection, sort ordering, plan memoization.

**Key behaviors:**
- `Plan(stmt Stmt) (*plan, error)`: build an operator tree from an AST.
- `memoize(key, plan)`: store the plan in a `map[string]*plan`.
- `estimateCost(op Operator) float64`: estimate based on row count (from statistics) and selectivity.
- `selectIndex(col string) bool`: check if an index exists for this column; if yes, use `IndexScan`.

### EX — Executor

**Responsibility:** Direct execution of operator tree against storage with SIMD acceleration and concurrent execution.

**Key behaviors:**

#### Execution Model
- `Exec(ctx, stmt, args)` → `Query` or `Exec`: parse → rewrite → plan → execute → return.
- Each operator implements `Next(ctx) (Row, error)`.
- **Vectorized execution (SIMD):** For filter-heavy queries, operators can process batches of 1024 rows at once using Go's `golang.org/x/exp/constraints` and manual SIMD-like patterns:
  - Batch layout: columnar arrays (`[]int64`, `[]float64`, `[]string`) instead of row-by-row.
  - Predicate evaluation: loop over arrays with manual unrolling (process 4-8 elements per iteration).
  - Selection vectors: `[]uint16` mask indicating which rows pass the filter.
- **Parallel execution:** For large scans and joins, use worker pool (`runtime.GOMAXPROCS(0)` workers):
  - Table scan split into key-range partitions, each worker scans a partition.
  - Merge results via channel with bounded buffer (non-blocking send, drop on overflow).
  - Hash join build phase: parallel hash table construction using `sync.Map` or sharded maps.

#### Operator Implementations

**SeqScan (with SIMD acceleration):**
- Default: iterate `ENG.NewIterator()` over the table's key range.
- **Vectorized mode:** Collect 1024 rows into columnar arrays, apply `filter` via SIMD-like batch evaluation:
  ```go
  func evaluateBatch(pred Expr, cols [][]byte, mask []uint16) (count int) {
      // Manual unrolling: process 4 rows per iteration
      for i := 0; i < len(cols[0]); i += 4 {
          // Load 4 values into registers
          v0, v1, v2, v3 := cols[0][i], cols[0][i+1], cols[0][i+2], cols[0][i+3]
          // Compare all 4 in parallel (SIMD-style)
          if pred(v0) { mask[count] = uint16(i); count++ }
          if pred(v1) { mask[count] = uint16(i+1); count++ }
          if pred(v2) { mask[count] = uint16(i+2); count++ }
          if pred(v3) { mask[count] = uint16(i+3); count++ }
      }
      return count
  }
  ```
- Yield rows matching the selection vector.
- **Parallel SeqScan:** If table size > 1 MB, split key range into `N = runtime.GOMAXPROCS(0)` partitions. Launch workers, merge results via channel.

**IndexScan:**
- Use the index to seek to `rangeStart`, iterate until `rangeEnd`.
- Apply any remaining filter via vectorized evaluation.
- **Parallel IndexScan:** For range scans covering > 1000 keys, split range into sub-ranges, scan in parallel.

**Filter (SIMD-optimized):**
- `Filter.Next`: loop on child `Next`, evaluate `predicate` on each row; yield if true.
- **Vectorized Filter:** Accept columnar batches from child, evaluate predicate on entire batch using SIMD-like loops, output selection vector.
- Predicate types supported:
  - Comparison: `=`, `!=`, `<`, `<=`, `>`, `>=`
  - Range: `BETWEEN`, `IN` (converted to sorted array + binary search)
  - Pattern: `LIKE` (prefix/suffix optimization, otherwise fallback to regex)

**Project:**
- `Project.Next`: call child `Next`, extract specified columns, yield.
- **Vectorized Project:** Process batches, extract columns into new columnar arrays.

**Sort:**
- **Single-threaded:** `Sort.Next` must collect all rows from child into a slice, sort by keys, yield in order.
- **Parallel Sort (top-k):** For `ORDER BY ... LIMIT k`:
  - Use parallel sample sort: each worker sorts its partition, then merge k smallest/largest.
  - For large datasets: external merge sort (spill to disk if memory exceeds threshold).

**Limit:**
- `Limit.Next`: loop on child `Next`, count rows, stop after `n` rows.
- **Parallel Limit:** For `LIMIT k` with large `k`, use parallel tournament: each worker finds top-k/N, then merge.

**Aggregate (SIMD acceleration):**
- Aggregates (`COUNT`, `SUM`, `AVG`, `MIN`, `MAX`): accumulate in per-worker local state, then merge.
- **Vectorized Aggregate:** Process batches, update accumulators with SIMD loops:
  ```go
  func sumBatch(vals []int64, acc *int64) {
      var local0, local1, local2, local3 int64
      for i := 0; i < len(vals); i += 4 {
          local0 += vals[i]
          local1 += vals[i+1]
          local2 += vals[i+2]
          local3 += vals[i+3]
      }
      *acc += local0 + local1 + local2 + local3
  }
  ```

**Insert (batched + parallel):**
- `Insert.Next`: evaluate all expressions per row, encode the entire batch (all rows) via `ENG/DP`, and call `txn.Insert()` once with the batched key-value data.
- **Parallel Insert:** For bulk load (> 10000 rows):
  - Split rows into N batches, each worker encodes and inserts its batch.
  - WAL serialization ensures atomicity: final `Commit` writes all batches atomically.
- Batch encoding: `[rowCount:varint][row_0:encoded][row_1:encoded]...` where each row is `[col_0:varint/blob]...[col_N:varint/blob]` — avoids per-row `Insert` call overhead.

**Update (parallel + optimistic concurrency):**
- `Update.Next`: find rows via `iter`, encode new version, call `txn.Insert()` (TXN creates new version).
- **Parallel Update:** Split scan range, workers update disjoint key ranges concurrently.
- **Optimistic locking:** Workers validate no write-write conflict at commit time (TXN/VL handles this).

**Delete (parallel + batch tombstones):**
- `Delete.Next`: find rows via `iter`, insert tombstone into TXN.
- **Parallel Delete:** Split scan range, workers insert tombstones for their partitions.
- **Batch tombstone encoding:** `[count:varint][key_0][key_1]...` — single WAL record for multiple deletions.

**HashJoin (future v2, parallel build + probe):**
- Build phase: scan build input, hash table construction using sharded maps (one map per worker, no contention).
- Probe phase: scan probe input, lookup in hash table, yield matches.
- **Parallel HashJoin:** Build: workers partition hash keys (key % N), each worker builds its shard. Probe: workers probe all shards in round-robin.

#### Memory Management
- **Row format:** `Row` struct borrows `[]byte` slices from `MEM/SP` — no copies, caller responsible for returning to pool.
- **Batch allocation:** Columnar arrays pre-allocated at 1024 rows, reused via `sync.Pool`.
- **Memory limit:** If Sort/Aggregate exceeds `BufferPoolMB * 0.5`, spill to disk (temp SST files) and continue.

#### Concurrency Patterns
- **Fan-out/Fan-in:** Scatter work to N workers, gather results via channel merge.
- **Pipeline parallelism:** Different operators run concurrently (e.g., SeqScan → Filter → Project), each stage buffered via bounded channel.
- **Data parallelism:** Same operator processes different partitions in parallel.

#### Future Enhancements (post-v1)
- **SIMD intrinsics:** Use Go `asm` or `golang.org/x/sys/cpu` for AVX2/NEON vectorization (bitwise AND/OR for bloom filters, comparison intrinsics).
- **Morsel execution:** Break input into 10K-row morsels, workers steal morsels from a shared queue (dynamic load balancing).
- **Code generation:** Generate LLVM IR or Go source for hot operators (experimental).

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
3. **`internal/SQL/PS/ps.go`** — `Parser`: recursive descent for all statement types. `parseStmt()`, `parseSelect()`, `parseInsert()`, `parseUpdate()`, `parseDelete()`, `parseCreateTable()`.
4. **`internal/SQL/PS/expr.go`** — `parseExpr()` with operator precedence. `parsePrimary()`, `parseUnary()`, `parseBinary()`.
5. **`internal/SQL/RE/re.go`** — `Rewrite`, `ConstantFold`, `PredicatePushdown`, `FlattenSubquery`.
6. **`internal/SQL/PL/pl.go`** — `Planner`: `Plan()`, `memoize()`, `estimateCost()`, `selectIndex()`.
7. **`internal/SQL/EX/ex.go`** — `Executor`: `Exec()`, `Query()`. Core execution framework.
8. **`internal/SQL/EX/operators.go`** — basic operator structs: `SeqScan`, `IndexScan`, `Filter`, `Project`, `Sort`, `Limit`, `Insert`, `Update`, `Delete`.
9. **`internal/SQL/EX/operators_vec.go`** — SIMD-optimized operators: vectorized `SeqScan`, `Filter`, `Project`, `Aggregate`. Batch evaluation with manual unrolling.
10. **`internal/SQL/EX/operators_parallel.go`** — parallel operators: parallel `SeqScan`, `IndexScan`, `Update`, `Delete`. Worker pool, fan-out/fan-in, channel merge.
11. **`internal/SQL/EX/eval.go`** — expression evaluation: `Eval(expr, row, params) (any, error)`. Handles all expression types.
12. **`internal/SQL/EX/batch.go`** — columnar batch management: `Batch` struct, selection vectors, memory pooling via `sync.Pool`.
13. **`internal/SQL/EX/sort_parallel.go`** — parallel sort: sample sort for top-k, external merge sort for large datasets.
14. **`internal/SQL/EX/join.go`** — (future v2) `HashJoin`, `NestedLoopJoin`: parallel build and probe phases.
15. **Tests:** 
    - `lx_test.go` (token round-trip)
    - `ps_test.go` (parse errors)
    - `re_test.go` (constant fold)
    - `pl_test.go` (plan memoization)
    - `ex_test.go` (end-to-end execution)
    - `ex_vec_test.go` (SIMD batch evaluation correctness)
    - `ex_parallel_test.go` (parallel execution, race detection)
    - `batch_test.go` (columnar batch management)
    - Use table-driven tests throughout.
16. **Benchmarks:**
    - `ex_bench.go`: single-threaded vs vectorized vs parallel for SeqScan, Filter, Aggregate.
    - Measure throughput (rows/s), latency (p50/p99), memory allocation (allocs/op).

## Open Issues

- Should the planner support subquery planning (currently just flatten)?
- How to estimate selectivity without statistics? Start with uniform distribution, add histogram support later.
- Should SIMD vectorization use Go 1.24's new `vector` package (if available) or hand-written manual unrolling?
- What is the optimal batch size for vectorized execution? 1024 rows is a starting point; may need tuning based on cache line size and predicate complexity.
- Should parallel query execution be enabled by default or opt-in via query hint (e.g., `SELECT /*+ PARALLEL(4) */ ...`)?