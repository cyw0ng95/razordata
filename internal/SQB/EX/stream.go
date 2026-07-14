package EX

import (
	"context"
	"errors"
	"sync"

	AG "github.com/cyw0ng95/razordata/internal/SQB/AG"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	OP "github.com/cyw0ng95/razordata/internal/SQB/OP"
	LX "github.com/cyw0ng95/razordata/internal/SQF/LX"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

const syncStreamRowThreshold = 100

// isEligibleForSyncStream checks if a query is simple enough to use
// the synchronous caller-pull path (no goroutine/channel overhead).
// Small queries where channel sync dominates actual execution work
// benefit from the sync path.
func isEligibleForSyncStream(stmt PS.Stmt, plan *pl.PlanResult, p *Planner) bool {
	sel, ok := stmt.(*PS.Select)
	if !ok {
		return false
	}
	// No explicit joins
	if len(sel.Joins) > 0 {
		return false
	}
	// No subquery in FROM
	if sel.SubqueryFrom != nil {
		return false
	}
	// No subquery in SELECT list
	els := hasSubqueryInSelect(sel.Cols)
	if els {
		return false
	}
	// No complex operators in plan tree
	if hasComplexOperator(plan.Root) {
		return false
	}
	// Estimate row count below threshold
	estimatedRows := estimateRowCount(sel, p)
	if estimatedRows >= syncStreamRowThreshold {
		return false
	}
	return true
}

// hasSubqueryInSelect walks the SELECT list for subquery expressions.
func hasSubqueryInSelect(cols []PS.Expr) bool {
	for _, c := range cols {
		if hasSubqueryExpr(c) {
			return true
		}
	}
	return false
}

// hasSubqueryExpr checks if an expression contains a subquery.
func hasSubqueryExpr(e PS.Expr) bool {
	switch ex := e.(type) {
	case *PS.SubqueryExpr:
		return true
	case *PS.ExistsExpr:
		return true
	case *PS.InExpr:
		return ex.Subquery != nil
	case *PS.BinaryExpr:
		return hasSubqueryExpr(ex.Left) || hasSubqueryExpr(ex.Right)
	case *PS.UnaryExpr:
		return hasSubqueryExpr(ex.Operand)
	case *PS.FunctionCall:
		for _, arg := range ex.Args {
			if hasSubqueryExpr(arg) {
				return true
			}
		}
	}
	return false
}

// hasComplexOperator walks the operator tree for join, aggregate, or sort
// operators that would make the sync path inappropriate.
func hasComplexOperator(op DT.Operator) bool {
	switch op.(type) {
	case *OP.NestedLoopJoin, *OP.HashJoin:
		return true
	case *AG.Aggregate:
		return true
	case *OP.Sort:
		return true
	}
	for _, child := range childrenOf(op) {
		if hasComplexOperator(child) {
			return true
		}
	}
	return false
}

// childrenOf returns direct children of an operator.
func childrenOf(op DT.Operator) []DT.Operator {
	switch o := op.(type) {
	case interface{ Child() DT.Operator }:
		c := o.Child()
		if c != nil {
			return []DT.Operator{c}
		}
	}
	return nil
}

// estimateRowCount estimates the result row count using planner stats
// and simple predicate selectivity.
func estimateRowCount(sel *PS.Select, p *Planner) int64 {
	if sel.From == "" {
		// SELECT without FROM — single virtual row (possibly filtered)
		if sel.Where != nil {
			return 1 // single row, may be filtered to 0
		}
		return 1
	}
	ts := p.getTableStats(sel.From)
	if ts == nil || ts.RowCount <= 0 {
		return 100 // unknown; use threshold boundary
	}
	// Apply simple selectivity factors for WHERE predicates
	selectivity := estimateWhereSelectivity(sel.Where)
	estimated := int64(float64(ts.RowCount) * selectivity)
	if estimated <= 0 {
		estimated = 1
	}
	return estimated
}

// estimateWhereSelectivity returns a multiplier (0.0 - 1.0) for WHERE
// predicates. Returns 1.0 (no filtering) when selectivity cannot be
// estimated. Handles AND recursively.
func estimateWhereSelectivity(e PS.Expr) float64 {
	if e == nil {
		return 1.0
	}
	if bin, ok := e.(*PS.BinaryExpr); ok {
		if bin.Op == LX.T_AND {
			return estimateWhereSelectivity(bin.Left) * estimateWhereSelectivity(bin.Right)
		}
	}
	return estimateSelectivity(e)
}

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
	execCtx.RowArena = e.ensureArena()
	propagateExecContext(plan.Root, execCtx)
	// Attempt vectorized execution for eligible query plans.
	plan.Root = tryVectorizePlan(plan.Root)

	// Read first row to discover schema
	firstRow, firstErr := plan.Root.Next(ctx)
	if firstErr != nil {
		if firstErr == DT.ErrNoRows {
			plan.Root.Close()
			return &streamIterator{
                cols:  nil,
                types: nil,
                done:  true,
            }, nil
        }
        plan.Root.Close()
        return nil, firstErr
    }
    DT.WithExecContext(&firstRow, execCtx)
    cols := append([]string(nil), firstRow.Cols...)
    types := append([]LX.TokenType(nil), firstRow.Types...)

    // REQ001409: for simple small queries, use synchronous caller-pull path
    // to avoid goroutine stack + channel sync overhead.
    // REQ001425: use lazy streaming from the operator tree instead of
    // pre-buffering all rows into a slice. Eliminates the append+slice
    // growth per query for small result sets.
    if isEligibleForSyncStream(stmt, plan, e.planner) {
        return e.streamFromOperator(ctx, plan, execCtx, firstRow, true, cols, types), nil
    }

    // REQ001410: do NOT defer resetRowArena here — the goroutine
    // launched below continues reading from plan.Root after this
    // function returns. The arena must survive until the goroutine
    // finishes. resetRowArena is called in the goroutine's deferred
    // cleanup (after close(rowCh)) instead.
    rowCh := make(chan DT.Row, 16)
    rowCh <- firstRow
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

// syncStreamPath accumulates all rows into a slice synchronously and
// returns a slice-backed streamIterator. No goroutine or channel needed.
// For very small result sets this is optimal because the caller can
// iterate over the slice without operator tree overhead.
func (e *Executor) syncStreamPath(ctx context.Context, plan *pl.PlanResult, execCtx *DT.ExecContext, firstRow DT.Row, cols []string, types []LX.TokenType) (*streamIterator, error) {
    rows := []DT.Row{firstRow}
    for {
        r, err := plan.Root.Next(ctx)
        if err != nil {
            if err == DT.ErrNoRows {
                break
            }
            plan.Root.Close()
            return nil, err
        }
        OP.WithExecContext(&r, execCtx)
        rows = append(rows, r)
    }
    plan.Root.Close()
	return &streamIterator{
		cols: cols,
		types: types,
		rows: rows,
		idx:  0, // start from firstRow
	}, nil
}

// streamFromOperator wraps a live operator tree in a streamIterator,
// pulling rows on demand instead of pre-buffering. The operator tree
// is kept open until the caller drains or closes the iterator.
// REQ001425: eliminates the per-query syncStreamPath append+slice
// growth for the common case of small result sets (select1 pattern).
func (e *Executor) streamFromOperator(ctx context.Context, plan *pl.PlanResult, execCtx *DT.ExecContext, firstRow DT.Row, firstRowFetched bool, cols []string, types []LX.TokenType) *streamIterator {
	si := &streamIterator{
		cols:  cols,
		types: types,
		closer: func() error {
			plan.Root.Close()
			return nil
		},
	}
	if firstRowFetched {
		si.lazyRow = &firstRow
	}
	si.lazyPlan = plan
	si.lazyCtx = ctx
	si.lazyExecCtx = execCtx
	return si
}

// streamIterator is the streaming row iterator returned by
// Executor.QueryStream. It buffers one row at a time so the caller can
// discover the schema before draining the rest.
// REQ000348.
// REQ001409: supports two backends — channel-based streaming (rowCh)
// for large/complex queries, and slice-based sync (rows/idx) for small
// queries to avoid goroutine + channel overhead.
type streamIterator struct {
    cols   []string
    types  []LX.TokenType
    rowCh  chan DT.Row
    closer func() error
    rows   []DT.Row
    idx    int

    // REQ001425: lazy stream path — pulls from operator tree on demand
    // instead of pre-buffering all rows. Set by streamFromOperator.
    lazyRow      *DT.Row      // first row (already fetched)
    lazyPlan     *pl.PlanResult
    lazyCtx      context.Context
    lazyExecCtx  *DT.ExecContext

    done bool
    mu   sync.Mutex
}

func (s *streamIterator) Cols() []string        { return s.cols }
func (s *streamIterator) Types() []LX.TokenType { return s.types }
func (s *streamIterator) Next() (DT.Row, error) {
    if s == nil || s.done {
        return DT.Row{}, DT.ErrNoRows
    }
    // Lazy (operator-pull) path: stream without pre-buffering.
    if s.lazyPlan != nil {
        // Return the already-fetched first row.
        if s.lazyRow != nil {
            r := *s.lazyRow
            s.lazyRow = nil
            return r, nil
        }
        r, err := s.lazyPlan.Root.Next(s.lazyCtx)
        if err != nil {
            if err == DT.ErrNoRows {
                s.done = true
                if s.closer != nil {
                    s.closer()
                }
                return DT.Row{}, DT.ErrNoRows
            }
            s.done = true
            if s.closer != nil {
                s.closer()
            }
            return DT.Row{}, err
        }
        OP.WithExecContext(&r, s.lazyExecCtx)
        return r, nil
    }
    // Sync (slice-backed) path
    if s.rows != nil {
        if s.idx >= len(s.rows) {
            s.done = true
            return DT.Row{}, DT.ErrNoRows
        }
        r := s.rows[s.idx]
        s.idx++
        return r, nil
    }
    // Channel path
    if s.rowCh == nil {
        s.done = true
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