# Iteration19 — SQL SIMD + Parallel Execution Foundation

**Subsystem:** `SQL/EX` (Executor)
**Status:** planned
**Est. LOC:** ~3,500-4,500 (3 phases)
**Requirements:** REQ000144, REQ000145, REQ000149, REQ000157
**Target release:** v0.14.0 → v0.15.0 → v0.16.0 (phased)
**Commit:** `<filled at completion>`
**Tag:** v0.14.0 (Phase 1), v0.15.0 (Phase 2), v0.16.0 (Phase 3)

## Overview

Implements the foundation for high-performance query execution via
**SIMD vectorization** and **data parallelism**. This iteration
transforms Razordata from a row-at-a-time executor to a modern
vectorized+parallel engine capable of 5-10x throughput improvements
on analytical workloads.

**Phase 1 (v0.14.0) — Columnar Batches + Vectorized Filter.**
Introduces columnar batch representation (`Batch` struct), memory
pooling (`sync.Pool`), and vectorized predicate evaluation. Target:
4-6x speedup on filter-heavy queries.

**Phase 2 (v0.15.0) — Parallel Table Scan + Pipeline.**
Adds worker pool coordination, parallel `SeqScan`/`IndexScan`,
and pipeline parallelism between operator stages. Target: 8-12x
speedup on 4-core systems for large scans.

**Phase 3 (v0.16.0) — Parallel Aggregates + Sort.**
Completes the foundation with SIMD-accelerated aggregates
(`COUNT`, `SUM`, `AVG`, `MIN`, `MAX`) and parallel sample sort.
Target: TPC-H Q1-6 benchmarks showing 5-10x improvement vs.
row-at-a-time baseline.

## Outcome

(empty — to be filled at end of iteration)

## Dependencies

- Required: iter-08 (`SQL/EX` operators exist)
- Required: iter-12 (catalog persistence)
- Required: iter-17 (WAL batch commit — TXN integration ready)
- Touches:
  - `internal/SQL/EX/batch.go` (new) — columnar batch mgmt
  - `internal/SQL/EX/eval_vec.go` (new) — vectorized eval
  - `internal/SQL/EX/operators_vec.go` (new) — vectorized ops
  - `internal/SQL/EX/parallel.go` (new) — worker pool
  - `internal/SQL/EX/operators_parallel.go` (new) — parallel ops
  - `internal/SQL/EX/pipeline.go` (new) — pipeline exec
  - `internal/SQL/EX/aggregate_vec.go` (new) — SIMD aggregates
  - `internal/SQL/EX/sort_parallel.go` (new) — parallel sort
  - `internal/SQL/EX/operators.go` — extend with batch interfaces
  - `internal/SQL/EX/eval.go` — add `EvalBatch` helper
  - `internal/MEM/SP/sp.go` — optional: expose for batch alloc
  - `internal/SQL/EX/*_test.go` — extensive new test coverage
  - `internal/SQL/EX/*_bench.go` — performance benchmarks

## Design Specification

### Phase 1 (v0.14.0) — Columnar Foundation

#### 1.1 Columnar Batch Representation (`batch.go`)

```go
// Batch represents a columnar batch of up to 1024 rows.
// Memory is pooled via sync.Pool to avoid per-batch allocation.
type Batch struct {
    // Cols holds column data in columnar layout.
    // Each column is typed: []int64, []float64, []string, etc.
    Cols []Column
    
    // Selection vector for filtered output.
    // If nil, all rows ( batchSize ) are valid.
    Sel []uint16
    
    // Number of rows in this batch (<= 1024)
    Size int
    
    // Pooled: if true, return to pool via batchPool.Put(b)
    Pooled bool
}

// Column is a typed container for batch column data.
// Uses interface{} to avoid generics pre-Go1.22, but
// actual storage is concrete slices.
type Column struct {
    Type   LX.TokenType
    Data   any  // []int64, []float64, []string, []bool, []byte
    Nulls  []bool  // optional null bitmap
}

// Global batch pool for reuse.
var batchPool = sync.Pool{
    New: func() any {
        return &Batch{
            Cols: make([]Column, MaxColumns),
            Sel:  make([]uint16, BatchSize),
        }
    },
}

// BatchSize is the default number of rows per batch.
const BatchSize = 1024

// GetBatch retrieves a batch from the pool.
func GetBatch(cols int) *Batch {
    b := batchPool.Get().(*Batch)
    b.Size = 0
    b.Sel = nil
    b.Pooled = true
    for i := 0; i < cols; i++ {
        b.Cols[i].Data = nil
        b.Cols[i].Nulls = nil
    }
    return b
}

// Put returns a batch to the pool.
func (b *Batch) Put() {
    if b.Pooled {
        b.Pooled = false
        batchPool.Put(b)
    }
}
```

