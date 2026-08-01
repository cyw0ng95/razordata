package PX

import (
	"context"
	"sync"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	PL "github.com/cyw0ng95/razordata/internal/SQF/PL"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// PipelinedExecutor runs a PipelineSpec in a concurrent, push-based
// fashion: the stage chain is built exactly like PipelineExecutor (so
// stage wiring, param/planner/execCtx propagation are identical), but
// batch production runs in its own goroutine and feeds the consumer via a
// bounded channel. This overlaps batch production with consumption by the
// caller, lowering per-Next blocking and peak memory pressure for wide
// intermediate results compared to the synchronous materializing
// Pipeline. Works for every stage category (Source, Transform, MapReduce,
// Join) because the existing root stage chain is reused unchanged.
//
// REQ002282: add PipelinedExecutor that streams the materializing
// Pipeline between stages via a bounded channel instead of blocking the
// consumer on each NextBatch call.
type PipelinedExecutor struct {
	spec *PipelineSpec
	mu   sync.Mutex

	// Per-execution state injected before first Execute/ExecuteStream.
	params  []any
	execCtx *DT.ExecContext
	planner PL.QueryPlanner
}

// NewPipelinedExecutor creates a pipelined executor from a spec.
func NewPipelinedExecutor(spec *PipelineSpec) *PipelinedExecutor {
	return &PipelinedExecutor{spec: spec}
}

// SetParams stores parameter values for propagation into stages.
func (e *PipelinedExecutor) SetParams(args []any) { e.params = args }

// SetExecContext stores the execution context for propagation into stages.
func (e *PipelinedExecutor) SetExecContext(ec *DT.ExecContext) { e.execCtx = ec }

// SetPlanner stores the query planner for propagation into stages.
func (e *PipelinedExecutor) SetPlanner(p PL.QueryPlanner) { e.planner = p }

// Execute drains the pipeline and returns all rows. Production runs
// concurrently in a goroutine feeding a bounded channel; batches are
// converted to rows with ToRowsShared (mirroring Pipeline.Execute), so the
// result is identical to the materializing executor, including correct
// handling of zero-size batches.
func (e *PipelinedExecutor) Execute(ctx context.Context) ([]DT.Row, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	pipe, err := e.ensurePipe(ctx)
	if err != nil {
		return nil, err
	}
	pCtx, cancel := context.WithCancel(ctx)
	prodCh := make(chan batchOrErr, pipelineChannelDepth)
	ps := &pipelinedStream{cancel: cancel, prodCh: prodCh, pipe: pipe}
	ps.wg.Add(1)
	go ps.produce(pCtx, prodCh, pipe)
	defer ps.stop()

	var rows []DT.Row
	for item := range prodCh {
		if item.err != nil {
			return nil, item.err
		}
		if item.batch == nil {
			continue // EOF terminator
		}
		batchRows, _ := item.batch.ToRowsShared(nil)
		for i := range batchRows {
			cp := make([]DT.Value, len(batchRows[i].Data))
			copy(cp, batchRows[i].Data)
			batchRows[i].Data = cp
			rows = append(rows, batchRows[i])
		}
		if item.batch.Pooled {
			item.batch.Put()
		}
	}
	return rows, nil
}

// ExecuteStream returns a streaming iterator that reads rows from the
// pipeline via the concurrent pipelined channel. The caller must call
// Close() on the returned iterator.
func (e *PipelinedExecutor) ExecuteStream(ctx context.Context) (*PipelineStream, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.startStream(ctx)
}

// BatchProducer is unsupported for the concurrent pipelined path (it
// assumes direct ownership of the root stage), so it delegates to a
// materializing Pipeline which exposes the root BatchProducer.
func (e *PipelinedExecutor) BatchProducer(ctx context.Context) (UT.BatchProducer, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	pipe, err := e.ensurePipe(ctx)
	if err != nil {
		return nil, err
	}
	return pipe.root, nil
}

// ensurePipe builds a fresh materializing Pipeline and applies the same
// per-execution propagation as PipelineExecutor.ensurePipeline.
func (e *PipelinedExecutor) ensurePipe(ctx context.Context) (*Pipeline, error) {
	pipe, err := e.spec.NewRuntime()
	if err != nil {
		return nil, err
	}
	if len(e.params) > 0 {
		pipe.PropagateParams(e.params, nil)
	}
	if e.planner != nil {
		pipe.PropagatePlanner(e.planner)
	}
	if e.execCtx != nil {
		pipe.PropagateExecContext(e.execCtx)
	}
	return pipe, nil
}

// pipelineChannelDepth bounds buffered in-flight batches between producer
// and consumer. A depth of 2 lets production of batch N+1 overlap with
// consumption of batch N without unbounded memory growth.
const pipelineChannelDepth = 2

// startStream builds the stage chain, then runs batch production in a
// goroutine feeding a bounded channel. The channel is wrapped by a
// channelSourceStage so the existing PipelineStream (returned to EX
// unchanged) pulls batches from it via NextBatch. A pipelinedStream is
// embedded for Close() to stop the producer and reclaim stage resources.
func (e *PipelinedExecutor) startStream(ctx context.Context) (*PipelineStream, error) {
	pipe, err := e.ensurePipe(ctx)
	if err != nil {
		return nil, err
	}

	pCtx, cancel := context.WithCancel(ctx)
	prodCh := make(chan batchOrErr, pipelineChannelDepth)
	src := &channelSourceStage{ch: prodCh, execCtx: e.execCtx}

	ps := &pipelinedStream{
		PipelineStream: &PipelineStream{pipe: &Pipeline{root: src}, ctx: pCtx, execCtx: e.execCtx},
		cancel:         cancel,
		prodCh:         prodCh,
		pipe:           pipe,
	}
	// Route Close() through the producer-stop logic so both the embedded
	// PipelineStream.Close and the producer goroutine are released.
	ps.PipelineStream.onClose = ps.stop
	ps.wg.Add(1)
	go ps.produce(pCtx, prodCh, pipe)
	return ps.PipelineStream, nil
}

// batchOrErr is the unit transferred over the producer channel.
type batchOrErr struct {
	batch *UT.Batch
	err   error
}

// produce pulls batches from the pipeline root and sends them on prodCh
// until EOF or context cancellation. A nil batch is the EOF terminator.
func (ps *pipelinedStream) produce(ctx context.Context, prodCh chan batchOrErr, pipe *Pipeline) {
	defer ps.wg.Done()
	defer close(prodCh)
	for {
		if err := ctx.Err(); err != nil {
			prodCh <- batchOrErr{err: err}
			return
		}
		batch, err := pipe.root.NextBatch(ctx)
		if err != nil {
			prodCh <- batchOrErr{err: err}
			return
		}
		if batch == nil {
			prodCh <- batchOrErr{}
			return
		}
		select {
		case prodCh <- batchOrErr{batch: batch}:
		case <-ctx.Done():
			prodCh <- batchOrErr{err: ctx.Err()}
			return
		}
	}
}

// pipelinedStream owns the producer goroutine and its lifecycle. It is
// wired into PipelineStream.onClose so a single Close() on the returned
// stream stops production, drains the channel, and releases the stage
// pipeline.
type pipelinedStream struct {
	*PipelineStream
	cancel context.CancelFunc
	prodCh chan batchOrErr
	pipe   *Pipeline // retained for Close() to release stage resources
	wg     sync.WaitGroup
}

// stop cancels production, drains the channel (so the producer goroutine
// can exit and return batches to the pool), and releases the stage
// pipeline. Invoked via PipelineStream.onClose; safe to call multiple
// times because PipelineStream.Close guards reentrancy.
func (ps *pipelinedStream) stop() error {
	if ps.cancel != nil {
		ps.cancel()
	}
	// Drain the channel so the producer goroutine can exit and release its
	// batches back to the pool.
	for item := range ps.prodCh {
		if item.batch != nil && item.batch.Pooled {
			item.batch.Put()
		}
	}
	ps.wg.Wait()
	if ps.pipe != nil {
		return ps.pipe.Close()
	}
	return nil
}

// channelSourceStage is a synthetic Source-stage that pulls batches from a
// bounded channel fed by the producer goroutine. It lets the existing
// PipelineStream.Next read pipeline output without modification.
type channelSourceStage struct {
	ch      chan batchOrErr
	execCtx *DT.ExecContext
}

// NextBatch returns the next batch from the channel. A nil batch signals
// EOF (returns nil, nil). A non-nil err terminates the stream.
func (s *channelSourceStage) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case item, ok := <-s.ch:
		if !ok {
			return nil, nil
		}
		if item.err != nil {
			return nil, item.err
		}
		if item.batch == nil {
			return nil, nil
		}
		if s.execCtx != nil {
			item.batch.ExecCtx = s.execCtx
		}
		return item.batch, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// PropagateExecContext is a no-op: channelSourceStage is a pass-through
// adapter; execCtx is applied directly in NextBatch.
func (s *channelSourceStage) PropagateExecContext(ec *DT.ExecContext) { s.execCtx = ec }

// Reset is unsupported for the channel adapter.
func (s *channelSourceStage) Reset(_ context.Context) error { return ErrResetNotSupported }

// Close is a no-op; production lifecycle is owned by pipelinedStream.
func (s *channelSourceStage) Close() error { return nil }

// Ensure the channelSourceStage satisfies the Stage interface.
var _ Stage = (*channelSourceStage)(nil)
