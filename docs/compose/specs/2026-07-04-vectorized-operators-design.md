# [S1] Vectorized Operator Design — Razordata

**Date:** 2026-07-04  
**Status:** Approved design  
**Based on:** `docs/compose/reports/vectorized-operators-research.md`

---

## [S2] Summary

Implement a full vectorized execution pipeline for razordata, covering expression evaluation, hash aggregate (GROUP BY), projection, and hash join. Built on the existing `UT.Batch` foundation. Uses an open-addressing hash table with int64 keys for aggregate/join, and extends `EvalBatch` to support all expression types (not just predicates).

---

## [S3] Expression System (EvalBatch-first Unification)

### [S3.1] New Entry Point

```go
// EvalBatchExpr evaluates any expression over a batch, returning a typed column.
// Returns a Column with the same logical size as the batch (respecting Sel).
func EvalBatchExpr(expr PS.Expr, batch *UT.Batch, params []any) UT.Column
```

### [S3.2] Kernel Dispatch

| Expression Kind | Kernel | Output Type |
|----------------|--------|-------------|
| Column ref (`*PS.Ident`) | Shallow copy `Column` struct | Same as source |
| Literal (`*PS.NumberLiteral`, etc.) | Fill all rows with constant value | Same as literal |
| Binary arithmetic (`+`, `-`, `*`, `/`) | `evalAddInt64`, `evalAddFloat64`, etc. | Depends on input types |
| String concat (`||`) | `evalConcatString` | `TEXT` |
| Comparison predicate | Existing `EvalBatch` → `[]uint16` | Selection vector |
| Scalar functions (LENGTH, SUBSTR, etc.) | Per-function batch kernel | As defined |
| Complex / unknown | Row-at-a-time fallback | Determined at runtime |

### [S3.3] Kernel Contract

Each kernel:
1. Reads from input columns respecting `batch.Sel` (selection vector)
2. Writes to an output `Column` with pre-allocated `Data` slice
3. Handles NULL propagation (any NULL input → NULL output)
4. Returns a column with `Size = batch.LogicalSize()`

### [S3.4] EvalValue Unification

`EvalValue` becomes:
```go
func EvalValue(expr PS.Expr, row *Row, params []any) (Value, error) {
    // Pack row into a 1-row batch
    batch := rowToBatch(row)
    col := EvalBatchExpr(expr, batch, params)
    val := columnValueAt(col, 0) // extract single value
    batch.Put()
    return val, nil
}
```

No duplicate code. The `batchToRow` / `rowToBatch` helper already exists.

---

## [S4] Hash Table (Open-addressing)

### [S4.1] Structure

```go
type HashTable struct {
    Capacity uint32    // power of 2, >= 16
    Occupied uint32
    Hashes   []uint64  // stored hash code (0 = empty sentinel, but we use bitmap)
    Keys     []int64   // grouping key (or hash of composite key)
    Bitmap   []uint64  // occupancy bitmap for O(1) empty-check
    // Payload depends on use case
}
```

- **Open-addressing** with linear probing
- **Power-of-2 capacity** — hash indexing is `hash & (cap-1)` (no modulo)
- **Stored hash codes** — probe compares hash first, then key (avoids false key compares)
- **Probe limit** — cap / 8 maximum probes before resize; resize doubles capacity
- **Single int64 key** for MVP; composite keys stored as combined hash + equality check on separate struct

### [S4.2] Lookup

```go
func (ht *HashTable) Lookup(key int64, hash uint64) (idx int, found bool, ok bool)
// Returns: slot index, whether key exists, whether insertion is possible
```

For aggregates:
- If `found`: slot holds existing group's aggregate state → update
- If `!found` and slot is empty: insert new group

### [S4.3] ProbeAll (batch processing)

```go
func (ht *HashTable) ProbeInt64(keys []int64, hashes []uint64, n int, update func(idx int, row int))
```

Processes `n` rows from a batch, calling `update(idx, row)` for each row's hash table slot after lookup.

---

## [S5] Vectorized Hash Aggregate

### [S5.1] Type

```go
type VectorizedHashAggregate struct {
    child     BatchProducer
    groupCol  int           // column index for GROUP BY key (-1 = no group)
    groupType LX.TokenType
    aggDefs   []aggDef      // aggregate definitions (SUM, COUNT, MIN, MAX, AVG on specific columns)
    ht        *HashTable    // open-addressing hash table
    done      bool
}

type aggDef struct {
    kind    AggKind  // Sum, Count, Min, Max, Avg
    colIdx  int      // column index (-1 for COUNT(*))
    resultType LX.TokenType
}
```

### [S5.2] Execution

1. Drain all batches from child via `NextBatch`
2. For each batch:
   a. Compute hash for GROUP BY column (or constant for no-group)
   b. Call `ht.ProbeInt64(groupKeys, hashes, batch.LogicalSize(), updateFn)`
   c. `updateFn(idx, row)` updates the aggregate state at slot `idx` with the row's value
3. Emit a single output batch with GROUP BY column + aggregate columns
4. For no-group aggregate (single-row result), skip hash table entirely

