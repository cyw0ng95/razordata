// Package PX (Pipeline eXecution) provides the unified execution model
// for the entire SQL pipeline. All legacy execution types (PL.Operator,
// AD.AdaptiveOp, PushPipeline, VectorizedXxx, HashAggregate, etc.) are
// deprecated and will be deleted once the PipelineBuilder and
// PipelineExecutor are fully wired into the Executor.
//
// Current status (REQ002135):
//   - PipelineBuilder is wired into the Executor (REQ002132) but the
//     pipeline path is disabled (pipelineBuilder = nil) because the
//     PipelineBuilder does not yet handle all query types correctly.
//   - PipelineExecutor is wired into drainPlanExecCtx (REQ002133) but
//     the pipeline path is disabled (falls through to legacy).
//   - DMLStage wrappers are implemented (REQ002134) but not wired into
//     the PipelineBuilder.
//
// Deletion blockers (must be resolved before any type can be deleted):
//   - [BLOCKED] PL.Operator (Next/Close): still used by DML operators
//     (WT.Insert, WT.Update, WT.Delete) and all legacy operators.
//     PipelineBuilder must be enabled first.
//   - [BLOCKED] AD.AdaptiveOp: still used by CompiledPlan and
//     Executor entry points (Query, QueryAll, QueryStream).
//   - [BLOCKED] tryVectorizePlan (vec_transform.go): still used by
//     Executor entry points at ex.go:1243 and store_test.go:500.
//     PipelineBuilder must be enabled first.
//   - [BLOCKED] VectorizedXxx types (VectorizedHashAggregate, etc.):
//     still referenced by vec_transform.go and stage_window.go.
//   - [BLOCKED] PushPipeline, PushOperator, etc.: still referenced
//     by vec_transform.go.
//
// Deletion plan (when blockers are resolved):
//   1. Enable PipelineBuilder (set pipelineBuilder to real value)
//   2. Verify all SELECT queries work through pipeline path
//   3. Verify all DML queries work through pipeline path
//   4. Delete PL.Operator interface (keep BatchToRowAdapter for driver)
//   5. Delete AD.AdaptiveOp and AD.GlobalAdqcCache
//   6. Delete tryVectorizePlan (vec_transform.go)
//   7. Delete VectorizedXxx types (aggregate_vec.go, etc.)
//   8. Delete PushPipeline, PushOperator, etc.
//   9. Delete HashAggregate (hashagg.go, hashagg_parallel.go)
//  10. Delete SQB/AD/adqc.go
//  11. Merge SQB/MR/ into SQB/PX/
package PX

// Deprecated: Legacy types are in the process of being replaced by
// the Stage/PipelineSpec/PipelineExecutor system. New code should
// use the Stage interface and concrete StageSpec types.
// See the deletion plan above for the current status.