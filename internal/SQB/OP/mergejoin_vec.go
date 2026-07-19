package OP

import (
	"context"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

// VectorizedMergeJoin wraps a row-based MergeJoin and implements
// BatchProducer by reading up to BatchSize rows and batching them
// into a columnar batch. REQ001602.
type VectorizedMergeJoin struct {
	mj        *MergeJoin
	cols      []string
	types     []LX.TokenType
	colMap    map[string]int
	initDone  bool
	done      bool
}

// NewVectorizedMergeJoin creates a vectorized merge join from a
// row-based MergeJoin operator.
func NewVectorizedMergeJoin(mj *MergeJoin) *VectorizedMergeJoin {
	return &VectorizedMergeJoin{mj: mj}
}

// NextBatch reads the next batch of rows from the underlying MergeJoin.
func (j *VectorizedMergeJoin) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if j.done {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if !j.initDone {
		// Read first row to discover schema.
		first, err := j.mj.Next(ctx)
		if err != nil {
			if err == ErrNoRows {
				j.done = true
				return nil, nil
			}
			return nil, err
		}
		j.cols = append([]string(nil), first.Cols...)
		j.types = append([]LX.TokenType(nil), first.Types...)
		j.colMap = make(map[string]int, len(first.Cols))
		for i, c := range first.Cols {
			j.colMap[c] = i
		}
		j.initDone = true

		batch := j.rowToBatch(first, nil)
		return j.fillBatch(ctx, batch, 1)
	}

	batch := UT.GetBatch(len(j.cols))
	for i, name := range j.cols {
		batch.Cols[i].Name = name
	}
	return j.fillBatch(ctx, batch, 0)
}

// fillBatch fills the batch from the MergeJoin until full or EOF.
func (j *VectorizedMergeJoin) fillBatch(ctx context.Context, batch *UT.Batch, started int) (*UT.Batch, error) {
	n := started
	for n < UT.BatchSize {
		row, err := j.mj.Next(ctx)
		if err != nil {
			if err == ErrNoRows {
				j.done = true
				break
			}
			batch.Put()
			return nil, err
		}
		j.rowToBatch(row, batch)
		n++
	}
	if n == 0 {
		batch.Put()
		return nil, nil
	}
	batch.Size = n
	batch.SetColMap(j.colMap)
	return batch, nil
}

// rowToBatch appends a single row to the batch, or creates a new batch
// if batch is nil.
func (j *VectorizedMergeJoin) rowToBatch(row pl.Row, batch *UT.Batch) *UT.Batch {
	if batch == nil {
		batch = UT.GetBatch(len(row.Cols))
		for i, name := range row.Cols {
			batch.Cols[i].Name = name
		}
	}
	for i, v := range row.Data {
		j.appendValue(batch, i, v)
	}
	batch.AdvanceSize()
	return batch
}

func (j *VectorizedMergeJoin) appendValue(batch *UT.Batch, colIdx int, v pl.Value) {
	switch v.Kind {
	case pl.KindInt:
		batch.AppendRow(colIdx, LX.T_INT_KW, v.I64, false)
	case pl.KindFloat:
		batch.AppendRow(colIdx, LX.T_FLOAT_KW, v.F64, false)
	case pl.KindText:
		batch.AppendRow(colIdx, LX.T_TEXT, v.S, false)
	case pl.KindBool:
		batch.AppendRow(colIdx, LX.T_BOOL, v.Bo, false)
	case pl.KindBlob:
		batch.AppendRow(colIdx, LX.T_TEXT, string(v.B), false)
	default:
		batch.AppendRow(colIdx, LX.T_NULL, nil, true)
	}
}

// Close delegates to the underlying MergeJoin.
func (j *VectorizedMergeJoin) Close() error {
	if j.mj != nil {
		return j.mj.Close()
	}
	return nil
}