**Key behaviors:**
- `BatchSize = 1024` rows balances cache locality vs. overhead.
- `Sel` enables filter pushdown without copying data.
- `sync.Pool` eliminates allocation in hot path.
- Caller responsibility: `batch.Put()` after consumption.

**REQ000149 satisfied:** Columnar batch memory management with
`sync.Pool` for 1024-row batches.

---

#### 1.2 Vectorized Expression Evaluation (`eval_vec.go`)

```go
// EvalBatch evaluates a predicate expression over an entire
// batch, producing a selection vector of matching rows.
// Returns nil if all rows match, empty slice if none match.
func EvalBatch(expr PS.Expr, batch *Batch, params []any) []uint16 {
    switch e := expr.(type) {
    case *PS.BinaryExpr:
        return evalBinaryBatch(e, batch, params)
    case *PS.UnaryExpr:
        return evalUnaryBatch(e, batch, params)
    // ... extend as needed
    default:
        // Fallback: row-at-a-time evaluation
        return evalBatchFallback(expr, batch, params)
    }
}

// evalBinaryBatch handles comparison operators with manual
// SIMD-style unrolling (4-wide for L1 cache efficiency).
func evalBinaryBatch(e *PS.BinaryExpr, batch *Batch, params []any) []uint16 {
    // Fast path: both operands are column references
    leftCol, leftOK := extractColumn(e.Left, batch)
    rightCol, rightOK := extractColumn(e.Right, batch)
    
    if leftOK && rightOK {
        // Vectorized column-column comparison
        return compareCols(leftCol, rightCol, e.Op)
    }
    
    // Fast path: left is column, right is literal
    if leftOK && isLiteral(e.Right) {
        litVal, _ := Eval(e.Right, nil, params)
        return compareColLiteral(leftCol, litVal, e.Op)
    }
    
    // Fallback: row-at-a-time
    return evalBatchFallback(e, batch, params)
}

// compareCols performs 4-wide unrolled comparison for int64 columns.
// Manual SIMD: process 4 elements per iteration for L1 cache efficiency.
func compareCols(left, right Column, op int) []uint16 {
    switch left.Type {
    case LX.T_INT_KW, LX.T_BIGINT:
        l := left.Data.([]int64)
        r := right.Data.([]int64)
        sel := make([]uint16, 0, len(l))
        
        // Manual unrolling: 4 elements per iteration
        n := len(l)
        i := 0
        for i+4 <= n {
            v0, v1, v2, v3 := l[i], l[i+1], l[i+2], l[i+3]
            w0, w1, w2, w3 := r[i], r[i+1], r[i+2], r[i+3]
            
            if op == int(LX.T_EQ) {
                if v0 == w0 { sel = append(sel, uint16(i)) }
                if v1 == w1 { sel = append(sel, uint16(i+1)) }
                if v2 == w2 { sel = append(sel, uint16(i+2)) }
                if v3 == w3 { sel = append(sel, uint16(i+3)) }
            } else if op == int(LX.T_GT) {
                if v0 > w0 { sel = append(sel, uint16(i)) }
                if v1 > w1 { sel = append(sel, uint16(i+1)) }
                if v2 > w2 { sel = append(sel, uint16(i+2)) }
                if v3 > w3 { sel = append(sel, uint16(i+3)) }
            }
            // ... handle other operators
            i += 4
        }
        
        // Tail: remaining elements
        for ; i < n; i++ {
            if compareValues(l[i], r[i], op) {
                sel = append(sel, uint16(i))
            }
        }
        return sel
    }
    // Fallback for other types
    return evalBatchFallback(...)
}
```

