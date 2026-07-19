package OP

import (
	"context"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

// VectorizedHashCrossJoin wraps a row-based HashCrossJoin and
// implements BatchProducer by reading up to BatchSize rows and
// batching them into a columnar batch. REQ001602.
type VectorizedHashCrossJoin struct {
	hcj      *HashCrossJoin
	cols     []string
	types    []LX.TokenType
	colMap   map[string]int
	initDone bool
	done     bool
}

// NewVectorizedHashCrossJoin creates a vectorized hash cross join.
func NewVectorizedHashCrossJoin(hcj *HashCrossJoin) *VectorizedHashCrossJoin {
	return &VectorizedHashCrossJoin{hcj: hcj}
}

func (v *VectorizedHashCrossJoin) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if v.done {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	batch := UT.GetBatch(len(v.cols))
	for i, name := range v.cols {
		batch.Cols[i].Name = name
	}
	n := 0
	for n < UT.BatchSize {
		row, err := v.hcj.Next(ctx)
		if err != nil {
			if err == ErrNoRows {
				v.done = true
				break
			}
			batch.Put()
			return nil, err
		}
		if !v.initDone {
			v.cols = make([]string, len(row.Cols))
			v.types = make([]LX.TokenType, len(row.Cols))
			v.colMap = make(map[string]int, len(row.Cols))
			for i, c := range row.Cols {
				v.cols[i] = c
				v.colMap[c] = i
			}
			for i := range row.Cols {
				batch.Cols[i].Name = v.cols[i]
			}
			v.initDone = true
		}
		for i, val := range row.Data {
			v.appendValue(batch, i, val)
		}
		batch.AdvanceSize()
		n++
	}
	if n == 0 {
		batch.Put()
		return nil, nil
	}
	batch.Size = n
	batch.SetColMap(v.colMap)
	return batch, nil
}

func (v *VectorizedHashCrossJoin) appendValue(batch *UT.Batch, colIdx int, val pl.Value) {
	switch val.Kind {
	case pl.KindInt:
		batch.AppendRow(colIdx, LX.T_INT_KW, val.I64, false)
	case pl.KindFloat:
		batch.AppendRow(colIdx, LX.T_FLOAT_KW, val.F64, false)
	case pl.KindText:
		batch.AppendRow(colIdx, LX.T_TEXT, val.S, false)
	case pl.KindBool:
		batch.AppendRow(colIdx, LX.T_BOOL, val.Bo, false)
	case pl.KindBlob:
		batch.AppendRow(colIdx, LX.T_TEXT, string(val.B), false)
	default:
		batch.AppendRow(colIdx, LX.T_NULL, nil, true)
	}
}

func (v *VectorizedHashCrossJoin) Close() error {
	if v.hcj != nil {
		return v.hcj.Close()
	}
	return nil
}