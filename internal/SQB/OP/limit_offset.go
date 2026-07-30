package OP

import (
	"context"
	"sync/atomic"

	ec "github.com/cyw0ng95/razordata/internal/LOG/EC"
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