package EX

import (
	CO "github.com/cyw0ng95/razordata/internal/SQO/CO"
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	EC "github.com/cyw0ng95/razordata/internal/LOG/EC"
	AD "github.com/cyw0ng95/razordata/internal/SQB/AD"
	AG "github.com/cyw0ng95/razordata/internal/SQB/AG"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
	OP "github.com/cyw0ng95/razordata/internal/SQB/OP"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	WT "github.com/cyw0ng95/razordata/internal/SQB/WT"
	OC "github.com/cyw0ng95/razordata/internal/SQO/OC"
	PF "github.com/cyw0ng95/razordata/internal/SQO/PF"
	QP "github.com/cyw0ng95/razordata/internal/SQF/QP"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	RE "github.com/cyw0ng95/razordata/internal/SQF/RE"
)

type plan struct {
	root    DT.Operator
	cost    float64
	memoKey string
}

type joinPlan struct {
	root   DT.Operator
	cost   float64
	tables map[string]bool
	order  []string
}

const n3HeapMaxSize = 24

const n3PruneMultiplier = 2.0

const HashAggregateThreshold = 1000

const maxPlanCacheSize = 1024

type Planner struct {
	mu   sync.Mutex
	memo map[string]*plan
	// REQ000989: memoOrder tracks insertion order for O(1) LRU eviction.
	// A ring buffer: oldest entry at memoOrder[0], newest at the end.
	// When the cache is full, memoOrder[0] is evicted and the head pointer
	// advances. This replaces the O(n) random scan on every cache insert.
	memoOrder []string
	memoHead  int // index of oldest entry in memoOrder
	memoSize  int // number of valid entries in memoOrder
	catalog   map[string]*tableInfo
	store     DT.Store
	// statsCatalog provides access to column statistics for
	// histogram-based selectivity estimation. REQ000085.
	statsCatalog pl.StatsCatalog
	// pool is the shared WorkerPool for parallel operator execution.
	// Set by Executor on creation; nil means serial-only execution.
	// REQ001044.
	pool pl.WorkerPool
	// joinBufferSize caps per-hash-join memory. 0 = unlimited.
	// Set by Executor.WithMemoryBudget. REQ001056.
	joinBufferSize int64
	// maxMemoryPerQuery caps total memory per query. 0 = unlimited.
	// Set by Executor.WithMemoryBudget. REQ001057.
	maxMemoryPerQuery int64
	// costParamsX holds the cost-model coefficients used by
	// estimateCost. nil = use DefaultCostParams. REQ001104.
	costParamsX *CostParams

	// splitAndCache caches the result of RE.SplitAnd for expression
	// nodes. REQ001167: `SplitAnd` was called repeatedly on the same
	// WHERE expression during a single Plan() call — up to 5 times
	// per SELECT — causing redundant AND-tree traversals. The cache
	// is keyed by `reflect.ValueOf(e).Pointer()` and cleared at the
	// start of every Plan() call (PlanResult cache invalidation is
	// handled separately).
	splitAndCache map[uintptr][]PS.Expr
	// REQ001432: optimizer is the SQO/OC Optimizer instance. Created
	// empty in NewPlanner; the full pass chain is wired in REQ001450.
	// Until then, Plan() does not invoke optimizer.Optimize() —
	// the field exists only to establish the SQF → SQO → SQB
	// dependency order.
	optimizer *OC.Optimizer
	// REQ002256: qpOptimizer runs QP passes on the QueryPlan DAG
	// built alongside the operator tree. Shadow-mode verified in tests.
	qpOptimizer *QP.Optimizer
	// REQ001448: outerAliases are the table names of the OUTER query
	// passed to SubPlanner.PlanSubquery so candidate-join-key logic
	// avoids columns belonging to the outer side. nil = no outer query.
	outerAliases []string
	// REQ001443: batchSize is the per-query batch size knob.
	// Chosen by chooseBatchSize() based on LIMIT clause, estimated
	// row count, and operator tree depth. 0 = use heuristic.
	batchSize int
	// REQ001332: collations is a registry of user-defined collation
	// functions keyed by name.
	collations map[string]DT.CollateFunc

	// REQ001672: reusable scratch maps for planSelect conjunct
	// decomposition. Avoids per-plan make(map) allocations.
	extractedPtrsScratch    map[PS.Expr]bool
	existsReplacedScratch   map[*PS.ExistsExpr]bool
}

type tableInfo struct {
	name    string
	cols    []DT.ColInfo
	pk      string
	indexes map[string][]string
	// REQ001420: atomic row count for COUNT(*) optimization.
	// Updated by the executor after INSERT/DELETE; read by the planner
	// for simple COUNT(*) queries with no WHERE/GROUP BY/DISTINCT.
	rowCount atomic.Int64
}

// RowCount returns the cached row count for a table (REQ001420).
func (t *tableInfo) GetRowCount() int64 {
	return t.rowCount.Load()
}

// AddRowCount atomically adjusts the cached row count (REQ001420).
func (t *tableInfo) AddRowCount(delta int64) {
	t.rowCount.Add(delta)
}

func NewPlanner() *Planner {
	return &Planner{
		memo:      make(map[string]*plan, maxPlanCacheSize),
		memoOrder: make([]string, maxPlanCacheSize),
		catalog:   make(map[string]*tableInfo),
		// REQ001450: wire SQO passes. Order: constant folding,
		// column pruning, predicate pushdown, limit pushdown (TopN fusion).
		optimizer: OC.New().
			AddPass(&PF.ConstantFoldingPass{}).
			AddPass(&PF.SubqueryDecorrelationPass{}).
			AddPass(&PF.IndexSelectionPass{}).
			AddPass(&PF.RedundantPredicateEliminationPass{}).
			AddPass(&PF.OrToInExpansionPass{}).
			AddPass(&PF.ImplicitCastEliminationPass{}).
			AddPass(&PF.SortEliminationPass{}).
			AddPass(&PF.ColumnPruningPass{}).
			AddPass(&PF.PredicatePushdownPass{}).
			AddPass(&PF.LimitPushdownPass{}),
		qpOptimizer: QP.NewOptimizer().
			AddPass(&QP.ConstantFoldingPass{}).
			AddPass(&QP.ColumnPruningPass{}).
			AddPass(&QP.PredicatePushdownPass{}),
	}
}

