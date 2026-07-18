package EX

import (
	"context"
	"errors"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

// drainPlan drains all rows from a plan into a slice.
func (e *Executor) drainPlan(ctx context.Context, plan *pl.PlanResult) ([]DT.Row, error) {
	return drainBatch(ctx, plan.Root, nil)
}

// drainPlanExecCtx drains all rows and threads execCtx through each row.
func (e *Executor) drainPlanExecCtx(ctx context.Context, plan *pl.PlanResult, execCtx *DT.ExecContext) ([]DT.Row, error) {
	return drainBatch(ctx, plan.Root, execCtx)
}

// drainBatch drains an operator into []DT.Row. If the root implements
// BatchProducer, it drains via NextBatch() + ToRows() — yielding all
// rows of each batch at once. Otherwise it falls back to Next() row-by-row.
// If execCtx is non-nil, it is threaded through each row via WithExecContext.
func drainBatch(ctx context.Context, root pl.Operator, execCtx *DT.ExecContext) ([]DT.Row, error) {
	if root == nil {
		return nil, errors.New("ex: drainBatch: nil root")
	}

	if bp, ok := root.(UT.BatchProducer); ok {
		return drainBatchProducer(ctx, bp, execCtx)
	}

	// Fallback: row-based drain.
	return drainPlanRows(ctx, root, execCtx)
}

// drainBatchProducer drains a BatchProducer into []DT.Row.
func drainBatchProducer(ctx context.Context, bp UT.BatchProducer, execCtx *DT.ExecContext) ([]DT.Row, error) {
	var out []DT.Row
	for {
		batch, err := bp.NextBatch(ctx)
		if err != nil {
			return nil, err
		}
		if batch == nil {
			break
		}
		rows := batch.ToRows()
		for i := range rows {
			if execCtx != nil {
				DT.WithExecContext(&rows[i], execCtx)
			}
		}
		out = append(out, rows...)
		if batch.Pooled {
			batch.Put()
		}
	}
	return out, nil
}

// drainPlanRows drains a row-based pl.Operator into []DT.Row via Next().
func drainPlanRows(ctx context.Context, root pl.Operator, execCtx *DT.ExecContext) ([]DT.Row, error) {
	var out []DT.Row
	for {
		row, err := root.Next(ctx)
		if err != nil {
			if err == DT.ErrNoRows {
				break
			}
			return nil, err
		}
		if execCtx != nil {
			DT.WithExecContext(&row, execCtx)
		}
		out = append(out, row)
	}
	return out, nil
}
