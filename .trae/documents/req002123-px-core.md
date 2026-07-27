# REQ002123: Unified Pipeline Core — SQB/PX Package

## Context

The current execution architecture has three inefficiencies:

1. **Three separate caches** (stmtCache, planCache, textPlanCache) with independent eviction policies, checked in inconsistent orders by different entry points.
2. **`tryVectorizePlan` runs every execution** (~50µs) — it walks the plan tree and converts row→batch operators but its result is NOT cached because it contains mutable runtime state.
3. **No "spec vs runtime" separation** — a cached `PlanResult` contains live operator instances with mutable state, forcing fragile `ShallowCopy`/`AdaptiveOp` workarounds.

REQ002123 introduces `SQB/PX/` (Pipeline eXecution) as the foundation for a unified model:
- `PipelineSpec` is a **cacheable** description with NO runtime state
- `Pipeline` is the **live** execution DAG created from a spec
- `PipelineCache` unifies the three caches
- `PipelineBuilder` provides the single compile entry point

## Design Decisions

### DD1: New `Stage` interface (not extending `BatchProducer`)

```go
type Stage interface {
    NextBatch(ctx context.Context) (*UT.Batch, error)
    Reset(ctx context.Context) error
    Close() error
}
```

`UT.BatchProducer` lacks `Reset`; adding it there would require modifying 50+ implementations, violating the "additive only" constraint. New interface keeps PX self-contained. Later (REQ002131) we delete the old one.

### DD2: `StageSpec` as typed factory structs

Each `StageSpec` holds configuration (expressions, schemas, indices) but NO runtime state (no iterators, no hash tables). `NewRuntime()` allocates fresh state. No `map[string]interface{}`.

### DD3: Bridge via `LegacyBatchStageSpec`

For REQ002123, `specialize()` produces a **single-stage** `PipelineSpec` wrapping `tryVectorizePlan`'s result. This is the bridge — the PX package works without modifying any existing operator. Concrete `StageSpec` types come in REQ002125-002127.

### DD4: `SpecializeFunc` avoids import cycle

PX cannot import EX (EX will later import PX). The builder accepts a `SpecializeFunc func(root DT.Operator, planner PL.QueryPlanner) UT.BatchProducer` at construction time. The Executor provides this function in REQ002129. Tests provide a mock.

### DD5: `LegacyBatchStage.Reset()` returns `ErrResetNotSupported`

Legacy BatchProducer stages don't support `Reset`. Pipeline handles this by recreating the stage from spec. Full `Reset()` optimization (preserving hash tables, scratch buffers) comes with concrete StageSpec types in REQ002125-002127.

## Package Layout

| File | Contents |
|------|----------|
| `SQB/PX/stage.go` | `Stage` interface, `StageCategory`, `ChildSetter`, `ParamPropagator` |
| `SQB/PX/spec.go` | `StageSpec`, `EdgeSpec`, `PipelineSpec`, `NewRuntime()` |
| `SQB/PX/pipeline.go` | `Pipeline`, `Execute()`, `Reset()`, `Close()` |
| `SQB/PX/cache.go` | `PipelineCache`, unified LRU eviction |
| `SQB/PX/builder.go` | `PipelineBuilder`, `Build()`, `SpecializeFunc`, `LegacyBatchStageSpec` |
| `SQB/PX/stage_test.go` | Stage interface tests |
| `SQB/PX/spec_test.go` | PipelineSpec/NewRuntime tests |
| `SQB/PX/pipeline_test.go` | Pipeline Execute/Reset/Close tests |
| `SQB/PX/cache_test.go` | Cache hit/miss/eviction/concurrency tests |
| `SQB/PX/builder_test.go` | Build flow tests |

## Implementation Steps

### Step 1: `stage.go` — Stage interface

Define `Stage`, `StageCategory` (CatSource/CatTransform/CatMapReduce/CatJoin), `ChildSetter` (optional interface for wiring children), `ParamPropagator` (optional interface for param injection).