// runQPPasses builds a QueryPlan from the operator tree and runs the
// QP optimizer passes. The QueryPlan is mutated in place. REQ002256.
func (p *Planner) runQPPasses(op DT.Operator, stmt PS.Stmt) {
	if p.qpOptimizer == nil || p.qpOptimizer.PassCount() == 0 {
		return
	}
	qp := QP.BuildQueryPlan(op)
	if qp == nil {
		return
	}
	qp.Stmt = stmt
	_ = p.qpOptimizer.Optimize(qp)
}

// runSQOPasses invokes the SQO optimizer on the built operator plan.
// Returns the original operator unchanged if the optimizer has no passes
// registered or if optimization fails. REQ001450.
func (p *Planner) runSQOPasses(op DT.Operator, stmt PS.Stmt) DT.Operator {
	if p.optimizer.PassCount() == 0 {
		return op
	}
	ctx := &OC.Context{
		Factory:    OP.Factory(),
		Tables:     p.buildTableSchema(),
		Catalog:    &exCatalogReader{catalog: p.catalog},
		SubPlanner: &exSubPlanner{p: p},
	}
	plan := &OC.Plan{Root: op, Stmt: stmt}
	result, err := p.optimizer.Optimize(plan, ctx)
	if err != nil {
		return op
	}
	if result == nil || result.Root == nil {
		return op
	}
	return result.Root.(DT.Operator)
}

// buildTableSchema converts the planner's internal catalog to
// OC.TableSchema for the SQO pass context.
func (p *Planner) buildTableSchema() map[string]OC.TableSchema {
	if len(p.catalog) == 0 {
		return nil
	}
	out := make(map[string]OC.TableSchema, len(p.catalog))
	for name, ti := range p.catalog {
		cols := make([]string, len(ti.cols))
		for i, c := range ti.cols {
			cols[i] = c.Name
		}
		out[name] = OC.TableSchema{Columns: cols, PK: ti.pk}
	}
	return out
}

// SetPool attaches a WorkerPool to the planner for parallel operator
// execution. Nil means serial-only. REQ001044.
func (p *Planner) SetPool(pool pl.WorkerPool) { p.pool = pool }

// Pool returns the attached WorkerPool (may be nil). REQ001044.
func (p *Planner) Pool() pl.WorkerPool { return p.pool }

// SetOuterAliases sets the outer-query table aliases that the next
// Plan() call should treat as OUTER (not INNER) — used by REQ001448
// subquery decorrelation so the planned subtree does not bind columns
// belonging to the outer side.
func (p *Planner) SetOuterAliases(aliases []string) { p.outerAliases = aliases }

// OuterAliases returns the currently set outer aliases (or nil).
func (p *Planner) OuterAliases() []string { return p.outerAliases }

// SetJoinBufferSize sets the per-hash-join memory cap.
// 0 = unlimited. REQ001056.
func (p *Planner) SetJoinBufferSize(v int64) { p.joinBufferSize = v }

// SetMaxMemoryPerQuery sets the per-query memory cap.
// 0 = unlimited. REQ001057.
func (p *Planner) SetMaxMemoryPerQuery(v int64) { p.maxMemoryPerQuery = v }

// InvalidateCache clears the plan cache. REQ000846: called when DTL
// changes the schema (CREATE/DROP/ALTER TABLE) so cached plans that
// reference the old schema are not reused.
func (p *Planner) InvalidateCache() {
	p.mu.Lock()
	p.clearMemoLocked()
	p.mu.Unlock()
}

// clearMemoLocked clears the plan cache. Caller must hold p.mu.
// REQ001673: use clear() instead of make() to avoid re-allocation;
// the map/slice capacity is preserved across Reset cycles.
func (p *Planner) clearMemoLocked() {
	clear(p.memo)
	clear(p.memoOrder)
	p.memoHead = 0
	p.memoSize = 0
	p.splitAndCache = nil
}

// NewPlannerWithStore returns a planner that routes its leaf operators
// through store. The store may be nil to fall back to in-memory mode.
func NewPlannerWithStore(store DT.Store) *Planner {
	return &Planner{
		memo:      make(map[string]*plan),
		memoOrder: make([]string, maxPlanCacheSize),
		catalog:   make(map[string]*tableInfo),
		store:     store,
		optimizer: OC.New().
			AddPass(&PF.ConstantFoldingPass{}).
			AddPass(&PF.SubqueryDecorrelationPass{}).
			AddPass(&PF.IndexSelectionPass{}).
			AddPass(&PF.RedundantPredicateEliminationPass{}).
			AddPass(&PF.OrToInExpansionPass{}).
			AddPass(&PF.ImplicitCastEliminationPass{}).
			AddPass(&PF.SortEliminationPass{}).
			AddPass(&PF.ColumnPruningPass{}).
			AddPass(&PF.PredicatePushdownPass{}).
			AddPass(&PF.LimitPushdownPass{}),
		qpOptimizer: QP.NewOptimizer().
			AddPass(&QP.ConstantFoldingPass{}).
			AddPass(&QP.ColumnPruningPass{}).
			AddPass(&QP.PredicatePushdownPass{}),
	}
}

// NewPlannerWithStats returns a planner with store and stats catalog
// wired. Used by SYS.Open when a stats catalog is available.
func NewPlannerWithStats(store DT.Store, statsCatalog DT.StatsCatalog) *Planner {
	return &Planner{
		memo:         make(map[string]*plan),
		catalog:      make(map[string]*tableInfo),
		store:        store,
		statsCatalog: statsCatalog,
		optimizer: OC.New().
			AddPass(&PF.ConstantFoldingPass{}).
			AddPass(&PF.SubqueryDecorrelationPass{}).
			AddPass(&PF.IndexSelectionPass{}).
			AddPass(&PF.RedundantPredicateEliminationPass{}).
			AddPass(&PF.OrToInExpansionPass{}).
			AddPass(&PF.ImplicitCastEliminationPass{}).
			AddPass(&PF.SortEliminationPass{}).
			AddPass(&PF.ColumnPruningPass{}).
			AddPass(&PF.PredicatePushdownPass{}).
			AddPass(&PF.LimitPushdownPass{}),
		qpOptimizer: QP.NewOptimizer().
			AddPass(&QP.ConstantFoldingPass{}).
			AddPass(&QP.ColumnPruningPass{}).
			AddPass(&QP.PredicatePushdownPass{}),
	}
}

