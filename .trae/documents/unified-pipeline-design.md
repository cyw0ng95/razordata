# Unified Pipeline Execution Model

## Context

The SQL engine currently has **9 stages across 6 packages with 5+ execution models**:

```
SQL text → [Parse] → [StmtCache] → [Rewrite] → [Plan] → [PlanCache] → [ResolveSlots] → [Vectorize] → [Execute] → [Deliver]
   PS         EX         RE            EX           EX          EX              EX              EX           EX         DS
```

Key problems:
1. **Vectorization runs on every execution** — the vectorized tree is NOT cached, so `tryVectorizePlan` rebuilds it from scratch even for identical SQL (ex.go:1240, 1403)
2. **5+ execution models coexist** — row pull, batch pull, push pipeline, fused scan, streaming iterator — each with separate operator types and wiring logic
3. **15+ operator structs** — `SeqScan`, `Filter`, `Project`, `FilterProject`, `Aggregate`, `HashAggregate`, `VectorizedHashAggregate`, `Sort`, `VectorizedSort`, `Distinct`, `VectorizedDistinct`, `Window`, `CompoundOp`, `Limit`, `Offset`, plus their vectorized variants
4. **3 separate caches** — stmtCache (parsed AST), planCache (operator tree by memo key), textPlanCache (operator tree by SQL text) — each with different invalidation rules
5. **No unified Reset()** — cached plans can't be reused cleanly; AdaptiveOp wraps to protect against mutation

## The Unified Model: Stage Pipeline

### Core Concept

Every SQL execution is a **Pipeline** — a DAG of **Stages**. A Stage is the only operator type. There are no separate "row operator", "batch operator", "push operator", or "vectorized operator" interfaces.

```go
// Stage is the sole operator interface in the unified pipeline model.
// Every SQL operator — scan, filter, aggregate, sort, join — implements Stage.
type Stage interface {
    // NextBatch returns the next output batch.
    // Streaming stages: pull one batch from child, transform, return.
    // Breaking stages: first call drains all input and caches results; 
    // subsequent calls return from cache.
    NextBatch(ctx context.Context) (*UT.Batch, error)
    
    // Reset clears runtime state for plan cache reuse.
    // Returns the Stage to "pre-execution" state without deallocating
    // buffers, hash tables, or scratch space.
    Reset(ctx context.Context) error
    
    // Close releases all resources.
    Close() error
}
```

### Stage Categories

There are exactly **4** stage categories, distinguished by behavior not by interface:

| Category | NextBatch behavior | IsBreaking | Examples |
|----------|-------------------|------------|----------|
| **Source** | Reads from table store/memory, emits batches | No | SeqScan, IndexScan |
| **Transform** | 1 batch in → 0-1 batch out, streaming | No | Filter, Project, Having, Limit, Offset, JoinProbe |
| **MapReduce** | N batches in → M batches out, breaks pipeline | Yes | Aggregate, Sort, Distinct, Window, JoinBuild, CompoundOp |
| **Sink** | Consumes batches, delivers to caller | No | ResultCollector |

All implement the same `Stage` interface. The `IsBreaking()` flag is metadata for the pipeline executor to optimize scheduling — it's not part of the interface.

### Why This Works

The current code ALREADY uses this pattern implicitly:

- `Aggregate.Next()` calls `materialize()` on first invocation → then returns from `buf[pos++]` → this IS MapReduce
- `Sort.Next()` materializes all rows on first call → then returns `buf[pos++]` → this IS MapReduce
- `Filter.Next()` pulls from child, filters, returns → this IS Transform
- `SeqScan.NextBatch()` reads from store, emits batch → this IS Source

The unified model just makes it explicit and consistent.

### Pipeline Structure

