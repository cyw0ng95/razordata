//go:build !px_validate

package EX

import (
	"context"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

// drainPlanExecCtx drains all rows and threads execCtx through each row.
// REQ002133: uses the legacy drainBatch path for SELECT queries.
// REQ002142: DML queries use execDMLPipeline (separate path).
// REQ002143: drainPipeline is not used for SELECT because the
// vectorized path has known issues with EXISTS subqueries, CASE WHEN,
// and other complex expressions.
func (e *Executor) drainPlanExecCtx(ctx context.Context, plan *pl.PlanResult, execCtx *DT.ExecContext) ([]DT.Row, error) {
	return drainBatch(ctx, plan.Root, execCtx)
}