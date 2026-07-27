# Map-Reduce Execution Model (REQ002113)

## Context

The SQL engine has 10+ pipeline-breaking operators (Aggregate, HashAggregate, VectorizedHashAggregate, Sort, Distinct, Window, CompoundOp, etc.) that independently implement the same conceptual three-phase pattern: **extract a key from each input row, group/partition by that key, then accumulate per-group state into output**. The fragmentation stems from three axes of variation — key extraction strategy, accumulation strategy, and data format (row vs batch) — each operator hardcodes a specific point in this 3D space.

Consequences:
- `DISTINCT` forces aggregate fallback from streaming to `ToRows()` + `EvalAggregateOver()` (7 of 7 queries in `evidence/slt_lang_aggfunc`)
- 5 separate aggregate code paths in `aggregate.go` + `aggregate_vec.go` (2767 lines total) with duplicated key hashing, accumulator updates, and result construction
- No unified `Reset()` for plan cache reuse (REQ002110)
- Adding a new aggregate function (e.g., `ARRAY_AGG`) requires touching all 5 paths
- Per-VectorizedXxx structs (VectorizedCount, VectorizedSum, VectorizedAvg, VectorizedMin, VectorizedMax) duplicate accumulator logic

The map-reduce model unifies these by decomposing every pipeline-breaker into three composable phases: **Map** (key + value extraction), **Shuffle** (grouping strategy), **Reduce** (accumulation + output). Each phase is a separate interface with multiple strategy implementations. The `MapReduceOperator` composes them and satisfies both `BatchProducer` and `pl.Operator`.

## Architecture

### Core Types

```
MapReduceOperator
  ├── child:  BatchProducer (input source)
  ├── mapper: Mapper        (key + value extraction)
  ├── shuffle: Shuffler     (grouping strategy)
  └── reducer: Reducer      (accumulation + output)
```

**Data flow:**
```
child.NextBatch() → mapper.MapBatch(batch, shuffle)  [drain all]
                        → shuffle.Accept(key, val)    [per row]
                            → reducer.Accumulate(slot, batch, row) [per group]
shuffle.Finalize() → []GroupDescriptor
reducer.Finalize(groups) → []*Batch (cached)
NextBatch() returns cached batches one at a time
```

### Key Interfaces

**Mapper** — extracts grouping keys and values from input batches:
```go
type Mapper interface {
    MapBatch(ctx context.Context, batch *UT.Batch, shuffle Shuffler) error
    Reset()
}
```

**Shuffler** — routes values to groups by key:
```go
type Shuffler interface {
    Accept(key MapKey, val MapValue) error
    AcceptBatch(keys []MapKey, vals []MapValue, n int) error  // hash fast path
    Finalize() ([]GroupDescriptor, error)
    Reset()
}
```

**Reducer** — accumulates per-group state, produces output:
```go
type Reducer interface {
    Init(spec ReduceSpec)
    Accumulate(slot int, batch *UT.Batch, rowIdx int) error
    Merge(dst, src int) error  // parallel partial aggregation
    Finalize(groups []GroupDescriptor) ([]*UT.Batch, error)
    Reset()
}
```

### Strategy Implementations

| Mapper | Shuffler | Reducer | Replaces |
|--------|----------|---------|----------|
| `AggregateMapper` | `ScalarShuffler` | `AggregateReducer` | Scalar aggregate streaming + fallback |
| `AggregateMapper` | `HashShuffler` | `AggregateReducer` | `VectorizedHashAggregate`, `HashAggregate`, GROUP BY path |
| `AggregateMapper` | `HashShuffler` | `AggregateReducer` + `HavingReducer` | Aggregate with HAVING |
| `DistinctMapper` | `DistinctShuffler` | `IdentityReducer` | `Distinct`, `VectorizedDistinct` |
| `SortMapper` | `SortShuffler` | `IdentityReducer` | `Sort`, `VectorizedSort` |
| `SortMapper` | `SortShuffler` | `TopNReducer` | `TopNSort`, `VectorizedTopNSort` |
| `WindowMapper` | `PartitionShuffler` | `WindowReducer` | `WindowOperator`, `VectorizedWindowOperator` |
| `CompoundMapper` | `DistinctShuffler` | `IdentityReducer` | UNION (dedup) |
| `CompoundMapper` | `ProbeShuffler` | `IdentityReducer` | EXCEPT/INTERSECT |

