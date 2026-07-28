package EX

import (
	"context"
	"errors"
	"fmt"
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

// drainPlanExecCtx is defined in drain_batch_default.go (normal mode)
// and drain_batch_shadow.go (build tag px_validate mode). REQ002140.

// drainPipeline drains a plan through the PipelineExecutor.
// REQ002133: builds a PipelineSpec via decomposePlan (REQ002138) which
// produces concrete StageSpecs. For unsupported operators, the spec
// wraps the plan tree in LegacyBatchStageSpec, which invokes the
// SpecializeFunc at runtime to call tryVectorizePlan and produce a
// batch producer.
//
// REQ002141: drainPipeline is now invoked from drainPlanExecCtx as the
// primary path. If pipeline compilation or execution fails, the caller
// falls back to drainBatch. Panics in the pipeline path are also
// recovered and converted to errors so the caller can fall back.
func (e *Executor) drainPipeline(ctx context.Context, plan *pl.PlanResult) (rows []DT.Row, err error) {
	if e.pipelineBuilder == nil {
		return nil, errors.New("ex: pipeline not available")
	}
	// REQ002141: defer recover to convert pipeline panics into errors
	// so drainPlanExecCtx can fall back to drainBatch cleanly.
	defer func() {
		if r := recover(); r != nil {
			rows = nil
			err = fmt.Errorf("ex: pipeline panic: %v", r)
		}
	}()
	// Vectorize the plan tree. tryVectorizePlan returns a DT.Operator
	// that may be a BatchProducer (e.g., BatchToRowAdapter wrapping
	// VectorizedSeqScan). Use it directly as the Specialize result to
	// avoid double-wrapping through RowOperatorAsProducer.
	vec := tryVectorizePlan(plan.Root, e.planner)
	spec := &PX.PipelineSpec{
		RootIdx: 0,
		Stages: []PX.StageSpec{
			&PX.LegacyBatchStageSpec{
				Root:    vec,
				Planner: e.planner,
				Specialize: func(root DT.Operator, _ pl.QueryPlanner) UT.BatchProducer {
					if bp, ok := root.(UT.BatchProducer); ok {
						return bp
					}
					return UT.NewBatchToRowAdapter(PX.RowOperatorAsProducer{Op: root})
				},
			},
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

// CompareRows compares two []DT.Row slices for shadow validation.
// REQ002140. Returns the count of mismatches.
// Both slices may be nil/empty (treated as equal).
// Per-row comparison uses Data values directly to avoid issues with
// RowEqual's Cols-length check when Cols is nil.
func CompareRows(pipeline, legacy []DT.Row) int {
	if len(pipeline) != len(legacy) {
		// Row count mismatch counts as 1 mismatch.
		return 1 + absDiff(len(pipeline), len(legacy))
	}
	mismatches := 0
	for i := range pipeline {
		if !rowsDataEqual(pipeline[i], legacy[i]) {
			mismatches++
		}
	}
	return mismatches
}

// rowsDataEqual compares two rows by their Data values only.
// More robust than DT.RowEqual which requires matching Cols length.
func rowsDataEqual(a, b DT.Row) bool {
	if len(a.Data) != len(b.Data) {
		return false
	}
	for i := range a.Data {
		if !valueEqual(a.Data[i], b.Data[i]) {
			return false
		}
	}
	return true
}

// valueEqual compares two DT.Value instances.
func valueEqual(a, b DT.Value) bool {
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case DT.KindInt:
		return a.I64 == b.I64
	case DT.KindFloat:
		return a.F64 == b.F64
	case DT.KindText, DT.KindBlob:
		return a.S == b.S
	case DT.KindBool:
		return a.Bo == b.Bo
	case DT.KindNull:
		return true
	default:
		return false
	}
}

func absDiff(a, b int) int {
	if a > b {
		return a - b
	}
	return b - a
}