```
Pipeline = DAG of Stages connected by NextBatch() calls

Simple query:
  Source ──→ Transform ──→ MapReduce ──→ Transform ──→ Sink
  (Scan)    (Filter)       (Aggregate)   (Having)      (Deliver)

Join query:
  Source₁ ──→ Transform ──→ ┐
                              ├──→ MapReduce ──→ Transform ──→ Sink
  Source₂ ──→ Transform ──→ ┘     (JoinBuild     (JoinProbe)
                                 + Probe)
```

Each `→` is a `NextBatch()` call. The Pipeline executor walks the DAG and calls `NextBatch()` on the root stage, which recursively pulls from children.

## The Complete SQL Pipeline

### Compile Phase (runs once per unique SQL, cacheable)

```
SQL text
  │
  ▼
┌─────────────────────────────────────────────────────┐
│                  PipelineBuilder                      │
│                                                      │
│  ┌─────┐    ┌──────┐    ┌──────┐    ┌────────────┐  │
│  │Parse│───►│Rewrite│──►│ Plan │───►│Specialize  │  │
│  │     │    │      │    │      │    │(vectorize+ │  │
│  │     │    │      │    │      │    │ resolve+   │  │
│  │     │    │      │    │      │    │ fuse)      │  │
│  └──┬──┘    └──┬───┘    └──┬───┘    └─────┬──────┘  │
│     │          │           │              │          │
│     ▼          ▼           ▼              ▼          │
│  StmtCache  (discard)  PlanCache    PipelineSpec     │
│  (PS.Stmt)            (memo key)   (final artifact)  │
│                                      │              │
└──────────────────────────────────────┼──────────────┘
                                       │
                                       ▼
                              ┌────────────────┐
                              │  PipelineCache  │
                              │ (by memo key    │
                              │  or SQL text)   │
                              └───────┬────────┘
                                      │
```

### Runtime Phase (runs per execution, instantiates fresh state)

```
┌─────────────────────────────────────────────┐
│              PipelineExecutor                │
│                                             │
│  PipelineSpec ──► Instantiate ──► Pipeline  │
│                   (NewRuntime     (live DAG  │
│                    per Stage)     of Stages)  │
│                        │              │      │
│                        ▼              ▼      │
│                   Execute ──► Result Rows    │
│                   (NextBatch                  │
│                    on root)                   │
│                        │                     │
│                   Reset (for cache reuse)    │
└─────────────────────────────────────────────┘
```

### Key Design: PipelineSpec vs Pipeline

The **compile phase** produces a `PipelineSpec` — a serializable description of the pipeline with NO runtime state. The **runtime phase** instantiates a `Pipeline` from the spec by calling `StageSpec.NewRuntime()` for each stage.

```go
// PipelineSpec is the cacheable artifact. It describes the pipeline
// structure and stage configurations, but contains no runtime state.
type PipelineSpec struct {
    stages  []StageSpec    // stage configurations
    edges   []EdgeSpec     // parent→child connections
    sources []int          // indices of source stages
    sink    int            // index of sink stage
    cost    float64
    memoKey string
    cols    []string       // output schema
    types   []LX.TokenType
}

// StageSpec describes one stage's configuration (no runtime state).
// Each StageSpec is a factory: NewRuntime() produces a live Stage.
type StageSpec interface {
    NewRuntime() Stage
    Kind() string       // "source", "transform", "mapreduce"
    String() string     // for EXPLAIN output
}

// Pipeline is the live, executable DAG of Stages.
type Pipeline struct {
    spec    *PipelineSpec
    stages  []Stage        // live runtime instances
    root    Stage          // root stage (sink or topmost)
}

// Execute runs the pipeline and returns all result rows.
func (p *Pipeline) Execute(ctx context.Context) ([]DT.Row, error)

// Reset returns all stages to pre-execution state for reuse.
func (p *Pipeline) Reset(ctx context.Context) error
```

### Why PipelineSpec Matters

Currently, the **vectorized operator tree is rebuilt on every execution** (ex.go:1240, 1403). This means:
- Every `Query()` call runs `tryVectorizePlan` — ~50µs overhead
- The textPlanCache stores the pre-vectorization plan, not the vectorized one
- The planCache stores the pre-vectorization plan with AdaptiveOp wrapping

