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

const syncStreamRowThreshold = 1000

// isEligibleForSyncStream checks if a query is simple enough to use
// the synchronous caller-pull path (no goroutine/channel overhead).
// Small queries where channel sync dominates actual execution work
// benefit from the sync path. REQ001529: threshold raised to 1000,
// complex operators ignored for tiny result sets (< 200 rows).
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
	// REQ001523: allow sync path when all subqueries in SELECT are
	// non-correlated single-table aggregates that would be globally
	// cached after first evaluation.
	if hasSubqueryInSelect(sel.Cols) && !allSelectSubqueriesCacheable(sel.Cols) {
		return false
	}
	// Estimate row count below threshold
	estimatedRows := estimateRowCount(sel, p)
	if estimatedRows >= syncStreamRowThreshold {
		return false
	}
	// REQ001529: for very small result sets (estimated < 200 rows),
	// the goroutine overhead dominates — use sync path even with
	// complex operators (Aggregate, Sort, etc.).
	if estimatedRows < 200 {
		return true
	}
	// No complex operators in plan tree
	if hasComplexOperator(plan.Root) {
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

// REQ001523: allSelectSubqueriesCacheable checks that every subquery
// in the SELECT list is a non-correlated single-table aggregate that
// would be globally cached after first evaluation. When true, the
// sync path is safe because after the first row each subquery is
// just a globalSubqueryCache lookup (no goroutine overhead needed).
func allSelectSubqueriesCacheable(cols []PS.Expr) bool {
	for _, c := range cols {
		if !subqueryExprCacheable(c) {
			return false
		}
	}
	return true
}

func subqueryExprCacheable(e PS.Expr) bool {
	switch ex := e.(type) {
	case *PS.BinaryExpr:
		return subqueryExprCacheable(ex.Left) && subqueryExprCacheable(ex.Right)
	case *PS.UnaryExpr:
		return subqueryExprCacheable(ex.Operand)
	case *PS.FunctionCall:
		for _, arg := range ex.Args {
			if !subqueryExprCacheable(arg) {
				return false
			}
		}
		return true
	case *PS.SubqueryExpr:
		return isCacheableSubquery(ex.Subquery)
	case *PS.ExistsExpr:
		return isCacheableSubquery(ex.Subquery)
	case *PS.InExpr:
		if ex.Subquery != nil {
			return isCacheableSubquery(ex.Subquery)
		}
		return true // IN with literal list is fine
	default:
		return true // other expression types are not subqueries
	}
}

// isCacheableSubquery checks whether a subquery statement is a non-correlated
// single-table aggregate that would be globally cached.
func isCacheableSubquery(stmt PS.Stmt) bool {
	sel, ok := stmt.(*PS.Select)
	if !ok {
		return false
	}
	return isCacheableSelect(sel)
}

// isCacheableSelect returns true when sel is a single-table aggregate
// with no WHERE/GROUP BY/HAVING/ORDER BY/LIMIT. Such subqueries are
// non-correlated (no WHERE means no outer column refs) and the global
// cache covers them after first evaluation.
func isCacheableSelect(sel *PS.Select) bool {
	if sel.From == "" || len(sel.Joins) != 0 {
		return false
	}
	if sel.Where != nil || len(sel.OrderBy) != 0 ||
		sel.Limit != nil || sel.Offset != nil ||
		len(sel.GroupBy) != 0 || sel.Having != nil {
		return false
	}
	if len(sel.Cols) != 1 {
		return false
	}
	_, ok := sel.Cols[0].(*PS.AggregateFunc)
	return ok
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
		// REQ001511: clone firstRow to heap
		rowCh <- cloneRowToHeap(firstRow)
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
				// REQ001511: clone to heap so Data survives op.Close()
				cloned := cloneRowToHeap(row)
				select {
				case rowCh <- cloned:
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
    if isEligibleForSyncStream(stmt, plan, e.planner) {
        return e.syncStreamPath(ctx, plan, execCtx, firstRow, cols, types)
    }

    // REQ001410: do NOT defer resetRowArena here — the goroutine
    // launched below continues reading from plan.Root after this
    // function returns. The arena must survive until the goroutine
    // finishes. resetRowArena is called in the goroutine's deferred
    // cleanup (after close(rowCh)) instead.
    rowCh := make(chan DT.Row, 16)
    // REQ001511: clone firstRow to heap — it comes from plan.Root.Next()
    // and references SeqScan arena memory that may be reset.
    rowCh <- cloneRowToHeap(firstRow)
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
            // REQ001511: clone to heap so Data survives plan.Root.Close()
            // (called by closer, which resets SeqScan's RowArena).
            cloned := cloneRowToHeap(r)
            select {
            case rowCh <- cloned:
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
func (e *Executor) syncStreamPath(ctx context.Context, plan *pl.PlanResult, execCtx *DT.ExecContext, firstRow DT.Row, cols []string, types []LX.TokenType) (*streamIterator, error) {
    rows := []DT.Row{firstRow}
    for {
        r, err := plan.Root.Next(ctx)
        if err != nil {
            if err == DT.ErrNoRows {
                break
            }
            plan.Root.Close()
            resetRowArena(execCtx)
            return nil, err
        }
        OP.WithExecContext(&r, execCtx)
        rows = append(rows, r)
    }
    // REQ001511: clone rows before closing plan — plan.Root.Close()
    // cascades to SeqScan.Close() → RowArena.Reset(), invalidating
    // Row.Data pointers into the arena slab.
    cloneArena := &DT.RowArena{}
    rows = cloneArena.CloneRowsBatch(rows)

    plan.Root.Close()
    resetRowArena(execCtx)
    return &streamIterator{
        cols:       cols,
        types:      types,
        rows:       rows,
        idx:        0,
        cloneArena: cloneArena, // keep backing memory alive
    }, nil
}

// cloneRowToHeap creates a heap copy of a DT.Row whose Data slice
// is backed by independent heap memory, safe to use after the origin
// arena is reset. REQ001511.
func cloneRowToHeap(r DT.Row) DT.Row {
    if len(r.Data) == 0 {
        return r
    }
    data := make([]DT.Value, len(r.Data))
    copy(data, r.Data)
    return DT.Row{
        Cols:     r.Cols,
        Types:    r.Types,
        ColIndex: r.ColIndex,
        Data:     data,
    }
}

// streamIterator is the streaming row iterator returned by
// Executor.QueryStream. It buffers one row at a time so the caller can
// discover the schema before draining the rest.
// REQ000348.
// REQ001409: supports two backends — channel-based streaming (rowCh)
// for large/complex queries, and slice-based sync (rows/idx) for small
// queries to avoid goroutine + channel overhead.
// REQ001511: cloneArena keeps the cloned-row backing memory alive for
// the sync path; the channel path uses cloneRowToHeap per row.
type streamIterator struct {
    cols   []string
    types  []LX.TokenType
    rowCh  chan DT.Row
    closer func() error
    rows   []DT.Row
    idx    int

    done       bool
    mu         sync.Mutex
    cloneArena *DT.RowArena // REQ001511: keeps clone backing memory alive
}

func (s *streamIterator) Cols() []string        { return s.cols }
func (s *streamIterator) Types() []LX.TokenType { return s.types }
func (s *streamIterator) Next() (DT.Row, error) {
    if s == nil || s.done {
        return DT.Row{}, DT.ErrNoRows
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