// SetStatsCatalog wires a stats catalog into an existing planner.
// REQ000085.
func (p *Planner) SetStatsCatalog(statsCatalog pl.StatsCatalog) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.statsCatalog = statsCatalog
}

// getTableStats returns aggregated TableStats for a table, combining
// column statistics from the stats catalog with in-memory table data.
// REQ000787.
func (p *Planner) getTableStats(table string) *AD.TableStats {
	p.mu.Lock()
	defer p.mu.Unlock()

	ts := &AD.TableStats{
		ColStats:     make(map[string]*ls.ColumnStats),
		RowCount:     0,
		TotalWidth:   0,
		LastAnalyzed: 0,
	}

	// First, try to get row count from in-memory tables.
	if rows, ok := DT.Tables[table]; ok {
		ts.RowCount = int64(len(rows))
		ts.TotalWidth = 100 // default width
	}

	// Then, aggregate column statistics from stats catalog.
	if p.statsCatalog != nil {
		// Get table info to know column names.
		if tInfo, ok := p.catalog[table]; ok {
			for _, col := range tInfo.cols {
				if cs := p.statsCatalog.ColumnStatsByName(table, col.Name); cs != nil {
					ts.ColStats[col.Name] = cs
					if cs.RowCount > ts.RowCount {
						ts.RowCount = cs.RowCount
					}
				}
			}
		}
	}

	// If no row count from either source, use default.
	if ts.RowCount == 0 {
		ts.RowCount = 100
	}

	return ts
}

func (p *Planner) RegisterTable(name string, cols []DT.ColInfo, pk string) {
	p.catalog[name] = &tableInfo{
		name:    name,
		cols:    cols,
		pk:      pk,
		indexes: make(map[string][]string),
	}
}

func (p *Planner) RegisterIndex(table, index string, cols []string) {
	if t, ok := p.catalog[table]; ok {
		t.indexes[index] = cols
	}
}

// commonColumns returns the case-insensitive intersection of column names
// between left and right tables. REQ001359: drives NATURAL JOIN ON-clause
// synthesis. Falls back to DT.InMemSchemas for tables created via
// CREATE TABLE (which populate the runtime schema map, not planner.catalog).
func (p *Planner) commonColumns(left, right string) []string {
	leftCols := p.tableColumns(left)
	rightCols := p.tableColumns(right)
	if leftCols == nil || rightCols == nil {
		return nil
	}
	rightSet := make(map[string]bool, len(rightCols))
	for _, c := range rightCols {
		rightSet[strings.ToLower(c)] = true
	}
	var common []string
	for _, c := range leftCols {
		if rightSet[strings.ToLower(c)] {
			common = append(common, c)
		}
	}
	return common
}

// tableColumns returns the column names for a table from planner.catalog
// or DT.InMemSchemas / DT.Schemas as a fallback (the latter is populated
// by CREATE TABLE).
func (p *Planner) tableColumns(table string) []string {
	if t, ok := p.catalog[table]; ok {
		out := make([]string, len(t.cols))
		for i, c := range t.cols {
			out[i] = c.Name
		}
		return out
	}
	if ss, ok := DT.InMemSchemas[table]; ok && ss != nil {
		return ss.Cols
	}
	if cols, ok := DT.Schemas[table]; ok && cols != nil {
		return cols
	}
	return nil
}

func (p *Planner) RegisterCollation(name string, fn DT.CollateFunc) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.collations == nil {
		p.collations = make(map[string]DT.CollateFunc)
	}
	if _, ok := p.collations[name]; ok {
		return fmt.Errorf("collation %q already registered", name)
	}
	p.collations[name] = fn
	return nil
}

func (p *Planner) LookupCollation(name string) DT.CollateFunc {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.collations == nil {
		return nil
	}
	return p.collations[name]
}

// UpdateTableRowCount adjusts the cached row count for a table (REQ001420).
// Called by the executor after INSERT/DELETE to maintain approximate counts.
// Also invalidates the plan cache so COUNT(*) fast-path plans are rebuilt
// with the current count on the next query.
func (p *Planner) UpdateTableRowCount(table string, delta int64) {
	p.mu.Lock()
	t, ok := p.catalog[table]
	if !ok {
		p.mu.Unlock()
		return
	}
	t.AddRowCount(delta)
	// Invalidate the plan cache so COUNT(*) fast-path plans are rebuilt
	// with the updated count on the next query.
	p.clearMemoLocked()
	p.mu.Unlock()
}

// GetTableRowCount returns the cached row count for a table (REQ001420).
// Returns 0 if the table is not registered.
func (p *Planner) GetTableRowCount(table string) int64 {
	p.mu.Lock()
	t, ok := p.catalog[table]
	p.mu.Unlock()
	if !ok {
		return 0
	}
	return t.GetRowCount()
}

// availableIndexes returns the index names registered for a table.
// REQ001296: used by buildPlanNodeTree to annotate SeqScan nodes.
func (p *Planner) availableIndexes(table string) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	t, ok := p.catalog[table]
	if !ok {
		return nil
	}
	var names []string
	for name := range t.indexes {
		names = append(names, name)
	}
	return names
}