With PipelineSpec:
- The **specialized (vectorized) pipeline IS the cached artifact**
- On cache hit: `spec.NewRuntime()` instantiates fresh runtime state — ~5µs (just allocator calls)
- No AdaptiveOp wrapper needed — Reset() handles reuse
- Vectorization runs once at compile time, not every execution

## Stage Implementations

### Source Stages

```go
// ScanStage reads from a table store or in-memory table.
type ScanStageSpec struct {
    table     string
    store     DT.Store       // nil for in-memory
    schema    *DT.StoreSchema
    predicate PS.Expr        // pushdown filter
    projection []int         // pushdown columns
    rangeMin  int64          // pushdown range
    rangeMax  int64
}

type ScanStage struct {
    spec      *ScanStageSpec
    rows      []DT.Row       // in-memory cursor
    it        Iterator       // store iterator
    pos       int
    batch     *UT.Batch      // reusable output batch
    execCtx   *DT.ExecContext
}

func (s *ScanStage) NextBatch(ctx context.Context) (*UT.Batch, error) {
    // Same logic as current SeqScan.NextBatch — reads up to 1024 rows
    // into columnar batch. Reuses s.batch across calls.
}

func (s *ScanStage) Reset(ctx context.Context) error {
    s.pos = 0
    s.rows = nil
    // Reset iterator if store-backed
    return nil
}
```

### Transform Stages

```go
// FilterStage applies a predicate to each batch.
type FilterStageSpec struct {
    predicate PS.Expr
}

type FilterStage struct {
    spec    *FilterStageSpec
    child   Stage
    eval    func(*UT.Batch) []uint16  // compiled filter fn
}

func (f *FilterStage) NextBatch(ctx context.Context) (*UT.Batch, error) {
    batch, err := f.child.NextBatch(ctx)
    if err != nil { return nil, err }
    if batch == nil { return nil, nil }  // EOF
    
    sel := f.eval(batch)  // EV.EvalBatch or compiled fn
    if sel == nil { return batch, nil }  // all pass
    if len(sel) == 0 { batch.Put(); return f.NextBatch(ctx) }  // none pass
    
    batch.Sel = sel
    batch.Size = len(sel)
    return batch, nil
}

func (f *FilterStage) Reset(ctx context.Context) error {
    return f.child.Reset(ctx)  // no local state
}
```

Other Transform stages follow the same pattern:
- **ProjectStage**: `child.NextBatch()` → evaluate projection columns → emit new batch
- **HavingStage**: Same as FilterStage but operates on aggregate result batches
- **LimitStage**: Counter only, truncate batch.Size when limit reached
- **OffsetStage**: Counter only, drop leading rows via batch.Sel slicing

### MapReduce Stages

MapReduce stages use the Mapper/Shuffler/Reducer decomposition from REQ002113:

```go
// AggregateStage computes aggregates with optional GROUP BY.
type AggregateStageSpec struct {
    groupCols  []int             // column indices for GROUP BY
    aggs       []AccumulatorSpec // aggregate definitions
    having     PS.Expr           // HAVING filter (nil if none)
    emitOrder  []PS.Expr         // fullCols / constCols ordering
}

type AggregateStage struct {
    spec      *AggregateStageSpec
    child     Stage
    mapper    Mapper
    shuffle   Shuffler
    reducer   Reducer
    result    []*UT.Batch        // cached result batches
    pos       int                // index into result for NextBatch
    executed  bool               // has the pipeline been drained?
}

func (a *AggregateStage) NextBatch(ctx context.Context) (*UT.Batch, error) {
    if !a.executed {
        // First call: drain child through map-shuffle-reduce
        for {
            batch, err := a.child.NextBatch(ctx)
            if err != nil { return nil, err }
            if batch == nil { break }
            if err := a.mapper.MapBatch(ctx, batch, a.shuffle); err != nil {
                return nil, err
            }
            batch.Put()
        }
        groups, err := a.shuffle.Finalize()
        if err != nil { return nil, err }
        a.result, err = a.reducer.Finalize(groups)
        if err != nil { return nil, err }
        a.executed = true
    }
    
    if a.pos >= len(a.result) {
        return nil, nil  // EOF
    }
    batch := a.result[a.pos]
    a.pos++
    return batch, nil
}

func (a *AggregateStage) Reset(ctx context.Context) error {
    a.executed = false
    a.pos = 0
    for _, b := range a.result {
        if b != nil { b.Put() }
    }
    a.result = nil
    a.mapper.Reset()
    a.shuffle.Reset()
    a.reducer.Reset()
    return a.child.Reset(ctx)
}
```

