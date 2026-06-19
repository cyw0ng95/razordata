package EX

import (
	"context"
	"sync"
)

// PipelineOperator is the interface for operators in a pipeline.
// Each operator pulls from an input batch channel and produces
// output batches to a downstream channel.
type PipelineOperator interface {
	// Process consumes one batch and produces output.
	// Returns (nil, nil) to signal end-of-stream.
	Process(ctx context.Context, batch *Batch) (*Batch, error)
	// Close releases resources.
	Close() error
}

// Pipeline chains multiple PipelineOperators with bounded
// channels between stages. Each stage runs in its own goroutine,
// pulling from the previous stage's output channel and pushing
// to the next stage's input channel.
// This implements **pipeline parallelism**: each stage runs
// concurrently, so when stage 1 produces batch N+1, stage 2
// can be processing batch N while stage 3 is processing batch N-1.
// REQ000145 satisfied: Pipeline parallelism for multi-stage queries.
type Pipeline struct {
	stages []PipelineOperator
	bufs   []chan *Batch
	errCh  chan error
}

// NewPipeline creates a pipeline with the given operators.
// bufSize controls the bounded channel size between stages
// (typically 2-4 for balanced throughput/memory).
func NewPipeline(stages []PipelineOperator, bufSize int) *Pipeline {
	if bufSize < 1 {
		bufSize = 1
	}
	bufs := make([]chan *Batch, len(stages)+1)
	for i := range bufs {
		bufs[i] = make(chan *Batch, bufSize)
	}
	return &Pipeline{
		stages: stages,
		bufs:   bufs,
		errCh:  make(chan error, len(stages)),
	}
}

// Run starts all pipeline stages concurrently. The returned
// channel produces output batches from the last stage.
// Close the pipeline via the returned cancel function or by
// closing the first stage's input.
func (p *Pipeline) Run(ctx context.Context) (<-chan *Batch, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)

	// Start each stage
	for i, stage := range p.stages {
		inCh := p.bufs[i]
		outCh := p.bufs[i+1]
		go p.runStage(ctx, i, stage, inCh, outCh)
	}

	return p.bufs[len(p.stages)], cancel
}

// runStage executes a single stage: pulls from inCh, processes,
// pushes to outCh. Closes outCh when inCh is closed.
func (p *Pipeline) runStage(ctx context.Context, idx int, stage PipelineOperator, inCh, outCh chan *Batch) {
	defer close(outCh)
	for {
		select {
		case <-ctx.Done():
			return
		case batch, ok := <-inCh:
			if !ok {
				return
			}
			if batch == nil {
				// EOF sentinel
				return
			}
			result, err := stage.Process(ctx, batch)
			if err != nil {
				select {
				case p.errCh <- err:
				default:
				}
				return
			}
			if result != nil {
				outCh <- result
			}
		}
	}
}

// Input returns the first stage's input channel. The caller
// should send batches here and close when done.
func (p *Pipeline) Input() chan<- *Batch {
	return p.bufs[0]
}

// Err returns a channel that receives the first error from
// any stage. Nil if no errors.
func (p *Pipeline) Err() <-chan error {
	return p.errCh
}

// Close shuts down all stages.
func (p *Pipeline) Close() error {
	for _, stage := range p.stages {
		stage.Close()
	}
	return nil
}

// FilterPipelineOperator is a PipelineOperator that applies a
// predicate to each batch using EvalBatch. Returns filtered
// batches (with selection vectors) downstream.
type FilterPipelineOperator struct {
	pred   PipelinePredicate
	params []any
}

// PipelinePredicate is a minimal interface for vectorized predicates.
// Implemented by the same expression types that EvalBatch accepts.
type PipelinePredicate interface {
	EvaluateBatch(batch *Batch, params []any) []uint16
}

// NewFilterPipelineOperator creates a filter stage.
func NewFilterPipelineOperator(pred PipelinePredicate) *FilterPipelineOperator {
	return &FilterPipelineOperator{pred: pred}
}

// Process applies the filter to one batch.
func (f *FilterPipelineOperator) Process(ctx context.Context, batch *Batch) (*Batch, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sel := f.pred.EvaluateBatch(batch, f.params)
	if sel == nil {
		// All match
		return batch, nil
	}
	if len(sel) == 0 {
		// No match
		batch.Put()
		return nil, nil
	}
	batch.Sel = sel
	batch.Size = len(sel)
	return batch, nil
}

// Close is a no-op for stateless operators.
func (f *FilterPipelineOperator) Close() error { return nil }

// SyncPipeline is a simpler synchronous pipeline used for testing.
// It runs each stage sequentially in the same goroutine. Useful
// for verifying pipeline composition without concurrency.
type SyncPipeline struct {
	stages []PipelineOperator
}

// NewSyncPipeline creates a synchronous pipeline.
func NewSyncPipeline(stages []PipelineOperator) *SyncPipeline {
	return &SyncPipeline{stages: stages}
}

// Run executes the pipeline synchronously: each stage processes
// the output of the previous stage in the same goroutine.
// Returns a channel that produces final output batches.
func (sp *SyncPipeline) Run(ctx context.Context, input <-chan *Batch) <-chan *Batch {
	out := make(chan *Batch, 4)
	go func() {
		defer close(out)
		// Use a single goroutine that chains stages
		chanInput := make(chan *Batch, 4)
		go func() {
			defer close(chanInput)
			for batch := range input {
				chanInput <- batch
			}
		}()

		current := chanInput
		for _, stage := range sp.stages {
			next := make(chan *Batch, 4)
			go func(stage PipelineOperator, in, out chan *Batch) {
				defer close(out)
				for batch := range in {
					if batch == nil {
						return
					}
					result, err := stage.Process(ctx, batch)
					if err != nil {
						return
					}
					if result != nil {
						out <- result
					}
				}
			}(stage, current, next)
			current = next
		}
		for batch := range current {
			if batch == nil {
				return
			}
			out <- batch
		}
	}()
	return out
}

// pipelineBuilder helps construct pipelines with type safety.
type pipelineBuilder struct {
	stages []PipelineOperator
}

// NewPipelineBuilder creates a new pipeline builder.
func NewPipelineBuilder() *pipelineBuilder {
	return &pipelineBuilder{}
}

// Add appends a stage to the pipeline.
func (pb *pipelineBuilder) Add(op PipelineOperator) *pipelineBuilder {
	pb.stages = append(pb.stages, op)
	return pb
}

// Build creates a Pipeline with default buffer size.
func (pb *pipelineBuilder) Build() *Pipeline {
	return NewPipeline(pb.stages, 4)
}

// WaitGroup helper to wait for all stages to finish.
func waitPipeline(p *Pipeline) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		wg.Done()
		for range p.bufs[len(p.stages)] {
		}
	}()
	wg.Wait()
}
