package UT

import (
	"context"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

// ScalarBatchProducer wraps a BatchProducer to return single-row batches.
// This enables scalar execution as a special case of vectorized execution:
// batch size = 1. It is used for:
//  1. Fallback when vectorization is not eligible
//  2. Mixed plans (vectorized outer, scalar inner)
//  3. Backward compatibility with DT.Operator API
//
// REQ001601.
type ScalarBatchProducer struct {
	wrapped BatchProducer
	buffer  *Batch // leftover rows from previous NextBatch
	bufPos  int    // current position in buffer
}

// NewScalarBatchProducer creates a ScalarBatchProducer that wraps bp.
// Every NextBatch call returns a batch of exactly 1 row (or EOF).
func NewScalarBatchProducer(bp BatchProducer) *ScalarBatchProducer {
	return &ScalarBatchProducer{wrapped: bp}
}

// NextBatch returns the next single-row batch from the wrapped producer.
// When the internal buffer has leftover rows, it serves from the buffer
// first. At EOF it returns (nil, nil).
func (s *ScalarBatchProducer) NextBatch(ctx context.Context) (*Batch, error) {
	// Serve from buffer first if available.
	if s.buffer != nil && s.bufPos < s.buffer.Size {
		// Create a single-row batch from the buffer.
		out := GetBatch(len(s.buffer.Cols))
		out.Size = 1
		for i := range out.Cols {
			out.Cols[i].Name = s.buffer.Cols[i].Name
			out.Cols[i].Type = s.buffer.Cols[i].Type
			switch s.buffer.Cols[i].Type {
			case LX.T_INT_KW, LX.T_BIGINT:
				if s.buffer.Cols[i].Data.Ints != nil && s.bufPos < len(s.buffer.Cols[i].Data.Ints) {
					out.Cols[i].Data.Ints = colDataPool.getInts(i, 1)
					out.Cols[i].Data.Ints[0] = s.buffer.Cols[i].Data.Ints[s.bufPos]
				}
			case LX.T_FLOAT_KW:
				if s.buffer.Cols[i].Data.Floats != nil && s.bufPos < len(s.buffer.Cols[i].Data.Floats) {
					out.Cols[i].Data.Floats = colDataPool.getFloats(i, 1)
					out.Cols[i].Data.Floats[0] = s.buffer.Cols[i].Data.Floats[s.bufPos]
				}
			case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
				if s.buffer.Cols[i].Data.Strs != nil && s.bufPos < len(s.buffer.Cols[i].Data.Strs) {
					out.Cols[i].Data.Strs = colDataPool.getStrs(i, 1)
					out.Cols[i].Data.Strs[0] = s.buffer.Cols[i].Data.Strs[s.bufPos]
				}
			case LX.T_BOOL:
				if s.buffer.Cols[i].Data.Bools != nil && s.bufPos < len(s.buffer.Cols[i].Data.Bools) {
					out.Cols[i].Data.Bools = colDataPool.getBools(i, 1)
					out.Cols[i].Data.Bools[0] = s.buffer.Cols[i].Data.Bools[s.bufPos]
				}
			}
			if s.buffer.Cols[i].Nulls != nil && s.bufPos < len(s.buffer.Cols[i].Nulls) && s.buffer.Cols[i].Nulls[s.bufPos] {
				out.Cols[i].Nulls = []bool{true}
			}
		}
		s.bufPos++
		return out, nil
	}

	// Buffer exhausted or empty — fetch next batch from source.
	batch, err := s.wrapped.NextBatch(ctx)
	if err != nil {
		return nil, err
	}
	if batch == nil {
		s.buffer = nil
		s.bufPos = 0
		return nil, nil
	}

	if batch.Size <= 1 {
		// Single-row batch — return as-is.
		return batch, nil
	}

	// Multi-row batch — save remainder in buffer, return first row.
	s.buffer = batch
	s.bufPos = 1

	out := GetBatch(len(batch.Cols))
	out.Size = 1
	for i := range out.Cols {
		out.Cols[i].Name = batch.Cols[i].Name
		out.Cols[i].Type = batch.Cols[i].Type
		switch batch.Cols[i].Type {
		case LX.T_INT_KW, LX.T_BIGINT:
			if batch.Cols[i].Data.Ints != nil && 0 < len(batch.Cols[i].Data.Ints) {
				out.Cols[i].Data.Ints = colDataPool.getInts(i, 1)
				out.Cols[i].Data.Ints[0] = batch.Cols[i].Data.Ints[0]
			}
		case LX.T_FLOAT_KW:
			if batch.Cols[i].Data.Floats != nil && 0 < len(batch.Cols[i].Data.Floats) {
				out.Cols[i].Data.Floats = colDataPool.getFloats(i, 1)
				out.Cols[i].Data.Floats[0] = batch.Cols[i].Data.Floats[0]
			}
		case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
			if batch.Cols[i].Data.Strs != nil && 0 < len(batch.Cols[i].Data.Strs) {
				out.Cols[i].Data.Strs = colDataPool.getStrs(i, 1)
				out.Cols[i].Data.Strs[0] = batch.Cols[i].Data.Strs[0]
			}
		case LX.T_BOOL:
			if batch.Cols[i].Data.Bools != nil && 0 < len(batch.Cols[i].Data.Bools) {
				out.Cols[i].Data.Bools = colDataPool.getBools(i, 1)
				out.Cols[i].Data.Bools[0] = batch.Cols[i].Data.Bools[0]
			}
		}
		if batch.Cols[i].Nulls != nil && 0 < len(batch.Cols[i].Nulls) && batch.Cols[i].Nulls[0] {
			out.Cols[i].Nulls = []bool{true}
		}
	}
	return out, nil
}

// Close closes the wrapped BatchProducer.
func (s *ScalarBatchProducer) Close() error {
	if s.buffer != nil {
		s.buffer.Put()
		s.buffer = nil
		s.bufPos = 0
	}
	if s.wrapped != nil {
		return s.wrapped.Close()
	}
	return nil
}

// IsBatchProducer checks whether an operator implements BatchProducer.
// REQ001601.
func IsBatchProducer(op pl.Operator) bool {
	_, ok := op.(BatchProducer)
	return ok
}

// REQ001614: WrapOperator and operatorBatchAdapter removed.
// All operators go through BatchProducer directly.