### UnifiedAccum — Single Accumulator Struct

Replaces `scalarAggAccum`, `aggPayload`, per-VectorizedXxx accumulators, and `Distinct`'s seen-set:

```go
type UnifiedAccum struct {
    // Numeric aggregate state
    Count   int64; SumI int64; SumF float64
    MinI    int64; MaxI int64; MinF float64; MaxF float64
    HasI, HasF, HasMin, HasMax, IsFloat bool
    
    // String aggregate state (GROUP_CONCAT/STRING_AGG)
    StrParts []string; StrSep string
    StrSeen  map[any]bool   // DISTINCT dedup for string aggs
    
    // DISTINCT dedup for numeric aggregates (lazily allocated)
    DistinctSets []map[int64]struct{}
    
    // Row collection (sort, distinct, window)
    Rows []RowRef
}
```

`Update(spec *AccumulatorSpec, batch *UT.Batch, rowIdx int)` — single method handles all aggregate kinds including DISTINCT. For DISTINCT, checks `DistinctSets[aggIdx]` before accumulating (same pattern as current `aggPayload.DistinctSeen`).

`ComputeResult(spec *AccumulatorSpec) any` — returns final value.

`Merge(other *UnifiedAccum, specs []AccumulatorSpec)` — combines partial states for parallel aggregation.

`Reset()` — clears all fields, releases maps to GC, prepares for reuse.

### KeyExtractor — Unified Key Extraction

```go
type KeyExtractor interface {
    ExtractKey(batch *UT.Batch, physIdx int) (MapKey, uint64)
    ExtractKeysBatch(batch *UT.Batch) (keys []int64, hashes []uint64, validRows []int)
    KeyStride() int
    IsScalar() bool
    Reset()
}
```

Implementations: `ScalarKeyExtractor`, `IntGroupKeyExtractor` (reuses scratch buffers per REQ001662), `DistinctKeyExtractor`, `SortKeyExtractor`, `PartitionKeyExtractor`.

### MapKey / MapValue

```go
type MapKey struct {
    Ints []int64    // flat-packed group-by key values
    Strs []string   // for string/partition keys
    hash uint64     // pre-computed hash
}

type MapValue struct {
    Batch  *UT.Batch  // borrowed reference (caller must not Put until MapBatch returns)
    RowIdx int        // physical row index in batch
}
```

## Migration Plan (9 Phases)

Each phase replaces one operator type with `MapReduceOperator`. Between phases, old and new operators coexist. Each phase is a separate commit with passing tests.

### Phase 1: VectorizedHashAggregate → MR (highest impact, lowest risk)

**Why first**: It's the most self-contained — already batch-native, already uses `UT.HashTable` + `aggPayload` + `AggDef`. Maps 1:1 to `AggregateMapper` + `HashShuffler` + `AggregateReducer`.

**Files to create**:
- `SQB/MR/mr.go` — `MapReduceOperator`, `NextBatch`, `Next`, `Close`, `Reset`
- `SQB/MR/mapper.go` — `Mapper` interface, `MapKey`, `MapValue`, `AggregateMapper`
- `SQB/MR/shuffle.go` — `Shuffler` interface, `GroupDescriptor`, `HashShuffler`
- `SQB/MR/reducer.go` — `Reducer` interface, `ReduceSpec`, `AccumulatorSpec`, `AggregateReducer`
- `SQB/MR/accum.go` — `UnifiedAccum`, `Update`, `ComputeResult`, `Merge`, `Reset`
- `SQB/MR/key.go` — `KeyExtractor` interface, `IntGroupKeyExtractor`, `ScalarKeyExtractor`
- `SQB/MR/mr_test.go`, `SQB/MR/accum_test.go`, `SQB/MR/key_test.go`, `SQB/MR/shuffle_test.go`

