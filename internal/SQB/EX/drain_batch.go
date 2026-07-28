package EX

import (
	"context"
	"errors"
	"sync"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	OP "github.com/cyw0ng95/razordata/internal/SQB/OP"
	PX "github.com/cyw0ng95/razordata/internal/SQB/PX"
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
// REQ002133: when the pipeline path is available, uses PipelineExecutor
// instead of the legacy drainBatch path.
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

// drainPipeline drains a plan through the PipelineExecutor.
// REQ002133.
func (e *Executor) drainPipeline(ctx context.Context, plan *pl.PlanResult) ([]DT.Row, error) {
	if e.pipelineBuilder == nil {
		return nil, errors.New("ex: pipeline not available")
	}
	spec := &PX.PipelineSpec{
		RootIdx: 0,
	}
	// Vectorize the plan tree before wrapping in LegacyBatchStageSpec.
	vec := tryVectorizePlan(plan.Root, e.planner)
	spec.Stages = []PX.StageSpec{
		&PX.LegacyBatchStageSpec{
			Root:       vec,
			Planner:    e.planner,
			Specialize: nil,
		},
	}
	executor := PX.NewPipelineExecutor(spec)
	defer executor.Close()
	return executor.Execute(ctx)
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
		// REQ001638: deep-copy Data since the shared buffer is
		// reused across batches.
		// REQ002012: use RowArena to avoid per-row make+copy heap
		// allocation (~5% of total alloc bytes). The arena is a
		// bump allocator; rows are valid until the arena is reset
		// at the end of the query.
		for i := range rows {
			row := rows[i]
			if execCtx != nil {
				if arena, ok := execCtx.RowArena.(*DT.RowArena); ok && arena != nil {
					arenaRow := arena.AllocRow(len(row.Data), nil)
					copy(arenaRow.Data, row.Data)
					arenaRow.Cols = row.Cols
					arenaRow.Types = row.Types
					arenaRow.ColIndex = row.ColIndex
					row = arenaRow
				} else {
					copied := make([]pl.Value, len(row.Data))
					copy(copied, row.Data)
					row.Data = copied
				}
			} else {
				copied := make([]pl.Value, len(row.Data))
				copy(copied, row.Data)
				row.Data = copied
			}
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
// REQ001637: pre-allocate output slice to eliminate growslice copies.
// REQ001678: cap the pre-alloc at 8 — the row-based fallback path is
// hit mostly by small result sets (e.g. `SELECT count(*) ...` returns
// 1 row), so a 256-row pre-alloc (EngineBatchSize) was almost entirely
// wasted (250MB flat / ~20% of BenchmarkSLT_Update). For larger result
// sets, append grows the slice amortized. Plain `min` builtin (Go 1.21+).
// REQ002016: bump initial cap from 64 → EngineBatchSize (512) to
// eliminate growslice churn for 100-200 row aggregate result sets.
// The row-based path sees enough medium-sized results that the 64-row
// cap caused 134 MB of growslice allocs via repeated doubling.
func drainPlanRows(ctx context.Context, root pl.Operator, execCtx *DT.ExecContext) ([]DT.Row, error) {
	out := make([]DT.Row, 0, OP.EngineBatchSize())
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
