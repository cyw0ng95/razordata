//go:build !px_validate

package EX

import (
	"context"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

// drainPlanExecCtx drains all rows and threads execCtx through each row.
// REQ002133: when the pipeline path is available, uses PipelineExecutor
// instead of the legacy drainBatch path.
//
// When build tag px_validate is enabled, drain_batch_shadow.go replaces
// this function with a shadow validation version that runs both paths.
// REQ002140.
func (e *Executor) drainPlanExecCtx(ctx context.Context, plan *pl.PlanResult, execCtx *DT.ExecContext) ([]DT.Row, error) {
	// Try the pipeline path first when available.
	if e.pipelineBuilder != nil {
		rows, err := e.drainPipeline(ctx, plan)
		if err == nil {
			return rows, nil
		}
	}
	// Fall back to the legacy drain path.
	return drainBatch(ctx, plan.Root, execCtx)
}