### [S5.3] Aggregate States

```go
type sumState struct { hasValue bool; sum int64 }  // or float64
type countState struct { count int64 }
type minState struct { hasValue bool; min int64 }   // or float64
type maxState struct { hasValue bool; max int64 }   // or float64

// Combined payload for multiple aggregates
type aggPayload struct {
    // Named fields per aggregate definition
    sum   sumState
    count int64
    min   int64
    max   int64
}
```

---

## [S6] Vectorized Projection

### [S6.1] Type

```go
type VectorizedProject struct {
    child  BatchProducer
    exprs  []PS.Expr
    names  []string
    done   bool
}
```

### [S6.2] Execution

1. Read batch from child
2. For each expression: `col := EvalBatchExpr(expr, batch, params)` → append to output columns
3. Return assembled batch
4. Wildcard (`*`) → copy child columns directly (shallow struct copy)

---

## [S7] Vectorized Hash Join

### [S7.1] Type

```go
type VectorizedHashJoin struct {
    build    BatchProducer  // smaller table (build side)
    probe    BatchProducer  // larger table (probe side)
    buildKey int            // column index in build side
    probeKey int            // column index in probe side
    ht       *HashJoinTable
    done     bool
}
```

### [S7.2] HashJoinTable

```go
type HashJoinTable struct {
    // Key → list of row positions in build-side buffer
    Capacity uint32
    Occupied uint32
    Hashes   []uint64
    Keys     []int64
    Bitmap   []uint64
    RowIDs   [][]uint32  // each slot has list of build-side row indices
    BuildCols []UT.Column // materialized build columns (from all batches)
}
```

### [S7.3] Execution

**Build phase:**
1. Read all batches from build child, append columns to `BuildCols`
2. For each row: hash → insert into hash table (`RowIDs[slot] = append(RowIDs[slot], rowIdx)`)
3. Multiple rows with same key → list in the same slot

**Probe phase:**
1. Read batch from probe child
2. For each probe row: hash → probe hash table
3. For each matching build-side row: concatenate `buildRow + probeRow` → output batch
4. Output batch size: up to `UT.BatchSize` (may produce multiple output batches per probe batch)

### [S7.4] Constraints

- Inner equi-join only, single key column (MVP)
- Build side must fit in memory (no external hash join for MVP)
- NULL join keys → no match (standard SQL semantics)

---

## [S8] Operator Interface Unification

### [S8.1] New Interface

```go
type Operator interface {
    Next(ctx context.Context) (Row, error)
    NextBatch(ctx context.Context) (*UT.Batch, error)
    Close() error
}
```

### [S8.2] Migration

1. **Add `NextBatch` to interface** with a default adapter on `OperatorBase`:
   - Old row-based operators: `NextBatch` creates a 1-row batch per call via `Next`
   - New vectorized operators: `Next` extracts one row from `NextBatch` result
2. **Gradually migrate** row-based operators to native `NextBatch`:
   - `SeqScan` → read page → fill batch
   - `Filter` → apply `EvalBatch` → set selection vector → forward batch
   - `Project` → `VectorizedProject` replacement
   - `Sort` → collect batches → sort → emit batches
3. **Benchmark after each migration** to verify no regression

---

## [S9] Implementation Order

| Phase | Component | Files |
|-------|-----------|-------|
| 1 | Hash table + `VectorizedHashAggregate` (no-group) | New files in `AG/` |
| 2 | `EvalBatchExpr` for arithmetic, literals, column refs | `EV/eval_vec.go` |
| 3 | `VectorizedProject` | `OP/operators_vec.go` |
| 4 | `VectorizedHashAggregate` (GROUP BY) | Reuses hash table from phase 1 |
| 5 | `VectorizedHashJoin` | New files in `OP/` or `OP/join.go` |
| 6 | Interface unification (`NextBatch` on `Operator`) | `UT/batch.go`, all operators |

---

## [S10] Testing Strategy

- **Unit tests per kernel** — each `evalAddInt64`, `evalConcatString`, etc. tested with known inputs
- **Hash table tests** — insert, lookup, resize, collision chains, NULL handling
- **Operator tests** — `VectorizedHashAggregate` against known datasets, compare with row-based `Aggregate`
- **Integration tests** — full query path: `SELECT col, SUM(x) FROM t GROUP BY col` via executor
- **Benchmarks** — compare vectorized vs. row-based for each operator, target 2-5× speedup
- **Null handling** — every operator tested with NULL values in input columns

---

## [S11] Key References

- `UT/batch.go` — existing `Batch`, `ColumnData`, `BatchSize = 1024`, `sync.Pool`
- `EV/eval_vec.go` — existing `EvalBatch`, comparison kernels, selection vector logic
- `OP/operators_vec.go` — existing `VectorizedSeqScan`, `VectorizedFilter`, `BatchProducer` interface
- `AG/aggregate_vec.go` — existing `VectorizedCount`, `VectorizedSum`, `VectorizedMin`, `VectorizedMax`, `VectorizedAvg`
- DuckDB internals: `Vector` formats, open-addressing hash table, morsel-driven parallelism