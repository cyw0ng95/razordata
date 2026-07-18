package EX

import (
	"context"
	"errors"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

// drainBatch drains an operator into []DT.Row. If the root implements
// BatchProducer, it drains via NextBatch() + ToRows() — yielding all
// rows of each batch at once. Otherwise it falls back to Next() row-by-row.
// This eliminates the BatchToRowAdapter downgrade: the vectorized operator
// chain produces 1024-row batches, and we materialize them all at once
// instead of one row at a time.
func drainBatch(ctx context.Context, root pl.Operator) ([]DT.Row, error) {
	if root == nil {
		return nil, errors.New("ex: drainBatch: nil root")
	}

	if bp, ok := root.(UT.BatchProducer); ok {
		return drainBatchProducer(ctx, bp)
	}

	// Fallback: row-based drain.
	return drainPlanRows(ctx, root)
}

// drainBatchProducer drains a BatchProducer into []DT.Row.
func drainBatchProducer(ctx context.Context, bp UT.BatchProducer) ([]DT.Row, error) {
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
		out = append(out, rows...)
		if batch.Pooled {
			batch.Put()
		}
	}
	return out, nil
}

// drainPlanRows drains a row-based pl.Operator into []DT.Row via Next().
// Used as fallback when root is not a BatchProducer.
func drainPlanRows(ctx context.Context, root pl.Operator) ([]DT.Row, error) {
	var out []DT.Row
	for {
		row, err := root.Next(ctx)
		if err != nil {
			if err == DT.ErrNoRows {
				break
			}
			return nil, err
		}
		out = append(out, row)
	}
	return out, nil
}
