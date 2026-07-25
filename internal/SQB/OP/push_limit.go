package OP

import (
	"context"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// PushLimit is the push-based equivalent of VectorizedLimit.
// It truncates each incoming batch in place to the remaining row
// budget and forwards it. Once the limit is reached, it signals
// stop by returning ErrPushLimitReached, which runWith propagates
// upstream to halt further source reads. REQ002002.
//
// Limit behavior matches VectorizedLimit:
//   - remaining <= 0                          → stop (ErrPushLimitReached).
//   - batch.LogicalSize() > remaining         → truncate in place, emit, stop.
//   - otherwise                               → forward, decrement remaining.
type PushLimit struct {
	basePushOp
	limit     int64
	remaining int64
}

// NewPushLimit constructs a PushLimit capped at n rows. A negative
// n is treated as unlimited (matches Limit semantics).
func NewPushLimit(n int64) *PushLimit {
	if n < 0 {
		n = -1
	}
	return &PushLimit{limit: n, remaining: n}
}

// LimitValue returns the configured limit.
func (l *PushLimit) LimitValue() int64 { return l.limit }

// PushBatch truncates the batch to the remaining limit and forwards
// it. Returns ErrPushLimitReached when the limit is exhausted,
// signaling runWith to stop pulling from the source.
func (l *PushLimit) PushBatch(ctx context.Context, batch *UT.Batch) error {
	if l.limit < 0 {
		// Unlimited — pass through.
		return l.sink.PushBatch(ctx, batch)
	}
	if l.remaining <= 0 {
		batch.Put()
		return ErrPushLimitReached
	}
	logical := int64(batch.LogicalSize())
	if logical > l.remaining {
		truncateBatchInPlace(batch, int(l.remaining))
		l.remaining = 0
		// Forward the truncated batch, then signal stop on the next call.
		// To halt upstream reads immediately, return ErrPushLimitReached
		// AFTER forwarding so the sink sees the final partial batch.
		if err := l.sink.PushBatch(ctx, batch); err != nil {
			return err
		}
		return ErrPushLimitReached
	}
	l.remaining -= logical
	return l.sink.PushBatch(ctx, batch)
}

// Run pulls batches from the source and pushes them through PushBatch.
func (l *PushLimit) Run(ctx context.Context) error {
	return l.runWith(ctx, l.PushBatch)
}

// Close releases the child operator.
func (l *PushLimit) Close() error {
	if l.child != nil {
		return l.child.Close()
	}
	return nil
}

// truncateBatchInPlace slices each column's Data slice to the first
// n logical rows. Handles both plain batches and batches with a
// selection vector (resolves physical indices via Sel). No new
// allocations — the underlying arrays remain owned by the batch
// and are released via Put. Mirrors the truncation logic in
// VectorizedLimit.NextBatch but operates in place.
func truncateBatchInPlace(batch *UT.Batch, n int) {
	if n < 0 {
		return
	}
	if n == 0 {
		batch.Size = 0
		batch.Sel = nil
		return
	}
	// When the batch has a selection vector, the logical rows are
	// Sel[0..Size); truncating to n logical rows means keeping only
	// the first n entries of Sel.
	if batch.Sel != nil {
		if n < len(batch.Sel) {
			batch.Sel = batch.Sel[:n]
		}
		batch.Size = n
		return
	}
	// Plain batch: slice each column's Data arrays in place.
	for i := range batch.Cols {
		col := &batch.Cols[i]
		if col.Type == 0 {
			break
		}
		if col.Nulls != nil && n < len(col.Nulls) {
			col.Nulls = col.Nulls[:n]
		}
		switch col.Type {
		case LX.T_INT_KW, LX.T_BIGINT:
			if col.Data.Ints != nil && n < len(col.Data.Ints) {
				col.Data.Ints = col.Data.Ints[:n]
			}
		case LX.T_FLOAT_KW:
			if col.Data.Floats != nil && n < len(col.Data.Floats) {
				col.Data.Floats = col.Data.Floats[:n]
			}
		case LX.T_BOOL:
			if col.Data.Bools != nil && n < len(col.Data.Bools) {
				col.Data.Bools = col.Data.Bools[:n]
			}
		case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
			if col.Data.Strs != nil && n < len(col.Data.Strs) {
				col.Data.Strs = col.Data.Strs[:n]
			}
		}
	}
	batch.Size = n
}
