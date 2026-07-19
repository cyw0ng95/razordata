package OP

import (
	"context"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

// REQ001617/REQ001626: Pure batch IndexScan — eliminates the row-based
// wrapper. Drains rows from IndexScan.Next() in chunks of BatchSize
// and emits columnar batches directly.
//
// Schema is discovered from the first row, then reused for
// subsequent rows. Columnar arrays are allocated once with BatchSize
// capacity to avoid per-row allocations.
//
// Note: For a truly store-native batch read, the IndexScan iterator
// would need to expose batch-mode reads (Store.NextBatch). That
// requires a new Store API. This implementation bridges the gap by
// draining from Next() in tight loops while maintaining columnar
// output, eliminating the per-row batch-construction overhead.
type BatchIndexScan struct {
	is       *IndexScan
	cols     []string
	types    []LX.TokenType
	colMap   map[string]int
	initDone bool
	done     bool
}

// NewBatchIndexScan creates a pure batch IndexScan that drains rows
// from the row-based IndexScan and emits columnar batches.
// REQ001617/REQ001626.
func NewBatchIndexScan(is *IndexScan) *BatchIndexScan {
	return &BatchIndexScan{is: is}
}

// NextBatch returns the next batch of rows from the IndexScan.
func (v *BatchIndexScan) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if v.done {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Read first row to discover schema.
	if !v.initDone {
		first, err := v.is.Next(ctx)
		if err != nil {
			if err == ErrNoRows {
				v.done = true
				return nil, nil
			}
			return nil, err
		}
		v.cols = make([]string, len(first.Cols))
		v.types = make([]LX.TokenType, len(first.Cols))
		v.colMap = make(map[string]int, len(first.Cols))
		for i, c := range first.Cols {
			v.cols[i] = c
			v.colMap[c] = i
			// Infer type from first row's data (row.Types may be nil).
			if i < len(first.Data) {
				v.types[i] = typeFromValueKind(first.Data[i].Kind)
			}
		}
		v.initDone = true

		// Allocate output batch with column metadata.
		output := v.newOutputBatch()
		// Append the first row directly into the output batch.
		v.appendRowToBatch(output, first, 0)
		// Fill the rest of the batch from subsequent rows.
		return v.fillBatch(ctx, output, 1)
	}

	output := v.newOutputBatch()
	return v.fillBatch(ctx, output, 0)
}

// fillBatch fills the batch from the IndexScan until full or EOF.
// startRow is the number of rows already in the batch (from first-row init).
func (v *BatchIndexScan) fillBatch(ctx context.Context, batch *UT.Batch, startRow int) (*UT.Batch, error) {
	n := startRow
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
		v.appendRowToBatch(batch, row, n)
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

// newOutputBatch allocates a fresh output batch with column metadata.
func (v *BatchIndexScan) newOutputBatch() *UT.Batch {
	output := UT.GetBatch(len(v.cols))
	for i := 0; i < len(v.cols); i++ {
		output.Cols[i].Name = v.cols[i]
		output.Cols[i].Type = v.types[i]
		allocateColData(&output.Cols[i], UT.BatchSize, v.types[i])
	}
	return output
}

// appendRowToBatch appends a single row's values to the output batch
// at the given row index. Uses the pre-allocated columnar arrays
// (no per-row make/slice grow).
func (v *BatchIndexScan) appendRowToBatch(batch *UT.Batch, row pl.Row, dstRow int) {
	for i := 0; i < len(row.Data) && i < len(batch.Cols); i++ {
		val := row.Data[i]
		switch val.Kind {
		case pl.KindInt:
			batch.Cols[i].Data.Ints[dstRow] = val.I64
		case pl.KindFloat:
			batch.Cols[i].Data.Floats[dstRow] = val.F64
		case pl.KindText:
			batch.Cols[i].Data.Strs[dstRow] = val.S
		case pl.KindBool:
			batch.Cols[i].Data.Bools[dstRow] = val.Bo
		case pl.KindBlob:
			batch.Cols[i].Data.Strs[dstRow] = string(val.B)
		default:
			// NULL — mark in Nulls slice.
			if batch.Cols[i].Nulls == nil {
				batch.Cols[i].Nulls = make([]bool, UT.BatchSize)
			}
			batch.Cols[i].Nulls[dstRow] = true
		}
	}
}

// typeFromValueKind infers the LX.TokenType from a pl.Value Kind.
// REQ001617/REQ001626.
func typeFromValueKind(k pl.ValueKind) LX.TokenType {
	switch k {
	case pl.KindInt:
		return LX.T_INT_KW
	case pl.KindFloat:
		return LX.T_FLOAT_KW
	case pl.KindText:
		return LX.T_TEXT
	case pl.KindBool:
		return LX.T_BOOL
	default:
		return LX.T_NULL
	}
}

// Close releases the underlying IndexScan.
func (v *BatchIndexScan) Close() error {
	if v.is != nil {
		return v.is.Close()
	}
	return nil
}

// BatchIndexOnlyScan is the pure batch version of IndexOnlyScan.
// Same pattern as BatchIndexScan.
type BatchIndexOnlyScan struct {
	ios      *IndexOnlyScan
	cols     []string
	types    []LX.TokenType
	colMap   map[string]int
	initDone bool
	done     bool
}

// NewBatchIndexOnlyScan creates a pure batch IndexOnlyScan.
// REQ001617/REQ001626.
func NewBatchIndexOnlyScan(ios *IndexOnlyScan) *BatchIndexOnlyScan {
	return &BatchIndexOnlyScan{ios: ios}
}

// NextBatch returns the next batch of rows from the IndexOnlyScan.
func (v *BatchIndexOnlyScan) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if v.done {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if !v.initDone {
		first, err := v.ios.Next(ctx)
		if err != nil {
			if err == ErrNoRows {
				v.done = true
				return nil, nil
			}
			return nil, err
		}
		v.cols = make([]string, len(first.Cols))
		v.types = make([]LX.TokenType, len(first.Cols))
		v.colMap = make(map[string]int, len(first.Cols))
		for i, c := range first.Cols {
			v.cols[i] = c
			v.colMap[c] = i
			// Infer type from first row's data.
			if i < len(first.Data) {
				v.types[i] = typeFromValueKind(first.Data[i].Kind)
			}
		}
		v.initDone = true

		output := v.newOutputBatch()
		v.appendRowToBatch(output, first, 0)
		return v.fillBatch(ctx, output, 1)
	}

	output := v.newOutputBatch()
	return v.fillBatch(ctx, output, 0)
}

// fillBatch fills the batch from the IndexOnlyScan until full or EOF.
func (v *BatchIndexOnlyScan) fillBatch(ctx context.Context, batch *UT.Batch, startRow int) (*UT.Batch, error) {
	n := startRow
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
		v.appendRowToBatch(batch, row, n)
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

// newOutputBatch allocates a fresh output batch with column metadata.
func (v *BatchIndexOnlyScan) newOutputBatch() *UT.Batch {
	output := UT.GetBatch(len(v.cols))
	for i := 0; i < len(v.cols); i++ {
		output.Cols[i].Name = v.cols[i]
		output.Cols[i].Type = v.types[i]
		allocateColData(&output.Cols[i], UT.BatchSize, v.types[i])
	}
	return output
}

// appendRowToBatch appends a single row's values to the output batch.
func (v *BatchIndexOnlyScan) appendRowToBatch(batch *UT.Batch, row pl.Row, dstRow int) {
	for i := 0; i < len(row.Data) && i < len(batch.Cols); i++ {
		val := row.Data[i]
		switch val.Kind {
		case pl.KindInt:
			batch.Cols[i].Data.Ints[dstRow] = val.I64
		case pl.KindFloat:
			batch.Cols[i].Data.Floats[dstRow] = val.F64
		case pl.KindText:
			batch.Cols[i].Data.Strs[dstRow] = val.S
		case pl.KindBool:
			batch.Cols[i].Data.Bools[dstRow] = val.Bo
		case pl.KindBlob:
			batch.Cols[i].Data.Strs[dstRow] = string(val.B)
		default:
			if batch.Cols[i].Nulls == nil {
				batch.Cols[i].Nulls = make([]bool, UT.BatchSize)
			}
			batch.Cols[i].Nulls[dstRow] = true
		}
	}
}

// Close releases the underlying IndexOnlyScan.
func (v *BatchIndexOnlyScan) Close() error {
	if v.ios != nil {
		return v.ios.Close()
	}
	return nil
}