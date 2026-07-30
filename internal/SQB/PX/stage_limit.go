package PX

import (
	"context"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// LimitStageSpec creates LimitStage instances. A LimitStage caps the
// total number of rows emitted to at most N rows across all output
// batches.
type LimitStageSpec struct {
	Limit int64 // maximum rows to emit; -1 = unlimited
}

// NewRuntime creates a LimitStage from this spec.
func (s *LimitStageSpec) NewRuntime() Stage {
	return &LimitStage{
		limit:     s.Limit,
		remaining: s.Limit,
	}
}

// Category returns CatTransform.
func (s *LimitStageSpec) Category() StageCategory { return CatTransform }

// LimitStage is a Transform-stage that caps the total number of rows
// emitted to at most N rows. It mirrors VectorizedLimit but implements
// the Stage interface with Reset support.
type LimitStage struct {
	child     Stage
	limit     int64
	remaining int64
}

// SetChild sets the child stage (implements ChildSetter).
func (l *LimitStage) SetChild(_ ChildSide, child Stage) {
	l.child = child
}

// PropagateExecContext stores per-execution context. REQ002148.
func (l *LimitStage) PropagateExecContext(ec *DT.ExecContext) {
	_ = ec
}

// NextBatch returns the next batch from the child, truncated if it
// would exceed the remaining row count. Returns (nil, nil) when the
// limit is exhausted or the child reaches EOF.
func (l *LimitStage) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if l.limit >= 0 && l.remaining <= 0 {
		return nil, nil
	}
	batch, err := l.child.NextBatch(ctx)
	if err != nil {
		return nil, err
	}
	if batch == nil {
		return nil, nil
	}

	if l.limit < 0 {
		return batch, nil
	}

	logical := int64(batch.LogicalSize())
	if logical <= l.remaining {
		l.remaining -= logical
		return batch, nil
	}

	// Need to truncate the batch to exactly l.remaining rows.
	truncated := UT.GetBatch(len(batch.Cols))
	truncated.Size = int(l.remaining)
	for i := range batch.Cols {
		src := &batch.Cols[i]
		dst := &truncated.Cols[i]
		dst.Name = src.Name
		dst.Type = src.Type
		n := int(l.remaining)
		switch src.Type {
		case LX.T_INT_KW, LX.T_BIGINT:
			if src.Data.Ints != nil && n <= len(src.Data.Ints) {
				dst.Data.Ints = make([]int64, n)
				copy(dst.Data.Ints, src.Data.Ints[:n])
			}
		case LX.T_FLOAT_KW:
			if src.Data.Floats != nil && n <= len(src.Data.Floats) {
				dst.Data.Floats = make([]float64, n)
				copy(dst.Data.Floats, src.Data.Floats[:n])
			}
		case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
			if src.Data.Strs != nil && n <= len(src.Data.Strs) {
				dst.Data.Strs = make([]string, n)
				copy(dst.Data.Strs, src.Data.Strs[:n])
			}
		case LX.T_BOOL:
			if src.Data.Bools != nil && n <= len(src.Data.Bools) {
				dst.Data.Bools = make([]bool, n)
				copy(dst.Data.Bools, src.Data.Bools[:n])
			}
		}
		if src.Nulls != nil && n <= len(src.Nulls) {
			dst.Nulls = make([]bool, n)
			copy(dst.Nulls, src.Nulls[:n])
		}
	}
	l.remaining = 0
	batch.Put()
	return truncated, nil
}

// Reset resets the limit counter to the original limit value.
func (l *LimitStage) Reset(_ context.Context) error {
	l.remaining = l.limit
	return nil
}

// Close releases the child stage.
func (l *LimitStage) Close() error {
	if l.child != nil {
		return l.child.Close()
	}
	return nil
}
