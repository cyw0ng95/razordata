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
// REQ002133: unified pipeline drain path. Uses ScanStageSpec wrapping
// the BatchProducer directly. When the fast path is off or the root is
// not a BatchProducer, falls back to drainBatch.
func (e *Executor) drainPlanExecCtx(ctx context.Context, plan *pl.PlanResult, execCtx *DT.ExecContext) ([]DT.Row, error) {
	if plan == nil || plan.Root == nil {
		return nil, errors.New("ex: drainPlanExecCtx: nil plan")
	}
	if e.usePipelineFastPath() {
		if bp, ok := plan.Root.(UT.BatchProducer); ok {
			spec := &PX.PipelineSpec{
				RootIdx: 0,
				Stages: []PX.StageSpec{
					&PX.ScanStageSpec{
						NewProducer: func() UT.BatchProducer { return bp },
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
					slog.Warn("px.drainPlanExecCtx pipeline panic, falling back",
						"err", fmt.Sprintf("%v", r))
				}
			}()
		}
	}
	return drainBatch(ctx, plan.Root, execCtx)
}
