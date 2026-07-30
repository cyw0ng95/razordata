// Package PX provides the unified execution model for the entire SQL
// pipeline. Legacy execution types have been deleted in REQ002171-002173.
// Remaining types (CompoundOp, PL.Operator) are still referenced by
// production code and will be cleaned up in future REQs.
//
// Deletion status:
//   - AD.AdaptiveOp: DELETED (REQ002171)
//   - vec_transform.go / tryVectorizePlan: DELETED (REQ002172)
//   - VectorizedXxx types: DELETED (REQ002172)
//   - HashAggregate / ParallelHashAggregate: DELETED (REQ002173)
//   - PushPipeline, PushOperator, etc.: DELETED (REQ002175)
//   - SQB/MR/ merged into SQB/PX/ (REQ002146)
//
// Still in use (not deprecated):
//   - CompoundOp / compound.go: used by PX/builder.go and EX/plan_node.go
//   - PL.Operator: used by RowOperatorAsProducer and all legacy ops
//   - BatchToRowAdapter: kept for database/sql driver boundary
package PX

// Deprecated: Legacy types are being replaced by the Stage/PipelineSpec
// system. New code should use the Stage interface and concrete StageSpec
// types.