//go:build !px_validate

package EX

import (
	"context"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

// drainPlanExecCtx drains all rows and threads execCtx through each row.
// REQ002129/2132: try the pure-PX pipeline fast path first via
// queryAllBuildPipeline-style cache → spec, but here we only have a
// plan-tree (the caller already parsed + planned). For the transitional
// bridge, we SKIP the tryVectorizePlan → LegacyBatchStageSpec path here
// because it double-wraps the plan-tree in BatchToRowAdapters, which
// silently drops execCtx wiring for complex operators (correlated subqueries,
// DISTINCT aggregates, HAVING, GROUP_CONCAT with separator). Instead we
// always drain the original operator tree via drainBatch — pure-PX fast
// path is still taken in the outer QueryAll entry point, which calls
// BuildPipeline(sql) directly (skips plan-tree construction entirely).
func (e *Executor) drainPlanExecCtx(ctx context.Context, plan *pl.PlanResult, execCtx *DT.ExecContext) ([]DT.Row, error) {
	return drainBatch(ctx, plan.Root, execCtx)
}