Other MapReduce stages:
- **SortStage**: `SortMapper` + `SortShuffler` + `IdentityReducer`
- **DistinctStage**: `DistinctMapper` + `DistinctShuffler` + `IdentityReducer`
- **WindowStage**: `WindowMapper` + `PartitionShuffler` + `WindowReducer`
- **CompoundStage**: `CompoundMapper` + `DistinctShuffler` or `ProbeShuffler`

### Join Stages

Joins are a **two-input MapReduce**: the build side is a MapReduce stage (drain all build rows into hash table), and the probe side is a Transform stage (one probe batch in, matched batch out).

```go
type JoinStageSpec struct {
    kind       JoinKind        // INNER, LEFT, RIGHT, FULL
    buildKeys  []int           // build-side key column indices
    probeKeys  []int           // probe-side key column indices
    buildCols  []string        // build-side output columns
    probeCols  []string        // probe-side output columns
}

type JoinStage struct {
    spec      *JoinStageSpec
    buildChild Stage           // left/bottom input
    probeChild Stage           // right/top input
    ht        *UT.HashTable    // built from buildChild
    bloomFilter *UT.BloomFilter
    // Probe state
    probeBatch *UT.Batch
    pending    []matchPair     // matched (buildRow, probeRow) pairs
    pendingPos int
    // Build-side row data for emit
    buildCols  []UT.Column     // flattened build-side batches
    // Outer join tracking
    buildMatched []bool
    probeMatched []bool
    phase      int             // 0=build, 1=probe, 2=unmatchedBuild, 3=unmatchedProbe
}
```

The `JoinStage` handles both build and probe internally:
- **First `NextBatch()` call**: drains `buildChild` completely (MapReduce-like), builds hash table
- **Subsequent calls**: pulls from `probeChild` (Transform-like), probes hash table, emits matches
- **After probe EOF**: emits unmatched build/probe rows for outer joins

### Sink Stages

```go
// CollectorStage drains the pipeline and returns all result rows.
type CollectorStage struct {
    child Stage
    rows  []DT.Row
    done  bool
}

func (c *CollectorStage) NextBatch(ctx context.Context) (*UT.Batch, error) {
    if !c.done {
        for {
            batch, err := c.child.NextBatch(ctx)
            if err != nil { return nil, err }
            if batch == nil { break }
            rows := batch.ToRowsShared(c.rowBuf)
            c.rows = append(c.rows, rows...)
            batch.Put()
        }
        c.done = true
    }
    // Emit rows as batches for consistency
    // Or: caller accesses c.rows directly
}
```

## PipelineBuilder — The Compile Phase

The PipelineBuilder replaces the current scattered flow of:
`PS.NewParser → RE.Rewrite → Planner.Plan → ResolvePlanSlots → tryVectorizePlan`

With a single coherent compile pipeline:

```go
// PipelineBuilder compiles SQL text into a PipelineSpec.
type PipelineBuilder struct {
    planner    *Planner
    stmtCache  *StmtCache      // parsed AST cache
    planCache  *PlanCache      // PipelineSpec cache by memo key
    textCache  *TextCache      // PipelineSpec cache by exact SQL
}

// Build compiles SQL into a PipelineSpec.
// Returns cached spec on cache hit (skips all compile steps).
func (b *PipelineBuilder) Build(sql string) (*PipelineSpec, error) {
    // 1. Check text cache (exact SQL match)
    if spec := b.textCache.Get(sql); spec != nil {
        return spec, nil
    }
    
    // 2. Parse (or use stmt cache)
    stmt, err := b.parseOrCache(sql)
    if err != nil { return nil, err }
    
    // 3. Rewrite (constant folding, boolean simplification)
    rewritten, err := RE.Rewrite(stmt)
    if err != nil { return nil, err }
    
    // 4. Check plan cache (parameterized memo key)
    memoKey := PL.EncodeMemoKey(rewritten)
    if spec := b.planCache.Get(memoKey); spec != nil {
        b.textCache.Put(sql, spec)  // populate text cache
        return spec, nil
    }
    
    // 5. Plan (AST → operator tree)
    plan, err := b.planner.Plan(rewritten)
    if err != nil { return nil, err }
    
    // 6. Resolve slots (O(1) column indexing)
    ResolvePlanSlots(plan.Root)
    
    // 7. Specialize (vectorize + fuse + resolve stages)
    spec, err := b.specialize(plan)
    if err != nil { return nil, err }
    
    // 8. Cache
    spec.memoKey = memoKey
    b.planCache.Put(memoKey, spec)
    b.textCache.Put(sql, spec)
    
    return spec, nil
}
```

### The specialize() Step — Replaces tryVectorizePlan

The key difference from the current model: **specialize() produces a PipelineSpec, not a live operator tree.** The spec is the cacheable artifact.

```go
// specialize converts a row-based plan tree into a PipelineSpec
// of batch-native Stages. This replaces tryVectorizePlan.
func (b *PipelineBuilder) specialize(plan *PL.PlanResult) (*PipelineSpec, error) {
    spec := &PipelineSpec{}
    
    // Walk the plan tree bottom-up, converting each operator
    // to a StageSpec. This is the same logic as transformOp
    // in vec_transform.go, but produces specs instead of live operators.
    rootSpec, err := b.transformNode(plan.Root)
    if err != nil { return nil, err }
    
    spec.sink = len(spec.stages)
    spec.stages = append(spec.stages, &CollectorStageSpec{child: len(spec.stages) - 1})
    
    return spec, nil
}

func (b *PipelineBuilder) transformNode(op PL.Operator) (int, error) {
    switch o := op.(type) {
    case *OP.SeqScan:
        return b.buildScanStage(o)
    case *OP.Filter:
        return b.buildFilterStage(o)
    case *OP.Project:
        return b.buildProjectStage(o)
    case *OP.Aggregate:
        return b.buildAggregateStage(o)
    case *OP.Sort:
        return b.buildSortStage(o)
    case *OP.Limit:
        return b.buildLimitStage(o)
    case *OP.HashJoin:
        return b.buildJoinStage(o)
    // ... all other operators
    }
}
```

**Fusion** is handled at the specialize step:
- `FilterProject(SeqScan)` → single `FusedScanStage` (same as current FusedBatchScan)
- `Limit(Project(Filter(SeqScan)))` → single `FusedScanStage` with limit
- `Aggregate` with GROUP BY → `AggregateStage` with `HashShuffler`
- `Aggregate` scalar → `AggregateStage` with `ScalarShuffler`

### FusedScanStage — The Fusion Pattern

```go
type FusedScanStageSpec struct {
    table      string
    store      DT.Store
    predicate  PS.Expr
    projections []PS.Expr
    limit      int64
    offset     int64
}

type FusedScanStage struct {
    spec   *FusedScanStageSpec
    // ... SeqScan state + filter eval + project eval + limit counter
}

func (f *FusedScanStage) NextBatch(ctx context.Context) (*UT.Batch, error) {
    // Same logic as current FusedBatchScan — one scan pass per output batch,
    // applying filter + project + limit in a single loop.
}
```