func (p *Planner) Plan(stmt PS.Stmt) (*pl.PlanResult, error) {
	// Clear per-plan cache at start of every Plan() call.
	// REQ001167: SplitAnd cache is only valid for one Plan() call.
	p.splitAndCache = nil
	rewritten, err := RE.Rewrite(stmt)
	if err != nil {
		return nil, err
	}

	// REQ001202: parameterized memo key using EncodeMemoKey so
	// structurally identical queries with different literal values
	// share a cache entry. On cache hit, substitute the current
	// query's literal values into the cached operator tree via
	// replaceLiteralsOnTree (same mechanism as the executor cache).
	// REQ001972: EncodeMemoKey pools the internal clone arena.
	key, params := pl.EncodeMemoKey(rewritten)
	p.mu.Lock()
	if cached, ok := p.memo[key]; ok {
		replaceLiteralsOnTree(cached.root, params)
		result := &pl.PlanResult{Root: cached.root, Cost: cached.cost, MemoKey: cached.memoKey}
		p.mu.Unlock()
		return result, nil
	}
	p.mu.Unlock()

	var root DT.Operator

	switch s := rewritten.(type) {
	case *PS.Select:
		// REQ001293: execute non-correlated scalar subqueries at
		// plan time and replace with constants.
		p.constantFoldSubqueries(context.Background(), s)
		root = p.planSelect(s)
	case *PS.CompoundStmt:
		root = p.planCompound(s)
	case *PS.Insert:
		root = p.planInsert(s)
	case *PS.Update:
		root = p.planUpdate(s)
	case *PS.Delete:
		root = p.planDelete(s)
	case *PS.CreateTable:
		root = p.planCreateTable(s)
		p.InvalidateCache()
	case *PS.DropTable:
		root = p.planDropTable(s)
		p.InvalidateCache()
	case *PS.CreateIndexStmt:
		root = p.planCreateIndex(s)
		p.InvalidateCache()
	case *PS.DropIndexStmt:
		root = p.planDropIndex(s)
		p.InvalidateCache()
	case *PS.ExplainStmt:
		root = p.planExplain(s)
	case *PS.AnalyzeStmt:
		root = p.planAnalyze(s)
	case *PS.VacuumStmt:
		root = p.planVacuum(s)
	case *PS.PragmaStmt:
		root = p.planPragma(s)
	case *PS.WithStmt:
		root = p.planWith(s)
	case *PS.ValuesStmt:
		root = OP.NewValuesRowsOp(s.Rows)
	}

	// REQ002171: AdaptiveOp removed — PipelineBuilder replaces ADQC.

	// REQ002189: propagate the planner into the operator tree at plan
	// creation time so that subsequent calls to Plan() from the memo
	// cache do not mutate the cached tree during pipeline execution
	// (propagatePlanner was previously called in the specialize closure,
	// which mutated the shared cached root on every pipeline execution).
	propagatePlanner(root, p)

	result := &plan{
		root:    root,
		cost:    p.estimateCost(root),
		memoKey: key,
	}

	p.mu.Lock()
	// REQ001420: skip memoization for ConstRow operators (COUNT(*)
	// fast path). The plan is trivially cheap to create and ConstRow
	// is shared via the memo cache — skipping storage avoids races
	// between concurrent Next/Close calls on the shared instance.
	// ConstRow may be wrapped in AdaptiveOp, so check the inner type.
	if !isConstRowPlan(root) {
		p.memo[key] = result
		// REQ000989: LRU ring-buffer eviction. When the cache exceeds
		// maxPlanCacheSize, evict the oldest entry (memoOrder[memoHead])
		// and advance the head pointer. O(1) per eviction vs O(n) scan.
		p.memoOrder[(p.memoHead+p.memoSize)%maxPlanCacheSize] = key
		p.memoSize++
		if p.memoSize > maxPlanCacheSize {
			oldest := p.memoOrder[p.memoHead]
			delete(p.memo, oldest)
			p.memoHead = (p.memoHead + 1) % maxPlanCacheSize
		}
	}
	p.mu.Unlock()

	return &pl.PlanResult{Root: root, Cost: result.cost, MemoKey: key}, nil
}

// constantFoldSubqueries walks the SELECT expression tree and replaces
// non-correlated scalar subqueries with their evaluated constants.
// REQ001293.
func (p *Planner) constantFoldSubqueries(ctx context.Context, s *PS.Select) {
	for i, col := range s.Cols {
		if sub, ok := col.(*PS.SubqueryExpr); ok {
			if sub.Subquery == nil {
				continue
			}
			if hasOuterRefInStmt(sub.Subquery) {
				continue
			}
			// Non-correlated: execute now at plan time.
			rows, err := p.ExecuteSubquery(ctx, sub.Subquery, nil, nil)
			if err != nil || len(rows) == 0 {
				continue
			}
			val := rows[0].Data[0]
			if iv, ok := val.ToAny().(int64); ok {
				s.Cols[i] = &PS.NumberLiteral{Val: iv}
			} else if fv, ok := val.ToAny().(float64); ok {
				s.Cols[i] = &PS.FloatLiteral{Val: fv}
			} else if sv, ok := val.ToAny().(string); ok {
				s.Cols[i] = &PS.StringLiteral{Val: sv}
			}
		}
	}
}

// hasOuterRef checks whether an expression references an outer row.
func hasOuterRef(e PS.Expr) bool {
	if e == nil {
		return false
	}
	switch v := e.(type) {
	case *PS.QualifiedName:
		return true
	case *PS.BinaryExpr:
		return hasOuterRef(v.Left) || hasOuterRef(v.Right)
	case *PS.UnaryExpr:
		return hasOuterRef(v.Operand)
	case *PS.FunctionCall:
		for _, a := range v.Args {
			if hasOuterRef(a) {
				return true
			}
		}
	}
	return false
}

// hasOuterRefInCols checks a list of SELECT columns for outer refs.
func hasOuterRefInCols(cols []PS.Expr) bool {
	for _, c := range cols {
		if ae, ok := c.(*PS.AliasedExpr); ok {
			if hasOuterRef(ae.Expr) {
				return true
			}
		}
		if hasOuterRef(c) {
			return true
		}
	}
	return false
}

// hasOuterRefInStmt checks if a statement contains any outer column
// references, simplified to checking SELECT columns + WHERE clause.
func hasOuterRefInStmt(stmt PS.Stmt) bool {
	if sel, ok := stmt.(*PS.Select); ok {
		if hasOuterRefInCols(sel.Cols) {
			return true
		}
		if sel.Where != nil && hasOuterRef(sel.Where) {
			return true
		}
	}
	return false
}

// ExecuteSubquery plans a subquery select statement and collects all
// results in a single slice. Implements pl.QueryPlanner.
func (p *Planner) ExecuteSubquery(ctx context.Context, stmt PS.Stmt, outer *DT.Row, params []any) ([]DT.Row, error) {
	sel, ok := stmt.(*PS.Select)
	if !ok {
		return nil, EV.ErrSubquery
	}
	planResult, err := p.Plan(sel)
	if err != nil {
		return nil, err
	}
	return WT.RunSubqueryPlan(ctx, planResult, outer, params)
}

