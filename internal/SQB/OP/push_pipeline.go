package OP

import (
	"context"
	"sync"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// CollectingSink is a PushSink that buffers all batches pushed into
// it for later draining via NextBatch. Used by PushPipeline to adapt
// the push model back to the pull-based BatchProducer interface.
// REQ002002.
//
// The sink is not goroutine-safe; it is intended for synchronous
// pipeline runs (PushToPullAdapter runs the pipeline to completion
// on the first NextBatch call).
type CollectingSink struct {
	mu      sync.Mutex
	batches []*UT.Batch
	idx     int
	closed  bool
}

// NewCollectingSink constructs an empty CollectingSink.
func NewCollectingSink() *CollectingSink {
	return &CollectingSink{}
}

// PushBatch appends the batch to the buffer. Takes ownership of the
// batch. If the sink is closed, the batch is dropped immediately.
func (c *CollectingSink) PushBatch(_ context.Context, batch *UT.Batch) error {
	if batch == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		batch.Put()
		return nil
	}
	c.batches = append(c.batches, batch)
	return nil
}

// NextBatch returns the next buffered batch, or (nil, nil) at EOF.
// The caller takes ownership of the returned batch and must call Put.
func (c *CollectingSink) NextBatch(_ context.Context) (*UT.Batch, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.idx >= len(c.batches) {
		return nil, nil
	}
	b := c.batches[c.idx]
	c.batches[c.idx] = nil // release reference
	c.idx++
	return b, nil
}

// Close drops any unbuffered batches and marks the sink closed.
// Idempotent.
func (c *CollectingSink) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	for i := c.idx; i < len(c.batches); i++ {
		if c.batches[i] != nil {
			c.batches[i].Put()
			c.batches[i] = nil
		}
	}
	c.batches = nil
	return nil
}

// PushPipeline wires a source, a chain of PushOperators, and a
// CollectingSink. Run pulls batches from the source through op[0]
// (which pushes to op[1], ..., to the sink). REQ002002.
//
// Construction order: ops[0] is the FIRST operator after the source
// (typically PushFilter), ops[len-1] is the LAST (typically PushLimit).
// The sink is set on the last op; each op[i]'s sink is op[i+1].
type PushPipeline struct {
	source UT.BatchProducer
	ops    []PushOperator
	sink   *CollectingSink
}

// NewPushPipeline wires the source, operator chain, and a fresh
// CollectingSink. Returns a pipeline ready to Run.
//   - ops[len-1].SetSink(sink)
//   - ops[i].SetSink(ops[i+1]) for i < len-1
//   - ops[0].SetSource(source)
//
// If ops is empty, the source's batches flow directly into the sink
// via a synthetic passthrough.
func NewPushPipeline(source UT.BatchProducer, ops ...PushOperator) *PushPipeline {
	sink := NewCollectingSink()
	if len(ops) > 0 {
		ops[len(ops)-1].SetSink(sink)
		for i := len(ops) - 2; i >= 0; i-- {
			ops[i].SetSink(ops[i+1])
		}
		ops[0].SetSource(source)
	}
	return &PushPipeline{source: source, ops: ops, sink: sink}
}

// Sink returns the pipeline's collecting sink (for draining).
func (p *PushPipeline) Sink() *CollectingSink { return p.sink }

// Run executes the pipeline: pulls all batches from the source
// through the operator chain into the sink. Returns the first error
// encountered; ErrPushLimitReached is treated as a normal stop.
func (p *PushPipeline) Run(ctx context.Context) error {
	if len(p.ops) == 0 {
		// No operators — push source batches directly to the sink.
		return drainSourceToSink(ctx, p.source, p.sink)
	}
	return p.ops[0].Run(ctx)
}

// drainSourceToSink is the degenerate pipeline: no PushOperators,
// just source → sink. Used when ops is empty (rare; mostly tests).
func drainSourceToSink(ctx context.Context, source UT.BatchProducer, sink PushSink) error {
	if source == nil {
		return nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		batch, err := source.NextBatch(ctx)
		if err != nil {
			return err
		}
		if batch == nil {
			return nil
		}
		if err := sink.PushBatch(ctx, batch); err != nil {
			batch.Put()
			return err
		}
	}
}

// Close closes the sink and (transitively) the source via the first
// operator. Idempotent. Returns the first error encountered.
func (p *PushPipeline) Close() error {
	var firstErr error
	capture := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if len(p.ops) > 0 {
		capture(p.ops[0].Close())
	} else if p.source != nil {
		capture(p.source.Close())
	}
	if p.sink != nil {
		capture(p.sink.Close())
	}
	return firstErr
}
