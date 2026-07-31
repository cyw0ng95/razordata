package PX

import (
	"context"
	"errors"
	"sync"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	OP "github.com/cyw0ng95/razordata/internal/SQB/OP"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	PL "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

// PipelineExecutor runs a PipelineSpec and delivers results.
// It replaces the current execute flow (drainBatch/drainRows)
// with a unified Pipeline execution path.
//
// Lifecycle:
//   - Execute: drain pipeline into []DT.Row (for small results)
//   - ExecuteStream: stream results via channel (for large results)
//   - Reset: return to pre-execution state (for plan cache reuse)
type PipelineExecutor struct {
	spec *PipelineSpec
	pipe *Pipeline
	mu   sync.Mutex
	// REQ002136: per-execution state injected before first Execute.
	params   []any
	execCtx  *DT.ExecContext
	planner  PL.QueryPlanner
	paramBuf []any
}

// NewPipelineExecutor creates an executor from a spec.
func NewPipelineExecutor(spec *PipelineSpec) *PipelineExecutor {
	return &PipelineExecutor{spec: spec}
}

// SetParams stores parameter values for propagation into stages
// on the first Execute/ExecuteStream call. REQ002136.
func (e *PipelineExecutor) SetParams(args []any) { e.params = args }

// SetExecContext stores the execution context for propagation into
// stages on the first Execute/ExecuteStream call. REQ002136.
func (e *PipelineExecutor) SetExecContext(ec *DT.ExecContext) { e.execCtx = ec }

// SetPlanner stores the query planner for propagation into stages
// on the first Execute/ExecuteStream call. REQ002136.
func (e *PipelineExecutor) SetPlanner(p PL.QueryPlanner) { e.planner = p }

// Execute drains the pipeline and returns all rows.
func (e *PipelineExecutor) Execute(ctx context.Context) ([]DT.Row, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	pipe, err := e.ensurePipeline(ctx)
	if err != nil {
		return nil, err
	}
	return pipe.Execute(ctx)
}

// ExecuteWithArgs sets params/planner/execCtx and executes in a single call.
// Shorthand for SetParams + SetPlanner + SetExecContext + Execute.
// planner and execCtx may be nil. REQ002142.
func (e *PipelineExecutor) ExecuteWithArgs(ctx context.Context, args []any, planner PL.QueryPlanner, execCtx *DT.ExecContext) ([]DT.Row, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(args) > 0 {
		e.params = args
	}
	if planner != nil {
		e.planner = planner
	}
	if execCtx != nil {
		e.execCtx = execCtx
	}
	pipe, err := e.ensurePipeline(ctx)
	if err != nil {
		return nil, err
	}
	return pipe.Execute(ctx)
}

// ExecuteStream returns a streaming iterator that reads from the
// pipeline one batch at a time. The caller must call Close() on
// the returned iterator when done.
// REQ002133: caches Pipeline.execCtx into the PipelineStream so
// streamed rows also get DT.WithExecContext applied.
func (e *PipelineExecutor) ExecuteStream(ctx context.Context) (*PipelineStream, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	pipe, err := e.ensurePipeline(ctx)
	if err != nil {
		return nil, err
	}
	return &PipelineStream{
		pipe:    pipe,
		ctx:     ctx,
		execCtx: e.execCtx,
	}, nil
}

// Reset returns the pipeline to pre-execution state for cache reuse.
func (e *PipelineExecutor) Reset(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.pipe == nil {
		return nil
	}
	return e.pipe.Reset(ctx)
}

// Close releases all resources.
func (e *PipelineExecutor) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.pipe == nil {
		return nil
	}
	return e.pipe.Close()
}

func (e *PipelineExecutor) ensurePipeline(ctx context.Context) (*Pipeline, error) {
	if e.pipe == nil {
		var err error
		e.pipe, err = e.spec.NewRuntime()
		if err != nil {
			return nil, err
		}
		// REQ002136: propagate per-execution state into stages.
		if len(e.params) > 0 {
			e.pipe.PropagateParams(e.params, &e.paramBuf)
		}
		if e.planner != nil {
			e.pipe.PropagatePlanner(e.planner)
		}
		if e.execCtx != nil {
			e.pipe.PropagateExecContext(e.execCtx)
		}
	}
	return e.pipe, nil
}

// PipelineStream provides row-by-row streaming from a pipeline.
// It reads batches from the pipeline and converts them to rows.
// REQ002133: carries execCtx for DT.WithExecContext row embedding.
type PipelineStream struct {
	pipe    *Pipeline
	ctx     context.Context
	batch   *UT.Batch
	buf     []PL.Value
	rowPos  int
	done    bool
	closeMu sync.Mutex
	closed  bool
	execCtx *DT.ExecContext // cached for fast-path check in Next()
	// Cached column count from the first batch to avoid re-scanning
	// Cols array on every row (batchRowToRow hot path). REQ002218.
	nCols int
	// Reusable row data buffer to avoid per-row make([]DT.Value, nCols).
	rowData []DT.Value
}

// Next returns the next row from the stream.
// Returns (DT.Row{}, DT.ErrNoRows) at EOF.
// REQ002133: when pipeline has an execCtx, rows are embedded with it
// (via DT.WithExecContext) so EV.EvalValue can find the Planner for
// subqueries and EV functions can access the session context.
func (s *PipelineStream) Next() (DT.Row, error) {
	if s.done {
		return DT.Row{}, DT.ErrNoRows
	}

	for {
		if s.batch == nil || s.rowPos >= s.batch.Size {
			// Fetch next batch
			if s.batch != nil {
				if s.batch.Pooled {
					s.batch.Put()
				}
				s.batch = nil
			}
			batch, err := s.pipe.root.NextBatch(s.ctx)
			if err != nil {
				return DT.Row{}, err
			}
			if batch == nil {
				s.done = true
				return DT.Row{}, DT.ErrNoRows
			}
			s.batch = batch
			s.rowPos = 0
			// Cache column count from the first batch.
			if s.nCols == 0 {
				s.nCols = countCols(batch)
				if cap(s.rowData) < s.nCols {
					s.rowData = make([]DT.Value, s.nCols)
				}
			}
		}

		// Convert batch row to DT.Row using cached buffer.
		phys := s.rowPos
		if s.batch.Sel != nil && s.rowPos < len(s.batch.Sel) {
			phys = int(s.batch.Sel[s.rowPos])
		}
		row := DT.Row{
			Data: s.rowData[:s.nCols],
		}
		for c := 0; c < s.nCols; c++ {
			row.Data[c] = UT.ToValue(s.batch.Cols[c], phys)
		}
		if s.execCtx != nil {
			DT.WithExecContext(&row, s.execCtx)
		}
		s.rowPos++
		return row, nil
	}
}

// countCols counts the number of populated columns in a batch.
// REQ002218: cached by PipelineStream to avoid re-scanning on every row.
func countCols(batch *UT.Batch) int {
	for i := range batch.Cols {
		if batch.Cols[i].Type == 0 && batch.Cols[i].Name == "" {
			return i
		}
	}
	return len(batch.Cols)
}

// Close stops the stream and releases resources.
func (s *PipelineStream) Close() error {
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.batch != nil {
		if s.batch.Pooled {
			s.batch.Put()
		}
		s.batch = nil
	}
	return nil
}

// Ensure PL.ErrNoRows is accessible.
var ErrNoRows = DT.ErrNoRows

// Ensure unused imports are valid.
var _ = OP.ErrNoRows
var _ = errors.New
