package PX

import (
	"context"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// FusedScanStageSpec creates FusedScanStage instances. A FusedScanStage
// applies filter + project + limit in a single pass over each batch from
// its source, eliminating the overhead of separate Filter/Project/Limit
// stages for the common case.
type FusedScanStageSpec struct {
	SourceFactory func() UT.BatchProducer // creates the scan producer
	Pred          PS.Expr                 // nil = no filter
	Exprs         []PS.Expr               // nil = no projection (SELECT *)
	Names         []string                // output column names
	CompiledEvals []batchEvalFunc         // compiled fast-path evaluators
	Limit         int64                   // -1 = unlimited
	HasBloom      bool                    // whether to build an IN-list bloom filter
	BloomVals     []int64                 // literal values for bloom filter
}

// NewRuntime creates a FusedScanStage from this spec.
func (s *FusedScanStageSpec) NewRuntime() Stage {
	f := &FusedScanStage{
		source:        s.SourceFactory(),
		pred:          s.Pred,
		exprs:         s.Exprs,
		names:         s.Names,
		compiledEvals: s.CompiledEvals,
		limit:         s.Limit,
		remaining:     s.Limit,
	}
	if s.HasBloom && len(s.BloomVals) > 8 {
		f.inBloom = UT.NewBloomFilter(len(s.BloomVals), 0.01)
		for _, v := range s.BloomVals {
			f.inBloom.Add(uint64(v))
		}
	}
	return f
}

// Category returns CatTransform.
func (s *FusedScanStageSpec) Category() StageCategory { return CatTransform }

// FusedScanStage is a Transform-stage that applies filter, projection,
// and limit in a single pass over each scan batch. It mirrors
// FusedBatchScan but implements the Stage interface with Reset support.
type FusedScanStage struct {
	source        UT.BatchProducer
	pred          PS.Expr
	params        []any
	exprs         []PS.Expr
	names         []string
	compiledEvals []batchEvalFunc
	limit         int64
	remaining     int64
	inBloom       *UT.BloomFilter
	execCtx       *DT.ExecContext
	done          bool
}

// PropagateExecContext stores per-execution context for subquery
// evaluation and row arena. REQ002148.
func (f *FusedScanStage) PropagateExecContext(ec *DT.ExecContext) {
	f.execCtx = ec
}

// PropagateParams receives parameter values for ? placeholders
// (implements ParamPropagator).
func (f *FusedScanStage) PropagateParams(args []any, buf *[]any) {
	// REQ002161: copy into own buffer — the incoming buf is shared
	// across all stages and subsequent stages would overwrite it.
	f.params = append(f.params[:0], args...)
}

// NextBatch applies filter + project + limit in a single pass.
// Returns (nil, nil) at EOF.
func (f *FusedScanStage) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if f.done {
		return nil, nil
	}
	if f.limit >= 0 && f.remaining <= 0 {
		f.done = true
		return nil, nil
	}

	for {
		batch, err := f.source.NextBatch(ctx)
		if err != nil {
			return nil, err
		}
		if batch == nil {
			f.done = true
			return nil, nil
		}
		if f.execCtx != nil {
			batch.ExecCtx = f.execCtx
		}

		// --- Filter ---
		if f.pred != nil {
			sel := EV.EvalBatch(f.pred, batch, f.params)
			if sel != nil && len(sel) == 0 {
				batch.Put()
				continue
			}
			if sel != nil && len(sel) == batch.LogicalSize() {
				// All rows match — no filtering needed.
			} else if sel != nil {
				batch.Sel = sel
				batch.Size = len(sel)
			}
		}

		// --- Project ---
		var out *UT.Batch
		if f.exprs != nil {
			out = UT.GetBatch(len(f.exprs))
			out.Size = batch.LogicalSize()
			for i, expr := range f.exprs {
				var col UT.Column
				if i < len(f.compiledEvals) && f.compiledEvals[i] != nil {
					col = f.compiledEvals[i](batch)
				} else {
					col = EV.EvalBatchExpr(expr, batch, f.params)
				}
				col.Name = f.names[i]
				if batch.Sel != nil && batch.Size < batch.LogicalSize() && batch.Size > 0 {
					physicalSize := 0
					for j := 0; j < batch.Size && j < len(batch.Sel); j++ {
						if int(batch.Sel[j])+1 > physicalSize {
							physicalSize = int(batch.Sel[j]) + 1
						}
					}
					col = compactColumn(col, batch.Sel[:batch.Size], physicalSize)
				}
				out.Cols[i] = col
			}
			batch.Put()
		} else {
			out = batch
		}

		// --- Limit ---
		if f.limit >= 0 {
			logical := out.LogicalSize()
			if int64(logical) > f.remaining {
				truncateBatchInPlace(out, int(f.remaining))
				f.remaining = 0
			} else {
				f.remaining -= int64(logical)
			}
		}

		return out, nil
	}
}

// Reset resets the fused scan to pre-execution state. Since the
// underlying scan producer cannot be reset, returns ErrResetNotSupported.
func (f *FusedScanStage) Reset(_ context.Context) error {
	return ErrResetNotSupported
}

// Close releases the scan producer.
func (f *FusedScanStage) Close() error {
	if f.source != nil {
		return f.source.Close()
	}
	return nil
}

// truncateBatchInPlace slices each column's data arrays and the
// selection vector in place to keep only the first n logical rows.
// No new allocations — the underlying arrays remain owned by the
// batch and are released via Put.
func truncateBatchInPlace(batch *UT.Batch, n int) {
	if n < 0 {
		return
	}
	if n == 0 {
		batch.Size = 0
		batch.Sel = nil
		return
	}
	if batch.Sel != nil {
		if n < len(batch.Sel) {
			batch.Sel = batch.Sel[:n]
		}
		batch.Size = n
		return
	}
	// Plain batch: slice each column in place.
	for i := range batch.Cols {
		col := &batch.Cols[i]
		if col.Type == 0 {
			break
		}
		if col.Nulls != nil && n < len(col.Nulls) {
			col.Nulls = col.Nulls[:n]
		}
		switch col.Type {
		case LX.T_INT_KW, LX.T_BIGINT:
			if col.Data.Ints != nil && n < len(col.Data.Ints) {
				col.Data.Ints = col.Data.Ints[:n]
			}
		case LX.T_FLOAT_KW:
			if col.Data.Floats != nil && n < len(col.Data.Floats) {
				col.Data.Floats = col.Data.Floats[:n]
			}
		case LX.T_BOOL:
			if col.Data.Bools != nil && n < len(col.Data.Bools) {
				col.Data.Bools = col.Data.Bools[:n]
			}
		case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
			if col.Data.Strs != nil && n < len(col.Data.Strs) {
				col.Data.Strs = col.Data.Strs[:n]
			}
		}
	}
	batch.Size = n
}
