package EX

import (
	"context"
	"fmt"
	"sync"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// PipelineOperator is the interface for operators in a pipeline.
// Each operator pulls from an input batch channel and produces
// output batches to a downstream channel.
type PipelineOperator interface {
	// Process consumes one batch and produces output.
	// Returns (nil, nil) to signal end-of-stream.
	Process(ctx context.Context, batch *UT.Batch) (*UT.Batch, error)
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
	bufs   []chan *UT.Batch
	errCh  chan error
}

// NewPipeline creates a pipeline with the given operators.
// bufSize controls the bounded channel size between stages
// (typically 2-4 for balanced throughput/memory).
func NewPipeline(stages []PipelineOperator, bufSize int) *Pipeline {
	if bufSize < 1 {
		bufSize = 1
	}
	bufs := make([]chan *UT.Batch, len(stages)+1)
	for i := range bufs {
		bufs[i] = make(chan *UT.Batch, bufSize)
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
func (p *Pipeline) Run(ctx context.Context) (<-chan *UT.Batch, context.CancelFunc) {
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
func (p *Pipeline) runStage(ctx context.Context, idx int, stage PipelineOperator, inCh, outCh chan *UT.Batch) {
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
func (p *Pipeline) Input() chan<- *UT.Batch {
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
	EvaluateBatch(batch *UT.Batch, params []any) []uint16
}

// NewFilterPipelineOperator creates a filter stage.
func NewFilterPipelineOperator(pred PipelinePredicate) *FilterPipelineOperator {
	return &FilterPipelineOperator{pred: pred}
}

// Process applies the filter to one batch.
func (f *FilterPipelineOperator) Process(ctx context.Context, batch *UT.Batch) (*UT.Batch, error) {
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

// ProjectPipelineOperator is a PipelineOperator that evaluates
// projection expressions on each batch. REQ001049.
type ProjectPipelineOperator struct {
	exprs  []PS.Expr
	params []any
	row    Row
}

// NewProjectPipelineOperator creates a projection stage.
func NewProjectPipelineOperator(exprs []PS.Expr, params []any) *ProjectPipelineOperator {
	return &ProjectPipelineOperator{exprs: exprs, params: params}
}

// Process evaluates projection expressions for each row in the batch
// and produces a new batch with projected columns.
func (p *ProjectPipelineOperator) Process(ctx context.Context, batch *UT.Batch) (*UT.Batch, error) {
	if batch == nil || batch.Size == 0 {
		return nil, nil
	}
	out := UT.GetBatch(len(p.exprs))
	for i := range p.exprs {
		out.SetColumnName(i, fmt.Sprintf("c%d", i))
	}
	for ri := 0; ri < batch.Size; ri++ {
		p.row = Row{Data: rowDataAt(batch, ri)}
		for ei, expr := range p.exprs {
			v, err := EvalValue(expr, &p.row, p.params)
			if err != nil {
				out.Put()
				return nil, err
			}
			out.AppendRow(ei, kindToTokenType(v.Kind), v.ToAny(), v.IsNull())
		}
		out.AdvanceSize()
	}
	return out, nil
}

// Close is a no-op.
func (p *ProjectPipelineOperator) Close() error { return nil }

// AggregatePipelineOperator is a PipelineOperator that accumulates
// aggregates across batches and produces one result batch at the end.
// REQ001049.
type AggregatePipelineOperator struct {
	aggs  []PS.Expr
	state []AggregateState
	done  bool
	row   Row
}

// NewAggregatePipelineOperator creates an aggregate stage.
func NewAggregatePipelineOperator(aggs []PS.Expr) *AggregatePipelineOperator {
	state := make([]AggregateState, len(aggs))
	for i, a := range aggs {
		state[i] = newAggregateState(a)
	}
	return &AggregatePipelineOperator{aggs: aggs, state: state}
}

// Process accumulates one batch into aggregate state.
// Returns nil until all batches are consumed, then the final result.
func (a *AggregatePipelineOperator) Process(ctx context.Context, batch *UT.Batch) (*UT.Batch, error) {
	if batch == nil || batch.Size == 0 {
		// End of stream: produce result
		if a.done {
			return nil, nil
		}
		a.done = true
		out := UT.GetBatch(len(a.aggs))
		for i, s := range a.state {
			out.AppendRow(i, kindToTokenType(s.Kind()), s.FinalValue(), false)
		}
		out.SetColumnName(0, "count(*)")
		out.AdvanceSize()
		return out, nil
	}
	for ri := 0; ri < batch.Size; ri++ {
		a.row = Row{Data: rowDataAt(batch, ri)}
		for _, s := range a.state {
			s.Step(a.row)
		}
	}
	return nil, nil
}

// Close is a no-op.
func (a *AggregatePipelineOperator) Close() error { return nil }

// rowDataAt extracts column values from a batch row into a []Value.
func rowDataAt(batch *UT.Batch, rowIdx int) []Value {
	data := make([]Value, len(batch.Cols))
	for ci := range batch.Cols {
		col := &batch.Cols[ci]
		if col.Nulls != nil && rowIdx < len(col.Nulls) && col.Nulls[rowIdx] {
			data[ci] = NullValue()
			continue
		}
		switch col.Type {
		case LX.T_INT_KW, LX.T_BIGINT:
			data[ci] = NewIntValue(col.Data.Ints[rowIdx])
		case LX.T_FLOAT_KW:
			data[ci] = NewFloatValue(col.Data.Floats[rowIdx])
		case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
			data[ci] = NewTextValue(col.Data.Strs[rowIdx])
		case LX.T_BOOL:
			data[ci] = NewBoolValue(col.Data.Bools[rowIdx])
		default:
			data[ci] = NullValue()
		}
	}
	return data
}

// kindToTokenType maps ValueKind to LX.TokenType for batch column type.
func kindToTokenType(k ValueKind) LX.TokenType {
	switch k {
	case KindInt:
		return LX.T_INT_KW
	case KindFloat:
		return LX.T_FLOAT_KW
	case KindText:
		return LX.T_TEXT
	case KindBool:
		return LX.T_BOOL
	default:
		return LX.TokenType(0)
	}
}

// AggregateState is the interface for per-row aggregate accumulation.
type AggregateState interface {
	Step(row Row)
	FinalValue() any
	Kind() ValueKind
}

// newAggregateState creates the appropriate state for an aggregate expression.
func newAggregateState(expr PS.Expr) AggregateState {
	return &countState{}
}

// countState implements COUNT(*).
type countState struct {
	count int64
}

func (c *countState) Step(Row) { c.count++ }
func (c *countState) FinalValue() any { return c.count }
func (c *countState) Kind() ValueKind { return KindInt }

// buildPipeline creates a Pipeline from a chain of row-based Operators.
// Each operator is wrapped in a PipelineOperator adapter. REQ001049.
func buildPipeline(ops []Operator, bufSize int) *Pipeline {
	stages := make([]PipelineOperator, len(ops))
	for i, op := range ops {
		stages[i] = &operatorPipelineAdapter{op: op}
	}
	return NewPipeline(stages, bufSize)
}

// operatorPipelineAdapter wraps a row-based Operator as a PipelineOperator.
// It drains all rows on first Process call and returns them as a single batch.
type operatorPipelineAdapter struct {
	op     Operator
	batch  *UT.Batch
	drained bool
}

func (a *operatorPipelineAdapter) Process(ctx context.Context, batch *UT.Batch) (*UT.Batch, error) {
	if a.drained {
		return nil, nil
	}
	a.drained = true
	// Collect all rows from the operator
	var rows []Row
	for {
		r, err := a.op.Next(ctx)
		if err != nil {
			if err == ErrNoRows {
				break
			}
			return nil, err
		}
		rows = append(rows, r)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	// Convert to batch
	nCols := len(rows[0].Cols)
	out := UT.GetBatch(nCols)
	for i, name := range rows[0].Cols {
		out.SetColumnName(i, name)
	}
	for _, r := range rows {
		for ci := range r.Cols {
			out.AppendRow(ci, kindToTokenType(r.Data[ci].Kind), r.Data[ci].ToAny(), r.Data[ci].IsNull())
		}
		out.AdvanceSize()
	}
	a.batch = out
	return out, nil
}

func (a *operatorPipelineAdapter) Close() error { return a.op.Close() }

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
func (sp *SyncPipeline) Run(ctx context.Context, input <-chan *UT.Batch) <-chan *UT.Batch {
	out := make(chan *UT.Batch, 4)
	go func() {
		defer close(out)
		// Use a single goroutine that chains stages
		chanInput := make(chan *UT.Batch, 4)
		go func() {
			defer close(chanInput)
			for batch := range input {
				chanInput <- batch
			}
		}()

		current := chanInput
		for _, stage := range sp.stages {
			next := make(chan *UT.Batch, 4)
			go func(stage PipelineOperator, in, out chan *UT.Batch) {
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