**Key behaviors:**
- 4-wide manual unrolling exploits L1 cache (avoid loop overhead).
- Column-column and column-literal fast paths avoid row iteration.
- Returns selection vector (`[]uint16`) — no data copying.
- Fall back to row-at-a-time for complex expressions.

**REQ000157 satisfied:** Expression evaluation SIMD acceleration
(batch predicate evaluation via manual unrolling).

---

#### 1.3 Vectorized Operators (`operators_vec.go`)

```go
// VectorizedFilter wraps a child operator and applies
// filter predicates using batch processing.
type VectorizedFilter struct {
    child Operator
    pred  PS.Expr
}

func (f *VectorizedFilter) NextBatch() (*Batch, error) {
    for {
        batch := f.child.NextBatch()
        if batch == nil || batch.Size == 0 {
            return nil, nil
        }
        
        // Apply filter via vectorized evaluation
        sel := EvalBatch(f.pred, batch, nil)
        
        if len(sel) == 0 {
            // No rows match, skip this batch
            batch.Put()
            continue
        }
        
        if sel == nil {
            // All rows match, return as-is
            return batch, nil
        }
        
        // Partial match: update selection vector
        batch.Sel = sel
        batch.Size = len(sel)
        return batch, nil
    }
}

// VectorizedSeqScan performs columnar table scan.
type VectorizedSeqScan struct {
    table   string
    cols    []string
    cursor  uint64
    endKey  uint64
    batch   *Batch
}

func (s *VectorizedSeqScan) NextBatch() (*Batch, error) {
    if s.cursor >= s.endKey {
        return nil, nil
    }
    
    // Allocate batch from pool
    batch := GetBatch(len(s.cols))
    batch.Size = 0
    
    // Scan up to BatchSize rows
    for i := 0; i < BatchSize && s.cursor < s.endKey; i++ {
        row, err := scanKey(s.table, s.cursor)
        if err != nil {
            batch.Put()
            return nil, err
        }
        
        // Store in columnar layout
        for colIdx, colName := range s.cols {
            val := row[colName]
            switch v := val.(type) {
            case int64:
                if batch.Cols[colIdx].Data == nil {
                    batch.Cols[colIdx].Data = make([]int64, BatchSize)
                }
                batch.Cols[colIdx].Data.([]int64)[i] = v
            case string:
                if batch.Cols[colIdx].Data == nil {
                    batch.Cols[colIdx].Data = make([]string, BatchSize)
                }
                batch.Cols[colIdx].Data.([]string)[i] = v
            // ... handle other types
            }
        }
        batch.Size++
        s.cursor++
    }
    
    return batch, nil
}
```

**Key behaviors:**
- `VectorizedFilter` uses selection vectors (no copying).
- `VectorizedSeqScan` produces columnar batches directly.
- Pool-based allocation minimizes GC pressure.

**REQ000144 satisfied:** SIMD vectorized execution with batch
processing and manual unrolling.

---

### Phase 2 (v0.15.0) — Parallel Execution

#### 2.1 Worker Pool (`parallel.go`)

```go
// WorkerPool coordinates parallel query execution.
type WorkerPool struct {
    workers   int
    taskQueue chan Task
    wg        sync.WaitGroup
    ctx       context.Context
    cancel    context.CancelFunc
}

// Task represents a unit of parallel work.
type Task func() (Result, error)

// NewWorkerPool creates a pool with N workers.
func NewWorkerPool(n int) *WorkerPool {
    ctx, cancel := context.WithCancel(context.Background())
    wp := &WorkerPool{
        workers:   n,
        taskQueue: make(chan Task, n*2),
        ctx:       ctx,
        cancel:    cancel,
    }
    
    // Spawn workers
    for i := 0; i < n; i++ {
        wp.wg.Add(1)
        go wp.worker(i)
    }
    
    return wp
}

func (wp *WorkerPool) worker(id int) {
    defer wp.wg.Done()
    for {
        select {
        case <-wp.ctx.Done():
            return
        case task := <-wp.taskQueue:
            task()  // Execute and handle result
        }
    }
}

// Submit adds a task to the queue.
func (wp *WorkerPool) Submit(task Task) {
    wp.taskQueue <- task
}

// Close shuts down the pool.
func (wp *WorkerPool) Close() {
    wp.cancel()
    wp.wg.Wait()
}
```