// ExecuteSubqueryFirstMatch is the REQ001073 short-circuit variant:
// plans the subquery and returns (true, nil) if at least one row
// matches, (false, nil) otherwise, stopping the scan at the first
// match instead of materializing all rows. Implements the "semi-join
// stops scanning after the first match" optimization.
//
// This is NOT part of the pl.QueryPlanner interface; evalExists calls
// it via a type assertion so existing QueryPlanner implementations
// that lack this method fall back to the legacy materialization path.
func (p *Planner) ExecuteSubqueryFirstMatch(ctx context.Context, stmt PS.Stmt, outer *DT.Row, params []any) (bool, error) {
	sel, ok := stmt.(*PS.Select)
	if !ok {
		return false, EV.ErrSubquery
	}
	planResult, err := p.Plan(sel)
	if err != nil {
		return false, err
	}
	// REQ002218: do NOT defer planResult.Root.Close(). The planResult
	// is memoized in the planner's cache; closing it would corrupt the
	// shared SeqScan underneath outerInjector, breaking subsequent
	// correlated evaluations on later outer rows. WT.RunSubqueryFirstMatch
	// now handles lifecycle via resetSubqueryTree at the start of each call.
	return WT.RunSubqueryFirstMatch(ctx, planResult, outer, params)
}

// ExecuteSubqueryInMatch is the REQ001671 short-circuit variant for IN
// subqueries. Returns (matched, hadNull, err) with three-valued logic.
// Stops at the first match instead of materializing all rows.
func (p *Planner) ExecuteSubqueryInMatch(ctx context.Context, stmt PS.Stmt, outer *DT.Row, params []any, target any) (bool, bool, error) {
	sel, ok := stmt.(*PS.Select)
	if !ok {
		return false, false, EV.ErrSubquery
	}
	planResult, err := p.Plan(sel)
	if err != nil {
		return false, false, err
	}
	defer planResult.Root.Close()
	return WT.RunSubqueryInMatch(ctx, planResult, target, outer, params)
}

// estimateCost returns a unitless cost for the operator tree rooted at op.
// The model uses uniform distribution: each row is 1.0 unit, filters and
// joins apply selectivity, sort adds a log(n) factor. Real statistics land
// in v2.
// CostParams captures the cost-model coefficients used by
// estimateCost. REQ001104. Defaults match PostgreSQL's conventional
// values (seq_page_cost=1.0, random_page_cost=4.0, cpu_tuple_cost=0.01,
// cpu_index_tuple_cost=0.005, cpu_operator_cost=0.0025) but are
// tunable via SetCostParams so callers can adjust for specific
// workloads (e.g. all-in-memory tables where random I/O is cheap).
type CostParams struct {
	// REQ001104: cost coefficients for I/O vs CPU. seqPageCost is
	// the cost of reading one row from a sequential scan; random
	// page cost is the cost of one random row from an index.
	SeqPageCost       float64
	RandomPageCost    float64
	CPUTupleCost      float64
	CPUIndexTupleCost float64
	CPUOperatorCost   float64
}

// DefaultCostParams returns the cost-model defaults. REQ001104.
func DefaultCostParams() CostParams {
	return CostParams{
		SeqPageCost:       1.0,
		RandomPageCost:    4.0,
		CPUTupleCost:      0.01,
		CPUIndexTupleCost: 0.005,
		CPUOperatorCost:   0.0025,
	}
}

// costParams returns the planner's active cost parameters, falling
// back to defaults when not explicitly set.
func (p *Planner) costParams() CostParams {
	if p.costParamsX == nil {
		return DefaultCostParams()
	}
	return *p.costParamsX
}

// SetCostParams installs custom cost-model coefficients. REQ001104.
func (p *Planner) SetCostParams(cp CostParams) *Planner {
	p.costParamsX = &cp
	return p
}

// estimateMemoryPressure reports whether the given operator tree's
// estimated memory footprint exceeds the planner's maxMemoryPerQuery
// budget. REQ001104. Returns the estimated bytes used and the budget.
// Currently a coarse heuristic: sort + hash-build operations are
// the dominant memory consumers.
func (p *Planner) estimateMemoryPressure(op DT.Operator) (estimated int64, budget int64) {
	budget = p.maxMemoryPerQuery
	if budget <= 0 {
		budget = 64 << 20 // 64 MB default
	}
	switch v := op.(type) {
	case *OP.Sort:
		// OP.Sort materializes all rows. Estimate ~120 bytes per row
		// (covers up to ~30 columns).
		const estBytesPerRow = 120
		if rows := p.estimateRowCountFromOp(v.Child()); rows > 0 {
			estimated += int64(rows) * estBytesPerRow
		} else {
			estimated += 1 << 20 // 1 MB default when unknown
		}
	case *OP.HashJoin:
		// Hash build holds the right side. Estimate the right side's
		// materialization cost.
		if rows := p.estimateRowCountFromOp(v.RightChild()); rows > 0 {
			estimated += int64(rows) * 1200
		} else {
			estimated += 1 << 20
		}
	case *AG.Aggregate:
		// HashAggregate holds distinct group keys. Estimate as
		// 256 bytes per group.
		if rows := p.estimateRowCountFromOp(v.Child()); rows > 0 {
			estimated += int64(rows) * 256
		} else {
			estimated += 1 << 20
		}
	}
	// Recurse into children. Use type-assertion helpers that the
	// planner's operators already expose (LeftChild/RightChild and
	// Child) — this avoids referencing OP types directly.
	if cp, ok := op.(interface {
		LeftChild() DT.Operator
		RightChild() DT.Operator
	}); ok {
		if l := cp.LeftChild(); l != nil {
			subE, _ := p.estimateMemoryPressure(l)
			estimated += subE
		}
		if r := cp.RightChild(); r != nil {
			subE, _ := p.estimateMemoryPressure(r)
			estimated += subE
		}
	} else if fl, ok := op.(interface{ Child() DT.Operator }); ok {
		if c := fl.Child(); c != nil {
			subE, _ := p.estimateMemoryPressure(c)
			estimated += subE
		}
	}
	return estimated, budget
}

