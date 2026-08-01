package EX

import (
	"context"
	"errors"
	"fmt"
	"sync"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	PX "github.com/cyw0ng95/razordata/internal/SQB/PX"
	LX "github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// QueryStream returns a streaming iterator for a SELECT statement.
// REQ002230: pipeline is the sole path; legacy parse→plan→stream fallback
// removed.
func (e *Executor) QueryStream(ctx context.Context, sql string, args ...any) (*streamIterator, error) {
	spec, bErr := e.pipelineBuilder.Build(sql)
	if bErr != nil {
		return nil, bErr
	}
	if spec == nil || len(spec.Stages) == 0 || !specHasNoLegacyStages(spec) {
		return nil, fmt.Errorf("ex: QueryStream: unsupported statement")
	}
	exec := PX.NewPipelineExecutor(spec)
	exec.SetParams(args)
	exec.SetPlanner(e.planner)
	execCtx := &DT.ExecContext{Planner: e.planner, SessionID: DT.GetCurrentSessionID(), TxWriter: e.txWriter, LastChanges: e.lastChanges, TotalChanges: e.totalChanges}
	execCtx.RowArena = e.ensureArena()
	exec.SetExecContext(execCtx)
	ps, pErr := exec.ExecuteStream(ctx)
	if pErr != nil {
		_ = exec.Close()
		return nil, pErr
	}
	firstRow, firstErr := ps.Next()
	if firstErr != nil && firstErr != DT.ErrNoRows {
		_ = ps.Close()
		_ = exec.Close()
		return nil, firstErr
	}
	iter := &streamIterator{
		cols:   append([]string(nil), spec.OutputCols...),
		types:  append([]LX.TokenType(nil), spec.OutputTypes...),
		pxExec: exec,
	}
	if firstErr == DT.ErrNoRows {
		iter.done = true
		_ = ps.Close()
	} else {
		iter.rows = []DT.Row{firstRow}
		iter.pxStream = ps
	}
	e.lastChanges = execCtx.LastChanges
	e.totalChanges = execCtx.TotalChanges
	return iter, nil
}

// QueryStreamCompiled runs a CompiledPlan through the streaming path.
// REQ002230: pipeline is the sole path; legacy plan-tree iterator fallback
// removed.
func (e *Executor) QueryStreamCompiled(ctx context.Context, cp *CompiledPlan, args ...any) (*streamIterator, error) {
	if cp == nil {
		return nil, errors.New("ex: QueryStreamCompiled: nil plan")
	}
	if cp.isDML {
		return nil, errors.New("ex: QueryStreamCompiled: DML not supported for streaming")
	}
	if cp.pipeSpec == nil || len(cp.pipeSpec.OutputCols) == 0 {
		return nil, fmt.Errorf("ex: QueryStreamCompiled: missing or empty spec")
	}
	exec := PX.NewPipelineExecutor(cp.pipeSpec)
	exec.SetParams(args)
	exec.SetPlanner(e.planner)
	execCtx := &DT.ExecContext{Planner: e.planner, SessionID: DT.GetCurrentSessionID(), TxWriter: e.txWriter, LastChanges: e.lastChanges, TotalChanges: e.totalChanges}
	execCtx.RowArena = e.ensureArena()
	exec.SetExecContext(execCtx)
	ps, pErr := exec.ExecuteStream(ctx)
	if pErr != nil {
		_ = exec.Close()
		return nil, pErr
	}
	firstRow, firstErr := ps.Next()
	if firstErr != nil && firstErr != DT.ErrNoRows {
		_ = ps.Close()
		_ = exec.Close()
		return nil, firstErr
	}
	cols := append([]string(nil), cp.pipeSpec.OutputCols...)
	types := append([]LX.TokenType(nil), cp.pipeSpec.OutputTypes...)
	iter := &streamIterator{cols: cols, types: types, pxExec: exec}
	if firstErr == DT.ErrNoRows {
		iter.done = true
		_ = ps.Close()
	} else {
		iter.rows = []DT.Row{firstRow}
		iter.pxStream = ps
	}
	e.lastChanges = execCtx.LastChanges
	e.totalChanges = execCtx.TotalChanges
	return iter, nil
}

// streamIterator is the streaming row iterator returned by
// Executor.QueryStream. REQ000348.
// REQ002230: pipeline is the sole backend — pure-PX PipelineStream
// with optional eager first-row buffer for schema discovery.
type streamIterator struct {
	cols  []string
	types []LX.TokenType
	rows  []DT.Row // eagerly-fetched first row; drained before pxStream
	idx   int

	// REQ002129/2132: pure-PX pipeline stream path. Lazily pulls from
	// PipelineStream.Next() one row at a time.
	pxStream *PX.PipelineStream
	pxExec   *PX.PipelineExecutor

	done bool
	mu   sync.Mutex
}

func (s *streamIterator) Cols() []string        { return s.cols }
func (s *streamIterator) Types() []LX.TokenType { return s.types }
func (s *streamIterator) Next() (DT.Row, error) {
	if s == nil || s.done {
		return DT.Row{}, DT.ErrNoRows
	}
	// Sync (slice-backed) path first — handles the eagerly-fetched
	// first row from QueryStream. REQ002148: must check before pxStream,
	// because QueryStream stores the first row in rows[] and also sets
	// pxStream for subsequent rows.
	if s.rows != nil {
		if s.idx >= len(s.rows) {
			// Drained the buffered first row; switch to pxStream.
			s.rows = nil
			if s.pxStream == nil {
				s.done = true
				return DT.Row{}, DT.ErrNoRows
			}
			r, err := s.pxStream.Next()
			if err != nil {
				if err == DT.ErrNoRows {
					s.done = true
					s.closePX()
					return DT.Row{}, DT.ErrNoRows
				}
				s.done = true
				s.closePX()
				return DT.Row{}, err
			}
			return r, nil
		}
		r := s.rows[s.idx]
		s.idx++
		return r, nil
	}
	// Pure PX pipeline stream path (BuildPipeline → PipelineStream.Next).
	// REQ002129/2132: no goroutine, no channel, no first-row schema
	// fetch — cols/types are known from PipelineSpec.
	if s.pxStream != nil {
		r, err := s.pxStream.Next()
		if err != nil {
			if err == DT.ErrNoRows {
				s.done = true
				s.closePX()
				return DT.Row{}, DT.ErrNoRows
			}
			s.done = true
			s.closePX()
			return DT.Row{}, err
		}
		return r, nil
	}
	s.done = true
	return DT.Row{}, DT.ErrNoRows
}

// closePX releases the PipelineStream + PipelineExecutor resources
// associated with a pure-PX stream iterator. Safe to call multiple times.
func (s *streamIterator) closePX() {
	if s.pxStream != nil {
		_ = s.pxStream.Close()
		s.pxStream = nil
	}
	if s.pxExec != nil {
		_ = s.pxExec.Close()
		s.pxExec = nil
	}
}

func (s *streamIterator) Close() error {
	if s == nil {
		return nil
	}
	s.done = true
	s.closePX()
	return nil
}
