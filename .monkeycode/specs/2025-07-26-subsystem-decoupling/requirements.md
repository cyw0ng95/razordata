# Requirements: Subsystem Decoupling

## Introduction

当前 `internal/` 子系统修改成本高，因为 `pl.Operator` 接口 + dual-path (row+batch) 混写形成了全局耦合锚点。本需求文档定义解耦的 functional requirements。

## Glossary

- **PullOperator**: row-at-a-time execution interface (`Next(ctx) (Row, error)`)
- **PushOperator**: batch execution interface (`NextBatch(ctx) (*Batch, error)`)
- **BTRA**: BatchToRowAdapter — bridges PushOperator → PullOperator
- **God file**: single file > 2000 lines importing 10+ subsystems

## Requirements

### R1: Interface Segregation

**User Story:** AS a developer modifying an operator's vectorized path, I want the row-based code to be clearly separated from batch code, so that I don't need to understand or risk touching dead row-based execution.

#### Acceptance Criteria

1. WHEN `pl` package is imported by external code, THEN it exports `PullOperator` and `PushOperator` interfaces separately (not a single mixed `Operator`)
2. WHILE `DT.Operator = pl.PullOperator` alias exists, THEN any code using `DT.Operator` compiles without warning when built with `go vet -printfuncs=deprecation`
3. IF a file in `internal/SQB/OP/` contains both `Next()` and `NextBatch()` implementations, THEN the `Next()` method MUST be in a file named `*_row.go`
4. WHEN `tryVectorizePlan()` wraps a plan root, THEN the wrapped type is always a `PushOperator` (via BTRA), never a raw `PullOperator`

### R2: God File Reduction

**User Story:** AS an engineer reading `SQB/EX/`, I want each file under 500 lines importing at most 6 packages, so that I can understand one file in a single context window.

#### Acceptance Criteria

1. WHEN reviewing `ex.go` after refactor, THEN it contains only Executor struct + constructor (<=300 lines)
2. IF a function in `SQB/EX/` calls `.Query()`, `.Exec()`, `.QueryStream()`, THEN each is in its own file (not all in one 2171-line file)
3. WHEN adding a new DML writer type (e.g., `Upsert`), THEN the change touches at most 3 files (no cross-cutting into planner or cache)

### R3: Dependency Direction Fix

**User Story:** AS a maintainer of `UT` (utils types), I don't want my package to depend on concrete operators like `VectorizedHashJoin`.

#### Acceptance Criteria

1. WHEN building `SQB/UT`, THEN it imports NO `SQB/OP` sub-packages
2. IF `batch_adapter.go` needs concrete types, THEN they are defined in `batch_adapter_impl.go` behind a build tag
3. WHEN running `go mod tidy`, THEN there are zero warnings about unused imports caused by split files

### R4: Test Isolation

**User Story:** AS a tester, I want fake test operators in a separate file so production code doesn't accidentally depend on them.

#### Acceptance Criteria

1. WHEN `go test ./internal/SQB/OP/...`, THEN no production file references `sliceScan`, `mockRowSource`, or `countingIndexScan`
2. IF a test creates a `MergeJoin` for testing, THEN it builds via a factory function (e.g., `NewTestMergeJoin(left, right, ...)`) instead of direct struct literal
3. WHEN `req001113_remaining_test.go` runs against vectorized path, THEN results match expected hash within 0.1% tolerance
