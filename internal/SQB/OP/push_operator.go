package OP

import (
	"context"
	"errors"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// REQ002002: Push-based execution model.
//
// In the pull-based model, every operator implements NextBatch and
// each batch handoff costs one virtual call. For deep pipelines
// (Filter→Project→Limit over SeqScan) this overhead dominates short
// OLTP queries.
//
// The push model inverts control: a source (or the pipeline itself)
// pulls batches from the head and pushes them through a chain of
// PushOperators. Each PushOperator transforms the batch in place
// (or allocates a new one) and forwards it to the next PushSink.
// Limit can short-circuit the entire upstream by returning a
// sentinel error that runWith propagates.
//
// The pull model remains the default; push is opt-in via
// tryPushPipeline in vec_transform.go, gated to conservative shapes.

// ErrPushLimitReached is the sentinel returned by PushLimit once the
// requested row count has been emitted. runWith treats it as a stop
// signal (not a real error) and stops pulling from the source.
var ErrPushLimitReached = errors.New("push: limit reached")

// PushSink consumes batches pushed by an upstream PushOperator.
// The sink takes ownership of the batch: it must either forward it
// (via another PushBatch call) or call batch.Put().
type PushSink interface {
	PushBatch(ctx context.Context, batch *UT.Batch) error
	Close() error
}

// PushOperator is a PushSink that also has a source (the upstream
// BatchProducer it pulls from) and a downstream sink. Run pulls all
// batches from the source and pushes them through the operator's
// PushBatch (which typically forwards to the sink).
type PushOperator interface {
	PushSink
	SetSource(source UT.BatchProducer)
	SetSink(sink PushSink)
	Run(ctx context.Context) error
}

// basePushOp provides the default SetSource/SetSink implementation
// and a runWith helper that pulls batches from the child and pushes
// them through a supplied push function.
type basePushOp struct {
	child UT.BatchProducer
	sink  PushSink
}

func (b *basePushOp) SetSource(source UT.BatchProducer) { b.child = source }
func (b *basePushOp) SetSink(sink PushSink)             { b.sink = sink }

// runWith pulls batches from b.child and invokes push for each one.
// It returns nil on EOF, the context error on cancellation, or the
// first non-nil error from push. ErrPushLimitReached is treated as
// a stop signal and returns nil.
//
// Ownership contract: push takes ownership of the batch. If push
// returns nil, ownership has been transferred downstream. If push
// returns a non-sentinel error, runWith assumes the operator did NOT
// release the batch and calls Put. ErrPushLimitReached is special:
// the operator has already handled ownership (either Put the batch
// or transferred it to the sink before returning the sentinel), so
// runWith must NOT Put.
func (b *basePushOp) runWith(ctx context.Context, push func(context.Context, *UT.Batch) error) error {
	if b.child == nil {
		return nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		batch, err := b.child.NextBatch(ctx)
		if err != nil {
			return err
		}
		if batch == nil {
			return nil // EOF
		}
		if err := push(ctx, batch); err != nil {
			if errors.Is(err, ErrPushLimitReached) {
				// Operator already handled ownership; do not Put.
				return nil
			}
			batch.Put()
			return err
		}
	}
}
