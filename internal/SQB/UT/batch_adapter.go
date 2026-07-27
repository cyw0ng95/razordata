package UT

import (
	"context"

	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// BatchToRowAdapter wraps a BatchProducer and implements pl.Operator
// (Next+Close) by materialising one batch at a time via Batch.ToRows
// and yielding one row per Next() call. This is the "row-at-a-time
// is BatchSize=1" boundary: the source runs vectorized end-to-end, we
// just iterate its output row by row. REQ001440.
type BatchToRowAdapter struct {
	source BatchProducer
	rows   []pl.Row
	pos    int
	done   bool
	// REQ001663: reusable row buffer across refills. ToRows allocates
	// a fresh []pl.Row per batch; this buffer avoids that allocation
	// by growing to the max batch size and re-slicing.
	rowBuf []pl.Row
	// REQ002096: reusable Data slab for ToRowsShared. Eliminates the
	// per-row make([]pl.Value, nCols) that ToRows() does.
	rowBufData []pl.Value
}

// NewBatchToRowAdapter creates an adapter that wraps a BatchProducer
// as a row-based Operator. Replaces the previous per-row per-Next
// decode loop with a per-batch ToRows() call, amortising the
// allocation cost across the entire batch.
func NewBatchToRowAdapter(source BatchProducer) *BatchToRowAdapter {
	return &BatchToRowAdapter{source: source}
}

// Next returns the next row from the buffered batch materialised
// from source. When the buffer is exhausted, fetches the next batch
// from source and materialises it via ToRows() before resuming.
// Returns pl.ErrNoRows at EOF.
func (a *BatchToRowAdapter) Next(ctx context.Context) (pl.Row, error) {
	if a.done {
		return pl.Row{}, pl.ErrNoRows
	}
	if a.pos >= len(a.rows) {
		// Refill from source: fetch next batch and materialise.
		b, err := a.source.NextBatch(ctx)
		if err != nil {
			return pl.Row{}, err
		}
		if b == nil {
			a.done = true
			return pl.Row{}, pl.ErrNoRows
		}
		// REQ001440: materialise the entire batch at once via
		// ToRows, then drop the batch's pool reference. This
		// amortises the per-batch allocation across every row
		// in the batch instead of allocating a fresh Row per
		// Next.
		// REQ001663: reuse rowBuf across refills to avoid
		// allocating a new []pl.Row per batch.
		// REQ002096: drain the batch directly into a.rowBuf with
		// a shared Data slab (a.rowBufData). This avoids the
		// ToRows() / ToRowsShared() call altogether, saving the
		// `make([]pl.Row, 0, logical)` allocation (13.6% flat
		// alloc in BenchmarkRazordata_SelectGroupBy) and the
		// copy into a.rowBuf.
		names := b.ColNames()
		nCols := len(names)
		logical := b.LogicalSize()
		needed := logical * nCols
		if cap(a.rowBufData) < needed {
			a.rowBufData = make([]pl.Value, needed)
		} else {
			a.rowBufData = a.rowBufData[:needed]
		}
		if cap(a.rowBuf) < logical {
			a.rowBuf = make([]pl.Row, logical)
		} else {
			a.rowBuf = a.rowBuf[:logical]
		}
		for r := 0; r < logical; r++ {
			phys := r
			if b.Sel != nil {
				phys = int(b.Sel[r])
			}
			off := r * nCols
			data := a.rowBufData[off : off+nCols : off+nCols]
			for c := range names {
				data[c] = ToValue(b.Cols[c], phys)
			}
			a.rowBuf[r] = pl.Row{
				Cols: names,
				Data: data,
			}
		}
		a.rows = a.rowBuf
		if b.Pooled {
			b.Put()
		}
		a.pos = 0
		if len(a.rows) == 0 {
			// Empty batch (e.g. all rows filtered out); retry
			// the fetch path so we don't claim EOF prematurely.
			return a.Next(ctx)
		}
	}
	r := a.rows[a.pos]
	a.pos++
	return r, nil
}

// NextBatch forwards to the source BatchProducer. This allows
// consumers that type-assert as BatchProducer to bypass the
// row-at-a-time Next() path and drain entire batches at once.
// REQ001582.
func (a *BatchToRowAdapter) NextBatch(ctx context.Context) (*Batch, error) {
	return a.source.NextBatch(ctx)
}

// Close closes the source.
func (a *BatchToRowAdapter) Close() error {
	a.rows = nil
	a.pos = 0
	if a.source != nil {
		return a.source.Close()
	}
	return nil
}

// batchValueAt converts Column data at the given physical index to a pl.Value.
func batchValueAt(col Column, idx int) pl.Value {
	if col.Nulls != nil && idx < len(col.Nulls) && col.Nulls[idx] {
		return pl.Value{Kind: pl.KindNull}
	}

	switch col.Type {
	case LX.T_INT_KW, LX.T_BIGINT:
		if idx < len(col.Data.Ints) {
			return pl.Value{Kind: pl.KindInt, I64: col.Data.Ints[idx]}
		}
	case LX.T_FLOAT_KW:
		if idx < len(col.Data.Floats) {
			return pl.Value{Kind: pl.KindFloat, F64: col.Data.Floats[idx]}
		}
	case LX.T_BOOL:
		if idx < len(col.Data.Bools) {
			return pl.Value{Kind: pl.KindBool, Bo: col.Data.Bools[idx]}
		}
	case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
		if idx < len(col.Data.Strs) {
			return pl.Value{Kind: pl.KindText, S: col.Data.Strs[idx]}
		}
	}
	return pl.Value{Kind: pl.KindNull}
}