// estimateRowCountFromOp walks an op tree to find the underlying
// table reference and returns the planner's row-count estimate for
// that table. Returns 0 when no reference is found.
func (p *Planner) estimateRowCountFromOp(op DT.Operator) float64 {
	if op == nil {
		return 0
	}
	switch v := op.(type) {
	case *OP.SeqScan:
		return float64(p.estimateRowCount(v.Table(), nil))
	}
	if fl, ok := op.(interface{ Child() DT.Operator }); ok {
		return p.estimateRowCountFromOp(fl.Child())
	}
	if cp, ok := op.(interface {
		LeftChild() DT.Operator
		RightChild() DT.Operator
	}); ok {
		if r := p.estimateRowCountFromOp(cp.LeftChild()); r > 0 {
			return r
		}
		return p.estimateRowCountFromOp(cp.RightChild())
	}
	return 0
}

func (p *Planner) estimateCost(op DT.Operator) float64 {
	if op == nil {
		return 0
	}
	// REQ001104: when CostParams are explicitly set, use the
	// PostgreSQL-style cost formulas (rows x page cost, etc).
	// When unset, fall back to the legacy per-operator heuristic
	// (1.0 for OP.SeqScan, 0.05/0.1 for OP.IndexScan, etc.) so existing
	// tests and behavior remain stable.
	cp := p.costParams()
	var cost float64
	if p.costParamsX != nil {
		cost = p.estimateCostWithParams(op, cp)
	} else {
		cost = p.estimateCostLegacy(op)
	}
	EC.BUG_ON(math.IsNaN(cost) || cost < 0 || math.IsInf(cost, 0), "planner.estimateCost: invalid cost")
	return cost
}

// estimateCostLegacy is the original per-operator heuristic. Kept
func (p *Planner) planInsert(s *PS.Insert) DT.Operator {
	// REQ000707: INSERT INTO t SELECT ...
	if s.Select != nil {
		selPlan, selErr := p.Plan(s.Select)
		if selErr == nil && selPlan != nil && selPlan.Root != nil {
			var op *WT.Insert
			if p.store != nil {
				// REQ001129: store-backed INSERT...SELECT needs
				// a store-backed Insert operator.
				op, selErr = WT.NewInsertWithStore(p.store, s.Table, s.Cols, nil, s.Returning, s.OnConflict)
				if selErr != nil {
					return nil
				}
			} else {
				op = WT.NewInsert(s.Table, s.Cols, nil, s.Returning, s.OnConflict)
			}
			op.SetSelectPlan(selPlan.Root)
			propagatePlanner(selPlan.Root, p)
			return op
		}
	}

	var op *WT.Insert
	if p.store != nil {
		var err error
		op, err = WT.NewInsertWithStore(p.store, s.Table, s.Cols, s.Values, s.Returning, s.OnConflict)
		if err == nil {
			op.SetDefaultValues(s.DefaultValues)
			return op
		}
	}
	op = WT.NewInsert(s.Table, s.Cols, s.Values, s.Returning, s.OnConflict)
	op.SetDefaultValues(s.DefaultValues)
	return op
}

func (p *Planner) planUpdate(s *PS.Update) DT.Operator {
	if DT.LookupView(s.Table) != nil {
		return WT.NewUnsupportedOp(s, fmt.Sprintf("ex: cannot modify view %s", s.Table))
	}
	if p.store != nil {
		scan, err := OP.NewSeqScanWithStore(p.store, s.Table)
		if err == nil {
			// REQ001248: split AND, reorder by cost, build filter chain.
			conjuncts := p.splitAnd(s.Where)
			if order := CO.ReorderIndices(conjuncts); order != nil {
				conjuncts = CO.OrderSlice(conjuncts, order)
			}
			var filter DT.Operator = scan
			for _, c := range conjuncts {
				filter = OP.NewFilter(filter, c, nil)
			}
			op, err := WT.NewUpdateWithStore(p.store, s.Table, s.Set, s.Where, filter, s.Returning)
			if err == nil {
				return op
			}
		}
	}
	scan := OP.NewSeqScan(s.Table)
	// REQ001248: split AND, reorder by cost, build filter chain.
	conjuncts := p.splitAnd(s.Where)
	if order := CO.ReorderIndices(conjuncts); order != nil {
		conjuncts = CO.OrderSlice(conjuncts, order)
	}
	var filter DT.Operator = scan
	for _, c := range conjuncts {
		filter = OP.NewFilter(filter, c, nil)
	}
	return WT.NewUpdate(s.Table, s.Set, s.Where, filter, s.Returning)
}

func (p *Planner) planDelete(s *PS.Delete) DT.Operator {
	if DT.LookupView(s.Table) != nil {
		return WT.NewUnsupportedOp(s, fmt.Sprintf("ex: cannot modify view %s", s.Table))
	}
	if p.store != nil {
		scan, err := OP.NewSeqScanWithStore(p.store, s.Table)
		if err == nil {
			// REQ001248: split AND, reorder by cost, build filter chain.
			conjuncts := p.splitAnd(s.Where)
			if order := CO.ReorderIndices(conjuncts); order != nil {
				conjuncts = CO.OrderSlice(conjuncts, order)
			}
			var filter DT.Operator = scan
			for _, c := range conjuncts {
				filter = OP.NewFilter(filter, c, nil)
			}
			op, err := WT.NewDeleteWithStore(p.store, s.Table, s.Where, filter, s.Returning)
			if err == nil {
				return op
			}
		}
	}
	scan := OP.NewSeqScan(s.Table)
	// REQ001248: split AND, reorder by cost, build filter chain.
	conjuncts := p.splitAnd(s.Where)
	if order := CO.ReorderIndices(conjuncts); order != nil {
		conjuncts = CO.OrderSlice(conjuncts, order)
	}
	var filter DT.Operator = scan
	for _, c := range conjuncts {
		filter = OP.NewFilter(filter, c, nil)
	}
	return WT.NewDelete(s.Table, s.Where, filter, s.Returning)
}

func (p *Planner) planCreateTable(s *PS.CreateTable) DT.Operator {
	if s.Select != nil {
		// CREATE TABLE AS SELECT: plan the inner SELECT and
		// wrap both in a CreateTable operator. REQ000520.
		innerPlan, innerErr := p.Plan(s.Select)
		if innerErr == nil && innerPlan != nil && innerPlan.Root != nil {
			return WT.NewCreateTableAs(s, innerPlan.Root)
		}
	}
	return WT.NewCreateTable(s)
}

