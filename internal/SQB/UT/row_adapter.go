package UT

import "context"

// RowOperatorAdapter wraps a row-based Operator to satisfy
// BatchProducer. Each NextBatch call invokes Inner.Next(),
// packs the result into a single-row Batch, and returns it.
// EOF is signaled by returning (nil, nil).
type RowOperatorAdapter struct {
	inner Operator
	done  bool
}

// NewRowOperatorAdapter creates an adapter that wraps a
// row-based Operator as a BatchProducer.
func NewRowOperatorAdapter(inner Operator) *RowOperatorAdapter {
	return &RowOperatorAdapter{inner: inner}
}

// NextBatch calls Inner.Next() and packs the row into a 1-row batch.
func (a *RowOperatorAdapter) NextBatch(ctx context.Context) (*Batch, error) {
	if a.done {
		return nil, nil
	}
	row, err := a.inner.Next(ctx)
	if err != nil {
		if err == ErrNoRows {
			a.done = true
			return nil, nil
		}
		return nil, err
	}
	n := len(row.Cols)
	b := GetBatch(n)
	b.Size = 0
	for i, name := range row.Cols {
		b.SetColumnName(i, name)
	}
	for i, v := range row.Data {
		isNull := v.IsNull()
		var raw any
		if !isNull {
			switch v.Kind {
			case KindInt:
				raw = v.I64
			case KindFloat:
				raw = v.F64
			case KindText:
				raw = v.S
			case KindBool:
				raw = v.Bo
			}
		}
		if i < len(row.Types) {
			b.AppendRow(i, row.Types[i], raw, isNull)
		}
	}
	b.AdvanceSize()
	b.colMap = nil
	return b, nil
}

// Close delegates to Inner.Close.
func (a *RowOperatorAdapter) Close() error {
	if a.inner != nil {
		return a.inner.Close()
	}
	return nil
}