**Files to modify**:
- `SQB/EX/vec_transform.go` — `transformAggregate()` creates `MapReduceOperator` instead of `VectorizedHashAggregate`
- `SQB/EX/planner_select.go` — `planAggregation()` may need minor adjustment

**Verification**: `BenchmarkRazordata_SelectGroupBy` identical performance; SLT select1..4 pass; `-alloc_objects` no regression.

### Phase 2: Scalar aggregate streaming → MR

Replace `tryStreamingScalarAggregate` + scalar fallback path in `Aggregate.materialize()`.

**New files**: `SQB/MR/shuffle_scalar.go` — `ScalarShuffler`

**Modified files**: `SQB/AG/aggregate.go` — `materialize()` scalar path delegates to `MapReduceOperator`

**Key benefit**: DISTINCT aggregates no longer force fallback — `AggregateMapper` + `ScalarShuffler` + `AggregateReducer` handles DISTINCT natively via `UnifiedAccum.DistinctSets`.

**Verification**: `evidence/slt_lang_aggfunc` DISTINCT queries stay in streaming path; SLT pass.

### Phase 3: Aggregate GROUP BY path → MR

Replace `Aggregate.materialize()` GROUP BY path.

**Key complexity**: HAVING, `fullCols`, `expandStar`, `constCols` must transfer to `HavingReducer` wrapper and `AggregateReducer` configuration.

**New files**: `SQB/MR/reducer_having.go` — `HavingReducer`

**Modified files**: `SQB/AG/aggregate.go` — `materialize()` GROUP BY path delegates to `MapReduceOperator`

### Phase 4: Delete HashAggregate

`HashAggregate` (hashagg.go, 166 lines) is subsumed by Phase 3. Delete the file and update `vec_transform.go` + `planner_select.go`.

### Phase 5: Distinct → MR

Replace `Distinct` + `VectorizedDistinct`.

**New files**: `SQB/MR/mapper_distinct.go`, `SQB/MR/shuffle_distinct.go`, `SQB/MR/reducer_identity.go`

**Modified files**: `SQB/OP/distinct.go`, `SQB/OP/operators_vec.go`, `SQB/EX/vec_transform.go`

### Phase 6: Sort / TopNSort → MR

Replace `Sort`, `VectorizedSort`, `TopNSort`, `VectorizedTopNSort`.

**New files**: `SQB/MR/mapper_sort.go`, `SQB/MR/shuffle_sort.go`, `SQB/MR/reducer_topn.go`

**Modified files**: `SQB/OP/intermediate_sort.go`, `SQB/OP/sort_vec.go`, `SQB/OP/topn_sort.go`, `SQB/EX/vec_transform.go`

### Phase 7: Window → MR

Replace `WindowOperator` + `VectorizedWindowOperator`.

**New files**: `SQB/MR/mapper_window.go`, `SQB/MR/shuffle_partition.go`, `SQB/MR/reducer_window.go`

**Modified files**: `SQB/AG/window.go`, `SQB/AG/window_vec.go`, `SQB/EX/vec_transform.go`

### Phase 8: CompoundOp → MR

Replace UNION/EXCEPT/INTERSECT paths.

**New files**: `SQB/MR/mapper_compound.go`, `SQB/MR/shuffle_probe.go`

**Modified files**: `SQB/OP/compound.go`, `SQB/OP/operators_vec.go`, `SQB/EX/vec_transform.go`

**Key subtlety**: UNION ALL is streaming (no pipeline-break). It stays as-is or uses `Mapper` with a pass-through shuffler that doesn't materialize.

### Phase 9: Parallel aggregation → ParallelMapReduceOperator

Replace `ParallelHashAggregate`. Each worker runs its own `MapReduceOperator`; partial results merged via `UnifiedAccum.Merge()`.

**New files**: `SQB/MR/parallel.go`

**Modified files**: `SQB/AG/hashagg_parallel.go` (deleted), `SQB/EX/vec_transform.go`

## REQ Impact

After full migration, the following REQs are addressed or superseded:

| REQ | Status | Why |
|-----|--------|-----|
| REQ002107 (streaming DISTINCT) | **Superseded** | `UnifiedAccum.DistinctSets` handles DISTINCT natively in streaming path |
| REQ002112 (pool materialize maps) | **Superseded** | `Shuffler`/`Reducer` state lives on `MapReduceOperator` struct, `Reset()` clears without `make()` |
| REQ002110 (single-shot cached plan) | **Superseded** | `MapReduceOperator.Reset()` provides clean plan cache reuse |
| REQ002111 (NOT INDEXED shortcut) | **Unaffected** | Planner concern, not execution |

## Package Layout

```
internal/SQB/MR/
    mr.go                 # MapReduceOperator: NextBatch, Next, Close, Reset
    mapper.go             # Mapper interface, MapKey, MapValue, AggregateMapper
    mapper_distinct.go    # DistinctMapper
    mapper_sort.go        # SortMapper
    mapper_window.go      # WindowMapper
    mapper_compound.go    # CompoundMapper
    shuffle.go            # Shuffler interface, GroupDescriptor
    shuffle_scalar.go     # ScalarShuffler
    shuffle_hash.go       # HashShuffler (uses UT.HashTable)
    shuffle_sort.go       # SortShuffler
    shuffle_distinct.go   # DistinctShuffler
    shuffle_partition.go  # PartitionShuffler
    shuffle_probe.go      # ProbeShuffler (EXCEPT/INTERSECT)
    reducer.go            # Reducer interface, ReduceSpec, AccumulatorSpec
    reducer_aggregate.go  # AggregateReducer (wraps UnifiedAccum)
    reducer_having.go     # HavingReducer (wraps Reducer, filters by HAVING)
    reducer_identity.go   # IdentityReducer (pass-through for sort/distinct)
    reducer_window.go     # WindowReducer
    reducer_topn.go       # TopNReducer (min-heap bounded)
    accum.go              # UnifiedAccum: Update, ComputeResult, Merge, Reset
    key.go                # KeyExtractor interface, MapKey.Hash()
    key_int.go            # IntGroupKeyExtractor (scratch-buffer batch extraction)
    key_scalar.go         # ScalarKeyExtractor
    key_distinct.go       # DistinctKeyExtractor
    key_sort.go           # SortKeyExtractor
    key_partition.go      # PartitionKeyExtractor
    parallel.go           # ParallelMapReduceOperator
    mr_test.go            # end-to-end tests per operator type
    accum_test.go         # UnifiedAccum unit tests
    key_test.go           # KeyExtractor unit tests
    shuffle_test.go       # Shuffler unit tests
    parallel_test.go      # parallel map-reduce tests
```

## Key Reuse Points

- `UT.HashTable` + `Probe`/`ProbeInt64` → `HashShuffler` uses directly
- `UT.Batch` + `GetBatch`/`Batch.Put()` → `Reducer.Finalize()` produces pooled batches
- `UT.WorkerPool` + `ParallelFanOut` → `ParallelMapReduceOperator`
- `BatchToRowAdapter` → `MapReduceOperator.Next()` adapter
- `AggDef` struct → `AccumulatorSpec` (renamed, same fields)
- `aggPayload` → `UnifiedAccum` (superset)
- Scratch buffer pattern (REQ001662) → `IntGroupKeyExtractor.ExtractKeysBatch()`
- `DistinctKey()` encoding → `DistinctKeyExtractor`
- `EV.EvalBatch` / `EvalBatchExpr` → unchanged (used by Filter/Project, not MR)

## Verification

Per-phase:
1. `go test ./internal/SQB/MR/... -race -count=1`
2. `go test ./internal/... -race -count=1`
3. `./before-commit-cases.sh` (internal + SLT select1..4)
4. Benchmarks for replaced operators show no regression
5. `-alloc_objects` profile shows expected reductions

End-to-end (after all phases):
- Full SLT corpus (385 files) passes
- All per-VectorizedXxx benchmarks replaced by MR benchmarks
- `SQB/AG/aggregate_vec.go` individual VectorizedCount/Sum/Avg/Min/Max deleted (subsumed by AggregateReducer)
- `SQB/AG/hashagg.go` deleted
- `SQB/AG/hashagg_parallel.go` deleted