## PipelineCache — Unified Caching

The three current caches merge into one `PipelineCache`:

```go
type PipelineCache struct {
    text  map[string]*PipelineSpec   // exact SQL text → spec
    memo  map[string]*PipelineSpec   // parameterized memo key → spec
    stmts map[string]PS.Stmt         // exact SQL text → parsed AST
    mu    sync.RWMutex
}
```

**Cache hit path** (most common, ~90% of OLTP queries):
```
SQL text → PipelineCache.Get(text) → PipelineSpec → spec.NewRuntime() → Execute
           ~100ns (hash lookup)      ~5µs (allocators)       ~µs-scale
```

**Cache miss path** (first-seen query):
```
SQL text → Parse → Rewrite → Plan → Specialize → Cache → PipelineSpec → NewRuntime → Execute
           ~50µs    ~5µs      ~20µs    ~10µs                           ~5µs         ~varies
```

**Current cache hit path** for comparison:
```
SQL text → stmtCache → planCache → AdaptiveOp clone → ResolveSlots → tryVectorizePlan → Execute
           ~100ns      ~200ns      ~1µs               ~5µs           ~50µs              ~varies
```

The unified model eliminates the `tryVectorizePlan` step on cache hit (~50µs savings) and the AdaptiveOp wrapper allocation.

## PipelineExecutor — The Runtime Phase

```go
type PipelineExecutor struct {
    engine *Engine
    pool   *UT.WorkerPool
    arena  *DT.RowArena
}

// Execute runs a PipelineSpec with the given parameters.
func (e *PipelineExecutor) Execute(ctx context.Context, spec *PipelineSpec, args ...any) ([]DT.Row, error) {
    // 1. Instantiate: create fresh runtime state for each stage
    pipeline := spec.NewRuntime()
    
    // 2. Inject parameters (? placeholder values)
    pipeline.InjectParams(args)
    
    // 3. Inject execution context (arena, planner, session)
    pipeline.InjectExecCtx(e.newExecCtx())
    
    // 4. Execute: drain the pipeline
    rows, err := pipeline.Execute(ctx)
    
    // 5. Reset for potential reuse (if caching)
    pipeline.Reset(ctx)
    
    return rows, err
}
```

### NewRuntime() — Instantiation

```go
func (s *PipelineSpec) NewRuntime() *Pipeline {
    stages := make([]Stage, len(s.stages))
    for i, spec := range s.stages {
        stages[i] = spec.NewRuntime()
    }
    // Wire children based on EdgeSpec
    for _, edge := range s.edges {
        parent := stages[edge.Parent].(ChildSetter)
        parent.SetChild(edge.ChildIndex, stages[edge.Child])
    }
    return &Pipeline{spec: s, stages: stages, root: stages[s.sink]}
}
```

Each `StageSpec.NewRuntime()` allocates only the runtime state needed:
- `ScanStageSpec.NewRuntime()`: allocates `ScanStage` + output `Batch`
- `FilterStageSpec.NewRuntime()`: allocates `FilterStage` + compiled filter fn
- `AggregateStageSpec.NewRuntime()`: allocates `AggregateStage` + Mapper + Shuffler + Reducer + UnifiedAccum array

Total allocation for a typical OLTP query (Scan → Filter → Project → ScalarAggregate):
- Current: ~15-20 heap objects (AdaptiveOp, operator structs, batch pools, arena)
- New: ~8-10 heap objects (Stage structs, UnifiedAccum, Batch from pool)

### Reset() — Reuse Without Deallocation

```go
func (p *Pipeline) Reset(ctx context.Context) error {
    for _, stage := range p.stages {
        if err := stage.Reset(ctx); err != nil {
            return err
        }
    }
    return nil
}
```

