# SQO Subsystem — Requirements

## 1. Problem Statement

SQB/EX currently mixes two fundamentally different concerns — query optimization and query execution — in a single package. Of its ~14,000 lines, ~10,000 are optimization logic (cost estimation, join ordering, predicate analysis, plan transformation passes) and ~4,000 are execution logic (dispatch, streaming, EXPLAIN, vectorization).

This creates three concrete problems:

1. **High cognitive load**: A developer adding a new optimization must navigate a 316-line god function (`planSelect`), a 383-line join planner (`planSelectJoins`), and understand how optimization interleaves with execution setup.

2. **Slow iteration**: Optimization passes cannot be tested independently — each test requires a full Planner + Executor + mock store, adding 100ms+ overhead per test case.

3. **No extension path**: Adding a new optimizer pass requires modifying `planSelect`'s function body, creating merge conflicts for parallel development.

## 2. Goal

Extract all optimization logic from SQB/EX into a new `SQO` package that is:

- **Self-contained**: SQO imports only SQF/* roots. No imports from SQB/*.
- **Interface-driven**: SQO manipulates operators via SQF/PL interfaces, never via concrete types from SQB/OP.
- **Independently testable**: Optimizer passes can be tested with mock operator trees, no engine or store needed.
- **Incrementally adoptable**: Each pass can move from SQB/EX to SQO independently, with the old path deleted after migration.

## 3. Non-Goals

- Not changing the execution path (SQB/EX stays as-is, just smaller)
- Not changing the AST or parser (SQF/PS unchanged)
- Not changing the concrete operator implementations (SQB/OP unchanged beyond adding interface methods)
- Not changing the storage engine (ENG/* unchanged)
- Not improving optimization quality (same optimizations, new home)

## 4. Scope

| In Scope | Out of Scope |
|----------|-------------|
| Package layout and dependency rules | Rewriting optimization logic |
| SQF/PL interface definitions (OperatorFactory, ColPrunable, etc.) | Changing operator semantics |
| Move predicate analysis to SQO | Changing cost model formulas |
| Move cost model to SQO | Adding new optimization passes |
| Move join ordering to SQO | Changing execution dispatch |
| Extract optimizer passes into individual files | Changing EXPLAIN output |
| Transform utility for operator tree rewriting | Changing streaming or vectorization |
| Resolve_slots as optimization output | |

## 5. Success Criteria

1. `SQO/` contains zero `import "github.com/cyw0ng95/razordata/internal/SQB/..."` lines. Verified by `grep -r "SQB/" SQO/` returning empty.
2. All existing SLT tests pass (select1-4, evidence, index, random).
3. All existing benchmark numbers do not regress beyond noise (±5%).
4. SQB/EX drops from ~10,000 lines of optimization to ~4,000 lines of execution.
5. Every optimizer pass has a standalone unit test that does not require creating a Planner or Executor.