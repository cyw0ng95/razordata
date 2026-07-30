package PX

import (
	"context"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// OffsetStageSpec creates OffsetStage instances. An OffsetStage skips
// the first N rows from its child, then passes through all remaining
// rows.
type OffsetStageSpec struct {
	Offset int64 // number of rows to skip; clamped to 0 if negative
}

// NewRuntime creates an OffsetStage from this spec.
func (s *OffsetStageSpec) NewRuntime() Stage {
	offset := s.Offset
	if offset < 0 {
		offset = 0
	}
	return &OffsetStage{
		offset:    offset,
		remaining: offset,
	}
}

// Category returns CatTransform.
func (s *OffsetStageSpec) Category() StageCategory { return CatTransform }

// OffsetStage is a Transform-stage that skips the first N rows from
// its child, then passes through all remaining rows. It mirrors
// VectorizedOffset but implements the Stage interface with Reset
// support.
type OffsetStage struct {
	child     Stage
	offset    int64
	remaining int64
}

// SetChild sets the child stage (implements ChildSetter).
func (o *OffsetStage) SetChild(_ ChildSide, child Stage) {
	o.child = child
}

// PropagateExecContext stores per-execution context. REQ002148.
func (o *OffsetStage) PropagateExecContext(ec *DT.ExecContext) {
	_ = ec
}

// NextBatch skips the first N rows, then passes through the rest.
// Returns (nil, nil) at EOF.
func (o *OffsetStage) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Skip phase: discard entire batches until remaining is exhausted.
	for o.remaining > 0 {
		batch, err := o.child.NextBatch(ctx)
		if err != nil {
			return nil, err
		}
		if batch == nil {
			return nil, nil
		}
		logical := int64(batch.LogicalSize())
		if logical <= o.remaining {
			o.remaining -= logical
			batch.Put()
			continue
		}
		// Partial skip: slice batch from offset.
		skip := int(o.remaining)
		o.remaining = 0
		return sliceBatch(batch, skip)
	}

	// Pass-through phase: just forward child batches.
	return o.child.NextBatch(ctx)
}

// Reset resets the skip counter to the original offset value.
func (o *OffsetStage) Reset(_ context.Context) error {
	o.remaining = o.offset
	return nil
}

// Close releases the child stage.
func (o *OffsetStage) Close() error {
	if o.child != nil {
		return o.child.Close()
	}
	return nil
}

// sliceBatch returns a new batch containing rows [start, logicalSize).
// The original batch is returned to the pool. Handles both plain
// batches and batches with a selection vector.
func sliceBatch(batch *UT.Batch, start int) (*UT.Batch, error) {
	logical := batch.LogicalSize()
	if start >= logical {
		batch.Put()
		return nil, nil
	}
	remaining := logical - start

	// Count populated columns.
	nCols := 0
	for i := range batch.Cols {
		if batch.Cols[i].Type == 0 && batch.Cols[i].Name == "" {
			break
		}
		nCols = i + 1
	}

	out := UT.GetBatch(nCols)
	out.Size = remaining

	for i := 0; i < nCols; i++ {
		src := &batch.Cols[i]
		dst := &out.Cols[i]
		dst.Name = src.Name
		dst.Type = src.Type

		if src.Nulls != nil {
			dst.Nulls = make([]bool, remaining)
			if batch.Sel != nil {
				for r := range remaining {
					idx := start + r
					if idx < len(batch.Sel) && int(batch.Sel[idx]) < len(src.Nulls) {
						dst.Nulls[r] = src.Nulls[batch.Sel[idx]]
					}
				}
			} else {
				copy(dst.Nulls, src.Nulls[start:start+remaining])
			}
		}

		switch src.Type {
		case LX.T_INT_KW, LX.T_BIGINT:
			if src.Data.Ints != nil {
				dst.Data.Ints = UT.PoolGetInts(i, remaining)
				if batch.Sel != nil {
					for r := range remaining {
						idx := start + r
						if idx < len(batch.Sel) && int(batch.Sel[idx]) < len(src.Data.Ints) {
							dst.Data.Ints[r] = src.Data.Ints[batch.Sel[idx]]
						}
					}
				} else {
					copy(dst.Data.Ints, src.Data.Ints[start:start+remaining])
				}
			}
		case LX.T_FLOAT_KW:
			if src.Data.Floats != nil {
				dst.Data.Floats = UT.PoolGetFloats(i, remaining)
				if batch.Sel != nil {
					for r := range remaining {
						idx := start + r
						if idx < len(batch.Sel) && int(batch.Sel[idx]) < len(src.Data.Floats) {
							dst.Data.Floats[r] = src.Data.Floats[batch.Sel[idx]]
						}
					}
				} else {
					copy(dst.Data.Floats, src.Data.Floats[start:start+remaining])
				}
			}
		case LX.T_BOOL:
			if src.Data.Bools != nil {
				dst.Data.Bools = UT.PoolGetBools(i, remaining)
				if batch.Sel != nil {
					for r := range remaining {
						idx := start + r
						if idx < len(batch.Sel) && int(batch.Sel[idx]) < len(src.Data.Bools) {
							dst.Data.Bools[r] = src.Data.Bools[batch.Sel[idx]]
						}
					}
				} else {
					copy(dst.Data.Bools, src.Data.Bools[start:start+remaining])
				}
			}
		case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
			if src.Data.Strs != nil {
				dst.Data.Strs = UT.PoolGetStrs(i, remaining)
				if batch.Sel != nil {
					for r := range remaining {
						idx := start + r
						if idx < len(batch.Sel) && int(batch.Sel[idx]) < len(src.Data.Strs) {
							dst.Data.Strs[r] = src.Data.Strs[batch.Sel[idx]]
						}
					}
				} else {
					copy(dst.Data.Strs, src.Data.Strs[start:start+remaining])
				}
			}
		}
	}

	batch.Put()
	return out, nil
}