func (p *Planner) planDropTable(s *PS.DropTable) DT.Operator {
	return WT.NewDropTable(s)
}

func (p *Planner) planExplain(s *PS.ExplainStmt) DT.Operator {
	// Plan the inner statement
	innerPlan, err := p.Plan(s.Inner)
	if err != nil || innerPlan == nil || innerPlan.Root == nil {
		// Return a placeholder operator that will produce empty output
		return OP.NewSeqScan("__explain_error__")
	}

	// Build the PlanNode tree for structured output
	planNode := buildPlanNodeTree(innerPlan.Root, p)

	// REQ001344: add subquery plan nodes as children to the root.
	p.addSubqueryPlanNodes(planNode, s.Inner)

	// Return an ExplainStmt operator that renders the plan
	return &AD.ExplainStmtOp{
		Mode:     s.Mode,
		Format:   s.Format,
		PlanNode: planNode,
		Root:     innerPlan.Root,
	}
}
func (p *Planner) ParseAndPlan(sql string) (*pl.PlanResult, error) {
	parser := PS.NewParser(sql)
	defer parser.Close()
	defer parser.Close()
	stmt, err := parser.Parse()
	if err != nil {
		return nil, fmt.Errorf("pl: parse error: %w", err)
	}
	return p.Plan(stmt)
}

// hasWriterIndex reports whether the given index name is
// registered for writer maintenance (RegisterIndexWithID). The
// planner uses this to decide between the real-seek path and
// the prefix-scan fallback.
func hasWriterIndex(table, indexName string) bool {
	for _, idx := range DT.GetRegisteredIndexes(table) {
		if idx.Name == indexName {
			return true
		}
	}
	return false
}

// planCreateIndex registers a secondary index. iter-22.
// Note: the planner's cost-based selection (REQ000156) is
// keyed off ex.RegisterIndex, not CREATE INDEX. CREATE INDEX
// does not backfill existing rows into the index keyspace, so
// the planner cannot rely on the index for query plans
// triggered by CREATE INDEX.
func (p *Planner) planCreateIndex(s *PS.CreateIndexStmt) DT.Operator {
	return WT.NewCreateIndex(s)
}

// planDropIndex removes a secondary index. iter-22.
func (p *Planner) planDropIndex(s *PS.DropIndexStmt) DT.Operator {
	return WT.NewDropIndex(s)
}

// planPragma handles PRAGMA statements. REQ000261.
func (p *Planner) planPragma(s *PS.PragmaStmt) DT.Operator {
	switch s.Name {
	case "quick_check":
		return UT.NewQuickCheck()
	case "integrity_check":
		return UT.NewIntegrityCheckWithStore(p.store)
	case "cell_size_check":
		return UT.NewCellSizeCheckWithStore(p.store)
	case "cache_size", "journal_mode", "synchronous", "user_version":
		// REQ000242: return pragma value as a single-row result
		return OP.NewPragmaResult(s.Name, s.Value)
	case "batch_size":
		if s.Value != "" {
			// PRAGMA batch_size = N — set the batch size
			if n, err := strconv.Atoi(s.Value); err == nil {
				OP.SetEngineBatchSize(n)
			}
		}
		return OP.NewPragmaResult("batch_size", strconv.Itoa(OP.EngineBatchSize()))
	case "vectorized_mode":
		// REQ001444: sets the planner's BatchSize knob.
		// off → 1, auto → 0 (use heuristic), on → 1024, <int> → explicit.
		if s.Value != "" {
			switch s.Value {
			case "off":
				p.SetBatchSize(1)
			case "auto":
				p.SetBatchSize(0)
			case "on":
				p.SetBatchSize(1024)
			default:
				if n, err := strconv.Atoi(s.Value); err == nil {
					p.SetBatchSize(n)
				}
			}
		}
		mode := p.batchSize
		switch mode {
		case 0:
			mode = -1 // sentinel for "auto"
		}
		return OP.NewPragmaResult("vectorized_mode", strconv.Itoa(mode))
	case "foreign_keys":
		// REQ001307: toggle FK enforcement.
		if s.Value != "" {
			enabled := s.Value == "on" || s.Value == "1"
			// REQ001307: PRAGMA foreign_keys is a no-op inside a
			// transaction (SQLite behavior). Matches the check in
			// WT.Pragma.Next() at writers_admin.go:285.
			if !DT.IsInTransaction() {
				DT.SetForeignKeysEnabled(enabled)
			}
		}
		enabled := DT.IsForeignKeysEnabled()
		return OP.NewPragmaIntResult("foreign_keys", boolToInt64(enabled))
	case "foreign_key_check":
		// REQ000906: handled by the Pragma operator.
		return WT.NewPragma(s).WithStore(p.store)
	case "wal_autocheckpoint", "busy_timeout", "busy_handler", "wal_checkpoint":
		// REQ001300 / REQ001301 / REQ001302 / REQ001303: handled by Pragma operator.
		return WT.NewPragma(s).WithStore(p.store)
	case "auto_compact":
		// REQ001304: toggle LSM auto-compaction mode.
		if s.Value != "" {
			switch s.Value {
			case "none":
				DT.SetAutoCompactMode(DT.AutoCompactNone)
			case "incremental":
				DT.SetAutoCompactMode(DT.AutoCompactIncremental)
			case "full":
				DT.SetAutoCompactMode(DT.AutoCompactFull)
			default:
				// Try parsing as integer: 0=none, 1=incremental, 2=full.
				if n, err := strconv.Atoi(s.Value); err == nil {
					switch n {
					case 0:
						DT.SetAutoCompactMode(DT.AutoCompactNone)
					case 1:
						DT.SetAutoCompactMode(DT.AutoCompactIncremental)
					case 2:
						DT.SetAutoCompactMode(DT.AutoCompactFull)
					}
				}
			}
		}
		mode := DT.GetAutoCompactMode()
		var modeStr string
		switch mode {
		case DT.AutoCompactNone:
			modeStr = "none"
		case DT.AutoCompactIncremental:
			modeStr = "incremental"
		case DT.AutoCompactFull:
			modeStr = "full"
		}
		return OP.NewPragmaResult("auto_compact", modeStr)
	case "incremental_vacuum":
		// REQ001306: trigger one incremental compaction and return
		// the count of SST files merged. The actual reclamation work
		// runs in the LS background compaction loop; here we
		// kick off the pass via the store adapter and report 0
		// (async; the next sync will observe reclaimed bytes).
		return OP.NewIncrementalVacuumResult(p.store)
	case "eval_fallback_stats":
		// REQ001994: read+reset the batch→row fallback counter.
		hits := EV.ReadAndResetFallbackHits()
		return OP.NewPragmaIntResult("eval_fallback_stats", hits)
	default:
		return OP.NewSeqScan("__pragma_unknown__")
	}
}

