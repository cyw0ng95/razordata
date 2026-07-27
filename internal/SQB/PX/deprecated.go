// Package PX (Pipeline eXecution) provides the unified execution model
// for the entire SQL pipeline. All legacy execution types (PL.Operator,
// AD.AdaptiveOp, PushPipeline, VectorizedXxx, HashAggregate, etc.) are
// deprecated and will be deleted in a future REQ once the PipelineBuilder
// and PipelineExecutor are fully wired into the Executor.
//
// Current status (REQ002131):
//   - PL.Operator (Next/Close): still used by DML operators (WT.Insert,
//     WT.Update, WT.Delete) and some legacy tests. Will be replaced by
//     Stage interface once DMLStage is implemented.
//   - AD.AdaptiveOp: still used by CompiledPlan. Will be replaced by
//     PipelineSpec caching.
//   - PushPipeline, PushOperator, etc.: replaced by Transform Stages.
//   - VectorizedXxx (VectorizedHashAggregate, VectorizedSort, etc.):
//     replaced by concrete Stage types (AggregateStage, SortStage, etc.).
//   - HashAggregate: replaced by AggregateStage with HashShuffler.
//   - tryVectorizePlan: replaced by PipelineBuilder.specialize().
//   - BatchToRowAdapter: kept as a thin adapter for the database/sql driver.
//
// Deletion plan:
//   1. Implement DMLStage (REQ002127) — replaces WT.Insert/Update/Delete
//   2. Wire PipelineBuilder into Executor (REQ002129 partial)
//   3. Wire PipelineExecutor into Executor (REQ002130 partial)
//   4. Delete legacy types (this REQ — complete after 1-3)
package PX

// Deprecated: Legacy types are in the process of being replaced by
// the Stage/PipelineSpec/PipelineExecutor system. New code should
// use the Stage interface and concrete StageSpec types.