Each Stage's Reset() clears runtime state but preserves allocated buffers:
- `ScanStage.Reset()`: resets iterator position, keeps batch buffer
- `FilterStage.Reset()`: no state to reset (delegates to child)
- `AggregateStage.Reset()`: resets accumulators, clears hash table but keeps capacity, keeps scratch buffers
- `SortStage.Reset()`: clears row collection, keeps scratch buffer

This means the second execution of the same query reuses all allocated memory — zero GC pressure.

## Full Query Flow Comparison

### Current Flow (9 steps, 5 models)

```
1. Parse SQL → PS.Select                    (SQF/PS, ~50µs)
2. StmtCache lookup                          (SQB/EX, ~100ns)
3. Rewrite → simplified PS.Select           (SQF/RE, ~5µs)
4. Plan → PL.PlanResult{Root: Operator}     (SQB/EX, ~20µs)
5. PlanCache lookup + AdaptiveOp clone       (SQB/EX, ~1µs)
6. ResolvePlanSlots                          (SQB/EX, ~5µs)
7. tryVectorizePlan → BatchToRowAdapter      (SQB/EX, ~50µs)  ← NOT CACHED
8. drainBatch/drainRows → []Row              (SQB/EX, varies)
9. DS driver → database/sql Rows             (SYS/DS, ~5µs)
```

**Total compile overhead on cache hit**: ~60µs (steps 5-7)
**Total compile overhead on cache miss**: ~130µs (steps 1-7)

### Unified Flow (3 steps, 1 model)

```
1. PipelineBuilder.Build → PipelineSpec      (all compile steps, cacheable)
2. PipelineSpec.NewRuntime → Pipeline        (fresh runtime state, ~5µs)
3. Pipeline.Execute → []Row                  (drain stages, varies)
```

**Total compile overhead on cache hit**: ~5µs (step 2 only — just allocators)
**Total compile overhead on cache miss**: ~85µs (steps 1-2 — parse+rewrite+plan+specialize)

**Savings on cache hit**: ~55µs (eliminates ResolveSlots + tryVectorizePlan)

## Migration Strategy

The migration is incremental — each phase replaces part of the current pipeline while keeping the rest unchanged.

### Phase 0: Stage Interface + PipelineSpec (foundation)
- Define `Stage` interface in `SQB/PX/`
- Define `PipelineSpec`, `StageSpec`, `Pipeline` types
- Define `PipelineCache`
- No existing operators changed — purely additive

### Phase 1: Source Stages (ScanStage)
- Implement `ScanStageSpec` + `ScanStage`
- Wire into `PipelineBuilder` for SeqScan operators
- Verify: ScanStage produces same batches as SeqScan.NextBatch

### Phase 2: Transform Stages (Filter, Project, Limit, Offset)
- Implement `FilterStageSpec`, `ProjectStageSpec`, `LimitStageSpec`, `OffsetStageSpec`
- Implement `FusedScanStageSpec` (fusion of Scan+Filter+Project)
- Wire into `PipelineBuilder`

### Phase 3: MapReduce Stages (Aggregate, Sort, Distinct)
- Implement using Mapper/Shuffler/Reducer from REQ002113
- Wire into `PipelineBuilder`

### Phase 4: Join Stages (HashJoin, MergeJoin, NLJ)
- Implement `JoinStageSpec` + `JoinStage`
- Wire into `PipelineBuilder`

### Phase 5: Window + CompoundOp
- Implement `WindowStageSpec`, `CompoundStageSpec`
- Wire into `PipelineBuilder`

### Phase 6: PipelineBuilder replaces current compile flow
- `PipelineBuilder.Build()` replaces the `planWithCache` + `tryVectorizePlan` flow
- `PipelineCache` replaces stmtCache + planCache + textPlanCache
- Verify: all SLT tests pass

### Phase 7: PipelineExecutor replaces current execute flow
- `PipelineExecutor.Execute()` replaces `drainBatch`/`drainRows`
- `PipelineSpec.NewRuntime()` replaces AdaptiveOp clone
- `Pipeline.Reset()` replaces Close+reopen for cached plans