Exports: `Stage`, `StageCategory`, `CatSource`, `CatTransform`, `CatMapReduce`, `CatJoin`, `ChildSetter`, `ChildSide`, `SingleChild`, `LeftChild`, `RightChild`, `ParamPropagator`, `ErrResetNotSupported`.

### Step 2: `spec.go` — PipelineSpec + StageSpec

Define `StageSpec` (interface: `NewRuntime() Stage`, `Category() StageCategory`), `EdgeSpec` (From/To/Side), `PipelineSpec` (Stages, Edges, RootIdx, OutputCols, OutputTypes, Cost, MemoKey, SQLText).

Key method: `PipelineSpec.NewRuntime() *Pipeline` — creates all stages via `StageSpec.NewRuntime()`, wires children per edge list.

### Step 3: `pipeline.go` — Pipeline

Define `Pipeline` (spec, stages, root). `Execute(ctx)` drains root via NextBatch, converts to `[]DT.Row` using existing batch→row conversion. `Reset(ctx)` calls Reset on each stage; if `ErrResetNotSupported`, recreates from spec and re-wires. `Close()` closes all stages.

### Step 4: `cache.go` — PipelineCache

Unified cache: `text map[string]*PipelineSpec`, `memo map[string]*PipelineSpec`, `stmts map[string]PS.Stmt`. Single mutex, single LRU eviction list. Methods: `GetByText`, `GetByMemo`, `Put`, `Clear`, `Len`.

### Step 5: `builder.go` — PipelineBuilder + LegacyBatchStageSpec

`PipelineBuilder` holds cache, planner (as `PL.QueryPlanner` interface), store, pool, and a `SpecializeFunc`.

`Build(sql)` flow: cache check → parse → cache check by memo → rewrite → plan → resolve slots → specialize → cache.

`LegacyBatchStageSpec` wraps a `DT.Operator` tree + `SpecializeFunc`. `NewRuntime()` calls the specialize func to get a `UT.BatchProducer`, wraps it in `LegacyBatchStage`.

`LegacyBatchStage` implements `Stage` by delegating to the inner `BatchProducer`. `Reset()` returns `ErrResetNotSupported`. `Close()` calls `producer.Close()`.

### Step 6: Tests

Per-file tests covering:
- **stage_test.go**: mock Stage, interface contract, category classification
- **spec_test.go**: PipelineSpec construction, NewRuntime, multi-stage wiring
- **pipeline_test.go**: Execute (happy path, empty, error), Reset (supported/not-supported), Close (idempotent)
- **cache_test.go**: text/memo hit/miss, LRU eviction, concurrent access, Clear
- **builder_test.go**: Build cache miss/hit, specialize, DML passthrough, invalid SQL

## Key Files Referenced

- [batch_operator.go](file:///home/cyw0ng/projects/razordata/internal/SQB/UT/batch_operator.go) — `BatchProducer` interface (Stage extends this pattern)
- [vec_transform.go](file:///home/cyw0ng/projects/razordata/internal/SQB/EX/vec_transform.go) — `tryVectorizePlan` (bridge target for LegacyBatchStageSpec)
- [ex.go](file:///home/cyw0ng/projects/razordata/internal/SQB/EX/ex.go) — Three caches (unified by PipelineCache)
- [resolve_slots.go](file:///home/cyw0ng/projects/razordata/internal/SQB/EX/resolve_slots.go) — Slot resolution (folded into specialize)
- [MR/operator.go](file:///home/cyw0ng/projects/razordata/internal/SQB/MR/operator.go) — MapReduceOperator Reset pattern
- [PL/types.go](file:///home/cyw0ng/projects/razordata/internal/SQF/PL/types.go) — `QueryPlanner` interface, `PlanResult`, `Resettable`

## Verification

1. `go test ./internal/SQB/PX/... -race -count=1` passes
2. `go vet ./internal/SQB/PX/` clean
3. `go build ./...` passes
4. Existing SLT select1..4 still pass (PX is additive, no existing code modified)
5. Every public API has test coverage (error paths, edge cases, idempotency)