---

#### 2.2 Parallel SeqScan (`operators_parallel.go`)

```go
// ParallelSeqScan splits table scan into N partitions.
type ParallelSeqScan struct {
    table    string
    cols     []string
    startKey uint64
    endKey   uint64
    pool     *WorkerPool
}

func (s *ParallelSeqScan) NextBatch() (*Batch, error) {
    // Split key range into N partitions
    rangeSize := (s.endKey - s.startKey) / uint64(s.pool.workers)
    
    // Fan-out: submit scan tasks
    resultChan := make(chan *Batch, s.pool.workers)
    var mu sync.Mutex
    var firstErr error
    
    for i := 0; i < s.pool.workers; i++ {
        pStart := s.startKey + uint64(i)*rangeSize
        pEnd := pStart + rangeSize
        if i == s.pool.workers-1 {
            pEnd = s.endKey  // Last partition takes remainder
        }
        
        s.pool.Submit(func() (Result, error) {
            batch := scanPartition(s.table, s.cols, pStart, pEnd)
            mu.Lock()
            if firstErr == nil {
                resultChan <- batch
            }
            mu.Unlock()
            return nil, nil
        })
    }
    
    // Fan-in: merge results
    // (simplified: return first batch, rest in subsequent calls)
    batch := <-resultChan
    return batch, firstErr
}

func scanPartition(table string, cols []string, start, end uint64) *Batch {
    batch := GetBatch(len(cols))
    // ... scan logic same as VectorizedSeqScan
    return batch
}
```

**Parallel IndexScan:** Similar pattern, split B-tree range into
sub-ranges, each worker traverses its partition.

---

#### 2.3 Pipeline Parallelism (`pipeline.go`)

```go
// Pipeline represents a chain of operators with buffered
// handoff between stages.
type Pipeline struct {
    stages []Operator
    bufs   []chan *Batch
}

// NewPipeline creates a pipeline with bounded buffers.
func NewPipeline(stages []Operator, bufSize int) *Pipeline {
    bufs := make([]chan *Batch, len(stages)+1)
    for i := range bufs {
        bufs[i] = make(chan *Batch, bufSize)
    }
    return &Pipeline{stages: stages, bufs: bufs}
}

// Run starts all stages concurrently.
func (p *Pipeline) Run(ctx context.Context) <-chan *Batch {
    out := p.bufs[len(p.stages)]
    
    // Start each stage as a goroutine
    for i, stage := range p.stages {
        in := p.bufs[i]
        outCh := p.bufs[i+1]
        go func(op Operator, inCh, outCh chan *Batch) {
            defer close(outCh)
            for batch := range inCh {
                result := op.NextBatch(batch)
                if result != nil {
                    outCh <- result
                }
            }
        }(stage, in, outCh)
    }
    
    return out
}
```

---

### Phase 3 (v0.16.0) — Advanced Operators

#### 3.1 Vectorized Aggregates (`aggregate_vec.go`)

```go
// VectorizedCount accumulates counts per batch.
type VectorizedCount struct {
    child Operator
    total int64
}

func (a *VectorizedCount) NextBatch() (*Batch, error) {
    for {
        batch := a.child.NextBatch()
        if batch == nil {
            return nil, nil
        }
        
        // Count matching rows (use Size if no filter)
        if batch.Sel == nil {
            a.total += int64(batch.Size)
        } else {
            a.total += int64(len(batch.Sel))
        }
        batch.Put()
    }
}

// VectorizedSum computes sum with 4-wide unrolled accumulation.
type VectorizedSum struct {
    child Operator
    colIdx int
    sum    float64
}

func (a *VectorizedSum) NextBatch() (*Batch, error) {
    for {
        batch := a.child.NextBatch()
        if batch == nil {
            return nil, nil
        }
        
        col := batch.Cols[a.colIdx]
        
        // SIMD-style accumulation
        switch col.Type {
        case LX.T_INT_KW:
            data := col.Data.([]int64)
            for i := 0; i < len(data); i += 4 {
                if i+4 <= len(data) {
                    a.sum += float64(data[i] + data[i+1] + data[i+2] + data[i+3])
                } else {
                    for ; i < len(data); i++ {
                        a.sum += float64(data[i])
                    }
                }
            }
        }
        batch.Put()
    }
}
```

