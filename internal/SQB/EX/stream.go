package EX

import (
	"context"
	"errors"
	"sync"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	OP "github.com/cyw0ng95/razordata/internal/SQB/OP"
	LX "github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func (e *Executor) QueryStream(ctx context.Context, sql string, args ...any) (*streamIterator, error) {
	// REQ000771: try the in-Executor cache before parsing. The
	// ST.Stmt.Query hot path goes through here, and avoiding the
	// parser pass on repeated queries reclaims the 12% CPU that
	// parsing was costing.
	var stmt PS.Stmt
	if e.stmtCache.entries != nil {
		if cached := e.getCachedStmt(sql); cached != nil {
			stmt = cached
		}
	}
	if stmt == nil {
		parser := PS.NewParser(sql)
		parsed, err := parser.Parse()
		if err != nil {
			return nil, err
		}
		stmt = parsed
		if e.stmtCache.entries != nil {
			e.putCachedStmt(sql, stmt)
		}
	}
	return e.QueryStreamFromAST(ctx, stmt, args...)
}

// QueryStreamFromAST runs a pre-parsed statement through the
// planner and returns a streaming iterator. Skips the parser pass
// — used by REQ000771's StmtCache to avoid re-parsing on repeated
// queries. The caller must ensure `stmt` is the AST for the same
// SQL that produced this entry (or accept semantic divergence if
// DDL changed schema).
func (e *Executor) QueryStreamFromAST(ctx context.Context, stmt PS.Stmt, args ...any) (*streamIterator, error) {

	// Check if this is a DML with RETURNING clause
	if hasReturning(stmt) {
		op, err := e.buildWriterOp(stmt)
		if err != nil {
			return nil, err
		}
		propagateParams(op, args)
		firstRow, firstErr := op.Next(ctx)
		if firstErr != nil && firstErr != DT.ErrNoRows {
			op.Close()
			return nil, firstErr
		}
		if firstErr == DT.ErrNoRows {
			// No RETURNING rows; return empty iterator
			op.Close()
			return &streamIterator{
				cols:  nil,
				types: nil,
				rowCh: nil,
				done:  true,
			}, nil
		}
		// Wrap in a buffered channel so the caller can pull rows
		// sequentially after the first.
		rowCh := make(chan DT.Row, 16)
		rowCh <- firstRow
		closed := false
		var closeOnce sync.Once
		closer := func() error {
			closeOnce.Do(func() {
				closed = true
			})
			return op.Close()
		}
		go func() {
			defer close(rowCh)
			for {
				if closed {
					return
				}
				row, err := op.Next(ctx)
				if err != nil {
					return
				}
				select {
				case rowCh <- row:
				case <-ctx.Done():
					return
				}
			}
		}()
		return &streamIterator{
			cols:   append([]string(nil), firstRow.Cols...),
			types:  append([]LX.TokenType(nil), firstRow.Types...),
			rowCh:  rowCh,
			closer: closer,
		}, nil
	}

	plan, err := e.planWithCache(stmt)
	if err != nil {
		return nil, err
	}
	if plan == nil || plan.Root == nil {
		return nil, errors.New("ex: plan produced no root")
	}
	propagateParams(plan.Root, args)
	propagatePlanner(plan.Root, e.planner)
	// REQ000586: thread DT.ExecContext to eliminate global.
	execCtx := &DT.ExecContext{Planner: e.planner, SessionID: DT.GetCurrentSessionID(), TxWriter: e.txWriter, LastChanges: e.lastChanges, TotalChanges: e.totalChanges}
	propagateExecContext(plan.Root, execCtx)
	// REQ001410: do NOT defer resetRowArena here — the goroutine
	// launched below continues reading from plan.Root after this
	// function returns. The arena must survive until the goroutine
	// finishes. resetRowArena is called in the goroutine's deferred
	// cleanup (after close(rowCh)) instead.
	// Attempt vectorized execution for eligible query plans.
	plan.Root = tryVectorizePlan(plan.Root)

	// Read first row to discover schema
	row, err := plan.Root.Next(ctx)
	if err != nil {
		if err == DT.ErrNoRows {
			plan.Root.Close()
			return &streamIterator{
				cols:  nil,
				types: nil,
				rowCh: nil,
				done:  true,
			}, nil
		}
		plan.Root.Close()
		return nil, err
	}
	DT.WithExecContext(&row, execCtx)
	cols := append([]string(nil), row.Cols...)
	types := append([]LX.TokenType(nil), row.Types...)

	rowCh := make(chan DT.Row, 16)
	rowCh <- row
	closed := false
	var closeMu sync.Mutex
	closer := func() error {
		closeMu.Lock()
		defer closeMu.Unlock()
		if closed {
			return nil
		}
		closed = true
		return plan.Root.Close()
	}
	go func() {
		defer close(rowCh)
		defer resetRowArena(execCtx)
		for {
			closeMu.Lock()
			if closed {
				closeMu.Unlock()
				return
			}
			closeMu.Unlock()
			r, err := plan.Root.Next(ctx)
			if err != nil {
				return
			}
			OP.WithExecContext(&r, execCtx)
			select {
			case rowCh <- r:
			case <-ctx.Done():
				return
			}
		}
	}()
	return &streamIterator{
		cols:   cols,
		types:  types,
		rowCh:  rowCh,
		closer: closer,
	}, nil
}

// streamIterator is the streaming row iterator returned by
// Executor.QueryStream. It buffers one row at a time so the caller can
// discover the schema before draining the rest.
// REQ000348.
type streamIterator struct {
	cols   []string
	types  []LX.TokenType
	rowCh  chan DT.Row
	closer func() error

	done bool
	mu   sync.Mutex
}

func (s *streamIterator) Cols() []string        { return s.cols }
func (s *streamIterator) Types() []LX.TokenType { return s.types }
func (s *streamIterator) Next() (DT.Row, error) {
	if s == nil || s.done || s.rowCh == nil {
		return DT.Row{}, DT.ErrNoRows
	}
	r, ok := <-s.rowCh
	if !ok {
		s.done = true
		return DT.Row{}, DT.ErrNoRows
	}
	return r, nil
}

func (s *streamIterator) Close() error {
	if s == nil {
		return nil
	}
	s.done = true
	if s.closer != nil {
		return s.closer()
	}
	return nil
}
