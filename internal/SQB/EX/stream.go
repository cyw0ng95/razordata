package EX

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	CO "github.com/cyw0ng95/razordata/internal/SQO/CO"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	AG "github.com/cyw0ng95/razordata/internal/SQB/AG"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	OP "github.com/cyw0ng95/razordata/internal/SQB/OP"
	PX "github.com/cyw0ng95/razordata/internal/SQB/PX"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
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
	// No complex operators in plan tree — but for small result sets
	// the goroutine+channel overhead dominates, so skip the check.
	// REQ001633.
	estimatedRows := estimateRowCount(sel, p)
	if hasComplexOperator(plan.Root) && estimatedRows >= syncStreamRowThreshold {
		return false
	}
	// Estimate row count below threshold
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
// REQ001461: Sort over a simple single-table SeqScan is NOT complex —
// it's just a post-process step that can safely run in the sync path.
func hasComplexOperator(op DT.Operator) bool {
	switch o := op.(type) {
	case *OP.NestedLoopJoin, *OP.HashJoin:
		return true
	case *AG.Aggregate:
		return true
	case *OP.Sort:
		// REQ001461: simple sort over SeqScan is OK for sync path.
		if isSimpleSort(o) {
			return false
		}
		return true
	}
	for _, child := range childrenOf(op) {
		if hasComplexOperator(child) {
			return true
		}
	}
	return false
}

// isSimpleSort checks if a Sort operator is over a single-table SeqScan
// with simple expressions (no subqueries, no joins, no aggregates).
// REQ001461.
func isSimpleSort(sortOp *OP.Sort) bool {
	child := sortOp.Child()
	_, isSeqScan := child.(*OP.SeqScan)
	if !isSeqScan {
		// Could be Filter(SeqScan) or Project(SeqScan) — walk down.
		for {
			switch c := child.(type) {
			case *OP.SeqScan:
				return true
			case interface{ Child() DT.Operator }:
				child = c.Child()
				if child == nil {
					return false
				}
			default:
				return false
			}
		}
	}
	return true
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
	selectivity := estimateWhereSelectivity(sel.Where, ts.ColStats)
	estimated := int64(float64(ts.RowCount) * selectivity)
	if estimated <= 0 {
		estimated = 1
	}
	return estimated
}

// estimateWhereSelectivity returns a multiplier (0.0 - 1.0) for WHERE
// predicates. Returns 1.0 (no filtering) when selectivity cannot be
// estimated. Handles AND recursively. REQ001655: uses column stats
// when available for better range selectivity estimation.
func estimateWhereSelectivity(e PS.Expr, colStats map[string]*ls.ColumnStats) float64 {
	if e == nil {
		return 1.0
	}
	if bin, ok := e.(*PS.BinaryExpr); ok {
		if bin.Op == LX.T_AND {
			return estimateWhereSelectivity(bin.Left, colStats) * estimateWhereSelectivity(bin.Right, colStats)
		}
		// REQ001655: use column stats for range/equality predicates.
		if colName, ok := extractColumnName(bin); ok {
			if stats, has := colStats[colName]; has {
				return CO.EstimateSelectivityWithStats(e, stats)
			}
		}
	}
	return CO.EstimateSelectivity(e)
}

// extractColumnName extracts the column name from a binary expression
// where one side is a column reference and the other is a literal.
// Returns the column name and true if found. REQ001655.
func extractColumnName(bin *PS.BinaryExpr) (string, bool) {
	if ident, ok := bin.Left.(*PS.Ident); ok {
		return ident.Name, true
	}
	if ident, ok := bin.Right.(*PS.Ident); ok {
		return ident.Name, true
	}
	if qn, ok := bin.Left.(*PS.QualifiedName); ok {
		return qn.Name, true
	}
	if qn, ok := bin.Right.(*PS.QualifiedName); ok {
		return qn.Name, true
	}
	return "", false
}

func (e *Executor) QueryStream(ctx context.Context, sql string, args ...any) (*streamIterator, error) {
	// REQ002129/2132: pure BuildPipeline streaming path first.
	// Produces streamIterator with pxStream set (lazy row-by-row pull
	// directly from PipelineStream.Next without goroutine or channel).
	// Any error/nil/empty spec/panic falls through to the legacy
	// parse→plan→stream path below so correctness is never sacrificed.
	if iter, ok := e.queryStreamBuildPipeline(ctx, sql, args); ok {
		return iter, nil
	}

	// REQ002145: legacy stmtCache removed — always parse fresh.
	parser := PS.NewParser(sql)
	defer parser.Close()
	stmt, err := parser.Parse()
	if err != nil {
		return nil, err
	}
	return e.QueryStreamFromAST(ctx, stmt, args...)
}

