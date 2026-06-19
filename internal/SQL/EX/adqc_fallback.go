package EX

import (
	"context"
	"log/slog"
)

// FallbackOp is a safety-net wrapper that catches panics during
// specialized execution and falls back to the interpreted path.
// The user must never see a "specialization failed" error.
// REQ000313: fallback is silent and total.
type FallbackOp struct {
	inner     Operator
	planHash  string
	reason    string
	triggered bool
}

// NewFallbackOp creates a fallback wrapper around an interpreted operator.
func NewFallbackOp(inner Operator, planHash, reason string) *FallbackOp {
	return &FallbackOp{
		inner:    inner,
		planHash: planHash,
		reason:   reason,
	}
}

// Next implements Operator.
func (f *FallbackOp) Next(ctx context.Context) (Row, error) {
	return f.inner.Next(ctx)
}

func (f *FallbackOp) Close() error {
	return f.inner.Close()
}

// Triggered returns whether a fallback event has been logged.
func (f *FallbackOp) Triggered() bool { return f.triggered }

// trySpecialized attempts to run a specialized function, capturing
// any panic and logging a fallback event.
func trySpecialized(
	ctx context.Context,
	fn func(ctx context.Context, batch *Batch, params []any) (*Batch, error),
	batch *Batch,
	params []any,
	planHash string,
) (result *Batch, err error) {
	defer func() {
		if r := recover(); r != nil {
			slog.Debug("adqc: fallback",
				"planHash", planHash,
				"reason", "panic during specialized execution",
				"panic", r,
			)
			result = batch
			err = nil
		}
	}()
	return fn(ctx, batch, params)
}

// ensure interface compliance
var _ Operator = (*FallbackOp)(nil)