---

#### 3.2 Parallel Sort (`sort_parallel.go`)

```go
// ParallelSort uses sample sort algorithm:
// 1. Sample input to find splitters
// 2. Partition input by splitters
// 3. Each worker sorts its partition
// 4. Merge sorted partitions

type ParallelSort struct {
    child   Operator
    orderBy []OrderItem
    pool    *WorkerPool
}

func (s *ParallelSort) NextBatch() (*Batch, error) {
    // Collect all batches (materialize for sort)
    batches := collectBatches(s.child)
    
    if len(batches) == 0 {
        return nil, nil
    }
    
    // Sample to find splitters
    splitters := findSplitters(batches, s.pool.workers)
    
    // Partition by splitters
    partitions := partitionBySplitters(batches, splitters)
    
    // Sort each partition in parallel
    sorted := make([]*Batch, len(partitions))
    var wg sync.WaitGroup
    
    for i, part := range partitions {
        wg.Add(1)
        go func(idx int, p []*Batch) {
            defer wg.Done()
            sorted[idx] = mergeSort(p, s.orderBy)
        }(i, part)
    }
    
    wg.Wait()
    
    // Return merged result one batch at a time
    return mergeBatches(sorted)
}
```

---

## Current State (audit, 2026-06-09)

**Vectorized execution gap.** The current `SQL/EX` executor is
row-at-a-time: `operators.go` processes one row per `Next()` call,
`eval.go` evaluates expressions on scalar values. For analytical
workloads (`SELECT COUNT(*) FROM t WHERE x > 100` with 1M rows),
this results in:
- 1M individual allocations (one `*Row` per iteration)
- 1M expression evaluations (no batch optimization)
- No SIMD-friendly memory layout (row-based, not columnar)

**Parallel execution gap.** No worker pool or partitioning logic
exists. `SeqScan` is single-threaded regardless of table size.
On 4-core systems, CPU utilization is ~25% for scan-heavy queries.

**Memory management gap.** No `sync.Pool` for batch reuse. Each
operator allocates fresh memory per call, increasing GC pressure
and reducing throughput under load.

---

## Implementation Plan

### Phase 1 (v0.14.0) — Foundation

| Step | Task | LOC | Tests | Benchmarks |
|------|------|-----|-------|------------|
| 1.1 | `batch.go`: Batch struct + pool | 200 | `batch_test.go` | `BenchmarkBatchPool` |
| 1.2 | `eval_vec.go`: EvalBatch for comparisons | 400 | `eval_vec_test.go` | `BenchmarkEvalBatch` |
| 1.3 | `operators_vec.go`: VectorizedFilter, VectorizedSeqScan | 500 | `operators_vec_test.go` | `BenchmarkVectorizedFilter` |
| 1.4 | Integration tests | - | `e2e_vec_test.go` | - |

**Phase 1 Completion Criteria:**
- `SELECT * FROM t WHERE x > 100` (100K rows) shows 4-6x speedup
- Zero allocation in hot path (`-allocs_per_op=0`)
- All tests green with `-race`

**Phase 1 Estimate:** ~1,100 LOC, 5-7 days

---

### Phase 2 (v0.15.0) — Parallelism

| Step | Task | LOC | Tests | Benchmarks |
|------|------|-----|-------|------------|
| 2.1 | `parallel.go`: WorkerPool | 250 | `parallel_test.go` | `BenchmarkWorkerPool` |
| 2.2 | `operators_parallel.go`: ParallelSeqScan, ParallelIndexScan | 350 | `operators_parallel_test.go` | `BenchmarkParallelSeqScan` |
| 2.3 | `pipeline.go`: Pipeline parallelism | 300 | `pipeline_test.go` | `BenchmarkPipeline` |
| 2.4 | Strong scaling tests | - | `scalability_test.go` | 1/2/4/8 core benchmarks |

**Phase 2 Completion Criteria:**
- `ParallelSeqScan` scales linearly to GOMAXPROCS
- Pipeline reduces latency by 20-30% vs. serial
- No race conditions under `-race -count=10`

