package EX

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
)

// countOp is a counting operator that returns its batch unchanged.
type countOp struct {
	count *int32
}

func (c *countOp) Process(ctx context.Context, batch *Batch) (*Batch, error) {
	atomic.AddInt32(c.count, 1)
	return batch, nil
}

func (c *countOp) Close() error { return nil }

// newCounter allocates a new counter for use with countOp.
func newCounter() *int32 {
	var c int32
	return &c
}

// TestPipeline_Basic verifies single-stage pipeline.
func TestPipeline_Basic(t *testing.T) {
	counter := newCounter()
	stage := &countOp{count: counter}
	p := NewPipeline([]PipelineOperator{stage}, 2)
	defer p.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out, _ := p.Run(ctx)

	go func() {
		in := p.Input()
		for i := 0; i < 5; i++ {
			batch := GetBatch(1)
			batch.AppendRow(0, 0, int64(i), false)
			batch.AdvanceSize()
			in <- batch
		}
		close(in)
	}()

	received := 0
	for batch := range out {
		received++
		batch.Put()
	}
	if received != 5 {
		t.Errorf("received %d, want 5", received)
	}
	if atomic.LoadInt32(counter) != 5 {
		t.Errorf("counter = %d, want 5", counter)
	}
}

// TestPipeline_MultiStage verifies 3-stage chain.
func TestPipeline_MultiStage(t *testing.T) {
	stage1Count, stage2Count, stage3Count := newCounter(), newCounter(), newCounter()
	stage1 := &countOp{count: stage1Count}
	stage2 := &countOp{count: stage2Count}
	stage3 := &countOp{count: stage3Count}

	p := NewPipeline([]PipelineOperator{stage1, stage2, stage3}, 2)
	defer p.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out, _ := p.Run(ctx)

	go func() {
		in := p.Input()
		for i := 0; i < 10; i++ {
			batch := GetBatch(1)
			batch.AppendRow(0, 0, int64(i), false)
			batch.AdvanceSize()
			in <- batch
		}
		close(in)
	}()

	received := 0
	for batch := range out {
		received++
		batch.Put()
	}
	if received != 10 {
		t.Errorf("received %d, want 10", received)
	}
	if atomic.LoadInt32(stage1Count) != 10 {
		t.Errorf("stage1 = %d, want 10", stage1Count)
	}
	if atomic.LoadInt32(stage2Count) != 10 {
		t.Errorf("stage2 = %d, want 10", stage2Count)
	}
	if atomic.LoadInt32(stage3Count) != 10 {
		t.Errorf("stage3 = %d, want 10", stage3Count)
	}
}

// TestPipeline_Builder verifies pipeline builder pattern.
func TestPipeline_Builder(t *testing.T) {
	stage1 := &countOp{count: newCounter()}
	stage2 := &countOp{count: newCounter()}

	p := NewPipelineBuilder().
		Add(stage1).
		Add(stage2).
		Build()
	defer p.Close()

	if len(p.stages) != 2 {
		t.Errorf("expected 2 stages, got %d", len(p.stages))
	}
}

// TestSyncPipeline verifies the synchronous pipeline.
func TestSyncPipeline(t *testing.T) {
	stage1Count, stage2Count := newCounter(), newCounter()
	stage1 := &countOp{count: stage1Count}
	stage2 := &countOp{count: stage2Count}

	sp := NewSyncPipeline([]PipelineOperator{stage1, stage2})

	ctx := context.Background()
	in := make(chan *Batch, 4)
	out := sp.Run(ctx, in)

	for i := 0; i < 3; i++ {
		batch := GetBatch(1)
		batch.AppendRow(0, 0, int64(i), false)
		batch.AdvanceSize()
		in <- batch
	}
	close(in)

	received := 0
	for batch := range out {
		received++
		batch.Put()
	}
	if received != 3 {
		t.Errorf("received %d, want 3", received)
	}
}

// TestPipeline_Close verifies Close releases resources.
func TestPipeline_Close(t *testing.T) {
	stage := &countOp{count: newCounter()}
	p := NewPipeline([]PipelineOperator{stage}, 2)
	if err := p.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	// Idempotent close
	if err := p.Close(); err != nil {
		t.Errorf("Close (second): %v", err)
	}
}

// TestPipeline_ConcurrentInput verifies pool safety.
func TestPipeline_ConcurrentInput(t *testing.T) {
	stage := &countOp{count: newCounter()}
	p := NewPipeline([]PipelineOperator{stage}, 8)
	defer p.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out, _ := p.Run(ctx)

	var wg sync.WaitGroup
	const senders = 4
	const perSender = 25
	for s := 0; s < senders; s++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			in := p.Input()
			for i := 0; i < perSender; i++ {
				batch := GetBatch(1)
				batch.AppendRow(0, 0, int64(i), false)
				batch.AdvanceSize()
				in <- batch
			}
		}()
	}
	go func() {
		wg.Wait()
		close(p.Input())
	}()

	received := 0
	for batch := range out {
		received++
		batch.Put()
	}
	if received != senders*perSender {
		t.Errorf("received %d, want %d", received, senders*perSender)
	}
}

// BenchmarkPipeline measures multi-stage pipeline throughput.
func BenchmarkPipeline(b *testing.B) {
	stage1 := &countOp{count: newCounter()}
	stage2 := &countOp{count: newCounter()}
	stage3 := &countOp{count: newCounter()}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p := NewPipeline([]PipelineOperator{stage1, stage2, stage3}, 4)
		ctx, cancel := context.WithCancel(context.Background())
		out, _ := p.Run(ctx)

		in := p.Input()
		go func() {
			for j := 0; j < 100; j++ {
				batch := GetBatch(1)
				batch.AppendRow(0, 0, int64(j), false)
				batch.AdvanceSize()
				in <- batch
			}
			close(in)
		}()

		for batch := range out {
			batch.Put()
		}
		cancel()
		p.Close()
	}
}
