package OP

import (
	"context"
	"sync/atomic"

	ec "github.com/cyw0ng95/razordata/internal/LOG/EC"
	"github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

type Limit struct {
	child  Operator
	limit  int64
	seen   int64
	params []any

	closed atomic.Bool
}

// Child returns the limit's child operator.
func (l *Limit) Child() Operator     { return l.child }
func (l *Limit) SetChild(c Operator) { l.child = c }

// LimitValue returns the limit value.
func (l *Limit) LimitValue() int64 { return l.limit }

func NewLimit(child Operator, n int64) *Limit {
	return &Limit{child: child, limit: n}
}

// WithParams propagates the bound `?` placeholders (R16-1..2).
func (l *Limit) WithParams(p []any) Operator {
	l.params = p
	return l
}

func (l *Limit) Next(ctx context.Context) (Row, error) {
	ec.BUG_ON(l.closed.Load(), "Limit.Next() after Close()")
	if l.seen >= l.limit {
		return Row{}, ErrNoRows
	}
	row, err := l.child.Next(ctx)
	if err != nil {
		return Row{}, err
	}
	l.seen++
	return row, nil
}

func (l *Limit) Close() error {
	l.closed.Store(true)
	l.seen = 0
	return l.child.Close()
}

// Reset reinitializes Limit cursor. Does NOT close the child. REQ001464.
func (l *Limit) Reset(ctx context.Context) error { l.seen = 0; return nil }

// Offset skips the first n rows from its child before yielding. It pairs
// with Limit to implement LIMIT/OFFSET pagination. A nil child or a
// negative n is treated as zero (no offset).
type Offset struct {
	child   Operator
	offset  int64
	skipped int64
	params  []any

	closed atomic.Bool
}

// Child returns the offset's child operator.
func (o *Offset) Child() Operator     { return o.child }
func (o *Offset) SetChild(c Operator) { o.child = c }

// OffsetValue returns the offset value.
func (o *Offset) OffsetValue() int64 { return o.offset }

func NewOffset(child Operator, n int64) *Offset {
	if n < 0 {
		n = 0
	}
	return &Offset{child: child, offset: n}
}

// WithParams propagates the bound `?` placeholders (R16-1..2).
func (o *Offset) WithParams(p []any) Operator {
	o.params = p
	return o
}

func (o *Offset) Next(ctx context.Context) (Row, error) {
	ec.BUG_ON(o.closed.Load(), "Offset.Next() after Close()")
	// REQ001650: fast path — use Skipper when child supports it.
	if skipper, ok := o.child.(Skipper); ok && o.skipped < o.offset {
		if err := skipper.Skip(ctx, o.offset-o.skipped); err != nil {
			return Row{}, err
		}
		o.skipped = o.offset
	}
	for o.skipped < o.offset {
		if err := ctx.Err(); err != nil {
			return Row{}, err
		}
		if _, err := o.child.Next(ctx); err != nil {
			return Row{}, err
		}
		o.skipped++
	}
	return o.child.Next(ctx)
}

func (o *Offset) Close() error {
	o.closed.Store(true)
	o.skipped = 0
	if o.child == nil {
		return nil
	}
	return o.child.Close()
}

// Reset reinitializes Offset cursor. Does NOT close the child. REQ001464.
func (o *Offset) Reset(ctx context.Context) error { o.skipped = 0; return nil }

// VectorizedOffset skips the first n rows from its child batch producer
// before yielding remaining rows. Unlike the row-based Offset which skips
// one row at a time, it discards full batches in bulk when the remaining
// offset exceeds the batch size. REQ001980.
type VectorizedOffset struct {
	child     UT.BatchProducer
	offset    int64
	remaining int64
}

// NewVectorizedOffset creates a vectorized offset operator.
func NewVectorizedOffset(child UT.BatchProducer, n int64) *VectorizedOffset {
	if n < 0 {
		n = 0
	}
	return &VectorizedOffset{child: child, offset: n, remaining: n}
}

// NextBatch returns the next batch after skipping offset rows.
// Full batches whose entire logical size fits within the remaining
// offset are discarded immediately. When the remaining offset falls
// within a single batch, a truncated batch starting after the skip
// is returned. Subsequent batches pass through unchanged.
func (o *VectorizedOffset) NextBatch(ctx context.Context) (*UT.Batch, error) {
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
		// Partial skip: slice this batch to drop the first `remaining` rows.
		skip := int(o.remaining)
		o.remaining = 0
		return sliceBatch(batch, skip)
	}
	return o.child.NextBatch(ctx)
}

// Close releases resources.
func (o *VectorizedOffset) Close() error {
	if o.child != nil {
		return o.child.Close()
	}
	return nil
}

// sliceBatch returns a new batch containing rows [start, logicalSize).
// The original batch is Put back to the pool. Handles both plain batches
// and batches with a selection vector.
func sliceBatch(batch *UT.Batch, start int) (*UT.Batch, error) {
	logical := batch.LogicalSize()
	if start >= logical {
		batch.Put()
		return nil, nil
	}
	remaining := logical - start
	numCols := len(batch.Cols)
	// Count actual populated columns (stop at first zero-type column).
	nCols := 0
	for i := 0; i < numCols; i++ {
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
				for r := 0; r < remaining; r++ {
					dst.Nulls[r] = src.Nulls[batch.Sel[start+r]]
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
					for r := 0; r < remaining; r++ {
						dst.Data.Ints[r] = src.Data.Ints[batch.Sel[start+r]]
					}
				} else {
					copy(dst.Data.Ints, src.Data.Ints[start:start+remaining])
				}
			}
		case LX.T_FLOAT_KW:
			if src.Data.Floats != nil {
				dst.Data.Floats = UT.PoolGetFloats(i, remaining)
				if batch.Sel != nil {
					for r := 0; r < remaining; r++ {
						dst.Data.Floats[r] = src.Data.Floats[batch.Sel[start+r]]
					}
				} else {
					copy(dst.Data.Floats, src.Data.Floats[start:start+remaining])
				}
			}
		case LX.T_BOOL:
			if src.Data.Bools != nil {
				dst.Data.Bools = UT.PoolGetBools(i, remaining)
				if batch.Sel != nil {
					for r := 0; r < remaining; r++ {
						dst.Data.Bools[r] = src.Data.Bools[batch.Sel[start+r]]
					}
				} else {
					copy(dst.Data.Bools, src.Data.Bools[start:start+remaining])
				}
			}
		case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
			if src.Data.Strs != nil {
				dst.Data.Strs = UT.PoolGetStrs(i, remaining)
				if batch.Sel != nil {
					for r := 0; r < remaining; r++ {
						dst.Data.Strs[r] = src.Data.Strs[batch.Sel[start+r]]
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

// REQ001088: predicate compilation is now per-Filter. The previous
// global `sync.Map` cache was keyed by `fmt.Sprintf("%v", e)` which
// serialized the entire AST on every call AND was shared across
// all Executors, creating cross-query pollution risk when one
// query's compiled closure referenced a stale row.ColIndex. Per-Filter
// compilation (via Filter.compiledOnce) is simpler and correct.
// `lookupOrCompilePredicate` is kept as a thin wrapper for backward
// compat with tests and any external callers.

// lookupOrCompilePredicate checks the global predicate cache for a
// compiled filter function. On cache miss it compiles and stores the
// result. REQ000802+.
//
// REQ001088: this cache is now best-effort. The Filter struct caches
// its own compiled predicate on first use, so the global cache only
// helps when many Filters share the same predicate text AND the
// compiled closure doesn't depend on per-Filter state. Most
