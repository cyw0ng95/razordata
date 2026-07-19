package EX

import (
	"context"
	"errors"
	"sync"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

// rowBufPool pools the shared Value buffer for ToRowsShared.
// REQ001638: eliminates per-row allocations in drainBatchProducer.
var rowBufPool = sync.Pool{
	New: func() any {
		buf := make([]pl.Value, 0, 1024*16) // 1024 rows × 16 cols
		return &buf
	},
}

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
// REQ001638: uses ToRowsShared with a pooled buffer to eliminate
// per-row Data slice allocations.
func drainBatchProducer(ctx context.Context, bp UT.BatchProducer, execCtx *DT.ExecContext) ([]DT.Row, error) {
	var out []DT.Row
	bufPtr := rowBufPool.Get().(*[]pl.Value)
	buf := *bufPtr
	defer func() {
		*bufPtr = buf[:0]
		if cap(buf) > 1024*64 {
			buf = make([]pl.Value, 0, 1024*16)
		}
		*bufPtr = buf
		rowBufPool.Put(bufPtr)
	}()
	for {
		batch, err := bp.NextBatch(ctx)
		if err != nil {
			return nil, err
		}
		if batch == nil {
			break
		}
		rows, newBuf := batch.ToRowsShared(buf)
		buf = newBuf
		for i := range rows {
			// REQ001638: deep-copy Data since the shared buffer is
			// reused across batches.
			row := rows[i]
			copied := make([]pl.Value, len(row.Data))
			copy(copied, row.Data)
			row.Data = copied
			if execCtx != nil {
				DT.WithExecContext(&row, execCtx)
			}
			out = append(out, row)
		}
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
