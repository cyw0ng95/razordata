package UT

import (
	"context"
	"fmt"
	"sync"
)

// PipelineOperator is the interface for operators in a pipeline.
type PipelineOperator interface {
	Process(ctx context.Context, batch *Batch) (*Batch, error)
	Close() error
}

// Pipeline chains multiple PipelineOperators with bounded
// channels between stages.
type Pipeline struct {
	stages    []PipelineOperator
	bufs      []chan *Batch
	done      chan struct{}
	err       chan error
	closeOnce sync.Once
}

// NewPipeline creates a pipeline with the given operators.
func NewPipeline(stages []PipelineOperator, bufSize int) *Pipeline {
	p := &Pipeline{
		stages: stages,
		bufs:   make([]chan *Batch, len(stages)+1),
		done:   make(chan struct{}),
		err:    make(chan error, 1),
	}
	for i := range p.bufs {
		p.bufs[i] = make(chan *Batch, bufSize)
	}
	return p
}

// Run starts the pipeline. Returns the output channel and a cancel func.
func (p *Pipeline) Run(ctx context.Context) (<-chan *Batch, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	for i, stage := range p.stages {
		go p.runStage(ctx, i, stage, p.bufs[i], p.bufs[i+1])
	}
	go func() {
		<-ctx.Done()
		close(p.done)
	}()
	return p.bufs[len(p.stages)], cancel
}

func (p *Pipeline) runStage(ctx context.Context, idx int, stage PipelineOperator, inCh, outCh chan *Batch) {
	defer func() {
		close(outCh)
		if r := recover(); r != nil {
			select {
			case p.err <- fmt.Errorf("pipeline stage %d panic: %v", idx, r):
			default:
			}
		}
	}()
	for batch := range inCh {
		result, err := stage.Process(ctx, batch)
		if err != nil {
			select {
			case p.err <- err:
			default:
			}
			return
		}
		if result != nil {
			select {
			case outCh <- result:
			case <-ctx.Done():
				return
			}
		}
		batch.Put()
	}
}

// Input returns the input channel for the first stage.
func (p *Pipeline) Input() chan<- *Batch { return p.bufs[0] }

// Err returns a channel that receives the first error from any stage.
func (p *Pipeline) Err() <-chan error { return p.err }

// Close stops all stages and waits for completion.
func (p *Pipeline) Close() error {
	p.closeOnce.Do(func() {
		close(p.bufs[0])
	})
	for range p.bufs[len(p.stages)] {
	}
	for _, stage := range p.stages {
		_ = stage.Close()
	}
	return nil
}

// SyncPipeline runs each stage sequentially in the same goroutine.
type SyncPipeline struct {
	stages []PipelineOperator
}

func NewSyncPipeline(stages []PipelineOperator) *SyncPipeline {
	return &SyncPipeline{stages: stages}
}

func (sp *SyncPipeline) Run(ctx context.Context, input <-chan *Batch) <-chan *Batch {
	out := make(chan *Batch, 4)
	go func() {
		defer close(out)
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

type pipelineBuilder struct {
	stages []PipelineOperator
}

func NewPipelineBuilder() *pipelineBuilder {
	return &pipelineBuilder{}
}

func (pb *pipelineBuilder) Add(op PipelineOperator) *pipelineBuilder {
	pb.stages = append(pb.stages, op)
	return pb
}

func (pb *pipelineBuilder) Build() *Pipeline {
	return NewPipeline(pb.stages, 4)
}

// WaitPipeline waits for all stages to finish.
func WaitPipeline(p *Pipeline) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		wg.Done()
		for range p.bufs[len(p.stages)] {
		}
	}()
	wg.Wait()
}
