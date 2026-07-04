package UT

import (
	"context"

	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// BatchToRowAdapter wraps a BatchProducer and implements pl.Operator
// (Next+Close) by extracting one row per Next() call from batches.
type BatchToRowAdapter struct {
	source BatchProducer
	batch  *Batch
	row    int
	done   bool
}

// NewBatchToRowAdapter creates an adapter that wraps a BatchProducer
// as a row-based Operator.
func NewBatchToRowAdapter(source BatchProducer) *BatchToRowAdapter {
	return &BatchToRowAdapter{source: source}
}

// Next returns the next row from the current batch, fetching a new
// batch from the source when the current one is exhausted. Returns
// pl.ErrNoRows at EOF.
func (a *BatchToRowAdapter) Next(ctx context.Context) (pl.Row, error) {
	if a.done {
		return pl.Row{}, pl.ErrNoRows
	}

	// Fetch a new batch if needed.
	if a.batch == nil {
		b, err := a.source.NextBatch(ctx)
		if err != nil {
			return pl.Row{}, err
		}
		if b == nil {
			a.done = true
			return pl.Row{}, pl.ErrNoRows
		}
		a.batch = b
		a.row = 0
	}

	// Check if we've exhausted the current batch.
	if a.row >= a.batch.LogicalSize() {
		if a.batch.Pooled {
			a.batch.Put()
		}
		a.batch = nil
		return a.Next(ctx)
	}

	row := a.batchRow(a.row)
	a.row++
	return row, nil
}

// Close returns the current batch to the pool and closes the source.
func (a *BatchToRowAdapter) Close() error {
	if a.batch != nil && a.batch.Pooled {
		a.batch.Put()
		a.batch = nil
	}
	if a.source != nil {
		return a.source.Close()
	}
	return nil
}

// batchRow extracts a row from the batch at the given logical index,
// respecting the Sel vector.
func (a *BatchToRowAdapter) batchRow(logicalIdx int) pl.Row {
	physicalIdx := logicalIdx
	if a.batch.Sel != nil {
		physicalIdx = int(a.batch.Sel[logicalIdx])
	}

	// Count actual columns (those with non-zero Type).
	n := 0
	for _, c := range a.batch.Cols {
		if c.Type != 0 {
			n++
		}
	}

	cols := make([]string, 0, n)
	types := make([]LX.TokenType, 0, n)
	data := make([]pl.Value, 0, n)

	for i := range a.batch.Cols {
		if a.batch.Cols[i].Type == 0 {
			continue
		}
		cols = append(cols, a.batch.Cols[i].Name)
		types = append(types, a.batch.Cols[i].Type)
		data = append(data, batchValueAt(a.batch.Cols[i], physicalIdx))
	}

	return pl.Row{Cols: cols, Types: types, Data: data}
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
