package OP

import (
	"context"

	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// FusedBatchScan is a vectorized BatchProducer that inlines Filter,
// Project, and Limit into a single NextBatch loop over a child
// BatchProducer (typically a VectorizedSeqScan). For the eligible
// shape (Limit(Project(Filter(SeqScan)))), this eliminates the
// per-batch handoff overhead of three separate operators.
//
// Compared to the push pipeline (PushFilter→PushProject→PushLimit),
// FusedBatchScan performs one NextBatch call per output batch
// instead of three PushBatch calls. For OLTP-sized queries this
// cuts per-batch overhead to near zero.
//
// REQ002003. The existing row-based FusedScan (fused.go) targets
// small in-memory tables; FusedBatchScan targets the vectorized
// store-backed path.
//
// Filter behavior: delegates to EV.EvalBatch; sets batch.Sel and
// batch.Size for partial matches, drops empty batches, forwards
// full matches unchanged.
//
// Project behavior: when exprs is non-nil, allocates one output
// batch per input batch and evaluates each expression into a column.
// When exprs is nil (SELECT *), passes the (possibly filtered)
// batch through. Compacts columns when the input has a Sel vector.
//
// Limit behavior: when limit >= 0, truncates the output batch in
// place (via truncateBatchInPlace) when it exceeds the remaining
// budget, then signals EOF on the next call.
type FusedBatchScan struct {
	source        UT.BatchProducer
	pred          PS.Expr
	params        []any
	exprs         []PS.Expr
	names         []string
	compiledEvals []batchEvalFunc
	limit         int64 // -1 = unlimited
	remaining     int64
	done          bool
}

// NewFusedBatchScan constructs a FusedBatchScan. Pass nil for pred
// to skip filtering, nil for exprs to skip projection (SELECT *),
// and -1 for limit to disable the limit. REQ002003.
func NewFusedBatchScan(source UT.BatchProducer, pred PS.Expr, exprs []PS.Expr, names []string, limit int64) *FusedBatchScan {
	f := &FusedBatchScan{
		source:    source,
		pred:      pred,
		exprs:     exprs,
		names:     names,
		limit:     limit,
		remaining: limit,
	}
	if exprs != nil {
		f.compiledEvals = make([]batchEvalFunc, len(exprs))
		for i, expr := range exprs {
			f.compiledEvals[i] = compileProjectExpr(expr)
		}
	}
	return f
}

// NextBatch produces the next output batch with filter+project+limit
// applied in a single pass. Returns (nil, nil) at EOF or when the
// limit is exhausted. REQ002003.
func (f *FusedBatchScan) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if f.done {
		return nil, nil
	}
	if f.limit >= 0 && f.remaining <= 0 {
		f.done = true
		return nil, nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		batch, err := f.source.NextBatch(ctx)
		if err != nil {
			return nil, err
		}
		if batch == nil {
			f.done = true
			return nil, nil
		}

		// ---- Filter ----
		if f.pred != nil {
			sel := EV.EvalBatch(f.pred, batch, f.params)
			if sel == nil {
				// All rows match — fall through with batch unchanged.
			} else if len(sel) == 0 {
				// No rows match — drop and continue.
				batch.Put()
				continue
			} else {
				batch.Sel = sel
				batch.Size = len(sel)
			}
		}

		// ---- Project ----
		var out *UT.Batch
		if f.exprs != nil {
			out = f.applyProjection(batch)
			batch.Put()
		} else {
			out = batch
		}

		// ---- Limit ----
		if f.limit >= 0 {
			logical := int64(out.LogicalSize())
			if logical > f.remaining {
				UT.TruncateBatchInPlace(out, int(f.remaining))
				f.remaining = 0
				return out, nil
			}
			f.remaining -= logical
		}
		return out, nil
	}
}

// applyProjection allocates a new output batch and evaluates each
// projection expression into a column. Mirrors VectorizedProject.
// The caller owns the returned batch; the input batch is released
// by the caller. REQ002003.
func (f *FusedBatchScan) applyProjection(batch *UT.Batch) *UT.Batch {
	n := batch.LogicalSize()
	output := UT.GetBatch(len(f.exprs))
	output.Size = n
	for i, expr := range f.exprs {
		var col UT.Column
		if i < len(f.compiledEvals) && f.compiledEvals[i] != nil {
			col = f.compiledEvals[i](batch)
		} else {
			col = EV.EvalBatchExpr(expr, batch, nil)
		}
		col.Name = f.names[i]
		// When the filter set a selection vector, compact the column
		// to the selected logical rows so the output is densely packed.
		if batch.Sel != nil {
			col = UT.CompactColumn(col, batch.Sel, batch.Size)
		}
		output.Cols[i] = col
	}
	return output
}

// Close releases the child operator. REQ002003.
func (f *FusedBatchScan) Close() error {
	if f.source != nil {
		return f.source.Close()
	}
	return nil
}

// Compile-time interface check.
var _ UT.BatchProducer = (*FusedBatchScan)(nil)
