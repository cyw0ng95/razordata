package OP

import (
	"context"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// PushToPullAdapter wraps a PushPipeline and implements the
// BatchProducer interface so it can be substituted anywhere a
// pull-based operator is expected (notably BatchToRowAdapter in
// the vec_transform path). REQ002002.
//
// On the first NextBatch call, the pipeline runs to completion (or
// until PushLimit halts it) and buffers batches in the CollectingSink.
// Subsequent calls drain the sink. This is a synchronous adapter —
// no background goroutine — which keeps OLTP-sized queries cheap.
type PushToPullAdapter struct {
	pipeline *PushPipeline
	started  bool
}

// NewPushToPullAdapter wraps the given pipeline.
func NewPushToPullAdapter(pipeline *PushPipeline) *PushToPullAdapter {
	return &PushToPullAdapter{pipeline: pipeline}
}

// NextBatch runs the pipeline on the first call (lazily), then
// drains one batch per call from the sink. Returns (nil, nil) at EOF.
func (a *PushToPullAdapter) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if !a.started {
		a.started = true
		if err := a.pipeline.Run(ctx); err != nil {
			return nil, err
		}
	}
	return a.pipeline.Sink().NextBatch(ctx)
}

// Close closes the pipeline (sink + source via first op).
func (a *PushToPullAdapter) Close() error {
	if a.pipeline != nil {
		return a.pipeline.Close()
	}
	return nil
}

// Compile-time interface check.
var _ UT.BatchProducer = (*PushToPullAdapter)(nil)