**Phase 2 Estimate:** ~900 LOC, 5-7 days

---

### Phase 3 (v0.16.0) — Advanced Operators

| Step | Task | LOC | Tests | Benchmarks |
|------|------|-----|-------|------------|
| 3.1 | `aggregate_vec.go`: SIMD aggregates | 300 | `aggregate_vec_test.go` | `BenchmarkVectorizedAgg` |
| 3.2 | `sort_parallel.go`: Parallel Sort | 400 | `sort_parallel_test.go` | `BenchmarkParallelSort` |
| 3.3 | TPC-H Q1-6 benchmarks | - | - | Full benchmark suite |
| 3.4 | Documentation + tuning guide | - | - | — |

**Phase 3 Completion Criteria:**
- TPC-H Q1 (`SELECT COUNT(*) WHERE ...`) shows 8-10x speedup
- TPC-H Q6 (`SELECT SUM(...) WHERE ...`) shows 6-8x speedup
- Sort performance: 1M rows in <500ms (vs. 2s baseline)

**Phase 3 Estimate:** ~700 LOC (+300 test/bench), 4-6 days

---

## Deviations / Risks

1. **Go generics availability.** If Go version is <1.22, column
   handling requires `interface{}` assertions. Mitigation: use
   type-switch helpers, document performance cost.

2. **AVX2/NEON intrinsics complexity.** True SIMD requires assembly
   or `golang.org/x/sys/cpu`. Mitigation: start with manual unrolling
   (provides 50-70% of benefit), add intrinsics as follow-up.

3. **Memory overhead.** Columnar batches may increase peak memory
   usage for wide-table queries. Mitigation: `BatchSize` tuning,
   document trade-offs.

4. **Query planner integration.** Vectorized operators may have
   different cost characteristics. Mitigation: Phase 2 adds
   `Cost()` method to operators for planner comparison.

5. **Amdahl's Law.** Point queries (e.g., `SELECT * WHERE id=1`)
   won't benefit. Mitigation: auto-fallback to row-at-a-time for
   small result sets (<100 rows).

---

## Completion Criteria

| Rule | Phase 1 (v0.14.0) | Phase 2 (v0.15.0) | Phase 3 (v0.16.0) |
|------|-------------------|-------------------|-------------------|
| `go vet ./...` zero warnings | green | green | green |
| `gofmt -s -l .` no drift | green | green | green |
| `go test ./... -race -count=1` all green | green | green | green |
| Columnar batch pool implemented | green | green | green |
| VectorizedFilter shows 4-6x speedup | green | green | green |
| ParallelSeqScan scales to GOMAXPROCS | - | green | green |
| Pipeline parallelism implemented | - | green | green |
| SIMD aggregates (COUNT/SUM/AVG) | - | - | green |
| Parallel Sort implemented | - | - | green |
| TPC-H Q1-6 benchmarks (8-10x improvement) | - | - | green |

---

## Benchmarks

Run `go test -bench=. -benchmem ./internal/SQL/EX/` and compare:

| Benchmark | Baseline (row) | Phase 1 (vec) | Phase 2 (+para) | Phase 3 (+agg) |
|-----------|----------------|---------------|-----------------|----------------|
| `BenchmarkSeqScan_100K` | 1.0x | 1.5x | 4.0x | 4.0x |
| `BenchmarkFilter_100K` | 1.0x | 4.0x | 8.0x | 8.0x |
| `BenchmarkAgg_SUM_1M` | 1.0x | 2.5x | 7.5x | 10.0x |
| `BenchmarkSort_1M` | 1.0x | 1.0x | 3.0x | 4.0x |

**Test hardware:** 4-core (8-thread) system, 32 GB RAM, SSD storage.

---

## Future Enhancements (post-iter-19)

- **REQ000151:** Parallel HashJoin (sharded hash tables)
- **REQ000173:** SIMD intrinsics (AVX2/NEON via Go asm)
- **Morsel execution:** Dynamic work stealing (vs. static partitioning)
- **Query scheduler:** Inter-query parallelism (`SYS/AP` coordination)
- **Code generation:** LLVM IR or Go source gen for hot operators