// planAnalyze collects table statistics. REQ000258.
func (p *Planner) planAnalyze(s *PS.AnalyzeStmt) DT.Operator {
	if p.store != nil {
		op, err := UT.NewAnalyzeWithStore(p.store, s)
		if err == nil {
			return op
		}
	}
	return UT.NewAnalyze(s)
}

// planVacuum reclaims storage. REQ000257.
func (p *Planner) planVacuum(s *PS.VacuumStmt) DT.Operator {
	return UT.NewVacuumWithStore(s, p.store)
}

// planCompound dispatches a UNION/UNION ALL/INTERSECT/EXCEPT
// statement. REQ000383.
func (p *Planner) planCompound(s *PS.CompoundStmt) DT.Operator {
	left := p.planSubStmt(s.Left)
	right := p.planSubStmt(s.Right)
	cop := OP.NewCompoundOp(left, right, s.Op, s.OrderBy, s.Limit, s.Offset)
	cop.WithCollationRegistry(p.LookupCollation)
	if p.maxMemoryPerQuery > 0 {
		cop.WithMemoryLimit(p.maxMemoryPerQuery)
	}
	// REQ001437: estimate expected rows for drainAll pre-sizing.
	// For UNION ALL, estimate left + right row counts.
	// For EXCEPT/INTERSECT/UNION, use the smaller side as estimate.
	if s.Left != nil {
		if leftEst := p.estimateRowCountFromStmt(s.Left); leftEst > 0 {
			rightEst := p.estimateRowCountFromStmt(s.Right)
			switch s.Op {
			case PS.CompoundUnionAll, PS.CompoundUnion:
				cop.WithExpectedRows(leftEst + rightEst)
			case PS.CompoundExcept, PS.CompoundIntersect:
				if leftEst < rightEst {
					cop.WithExpectedRows(leftEst)
				} else {
					cop.WithExpectedRows(rightEst)
				}
			}
		}
	}
	return cop
}

// planSubStmt is a sub-dispatcher for the inner Stmt of a
// CompoundStmt. REQ000383.
func (p *Planner) planSubStmt(stmt PS.Stmt) DT.Operator {
	switch s := stmt.(type) {
	case *PS.Select:
		return p.planSelect(s)
	case *PS.CompoundStmt:
		return p.planCompound(s)
	}
	return nil
}

// isSubqueryFlattenable checks whether a subquery can accept pushed-down
// predicates from the outer query. A subquery is flattenable when it has
// no aggregation, no DISTINCT, no GROUP BY, no LIMIT, no OFFSET, and
// no ORDER BY — pushing predicates into such subqueries is semantically
// safe and reduces intermediate row counts. REQ001072.

// isConstRowPlan reports whether the operator tree is a ConstRow (possibly
// wrapped in AdaptiveOp). REQ001420: used to skip memoization for the
// COUNT(*) fast path since ConstRow is trivially cheap to create.
func isConstRowPlan(op DT.Operator) bool {
	if _, ok := op.(*OP.ConstRow); ok {
		return true
	}
	if _, ok := op.(*OP.PragmaResult); ok {
		return true
	}
	return false
}

// SetBatchSize sets the batch size for the planner. 0 means use
// the adaptive heuristic (chooseBatchSize). REQ001443.
func (p *Planner) SetBatchSize(size int) {
	p.batchSize = size
}

// BatchSize returns the current batch size setting. REQ001443.
func (p *Planner) BatchSize() int {
	return p.batchSize
}

// chooseBatchSize selects the optimal batch size for a query based on
// LIMIT clause, estimated row count, and operator tree depth.
// Returns one of {1, 64, 256, 1024}. REQ001443.
func (p *Planner) chooseBatchSize(stmt PS.Stmt, root DT.Operator) int {
	// If the user set an explicit batch size, honour it.
	if p.batchSize > 0 {
		return p.batchSize
	}

	// 1. Check LIMIT: LIMIT 0 → 1, LIMIT ≤ 64 → 64, else → 256.
	limitN := p.extractLimit(stmt)
	if limitN > 0 {
		if limitN <= 1 {
			return 1
		}
		if limitN <= 64 {
			return 64
		}
		return 256
	}

	// 2. Check estimated row count from catalog via existing helper.
	est := int64(p.estimateRowCountFromOp(root))
	if est <= 0 {
		// Unknown estimate — default to 256.
		return 256
	}
	if est <= 64 {
		return 64
	}
	if est <= 256 {
		return 256
	}
	return 1024
}

// extractLimit walks the parsed statement AST to find the LIMIT count.
// Returns -1 if no LIMIT clause is present. REQ001443.
func (p *Planner) extractLimit(stmt PS.Stmt) int64 {
	switch s := stmt.(type) {
	case *PS.Select:
		if s.Limit != nil {
			if lit, ok := s.Limit.(*PS.NumberLiteral); ok {
				return lit.Val
			}
		}
	}
	return -1
}

// estimateRowCountFromStmt estimates row count from a SELECT statement.
// REQ001437.
func (p *Planner) estimateRowCountFromStmt(stmt PS.Stmt) int64 {
	sel, ok := stmt.(*PS.Select)
	if !ok {
		return 0
	}
	if sel.From == "" {
		return 1
	}
	base := int64(p.estimateRowCount(sel.From, nil))
	if base <= 0 {
		return 0
	}
	// Apply WHERE selectivity
	if sel.Where != nil {
		base = int64(float64(base) * CO.EstimateSelectivity(sel.Where))
	}
	if base <= 0 {
		base = 1
	}
	return base
}

func boolToInt64(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
