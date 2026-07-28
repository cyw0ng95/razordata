//go:build !px_validate

package EX

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	PX "github.com/cyw0ng95/razordata/internal/SQB/PX"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

// drainPlanExecCtx drains all rows and threads execCtx through each row.
// REQ002133: unified pipeline drain path. Considers the same
// `usePipelineFastPath()` gate as every other EX entry point for
// consistency. When the fast path is active AND plan.Root is already a
// BatchProducer (vectorized path via tryVectorizePlan), wrap it in a
// LegacyBatchStageSpec → PipelineExecutor.Execute(). The pipeline
// executor handles RowArena allocation and DT.WithExecContext row
// embedding (Pipeline.Execute now does what drainBatchProducer did).
//
// When the fast path is off (default today), or for row-based roots
// (non-BatchProducer operators — correlated subqueries, DISTINCT
// aggregates, complex expressions where RowOperatorAsProducer loses
// ValueKind metadata), call the legacy drainBatch which falls through
// to drainPlanRows. The RowOperatorAsProducer adapter loses type-
// specific metadata (e.g. ValueKind-preserving row.Cols[]) that
// EV.Equal/CompareRows rely on, so row-based roots still go through
// the direct drainPlanRows path until the adapter is lossless.
func (e *Executor) drainPlanExecCtx(ctx context.Context, plan *pl.PlanResult, execCtx *DT.ExecContext) ([]DT.Row, error) {
	if plan == nil || plan.Root == nil {
		return nil, errors.New("ex: drainPlanExecCtx: nil plan")
	}
	if e.usePipelineFastPath() {
		if bp, ok := plan.Root.(UT.BatchProducer); ok {
			spec := &PX.PipelineSpec{
				RootIdx: 0,
				Stages: []PX.StageSpec{
					&PX.LegacyBatchStageSpec{
						Root:    plan.Root,
						Planner: e.planner,
						Specialize: func(_ DT.Operator, _ pl.QueryPlanner) UT.BatchProducer {
							return bp
						},
					},
				},
			}
			executor := PX.NewPipelineExecutor(spec)
			defer executor.Close()
			executor.SetExecContext(execCtx)
			rows, err := executor.Execute(ctx)
			if err == nil {
				return rows, nil
			}
			defer func() {
				if r := recover(); r != nil {
					slog.Warn("px.drainPlanExecCtx legacy fallback (panic)",
						"err", fmt.Sprintf("%v", r))
				}
			}()
		}
	}
	return drainBatch(ctx, plan.Root, execCtx)
}
