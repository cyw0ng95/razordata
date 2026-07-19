package OP

import (
	"context"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

// VectorizedIndexScan wraps a row-based IndexScan and implements
// BatchProducer by reading up to BatchSize rows and batching them
// into a columnar batch. REQ001602.
type VectorizedIndexScan struct {
	is       *IndexScan
	cols     []string
	types    []LX.TokenType
	colMap   map[string]int
	initDone bool
	done     bool
}

func NewVectorizedIndexScan(is *IndexScan) *VectorizedIndexScan {
	return &VectorizedIndexScan{is: is}
}

func (v *VectorizedIndexScan) NextBatch(ctx context.Context) (*UT.Batch, error) {
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
		row, err := v.is.Next(ctx)
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

func (v *VectorizedIndexScan) appendValue(batch *UT.Batch, colIdx int, val pl.Value) {
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

func (v *VectorizedIndexScan) Close() error {
	if v.is != nil {
		return v.is.Close()
	}
	return nil
}

// VectorizedIndexOnlyScan wraps a row-based IndexOnlyScan and
// implements BatchProducer by reading up to BatchSize rows and
// batching them into a columnar batch. REQ001602.
type VectorizedIndexOnlyScan struct {
	ios      *IndexOnlyScan
	cols     []string
	types    []LX.TokenType
	colMap   map[string]int
	initDone bool
	done     bool
}

func NewVectorizedIndexOnlyScan(ios *IndexOnlyScan) *VectorizedIndexOnlyScan {
	return &VectorizedIndexOnlyScan{ios: ios}
}

func (v *VectorizedIndexOnlyScan) NextBatch(ctx context.Context) (*UT.Batch, error) {
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
		row, err := v.ios.Next(ctx)
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

func (v *VectorizedIndexOnlyScan) appendValue(batch *UT.Batch, colIdx int, val pl.Value) {
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

func (v *VectorizedIndexOnlyScan) Close() error {
	if v.ios != nil {
		return v.ios.Close()
	}
	return nil
}

// VectorizedCoveringIndexScan wraps a covering IndexScan (already
// implements BatchProducer via VectorizedIndexScan's covering fast
// path). This is a type alias for the existing VectorizedCoveringIndexScan
// that already exists. The wrapper is only needed for non-covering scans.
// Re-export the existing type for transformOp.
// Already defined in existing code — this comment is for discoverability.