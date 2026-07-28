package PX

import (
	"context"
	"errors"
	"sync"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	PL "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

// Pipeline is a live, executable DAG of Stages instantiated from a
// PipelineSpec. It holds runtime state and is NOT safe for concurrent
// use. Create via PipelineSpec.NewRuntime().
type Pipeline struct {
	spec   *PipelineSpec
	stages []Stage
	root   Stage
	closed bool
}

// Execute drains the root stage and collects all result rows.
// The pipeline must not be closed. After Execute, call Reset to
// re-execute or Close to release resources.
func (p *Pipeline) Execute(ctx context.Context) ([]DT.Row, error) {
	if p.closed {
		return nil, errors.New("px: execute on closed pipeline")
	}

	var rows []DT.Row
	bufPtr := rowBufPool.Get().(*[]PL.Value)
	buf := *bufPtr
	defer func() {
		*bufPtr = buf[:0]
		if cap(buf) > 1024*64 {
			buf = make([]PL.Value, 0, 1024*16)
		}
		*bufPtr = buf
		rowBufPool.Put(bufPtr)
	}()

	for {
		batch, err := p.root.NextBatch(ctx)
		if err != nil {
			return rows, err
		}
		if batch == nil {
			break
		}
		batchRows, newBuf := batch.ToRowsShared(buf)
		buf = newBuf
		for i := range batchRows {
			row := batchRows[i]
			// Deep-copy Data since the shared buffer is reused across batches.
			copied := make([]PL.Value, len(row.Data))
			copy(copied, row.Data)
			row.Data = copied
			rows = append(rows, row)
		}
		if batch.Pooled {
			batch.Put()
		}
	}
	return rows, nil
}

// Reset returns all stages to pre-execution state, preserving
// allocated buffers for plan cache reuse. If a stage returns
// ErrResetNotSupported, it is recreated from its StageSpec and
// the pipeline is re-wired.
func (p *Pipeline) Reset(ctx context.Context) error {
	if p.closed {
		return errors.New("px: reset on closed pipeline")
	}

	needsRewire := false
	for i, s := range p.stages {
		if err := s.Reset(ctx); err != nil {
			if errors.Is(err, ErrResetNotSupported) {
				// Recreate this stage from spec
				newStage := p.spec.Stages[i].NewRuntime()
				_ = s.Close()
				p.stages[i] = newStage
				if i == p.spec.RootIdx {
					p.root = newStage
				}
				needsRewire = true
				continue
			}
			return err
		}
	}

	if needsRewire {
		p.wireChildren()
	}
	return nil
}

// Close releases all resources held by the pipeline.
// Idempotent — safe to call multiple times.
func (p *Pipeline) Close() error {
	if p.closed {
		return nil
	}
	p.closed = true
	var firstErr error
	for _, s := range p.stages {
		if err := s.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// wireChildren re-wires child stages after some stages were
// recreated from their specs during Reset.
func (p *Pipeline) wireChildren() {
	for _, edge := range p.spec.Edges {
		parent, ok := p.stages[edge.From].(ChildSetter)
		if !ok {
			continue
		}
		parent.SetChild(edge.Side, p.stages[edge.To])
	}
}

// PropagateParams injects parameter values into all stages that
// implement ParamPropagator. This is called after NewRuntime() to
// set per-execution ? placeholder values without modifying the
// cached PipelineSpec.
func (p *Pipeline) PropagateParams(args []any, buf *[]any) {
	for _, s := range p.stages {
		if pp, ok := s.(ParamPropagator); ok {
			pp.PropagateParams(args, buf)
		}
	}
}

// PropagateExecContext injects per-execution context into all stages
// that implement ExecContextPropagator. REQ002136.
func (p *Pipeline) PropagateExecContext(ec *DT.ExecContext) {
	for _, s := range p.stages {
		if ep, ok := s.(ExecContextPropagator); ok {
			ep.PropagateExecContext(ec)
		}
	}
}

// PropagatePlanner injects the query planner into all stages that
// implement PlannerPropagator. REQ002136.
func (p *Pipeline) PropagatePlanner(pl PL.QueryPlanner) {
	for _, s := range p.stages {
		if pp, ok := s.(PlannerPropagator); ok {
			pp.PropagatePlanner(pl)
		}
	}
}

// rowBufPool pools the shared Value buffer for batch→row conversion.
// Matches the pattern in EX/drain_batch.go.
var rowBufPool = sync.Pool{
	New: func() any {
		buf := make([]PL.Value, 0, 1024*16) // 1024 rows × 16 cols
		return &buf
	},
}