// queryStreamBuildPipeline attempts the pure BuildPipeline streaming path
// for QueryStream. Returns (iterator, true) on success, (nil, false) to
// fall through to legacy. Panics are converted to fallback (returns false).
// REQ002129/2132.
func (e *Executor) queryStreamBuildPipeline(ctx context.Context, sql string, args []any) (*streamIterator, bool) {
	if !e.usePipelineFastPath() {
		return nil, false
	}
	defer func() {
		if r := recover(); r != nil {
			slog.Warn("px.QueryStream pipeline panic, falling back",
				"err", fmt.Sprintf("%v", r),
				"sql_len", len(sql))
		}
	}()
	spec, err := e.pipelineBuilder.Build(sql)
	if err != nil || spec == nil || len(spec.Stages) == 0 {
		return nil, false
	}
	// Need output columns to expose Cols()/Types() — if pure PX path
	// hasn't derived output schema yet, defer to legacy path (which
	// fetches first row via Next() to discover schema).
	if len(spec.OutputCols) == 0 {
		return nil, false
	}
	// Full-specialization check: any LegacyBatchStageSpec → legacy path.
	// See queryAllBuildPipeline in ex.go for rationale.
	if !specHasNoLegacyStages(spec) {
		return nil, false
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
		slog.Debug("px.QueryStream ExecuteStream error, falling back",
			"err", pErr.Error(),
			"sql_len", len(sql))
		return nil, false
	}
	// Read first row eagerly to distinguish "empty result" from
	// "has rows" so the iterator behavior matches the legacy path
	// (legacy does first-row fetch in QueryStreamFromAST to discover
	// schema). If first row returns ErrNoRows we still return an
	// empty iterator with correct cols/types set.
	firstRow, firstErr := ps.Next()
	if firstErr != nil && firstErr != DT.ErrNoRows {
		_ = ps.Close()
		_ = exec.Close()
		return nil, false
	}
	// Build streamIterator backed by pxStream. If there was a first
	// row before EOF, return it via rows/idx so the caller gets it.
	iter := &streamIterator{
		cols:   append([]string(nil), spec.OutputCols...),
		types:  append([]LX.TokenType(nil), spec.OutputTypes...),
		pxExec: exec,
	}
	if firstErr == DT.ErrNoRows {
		// Empty: no more rows will arrive. Close stream eagerly,
		// keep iterator empty so Next() returns ErrNoRows immediately.
		iter.done = true
		_ = ps.Close()
	} else {
		// First row is valid: serve it via single-row buffer, then
		// continue pulling from pxStream for subsequent rows.
		iter.rows = []DT.Row{firstRow}
		iter.pxStream = ps
	}
	// Propagate LastChanges for CHANGES() / TOTAL_CHANGES() (no-op
	// for pure SELECT; keeps DML semantics consistent).
	e.lastChanges = execCtx.LastChanges
	e.totalChanges = execCtx.TotalChanges
	return iter, true
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
		propagateParams(op, args, &e.paramBuf)
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
	propagateParams(plan.Root, args, &e.paramBuf)
	propagatePlanner(plan.Root, e.planner)
	// REQ000586: thread DT.ExecContext to eliminate global.
	execCtx := &DT.ExecContext{Planner: e.planner, SessionID: DT.GetCurrentSessionID(), TxWriter: e.txWriter, LastChanges: e.lastChanges, TotalChanges: e.totalChanges}
	execCtx.RowArena = e.ensureArena()
	propagateExecContext(plan.Root, execCtx)
	// REQ002172: tryVectorizePlan removed — raw operator tree used directly.
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
		// REQ002059: always close the plan on goroutine exit so the
		// operator tree is cleaned up even when the caller abandons
		// the iterator without calling Close().
		defer plan.Root.Close()
		// REQ001604: if the plan is vectorized, drain via NextBatch()
		// instead of per-row Next() to avoid goroutine-per-row overhead.
		// REQ001587: exclude *UT.BatchToRowAdapter from the fast-path —
		// the schema-discovery Next() at line 300 above already consumed
		// the first batch via BatchToRowAdapter.Next and materialised the
		// rest into adapter.rows. Calling NextBatch() here would drain
		// the underlying BatchProducer independently, losing the rows
		// already cached in the adapter. Use the row path to consume the
		// adapter's buffered rows correctly.
		bp, isBP := plan.Root.(UT.BatchProducer)
		_, isAdapter := plan.Root.(*UT.BatchToRowAdapter)
		if isBP && !isAdapter {
			for {
				batch, err := bp.NextBatch(ctx)
				if err != nil {
					return
				}
				if batch == nil {
					break
				}
				rows := batch.ToRows()
				for i := range rows {
					OP.WithExecContext(&rows[i], execCtx)
					select {
					case rowCh <- rows[i]:
					case <-ctx.Done():
						return
					}
				}
				if batch.Pooled {
					batch.Put()
				}
			}
			return
		}
		// REQ002059: hold the mutex across the closed check AND the
		// Next() call to avoid the TOCTOU race where closer() closes
		// the plan between the check and the call.
		//
		// The outer loop acquires the lock, checks closed, calls Next,
		// and releases. The closer() function (lines 362-370) also
		// acquires the lock and sets closed=true before calling Close().
		// This ensures that if we see closed=false, the plan is still
		// open for the subsequent Next() call.
		for {
			closeMu.Lock()
			if closed {
				closeMu.Unlock()
				return
			}
			r, err := plan.Root.Next(ctx)
			closeMu.Unlock()
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

// QueryStreamCompiled runs a CompiledPlan through the streaming path,
// skipping re-parsing and re-planning. Only supports SELECT/compound
// statements (non-DML). REQ001422.
// REQ002129/2130: when cp.pipeSpec is non-nil, uses pure-PX pipeline
// streaming path (PipelineStream backed iterator) instead of legacy
// plan-tree iterator. This avoids goroutine + channel overhead for
// small-to-medium queries since PipelineStream.Next() pulls directly
// without spawning a goroutine. Falls back to legacy if pipeSpec
// is missing output columns, or if ExecuteStream/Next-1st-row errors.
func (e *Executor) QueryStreamCompiled(ctx context.Context, cp *CompiledPlan, args ...any) (*streamIterator, error) {
	if cp == nil {
		return nil, errors.New("ex: QueryStreamCompiled: nil plan")
	}
	if cp.isDML {
		return nil, errors.New("ex: QueryStreamCompiled: DML not supported for streaming")
	}

	// REQ002129/2130: pure-PX pipeline streaming path.
	// Unified gate: usePipelineFastPath + no legacy stages.
	if cp.pipeSpec != nil && len(cp.pipeSpec.OutputCols) > 0 &&
		e.usePipelineFastPath() && specHasNoLegacyStages(cp.pipeSpec) {
		defer func() {
			if r := recover(); r != nil {
				slog.Warn("px.QueryStreamCompiled pipeline panic, falling back",
					"err", fmt.Sprintf("%v", r))
			}
		}()
		exec := PX.NewPipelineExecutor(cp.pipeSpec)
		exec.SetParams(args)
		exec.SetPlanner(e.planner)
		execCtx := &DT.ExecContext{Planner: e.planner, SessionID: DT.GetCurrentSessionID(), TxWriter: e.txWriter, LastChanges: e.lastChanges, TotalChanges: e.totalChanges}
		execCtx.RowArena = e.ensureArena()
		exec.SetExecContext(execCtx)
		ps, pErr := exec.ExecuteStream(ctx)
		if pErr == nil {
			// Read first row eagerly: distinguish empty from non-empty,
			// same as legacy path.
			firstRow, firstErr := ps.Next()
			if firstErr == nil || firstErr == DT.ErrNoRows {
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
			_ = ps.Close()
		}
		_ = exec.Close()
		slog.Debug("px.QueryStreamCompiled pipeline err, falling back", "err", fmt.Sprintf("%v %v", pErr, nil))
		// Fall through to legacy path.
	}

	plan := cp.plan
	if plan == nil || plan.Root == nil {
		return nil, errors.New("ex: QueryStreamCompiled: plan produced no root")
	}
	propagateParams(plan.Root, args, &e.paramBuf)
	propagatePlanner(plan.Root, e.planner)
	execCtx := &DT.ExecContext{Planner: e.planner, SessionID: DT.GetCurrentSessionID(), TxWriter: e.txWriter, LastChanges: e.lastChanges, TotalChanges: e.totalChanges}
	execCtx.RowArena = e.ensureArena()
	propagateExecContext(plan.Root, execCtx)
	// REQ002172: tryVectorizePlan removed — raw operator tree used directly.
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

	if isEligibleForSyncStream(cp.stmt, plan, e.planner) {
		return e.streamFromOperator(ctx, plan, execCtx, firstRow, true, cols, types), nil
	}

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
		cols:  cols,
		types: types,
		rows:  rows,
		idx:   0, // start from firstRow
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
	lazyRow     *DT.Row // first row (already fetched)
	lazyPlan    *pl.PlanResult
	lazyCtx     context.Context
	lazyExecCtx *DT.ExecContext

	// REQ002129/2132: pure-PX pipeline stream path. Lazily pulls from
	// PipelineStream.Next() one row at a time. Cols/types come from
	// PipelineSpec.OutputCols/OutputTypes directly (no first-row fetch
	// needed, unlike the operator-tree path that requires a Next() call
	// to discover schema). Set by queryStreamBuildPipeline.
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
	// first row from queryStreamBuildPipeline. REQ002148: must check
	// before pxStream, because queryStreamBuildPipeline stores the
	// first row in rows[] and also sets pxStream for subsequent rows.
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
	// Close PX resources if the stream used the pure-PX path.
	s.closePX()
	if s.closer != nil {
		return s.closer()
	}
	return nil
}