### Phase 8: Delete legacy operator types
- Remove `Operator.Next()` row-based interface (all operators are Stage now)
- Remove `AdaptiveOp`, `PushPipeline`, `BatchToRowAdapter`
- Remove per-VectorizedXxx structs (subsumed by StageSpec)
- `BatchToRowAdapter` kept as a thin adapter for the `database/sql` driver

## What This Eliminates

| Current | Replaced By |
|---------|-------------|
| `PL.Operator` interface (`Next/Close`) | `Stage` interface (`NextBatch/Reset/Close`) |
| `UT.BatchProducer` interface | `Stage` interface (same methods) |
| `AD.AdaptiveOp` wrapper | `Pipeline.Reset()` |
| `OP.PushPipeline` + `PushOperator` | `TransformStage` (streaming) |
| `OP.FusedBatchScan` | `FusedScanStage` (fusion in spec) |
| 3 separate caches (stmt/plan/text) | `PipelineCache` (unified) |
| `tryVectorizePlan` (per execution) | `specialize()` (at compile time) |
| `BatchToRowAdapter` at every operator | Only at final delivery |
| 15+ operator struct types | 4 stage categories (Source/Transform/MR/Join) |
| `ResolvePlanSlots` (per execution) | Built into `StageSpec` (at compile time) |

## REQ Mapping

### New REQs (pipeline architecture)

| REQ | What | Depends |
|-----|------|---------|
| REQ002123 | **Stage interface + PipelineSpec + PipelineCache** — core types in `SQB/PX/` | REQ002113 (UnifiedAccum) |
| REQ002124 | **PipelineBuilder — specialize() replaces tryVectorizePlan** | REQ002123 |
| REQ002125 | **ScanStage + FilterStage + ProjectStage + FusedScanStage** | REQ002123 |
| REQ002126 | **AggregateStage + SortStage + DistinctStage** (uses MR core) | REQ002123, REQ002113 |
| REQ002127 | **JoinStage** (build+probe in one Stage) | REQ002123 |
| REQ002128 | **WindowStage + CompoundStage** | REQ002123, REQ002126 |
| REQ002129 | **PipelineBuilder replaces compile flow** — unified cache | REQ002124-002127 |
| REQ002130 | **PipelineExecutor replaces execute flow** — Reset() for cache reuse | REQ002129 |
| REQ002131 | **Delete legacy operators** — Operator.Next, AdaptiveOp, PushPipeline, per-VectorizedXxx | REQ002130 |

### Existing MR REQs mapping

| REQ | Status |
|-----|--------|
| REQ002113 (MR core: UnifiedAccum, Mapper/Shuffler/Reducer) | **Unchanged** — the MR core is the foundation for MapReduce Stages |
| REQ002114 (VHA → MR) | **Replaced by REQ002126** — AggregateStage uses MR core directly |
| REQ002115 (scalar → MR) | **Replaced by REQ002126** — AggregateStage with ScalarShuffler |
| REQ002116 (GROUP BY + HAVING → MR) | **Replaced by REQ002126** — AggregateStage with HashShuffler |
| REQ002117 (delete HashAggregate) | **Subsumed by REQ002131** |
| REQ002118 (Distinct → MR) | **Replaced by REQ002126** — DistinctStage |
| REQ002119 (Sort → MR) | **Replaced by REQ002126** — SortStage |
| REQ002120 (Window → MR) | **Replaced by REQ002128** — WindowStage |
| REQ002121 (CompoundOp → MR) | **Replaced by REQ002128** — CompoundStage |
| REQ002122 (parallel MR) | **Replaced by REQ002126** — AggregateStage with parallel map |

The MR core (REQ002113) remains unchanged. The MR migration REQs (002114-002122) are superseded by the Stage-based REQs (002125-002128) because the Stage model subsumes both the MR and Transform patterns.
