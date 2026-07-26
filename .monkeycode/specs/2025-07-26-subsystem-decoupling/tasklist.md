# Implementation Task List: Subsystem Decoupling

## Phase 1: Interface Segregation (Sprint N)

### Task 1.1: Split `pl.Operator` into PullOperator + PushOperator
- [ ] Read `internal/SQF/PL/types.go` lines 44-49
- [ ] Add `PullOperator` interface (Next, Close)
- [ ] Add `PushOperator` interface (NextBatch, Close)
- [ ] Keep `type Operator = PullOperator` for backward compat
- [ ] Update 58 files that use `pl.Operator` to use specific interface where possible
- [ ] Run `go test ./... -count=1` — zero errors

### Task 1.2: Fix `DT.Operator` alias
- [ ] Update `internal/SQB/DT/types.go:94` comment to mark deprecated
- [ ] Verify `DT.Operator = PullOperator` still works

## Phase 2: ex.go God File Split (Sprint N+1)

### Task 2.1: Extract query execution path
- [ ] Read `ex.go:981-1300` (Exec and Query methods)
- [ ] Move `Exec()` implementation to `exec_dml.go`
- [ ] Move `Query()` + `QueryAll()` + `QueryStream*()` to `exec_query.go`
- [ ] Verify compilation

### Task 2.2: Extract cache management
- [ ] Read `ex.go:500-750` (planCache, stmtCache, textPlanCache)
- [ ] Move all cache functions to `cache.go`
- [ ] Verify `cachedPlan`, `getCachedStmt`, `getTextPlan` moved correctly

### Task 2.3: Extract DML builder
- [ ] Move `buildWriterOp()` from `ex.go` to `write_ops.go`
- [ ] Move `extractResult()` and related helpers
- [ ] Verify compilation

### Task 2.4: Extract arena management
- [ ] Move `ensureArena()`, `RowArena` init/cleanup to `arena.go`
- [ ] Remove dead code in arena initialization (REQ002054 fix)

## Phase 3: Dead Code Removal (Sprint N+2)

### Task 3.1: Move row-based Next() to *_row.go files
- [ ] `intermediate_basic.go`: extract Filter.Next(), FilterProject.Next(), Project.Next() → `intermediate_basic_row.go`
- [ ] `intermediate_sort.go`: extract Sort.Next() → `intermediate_sort_row.go`
- [ ] `intermediate_limit.go`: extract Limit.Next(), Offset.Next() → `intermediate_limit_row.go`
- [ ] `topn_sort.go`: extract TopNSort.Next() → `topn_sort_row.go`
- [ ] `distinct.go`: extract Distinct.Next() → `distinct_row.go`
- [ ] `compound.go`: extract CompoundOp.Next() → `compound_row.go`
- [ ] `operators.go`: extract SeqScan.Next(), IndexScan.Next(), BitmapHeapScan.Next(), etc. → `operators_row.go`
- [ ] `join.go`: extract NestedLoopJoin.Next() → `join_row.go`
- [ ] `mergejoin.go`: extract MergeJoin.Next() → `mergejoin_row.go`

### Task 3.2: Migrate affected tests
- [ ] `sort_bench_test.go`: convert Sort.Next() benchmark → sort key extraction from batch data
- [ ] `mergejoin_test.go`: wrap BMergeJoin result in BTRA → test via adapter
- [ ] `req001113_remaining_test.go`: same pattern
- [ ] `indexscan_strategy_test.go`: use batch path

### Task 3.3: Verify
- [ ] `go test ./internal/SQB/OP/... -count=1 -race`
- [ ] Count total LOC removed — expect >= 2000 lines
- [ ] No references to removed `Next()` methods remain in production code

## Phase 4: Dependency Direction (Sprint N+3)

### Task 4.1: Break UT→OP dependency
- [ ] Read `internal/SQB/UT/batch_adapter.go` current imports
- [ ] Create `batch_adapter_impl.go` with concrete type constructors
- [ ] Remove OP import from `batch_adapter.go`
- [ ] Verify compilation

### Task 4.2: Build tag gate (optional, Sprint N+5)
- [ ] Add `//go:build !novector` on row-based files
- [ ] Default build uses `go build -tags novector`
- [ ] Document in README why novector tag exists

## Phase 5: Validation (Sprint N+5)

### Task 5.1: Full integration test
- [ ] Run `./before-commit-cases.sh` — all 264 cases pass
- [ ] Run `go test ./internal/... -race -count=1 -timeout 180s`
- [ ] Run `go vet ./...` — zero warnings
- [ ] Run pprof comparison: before vs after — confirm no regression in alloc objects / CPU
