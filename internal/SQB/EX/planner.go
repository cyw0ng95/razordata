package EX

import (
	"bytes"
	"context"
	"fmt"
	AD "github.com/cyw0ng95/razordata/internal/SQB/AD"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	"slices"
	"strings"
	"sync"
	"unicode"

	"github.com/cyw0ng95/razordata/internal/ENG/LS"
	AG "github.com/cyw0ng95/razordata/internal/SQB/AG"
	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
	OP "github.com/cyw0ng95/razordata/internal/SQB/OP"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	RE "github.com/cyw0ng95/razordata/internal/SQF/RE"
)

// cloneExpr creates a deep copy of an expression to avoid
// mutating the original AST when resolving ORDER BY position references.
func cloneExpr(e PS.Expr) PS.Expr {
	if e == nil {
		return nil
	}
	switch x := e.(type) {
	case *PS.Ident:
		return &PS.Ident{Name: x.Name}
	case *PS.QualifiedName:
		return &PS.QualifiedName{Table: x.Table, Name: x.Name}
	case *PS.NumberLiteral:
		return &PS.NumberLiteral{Val: x.Val}
	case *PS.FloatLiteral:
		return &PS.FloatLiteral{Val: x.Val}
	case *PS.StringLiteral:
		return &PS.StringLiteral{Val: x.Val}
	case *PS.BoolLiteral:
		return &PS.BoolLiteral{Val: x.Val}
	case *PS.NullLiteral:
		return &PS.NullLiteral{}
	case *PS.Param:
		return &PS.Param{Index: x.Index}
	case *PS.AliasedExpr:
		return &PS.AliasedExpr{Expr: cloneExpr(x.Expr), Alias: x.Alias}
	case *PS.BinaryExpr:
		return &PS.BinaryExpr{
			Left:  cloneExpr(x.Left),
			Op:    x.Op,
			Right: cloneExpr(x.Right),
		}
	case *PS.UnaryExpr:
		return &PS.UnaryExpr{
			Op:      x.Op,
			Operand: cloneExpr(x.Operand),
		}
	case *PS.AggregateFunc:
		return &PS.AggregateFunc{
			Name:      x.Name,
			Arg:       cloneExpr(x.Arg),
			Distinct:  x.Distinct,
			Separator: cloneExpr(x.Separator),
		}
	case *PS.CaseExpr:
		whenList := make([]PS.WhenClause, len(x.WhenList))
		for i, w := range x.WhenList {
			whenList[i] = PS.WhenClause{
				Cond: cloneExpr(w.Cond),
				Then: cloneExpr(w.Then),
			}
		}
		var elseExpr PS.Expr
		if x.Else != nil {
			elseExpr = cloneExpr(x.Else)
		}
		return &PS.CaseExpr{
			Expr:     cloneExpr(x.Expr),
			WhenList: whenList,
			Else:     elseExpr,
		}
	case *PS.SubqueryExpr:
		return &PS.SubqueryExpr{
			Subquery: x.Subquery,
		}
	default:
		// For unknown types, return as-is (shallow copy).
		// This covers most simple cases; complex expressions may need more work.
		return e
	}
}

// REQ000858: buildSelectAliasMap extracts column aliases from the SELECT list.
// For `SELECT v AS value`, it maps "value" → &PS.QualifiedName{Table: "", Name: "v"}.
func buildSelectAliasMap(cols []PS.Expr) map[string]PS.Expr {
	if len(cols) == 0 {
		return nil
	}
	m := make(map[string]PS.Expr)
	for _, c := range cols {
		if ae, ok := c.(*PS.AliasedExpr); ok && ae.Alias != "" {
			m[ae.Alias] = cloneExpr(ae.Expr)
		}
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

// REQ000858: resolveAliases walks expr and replaces any Ident matching
// an alias in the map with the aliased expression. Returns a new
// expression tree (deep copy); the original is not mutated.
func resolveAliases(expr PS.Expr, aliases map[string]PS.Expr) PS.Expr {
	if expr == nil || aliases == nil {
		return expr
	}
	switch e := expr.(type) {
	case *PS.Ident:
		if replacement, ok := aliases[e.Name]; ok {
			return cloneExpr(replacement)
		}
		return e
	case *PS.QualifiedName:
		return e
	case *PS.BinaryExpr:
		return &PS.BinaryExpr{
			Left:  resolveAliases(e.Left, aliases),
			Op:    e.Op,
			Right: resolveAliases(e.Right, aliases),
		}
	case *PS.UnaryExpr:
		return &PS.UnaryExpr{
			Op:      e.Op,
			Operand: resolveAliases(e.Operand, aliases),
		}
	case *PS.BetweenExpr:
		return &PS.BetweenExpr{
			Expr: resolveAliases(e.Expr, aliases),
			Low:  resolveAliases(e.Low, aliases),
			High: resolveAliases(e.High, aliases),
		}
	case *PS.InExpr:
		list := make([]PS.Expr, len(e.List))
		for i, item := range e.List {
			list[i] = resolveAliases(item, aliases)
		}
		return &PS.InExpr{
			Expr:     resolveAliases(e.Expr, aliases),
			List:     list,
			Subquery: e.Subquery,
		}
	case *PS.CaseExpr:
		whenList := make([]PS.WhenClause, len(e.WhenList))
		for i, w := range e.WhenList {
			whenList[i] = PS.WhenClause{
				Cond: resolveAliases(w.Cond, aliases),
				Then: resolveAliases(w.Then, aliases),
			}
		}
		var elseExpr PS.Expr
		if e.Else != nil {
			elseExpr = resolveAliases(e.Else, aliases)
		}
		return &PS.CaseExpr{
			Expr:     resolveAliases(e.Expr, aliases),
			WhenList: whenList,
			Else:     elseExpr,
		}
	case *PS.AggregateFunc:
		return &PS.AggregateFunc{
			Name:      e.Name,
			Arg:       resolveAliases(e.Arg, aliases),
			Distinct:  e.Distinct,
			Separator: resolveAliases(e.Separator, aliases),
		}
	case *PS.FunctionCall:
		args := make([]PS.Expr, len(e.Args))
		for i, a := range e.Args {
			args[i] = resolveAliases(a, aliases)
		}
		return &PS.FunctionCall{
			Name: e.Name,
			Args: args,
		}
	case *PS.NullLiteral, *PS.NumberLiteral, *PS.FloatLiteral, *PS.StringLiteral, *PS.BoolLiteral, *PS.Param:
		return e
	default:
		return e
	}
}

type plan struct {
	root    DT.Operator
	cost    float64
	memoKey string
}

// joinPlan captures a partial or complete join plan for the N3
// nearest-neighbor search. Used internally by n3JoinOrdering.
type joinPlan struct {
	root   DT.Operator
	cost   float64
	tables map[string]bool
	order  []string
}

// n3HeapMaxSize limits the number of partial plans retained at each
// step of the N3 algorithm. With N=24 and K<=8, we evaluate at most
// 192 partial plans instead of 40,320 for worst-case 8-table join.
// REQ000859: increased from 12 to 24 for better plan diversity in
// 7-8 table joins (select4 corpus).
const n3HeapMaxSize = 24

// n3PruneMultiplier is the max cost ratio retained for partial plans
// at each N3 step. MySQL's optimizer_prune_level=1 uses 1 + a small
// delta; PostgreSQL's geqo_effort uses 2.0. Higher = more candidates
// retained = more accurate plan but slower planning.
// REQ000947: candidates with cost > bestCost × n3PruneMultiplier are
// discarded. The "bestCost" is the heap minimum after each iteration.
const n3PruneMultiplier = 2.0

// HashAggregateThreshold is the row count above which the
// planner prefers HashAggregate over streaming Aggregate
// (REQ000196). HashAggregate has higher upfront cost
// (full materialization + hash table) but better performance
// for large groups. Threshold tuned for typical analytical
// workloads.
const HashAggregateThreshold = 1000

// REQ000846: max plan cache size. With 48,300 SLT queries, an
// unbounded memo would consume ~4.8GB of memory. With this bound,
// the cache holds only the most recent 1024 plans.
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
}

type tableInfo struct {
	name    string
	cols    []DT.ColInfo
	pk      string
	indexes map[string][]string
}

func NewPlanner() *Planner {
	return &Planner{
		memo:      make(map[string]*plan, maxPlanCacheSize),
		memoOrder: make([]string, maxPlanCacheSize),
		catalog:   make(map[string]*tableInfo),
	}
}

// SetPool attaches a WorkerPool to the planner for parallel operator
// execution. Nil means serial-only. REQ001044.
func (p *Planner) SetPool(pool pl.WorkerPool) { p.pool = pool }

// Pool returns the attached WorkerPool (may be nil). REQ001044.
func (p *Planner) Pool() pl.WorkerPool { return p.pool }

// SetJoinBufferSize sets the per-hash-join memory cap.
// 0 = unlimited. REQ001056.
func (p *Planner) SetJoinBufferSize(v int64) { p.joinBufferSize = v }

// SetMaxMemoryPerQuery sets the per-query memory cap.
// 0 = unlimited. REQ001057.
func (p *Planner) SetMaxMemoryPerQuery(v int64) { p.maxMemoryPerQuery = v }

// InvalidateCache clears the plan cache. REQ000846: called when DDL
// changes the schema (CREATE/DROP/ALTER TABLE) so cached plans that
// reference the old schema are not reused.
func (p *Planner) InvalidateCache() {
	p.mu.Lock()
	p.memo = make(map[string]*plan, maxPlanCacheSize)
	p.memoOrder = make([]string, maxPlanCacheSize)
	p.memoHead = 0
	p.memoSize = 0
	p.mu.Unlock()
}

// NewPlannerWithStore returns a planner that routes its leaf operators
// through store. The store may be nil to fall back to in-memory mode.
func NewPlannerWithStore(store DT.Store) *Planner {
	return &Planner{
		memo:      make(map[string]*plan),
		memoOrder: make([]string, maxPlanCacheSize),
		catalog:   make(map[string]*tableInfo),
		store:     store,
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
func (p *Planner) getTableStats(table string) *TableStats {
	p.mu.Lock()
	defer p.mu.Unlock()

	ts := &TableStats{
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

func (p *Planner) Plan(stmt PS.Stmt) (*pl.PlanResult, error) {
	rewritten, err := RE.Rewrite(stmt)
	if err != nil {
		return nil, err
	}

	key := pl.SerializeKey(rewritten)
	p.mu.Lock()
	if cached, ok := p.memo[key]; ok {
		p.mu.Unlock()
		return &pl.PlanResult{Root: cached.root, Cost: cached.cost, MemoKey: cached.memoKey}, nil
	}
	p.mu.Unlock()

	var root DT.Operator

	switch s := rewritten.(type) {
	case *PS.Select:
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

	// Wrap query plans in AdaptiveOp for hot-path specialization.
	// DDL/DML operators (Insert/Update/Delete/CreateTable/DropTable)
	// are typically one-shot and don't benefit from ADQC.
	switch root.(type) {
	case *Insert, *Update, *Delete, *CreateTable, *DropTable:
		// no adaptive wrapper for DDL/DML
	default:
		root = AD.NewAdaptiveOp(root, key)
	}

	result := &plan{
		root:    root,
		cost:    p.estimateCost(root),
		memoKey: key,
	}

	p.mu.Lock()
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
	p.mu.Unlock()

	return &pl.PlanResult{Root: root, Cost: result.cost, MemoKey: key}, nil
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
	return runSubqueryPlan(ctx, planResult, outer, params)
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
	if aop, ok := op.(*AD.AdaptiveOp); ok {
		return p.estimateRowCountFromOp(aop.Inner)
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
	// PostgreSQL-style cost formulas (rows × page cost, etc).
	// When unset, fall back to the legacy per-operator heuristic
	// (1.0 for OP.SeqScan, 0.05/0.1 for OP.IndexScan, etc.) so existing
	// tests and behavior remain stable.
	cp := p.costParams()
	if p.costParamsX != nil {
		return p.estimateCostWithParams(op, cp)
	}
	return p.estimateCostLegacy(op)
}

// estimateCostLegacy is the original per-operator heuristic. Kept
// as the default to avoid breaking existing tests that pin exact
// numeric cost values.
func (p *Planner) estimateCostLegacy(op DT.Operator) float64 {
	if op == nil {
		return 0
	}
	// Unwrap AdaptiveOp to estimate cost of the inner operator.
	if aop, ok := op.(*AD.AdaptiveOp); ok {
		return p.estimateCostLegacy(aop.Inner)
	}
	switch v := op.(type) {
	case *OP.SeqScan:
		// In v1 we don't track row counts; assume 1.0 per row.
		return 1.0
	case *OP.IndexScan:
		// REQ000156 (iter-27): the cost depends on the scan
		// mode. Real index seek (indexMode=true) is the
		// cheapest; range seek is slightly more expensive;
		// full prefix read is the most expensive of the
		// index paths but still cheaper than OP.SeqScan.
		if v.IndexMode() {
			return 0.05
		}
		return 0.1
	case *OP.BitmapHeapScan:
		// REQ001106: bitmap heap scan cost = sum of child
		// index seek costs + a single heap-fanout pass. We
		// model each child as a real seek (0.05) and add a
		// fixed bookkeeping factor so a 2-child bitmap is
		// cheaper than 2 separate IndexScans+OP.Filter stacks.
		return 0.05*float64(len(v.IndexScans())) + 0.05
	case *OP.IndexOnlyScan:
		// REQ001107: index-only scan is the cheapest path —
		// no heap fetch, just index entry emission.
		return 0.03
	case *OP.Filter:
		return p.estimateCostLegacy(v.Child()) * p.estimatePredicateSelectivity(v.Predicate())
	case *OP.Project:
		return p.estimateCostLegacy(v.Child())
	case *OP.Limit:
		return p.estimateCostLegacy(v.Child())
	case *OP.Offset:
		return p.estimateCostLegacy(v.Child())
	case *OP.Distinct:
		return p.estimateCostLegacy(v.Child())
	case *OP.Sort:
		childCost := p.estimateCostLegacy(v.Child())
		if childCost < 1 {
			childCost = 1
		}
		return childCost * (1 + log2ish(childCost))
	case *AG.Aggregate:
		return p.estimateCostLegacy(v.Child()) + 1
	case *OP.NestedLoopJoin:
		leftCost := p.estimateCostLegacy(v.LeftChild())
		rightCost := p.estimateCostLegacy(v.RightChild())
		return leftCost * rightCost
	case *OP.HashJoin:
		leftCost := p.estimateCostLegacy(v.LeftChild())
		rightCost := p.estimateCostLegacy(v.RightChild())
		if leftCost < 1 {
			leftCost = 1
		}
		if rightCost < 1 {
			rightCost = 1
		}
		return leftCost + rightCost
	case *OP.HashCrossJoin:
		leftCost := p.estimateCostLegacy(v.LeftChild())
		rightCost := p.estimateCostLegacy(v.RightChild())
		if leftCost < 1 {
			leftCost = 1
		}
		if rightCost < 1 {
			rightCost = 1
		}
		return leftCost + rightCost
	case *OP.MergeJoin:
		// REQ001102: MergeJoin is O(N+M) on pre-sorted inputs. Cost
		// is dominated by the children plus a small merge overhead.
		leftCost := p.estimateCostLegacy(v.LeftChild())
		rightCost := p.estimateCostLegacy(v.RightChild())
		if leftCost < 1 {
			leftCost = 1
		}
		if rightCost < 1 {
			rightCost = 1
		}
		return leftCost + rightCost + 1
	case *Insert, *Update, *Delete, *CreateTable, *DropTable:
		// Writer operators: cost ~ 1 (single mutation).
		return 1.0
	default:
		return 1.0
	}
}

// estimateCostWithParams applies the PostgreSQL-style cost formulas
// driven by the supplied CostParams. REQ001104: row counts are
// estimated via estimateRowCount; CPU/IO costs use the supplied
// coefficients; memory pressure is exposed via estimateMemoryPressure.
func (p *Planner) estimateCostWithParams(op DT.Operator, cp CostParams) float64 {
	if op == nil {
		return 0
	}
	if aop, ok := op.(*AD.AdaptiveOp); ok {
		return p.estimateCostWithParams(aop.Inner, cp)
	}
	switch v := op.(type) {
	case *OP.SeqScan:
		rows := p.estimateRowCount(v.Table(), nil)
		cost := float64(rows) * cp.SeqPageCost
		if cost < 1.0 {
			cost = 1.0
		}
		return cost
	case *OP.IndexScan:
		rows := p.estimateRowCount(v.Table(), nil)
		base := float64(rows) * cp.CPUIndexTupleCost
		indexIO := float64(rows) * cp.RandomPageCost / 100
		if v.IndexMode() {
			return base + indexIO
		}
		return 2*base + 2*indexIO
	case *OP.Filter:
		childCost := p.estimateCostWithParams(v.Child(), cp)
		return childCost + childCost*p.estimatePredicateSelectivity(v.Predicate())*cp.CPUOperatorCost
	case *OP.Project:
		return p.estimateCostWithParams(v.Child(), cp) + cp.CPUTupleCost
	case *OP.Limit:
		return p.estimateCostWithParams(v.Child(), cp)
	case *OP.Offset:
		return p.estimateCostWithParams(v.Child(), cp)
	case *OP.Distinct:
		return p.estimateCostWithParams(v.Child(), cp) + cp.CPUTupleCost
	case *OP.Sort:
		childCost := p.estimateCostWithParams(v.Child(), cp)
		if childCost < 1 {
			childCost = 1
		}
		return childCost*(1+log2ish(childCost)) + childCost*cp.CPUOperatorCost
	case *AG.Aggregate:
		return p.estimateCostWithParams(v.Child(), cp) + 1
	case *OP.NestedLoopJoin:
		leftCost := p.estimateCostWithParams(v.LeftChild(), cp)
		rightCost := p.estimateCostWithParams(v.RightChild(), cp)
		if leftCost < 1 {
			leftCost = 1
		}
		if rightCost < 1 {
			rightCost = 1
		}
		return leftCost * (rightCost + cp.CPUOperatorCost)
	case *OP.HashJoin:
		leftCost := p.estimateCostWithParams(v.LeftChild(), cp)
		rightCost := p.estimateCostWithParams(v.RightChild(), cp)
		if leftCost < 1 {
			leftCost = 1
		}
		if rightCost < 1 {
			rightCost = 1
		}
		return rightCost + leftCost*cp.CPUOperatorCost + rightCost*cp.CPUTupleCost
	case *OP.HashCrossJoin:
		leftCost := p.estimateCostWithParams(v.LeftChild(), cp)
		rightCost := p.estimateCostWithParams(v.RightChild(), cp)
		if leftCost < 1 {
			leftCost = 1
		}
		if rightCost < 1 {
			rightCost = 1
		}
		return leftCost + rightCost
	case *OP.MergeJoin:
		leftCost := p.estimateCostWithParams(v.LeftChild(), cp)
		rightCost := p.estimateCostWithParams(v.RightChild(), cp)
		if leftCost < 1 {
			leftCost = 1
		}
		if rightCost < 1 {
			rightCost = 1
		}
		return leftCost + rightCost + cp.CPUTupleCost
	case *Insert, *Update, *Delete, *CreateTable, *DropTable:
		return 1.0
	default:
		return 1.0
	}
}

func estimateSelectivity(e PS.Expr) float64 {
	if e == nil {
		return 1.0
	}
	if v, ok := e.(*PS.BinaryExpr); ok {
		if isColumnLiteralPair(v.Left, v.Right) || isColumnLiteralPair(v.Right, v.Left) {
			switch v.Op {
			case LX.T_EQ:
				return 0.1
			}
		}
	}
	return 0.5
}

// estimateSelectivityWithStats computes selectivity using column
// histograms when available, falling back to uniform distribution.
// REQ000085.
// The function recognizes:
//   - column = literal  → 1 / distinctCount
//   - column < literal  → bucket fraction below literal
//   - column > literal  → bucket fraction above literal
//   - column BETWEEN a AND b → bucket fraction between a and b
//   - IS NULL → nullCount / rowCount
//   - IS NOT NULL → (rowCount - nullCount) / rowCount
func estimateSelectivityWithStats(e PS.Expr, stats *ls.ColumnStats) float64 {
	if e == nil {
		return 1.0
	}

	// Binary expression: column OP literal
	if v, ok := e.(*PS.BinaryExpr); ok {
		_, lit, isColLit := extractColumnLiteral(v)
		if isColLit && stats != nil {
			switch v.Op {
			case LX.T_EQ:
				return estimateEqSelectivity(stats, lit)
			case LX.T_LT, LX.T_LE:
				return estimateRangeSelectivity(stats, nil, lit, false)
			case LX.T_GT, LX.T_GE:
				return estimateRangeSelectivity(stats, lit, nil, false)
			}
		}
		// IS NULL / IS NOT NULL handled at the operator level
		// Default for binary expressions
		return 0.5
	}

	return 0.5
}

// estimateEqSelectivity returns selectivity for column = literal.
func estimateEqSelectivity(stats *ls.ColumnStats, lit []byte) float64 {
	if stats == nil {
		return 0.1
	}
	if stats.RowCount == 0 {
		return 0.1
	}
	// Use histogram if available
	if len(stats.Histogram) > 0 {
		// Find bucket containing the literal
		matched := int64(0)
		for _, b := range stats.Histogram {
			if bytes.Compare(lit, b.LowerBound) >= 0 && bytes.Compare(lit, b.UpperBound) <= 0 {
				matched = b.Count
				break
			}
		}
		if matched > 0 {
			return float64(matched) / float64(stats.RowCount)
		}
		return 0.0
	}
	// Uniform distribution fallback
	if stats.DistinctCount > 0 {
		return 1.0 / float64(stats.DistinctCount)
	}
	return 0.1
}

// estimateRangeSelectivity returns selectivity for a range predicate
// [low, high]. If low is nil, range is (-inf, high]. If high is nil,
// range is [low, +inf).
func estimateRangeSelectivity(stats *ls.ColumnStats, low, high []byte, inclusive bool) float64 {
	if stats == nil || stats.RowCount == 0 {
		return 0.3
	}
	// No histogram: assume uniform distribution over [min, max]
	if len(stats.Histogram) == 0 {
		if stats.DistinctCount <= 1 {
			return 1.0
		}
		return 0.33
	}

	totalRows := stats.RowCount
	lowRows := int64(0)
	highRows := int64(0)

	for _, b := range stats.Histogram {
		// Count rows below `low`
		if low != nil && bytes.Compare(b.UpperBound, low) < 0 {
			lowRows += b.Count
		}
		// Count rows at or below `high`
		if high != nil && bytes.Compare(b.LowerBound, high) <= 0 {
			highRows += b.Count
		}
	}

	if low == nil {
		return float64(highRows) / float64(totalRows)
	}
	if high == nil {
		return float64(totalRows-lowRows) / float64(totalRows)
	}
	// Both bounds
	sel := float64(highRows-lowRows) / float64(totalRows)
	if sel < 0 {
		sel = 0
	}
	return sel
}

// extractColumnLiteral extracts (column, literal) from a binary
// expression of the form column OP literal.
func extractColumnLiteral(v *PS.BinaryExpr) (string, []byte, bool) {
	col, ok := v.Left.(*PS.Ident)
	if !ok {
		return "", nil, false
	}
	lit, ok := literalToBytes(v.Right)
	if !ok {
		return "", nil, false
	}
	return col.Name, lit, true
}

// literalToBytes extracts byte representation from a literal expression.
func literalToBytes(e PS.Expr) ([]byte, bool) {
	switch v := e.(type) {
	case *PS.NumberLiteral:
		return []byte(fmt.Sprintf("%d", v.Val)), true
	case *PS.StringLiteral:
		return []byte(v.Val), true
	case *PS.BoolLiteral:
		if v.Val {
			return []byte("true"), true
		}
		return []byte("false"), true
	}
	return nil, false
}

// estimatePredicateSelectivity returns the selectivity of a
// predicate, using column histograms when available. REQ000085.
func (p *Planner) estimatePredicateSelectivity(e PS.Expr) float64 {
	if e == nil {
		return 1.0
	}

	// Try to extract column name from the predicate
	col, _, isColLit := extractColumnLiteralExpr(e)
	if !isColLit {
		return 0.5
	}

	// Find table for this column by searching registered tables
	tableName := p.findTableForColumn(col)
	if tableName == "" || p.statsCatalog == nil {
		return 0.5
	}

	stats := p.statsCatalog.ColumnStatsByName(tableName, col)
	if stats == nil {
		return 0.5
	}

	histSel := estimateSelectivityWithStats(e, stats)

	lm := pl.Learned()
	if lm.IsTrained() {
		predType := 0
		if v, ok := e.(*PS.BinaryExpr); ok {
			switch v.Op {
			case LX.T_EQ:
				predType = 0
			case LX.T_LT, LX.T_LE, LX.T_GT, LX.T_GE:
				predType = 1
			}
		}
		features := lm.PredicateFeatures(
			predType,
			histSel,
			float64(stats.RowCount),
			float64(stats.DistinctCount),
			float64(stats.NullCount),
		)
		learnedSel := lm.Predict(features)
		alpha := float64(lm.TrainingCount()) / float64(lm.TrainingCount()+100)
		if alpha > 0.8 {
			alpha = 0.8
		}
		return alpha*learnedSel + (1-alpha)*histSel
	}

	return histSel
}

// findTableForColumn returns the first table name that has the
// given column registered.
func (p *Planner) findTableForColumn(col string) string {
	for tableName, t := range p.catalog {
		for _, c := range t.cols {
			if c.Name == col {
				return tableName
			}
		}
	}
	// Fallback: try SLT naming convention (e8 => t8.e).
	DT.TablesMu.RLock()
	defer DT.TablesMu.RUnlock()
	if tbl := resolveTableForColumn(col); tbl != "" {
		return tbl
	}
	return ""
}

// extractTablesFromExpr extracts all table names referenced by
// columns in the expression. Returns a set of table names.
func (p *Planner) extractTablesFromExpr(e PS.Expr) map[string]bool {
	tables := make(map[string]bool)
	if e == nil {
		return tables
	}
	p.walkExprForTables(e, tables)
	return tables
}

// walkExprForTables recursively walks the expression tree and
// collects table names for each column reference.
func (p *Planner) walkExprForTables(e PS.Expr, tables map[string]bool) {
	walkExpr(e, func(node PS.Expr) {
		switch v := node.(type) {
		case *PS.Ident:
			tbl := p.findTableForColumn(v.Name)
			if tbl == "" {
				tbl = findTableInSchemas(v.Name)
			}
			if tbl != "" {
				tables[tbl] = true
			}
		case *PS.QualifiedName:
			tables[v.Table] = true
		}
	})
}

// walkExpr is a generic expression tree walker that calls fn for each
// expression node. REQ000983: replaces three duplicate walkers.
func walkExpr(e PS.Expr, fn func(PS.Expr)) {
	if e == nil {
		return
	}
	fn(e)
	switch v := e.(type) {
	case *PS.BinaryExpr:
		walkExpr(v.Left, fn)
		walkExpr(v.Right, fn)
	case *PS.UnaryExpr:
		walkExpr(v.Operand, fn)
	case *PS.ListExpr:
		for _, item := range v.Items {
			walkExpr(item, fn)
		}
	case *PS.InExpr:
		walkExpr(v.Expr, fn)
		for _, item := range v.List {
			walkExpr(item, fn)
		}
	case *PS.BetweenExpr:
		walkExpr(v.Expr, fn)
		walkExpr(v.Low, fn)
		walkExpr(v.High, fn)
	case *PS.AggregateFunc:
		if v.Arg != nil {
			walkExpr(v.Arg, fn)
		}
	case *PS.CaseExpr:
		if v.Expr != nil {
			walkExpr(v.Expr, fn)
		}
		for _, w := range v.WhenList {
			walkExpr(w.Cond, fn)
			walkExpr(w.Then, fn)
		}
		if v.Else != nil {
			walkExpr(v.Else, fn)
		}
	case *PS.FunctionCall:
		for _, arg := range v.Args {
			walkExpr(arg, fn)
		}
	case *PS.WindowFunc:
		for _, arg := range v.Args {
			walkExpr(arg, fn)
		}
	case *PS.CastExpr:
		walkExpr(v.Expr, fn)
	case *PS.AliasedExpr:
		walkExpr(v.Expr, fn)
	case *PS.SubqueryExpr, *PS.ExistsExpr:
		// Subqueries have their own scope — don't walk.
	}
}

// canPushDown checks if a predicate can be pushed down to a
// specific table. A predicate can be pushed down if all column
// references belong to the same table.
func (p *Planner) canPushDown(e PS.Expr, table string) bool {
	tables := p.extractTablesFromExpr(e)
	// The predicate must reference exactly one table, and it
	// must be the target table
	return len(tables) == 1 && tables[table]
}

// splitAlphaNum splits "e8" into ("e", "8").
// Returns ("", "") if the string doesn't match {letters}{digits}.
func splitAlphaNum(s string) (string, string) {
	if s == "" {
		return "", ""
	}
	// Find where digits start.
	i := 0
	for i < len(s) && unicode.IsLetter(rune(s[i])) {
		i++
	}
	if i == 0 || i == len(s) {
		return "", ""
	}
	// Verify the rest is all digits.
	for j := i; j < len(s); j++ {
		if !unicode.IsDigit(rune(s[j])) {
			return "", ""
		}
	}
	return s[:i], s[i:]
}

// resolveTableForColumn tries to resolve a column name using the
// SLT naming convention: "e8" => column "e" of table "t8".
// Must be called under tablesMu.RLock.
func resolveTableForColumn(col string) string {
	base, numStr := splitAlphaNum(col)
	if base == "" {
		return ""
	}
	tbl := "t" + numStr
	cols, ok := DT.Schemas[tbl]
	if !ok {
		return ""
	}
	for _, c := range cols {
		if c == base {
			return tbl
		}
	}
	return ""
}

// findTableInSchemas searches the in-memory schemas (populated
// by CREATE TABLE) to find which table owns the given column.
// Returns empty string if not found.
func findTableInSchemas(col string) string {
	DT.TablesMu.RLock()
	defer DT.TablesMu.RUnlock()
	for tbl, cols := range DT.Schemas {
		for _, c := range cols {
			if c == col {
				return tbl
			}
		}
	}
	// Fallback: try SLT naming convention (e8 => t8.e)
	if tbl := resolveTableForColumn(col); tbl != "" {
		return tbl
	}
	return ""
}

// splitPredicatesByTable splits WHERE conjuncts into per-table
// predicates and cross-table predicates.
func (p *Planner) splitPredicatesByTable(conjuncts []PS.Expr, tables []string) (map[string][]PS.Expr, []PS.Expr) {
	perTable := make(map[string][]PS.Expr)
	for _, t := range tables {
		perTable[t] = nil
	}
	var crossTable []PS.Expr

	for _, c := range conjuncts {
		pushed := false
		for _, t := range tables {
			if p.canPushDown(c, t) {
				perTable[t] = append(perTable[t], c)
				pushed = true
				break
			}
		}
		if !pushed {
			// REQ001092: force-push single-table predicates that
			// canPushDown failed to recognize (e.g., cold-start
			// catalog). Try the naming-convention-based resolver
			// (findTableInSchemas -> resolveTableForColumn).
			if tbl := resolveSingleTablePredicate(c, tables); tbl != "" {
				perTable[tbl] = append(perTable[tbl], c)
			} else {
				crossTable = append(crossTable, c)
			}
		}
	}
	return perTable, crossTable
}

// resolveSingleTablePredicate attempts to find which single table
// a predicate references, using findTableInSchemas (which includes
// the SLT naming-convention fallback via resolveTableForColumn).
// Returns the table name if the predicate references exactly one
// table from the candidate set, or "" if it references multiple
// tables (cross-table) or cannot be resolved. REQ001092.
func resolveSingleTablePredicate(e PS.Expr, candidates []string) string {
	// Collect all column references.
	var cols []string
	walkExpr(e, func(node PS.Expr) {
		switch v := node.(type) {
		case *PS.Ident:
			if v.Name != "" {
				cols = append(cols, v.Name)
			}
		case *PS.QualifiedName:
			cols = append(cols, v.Table+"."+v.Name)
		}
	})
	if len(cols) == 0 {
		return "" // constant expression, no table to push to
	}
	// Resolve each column to a table.
	var resolvedTables []string
	for _, col := range cols {
		var tbl string
		if dotIdx := strings.IndexByte(col, '.'); dotIdx >= 0 {
			tbl = col[:dotIdx]
		} else {
			tbl = findTableInSchemas(col)
		}
		if tbl == "" {
			return "" // unresolvable column
		}
		resolvedTables = append(resolvedTables, tbl)
	}
	// All columns must resolve to the same table.
	first := resolvedTables[0]
	for _, t := range resolvedTables[1:] {
		if t != first {
			return "" // cross-table predicate
		}
	}
	// Verify the table is in the candidate set.
	for _, c := range candidates {
		if c == first {
			return first
		}
	}
	return ""
}

// extractColumnLiteralExpr is a safe variant of extractColumnLiteral
// that accepts any expression.
func extractColumnLiteralExpr(e PS.Expr) (string, []byte, bool) {
	v, ok := e.(*PS.BinaryExpr)
	if !ok {
		return "", nil, false
	}
	return extractColumnLiteral(v)
}

func isColumnLiteralPair(a, b PS.Expr) bool {
	if _, ok := a.(*PS.Ident); !ok {
		return false
	}
	switch b.(type) {
	case *PS.NumberLiteral, *PS.StringLiteral, *PS.BoolLiteral, *PS.NullLiteral:
		return true
	}
	return false
}

// tryApplyPointLookup checks if pred is a col IN (literal, ...) or
// col = literal expression and sets up point-lookup on the scan.
// REQ000820: only applies to in-memory OP.SeqScan operators.
func tryApplyPointLookup(scan DT.Operator, pred PS.Expr) {
	ss, ok := scan.(*OP.SeqScan)
	if !ok || ss.Store() != nil {
		return // only for in-memory tables
	}
	col, values, ok := extractInListValues(pred)
	if ok && len(values) > 0 {
		ss.WithPointLookup(col, values)
		return
	}
	// Single equality: col = literal
	col, val, ok := extractSingleEquality(pred)
	if ok {
		ss.WithPointLookup(col, []any{val})
	}
}

// extractInListValues extracts (columnName, values, ok) from a
// predicate of the form "col IN (val1, val2, ...)" where all values
// are literals. Only succeeds for Ident columns.
func extractInListValues(pred PS.Expr) (string, []any, bool) {
	bin, ok := pred.(*PS.BinaryExpr)
	if !ok || bin.Op != LX.T_IN {
		return "", nil, false
	}
	col, ok := bin.Left.(*PS.Ident)
	if !ok {
		return "", nil, false
	}
	list, ok := bin.Right.(*PS.ListExpr)
	if !ok || len(list.Items) == 0 {
		return "", nil, false
	}
	values := make([]any, 0, len(list.Items))
	for _, item := range list.Items {
		switch v := item.(type) {
		case *PS.NumberLiteral:
			values = append(values, v.Val)
		case *PS.StringLiteral:
			values = append(values, v.Val)
		case *PS.BoolLiteral:
			values = append(values, v.Val)
		case *PS.NullLiteral:
			// skip NULLs — NULL IN (...) is always UNKNOWN
		default:
			return "", nil, false // non-literal value, can't pre-filter
		}
	}
	return col.Name, values, true
}

// extractSingleEquality extracts (columnName, value, ok) from a
// predicate of the form "col = literal".
func extractSingleEquality(pred PS.Expr) (string, any, bool) {
	bin, ok := pred.(*PS.BinaryExpr)
	if !ok || bin.Op != LX.T_EQ {
		return "", nil, false
	}
	col, ok := bin.Left.(*PS.Ident)
	if !ok {
		col, ok = bin.Right.(*PS.Ident)
		if !ok {
			return "", nil, false
		}
		// col = literal form: right is the literal
		switch v := bin.Left.(type) {
		case *PS.NumberLiteral:
			return col.Name, v.Val, true
		case *PS.StringLiteral:
			return col.Name, v.Val, true
		case *PS.BoolLiteral:
			return col.Name, v.Val, true
		}
		return "", nil, false
	}
	// col = literal: left is the ident
	switch v := bin.Right.(type) {
	case *PS.NumberLiteral:
		return col.Name, v.Val, true
	case *PS.StringLiteral:
		return col.Name, v.Val, true
	case *PS.BoolLiteral:
		return col.Name, v.Val, true
	}
	return "", nil, false
}

// equiJoinKey checks if an expression is an equi-join condition
// between two specific tables (col1 = col2). Returns the left and
// right column names if it is, empty strings otherwise.
// Handles the case where leftTbl may be a join of multiple tables.
func (p *Planner) equiJoinKey(e PS.Expr, joinedTables map[string]bool, rightTbl string) (leftCol, rightCol string) {
	bin, ok := e.(*PS.BinaryExpr)
	if !ok || bin.Op != LX.T_EQ {
		return "", ""
	}
	leftTables := p.extractTablesFromExpr(bin.Left)
	rightTables := p.extractTablesFromExpr(bin.Right)

	// Check that right side references exactly the right table.
	if len(rightTables) == 1 && rightTables[rightTbl] {
		// Check that left side references only tables already joined.
		if allInSet(leftTables, joinedTables) {
			return colNameFromExpr(bin.Left), colNameFromExpr(bin.Right)
		}
	}
	// Reversed: left side references rightTbl, right side references joined tables.
	if len(leftTables) == 1 && leftTables[rightTbl] {
		if allInSet(rightTables, joinedTables) {
			return colNameFromExpr(bin.Right), colNameFromExpr(bin.Left)
		}
	}
	return "", ""
}

// allInSet returns true when every key in m is present in set.
func allInSet(m, set map[string]bool) bool {
	if len(m) == 0 {
		return false
	}
	for k := range m {
		if !set[k] {
			return false
		}
	}
	return true
}

// colNameFromExpr extracts a column name from an expression.
// Handles both Ident (bare column) and QualifiedName (table.col).
func colNameFromExpr(e PS.Expr) string {
	switch v := e.(type) {
	case *PS.Ident:
		return v.Name
	case *PS.QualifiedName:
		// Return fully qualified name (table.col) so the
		// OP.HashJoin lookupKeys can find the correct column
		// when multiple tables share the same column name.
		// REQ000794: multi-table equi-join fix.
		return v.Table + "." + v.Name
	}
	return ""
}

// extractEquiJoinKeys finds equi-join conditions between any table
// in the left side and the rightTbl from the cross-table conjuncts.
// This handles multi-table joins where the left side is already a join.
func (p *Planner) extractEquiJoinKeys(crossTable []PS.Expr, joinedTables map[string]bool, rightTbl string) (leftKeys, rightKeys []string, remaining []PS.Expr) {
	for _, c := range crossTable {
		lc, rc := p.equiJoinKey(c, joinedTables, rightTbl)
		if lc != "" && rc != "" {
			leftKeys = append(leftKeys, lc)
			rightKeys = append(rightKeys, rc)
		} else {
			remaining = append(remaining, c)
		}
	}
	return leftKeys, rightKeys, remaining
}

// extractSingleOnEquiKey returns (leftKey, rightKey, true) when ON
// is a simple equality of one column from leftTbl and one from
// rightTbl. Output is normalized so the first column is always
// from leftTbl and the second from rightTbl, regardless of the
// ON-clause order. Returns false for compound conditions, non-equi
// predicates, or keys from tables other than leftTbl/rightTbl.
// REQ000800.
func (p *Planner) extractSingleOnEquiKey(on PS.Expr, leftTbl, rightTbl string) (string, string, bool) {
	bin, ok := on.(*PS.BinaryExpr)
	if !ok || bin.Op != LX.T_EQ {
		return "", "", false
	}
	a, aok := bin.Left.(*PS.QualifiedName)
	b, bok := bin.Right.(*PS.QualifiedName)
	if !aok || !bok {
		return "", "", false
	}
	// Normalize: first return = column from leftTbl, second from rightTbl.
	if a.Table == leftTbl && b.Table == rightTbl {
		return a.Name, b.Name, true
	}
	if a.Table == rightTbl && b.Table == leftTbl {
		return b.Name, a.Name, true
	}
	return "", "", false
}

// collectReferencedTables returns the set of table names referenced
// in the SELECT statement. Returns nil (not empty map) when
// elimination is not safe because unqualified columns exist.
// REQ000799.
// collectReferencedColNames collects bare column names referenced in
// SELECT, WHERE, ORDER BY, GROUP BY, and HAVING. Unlike
// collectReferencedColumns, this returns bare names (not qualified)
// and works for single-table queries where columns may be unqualified.
// REQ001080.
func collectReferencedColNames(s *PS.Select) []string {
	cols := make(map[string]bool)
	addCol := func(e PS.Expr) {
		walkExpr(e, func(node PS.Expr) {
			switch v := node.(type) {
			case *PS.Ident:
				cols[v.Name] = true
			case *PS.QualifiedName:
				cols[v.Name] = true
			}
		})
	}
	for _, c := range s.Cols {
		if _, ok := c.(*PS.StarExpr); ok {
			return nil
		}
		addCol(c)
		// REQ001098: correlated subqueries in the SELECT list
		// (scalar subqueries) also reference outer table columns.
		walkSubqueryColRefs(c, cols)
	}
	if s.Where != nil {
		addCol(s.Where)
		// REQ001098: correlated subqueries in WHERE (EXISTS, IN, scalar)
		// reference outer table columns via QualifiedName.
		// walkExpr skips subqueries (they have their own scope), so the
		// outer table's columns used only inside subqueries would be
		// pruned away — the outer OP.SeqScan wouldn't read them and the
		// EvalValue fallback would resolve the QualifiedName to the
		// inner row instead. Walk subqueries explicitly to collect
		// QualifiedName references that reference outer tables.
		walkSubqueryColRefs(s.Where, cols)
	}
	for _, j := range s.Joins {
		if j.On != nil {
			addCol(j.On)
		}
	}
	for _, o := range s.OrderBy {
		addCol(o.Expr)
	}
	for _, g := range s.GroupBy {
		addCol(g)
	}
	if s.Having != nil {
		addCol(s.Having)
	}
	if len(cols) == 0 {
		return nil
	}
	result := make([]string, 0, len(cols))
	for c := range cols {
		result = append(result, c)
	}
	return result
}

// walkSubqueryColRefs walks into subqueries (EXISTS, IN, scalar) within the
// expression tree and collects QualifiedName column references. Bare idents
// inside subqueries are NOT collected — they resolve to the inner table and
// would cause unnecessary column reads. Only QualifiedName references like
// t1.b are clearly outer references that the outer scan must include.
// REQ001098.
func walkSubqueryColRefs(e PS.Expr, cols map[string]bool) {
	if e == nil {
		return
	}
	switch v := e.(type) {
	case *PS.ExistsExpr:
		if v.Subquery != nil {
			collectQualifiedFromSubquery(v.Subquery, cols)
		}
	case *PS.SubqueryExpr:
		if v.Subquery != nil {
			collectQualifiedFromSubquery(v.Subquery, cols)
		}
	case *PS.InExpr:
		if v.Subquery != nil {
			collectQualifiedFromSubquery(v.Subquery, cols)
		}
		// Also walk the target expression (e.g., col IN (subq))
		walkSubqueryColRefs(v.Expr, cols)
		for _, item := range v.List {
			walkSubqueryColRefs(item, cols)
		}
	case *PS.BinaryExpr:
		walkSubqueryColRefs(v.Left, cols)
		walkSubqueryColRefs(v.Right, cols)
	case *PS.UnaryExpr:
		walkSubqueryColRefs(v.Operand, cols)
	case *PS.CaseExpr:
		walkSubqueryColRefs(v.Expr, cols)
		for _, w := range v.WhenList {
			walkSubqueryColRefs(w.Cond, cols)
			walkSubqueryColRefs(w.Then, cols)
		}
		walkSubqueryColRefs(v.Else, cols)
	}
}

// collectQualifiedFromSubquery walks a subquery SELECT and collects all
// QualifiedName column references from its WHERE, ON, and other clauses.
// These are references to outer table columns (e.g., t1.b inside a
// correlated subquery). REQ001098.
func collectQualifiedFromSubquery(stmt PS.Stmt, cols map[string]bool) {
	sel, ok := stmt.(*PS.Select)
	if !ok {
		return
	}
	walkInto := func(e PS.Expr) {
		if e == nil {
			return
		}
		walkExpr(e, func(node PS.Expr) {
			if qn, ok := node.(*PS.QualifiedName); ok {
				cols[qn.Name] = true
			}
			// Recursively handle nested subqueries within this
			// subquery's WHERE/ON etc.
			switch v := node.(type) {
			case *PS.ExistsExpr:
				collectQualifiedFromSubquery(v.Subquery, cols)
			case *PS.SubqueryExpr:
				collectQualifiedFromSubquery(v.Subquery, cols)
			case *PS.InExpr:
				collectQualifiedFromSubquery(v.Subquery, cols)
			}
		})
	}
	if sel.Where != nil {
		walkInto(sel.Where)
	}
	for _, j := range sel.Joins {
		if j.On != nil {
			walkInto(j.On)
		}
	}
}

func collectReferencedTables(s *PS.Select) map[string]bool {
	tables := map[string]bool{s.From: true}
	hasUnqualified := false

	// Walk SELECT columns
	for _, c := range s.Cols {
		collectTablesFromExpr(c, tables, &hasUnqualified)
	}
	// WHERE
	if s.Where != nil {
		collectTablesFromExpr(s.Where, tables, &hasUnqualified)
	}
	// ORDER BY
	for _, o := range s.OrderBy {
		collectTablesFromExpr(o.Expr, tables, &hasUnqualified)
	}
	// GROUP BY
	for _, g := range s.GroupBy {
		collectTablesFromExpr(g, tables, &hasUnqualified)
	}
	// HAVING
	if s.Having != nil {
		collectTablesFromExpr(s.Having, tables, &hasUnqualified)
	}

	// REQ001076: do NOT walk JOIN ON clauses — tables that appear only in
	// join conditions can be eliminated when not referenced in SELECT/WHERE
	// /ORDER BY/GROUP BY/HAVING. The join condition is only meaningful when
	// both sides contribute columns to the output.

	// If SELECT contains *, keep all joined tables.
	for _, c := range s.Cols {
		if _, ok := c.(*PS.StarExpr); ok {
			for _, j := range s.Joins {
				tables[j.Right] = true
			}
			return tables
		}
	}

	// If we found any unqualified column, we can't prove which
	// tables are needed — don't eliminate.
	if hasUnqualified {
		return nil
	}
	return tables
}

// collectTablesFromExpr walks an expression and adds referenced
// table names. Sets hasUnqualified when a bare column name is found
// (we can't determine which table it belongs to).
func collectTablesFromExpr(e PS.Expr, tables map[string]bool, hasUnqualified *bool) {
	walkExpr(e, func(node PS.Expr) {
		switch v := node.(type) {
		case *PS.QualifiedName:
			tables[v.Table] = true
		case *PS.StarExpr, *PS.Ident:
			*hasUnqualified = true
		}
	})
}

// joinOnReferences reports whether a join's ON clause references any
// of the given table names. Used by REQ001155 to decide whether a join
// can be safely eliminated when SELECT/WHERE/ORDER/GROUP/HAVING do not
// reference the joined table. The ON clause is part of the query's
// semantic contract: if it references a table, dropping the join
// changes the result set.
func joinOnReferences(j PS.JoinClause, tableNames ...string) bool {
	if j.On == nil {
		return false
	}
	wanted := make(map[string]bool, len(tableNames))
	for _, n := range tableNames {
		if n != "" {
			wanted[n] = true
		}
	}
	if len(wanted) == 0 {
		return false
	}
	referenced := false
	walkExpr(j.On, func(node PS.Expr) {
		if qn, ok := node.(*PS.QualifiedName); ok {
			if wanted[qn.Table] {
				referenced = true
			}
		}
	})
	return referenced
}

func log2ish(x float64) float64 {
	if x <= 1 {
		return 0
	}
	n := 0.0
	for x > 1 {
		x /= 2
		n++
	}
	return n
}

// collectReferencedColumns returns the set of qualified column names
// (e.g., "t1.a", "t2.b") referenced in SELECT, WHERE, ORDER BY,
// GROUP BY, and HAVING clauses. Returns nil if unqualified columns
// or * are present (can't determine a safe projection). REQ000803.
func collectReferencedColumns(s *PS.Select) map[string]bool {
	cols := map[string]bool{}
	hasUnqualified := false

	addCols := func(e PS.Expr) {
		collectColsFromExpr(e, cols, &hasUnqualified)
	}

	for _, c := range s.Cols {
		addCols(c)
	}
	if s.Where != nil {
		addCols(s.Where)
	}
	for _, j := range s.Joins {
		if j.On != nil {
			addCols(j.On)
		}
	}
	for _, o := range s.OrderBy {
		addCols(o.Expr)
	}
	for _, g := range s.GroupBy {
		addCols(g)
	}
	if s.Having != nil {
		addCols(s.Having)
	}

	// If SELECT contains *, can't project safely.
	for _, c := range s.Cols {
		if _, ok := c.(*PS.StarExpr); ok {
			return nil
		}
	}

	if hasUnqualified {
		return nil
	}
	if len(cols) == 0 {
		return nil
	}
	return cols
}

// collectColsFromExpr walks an expression and adds qualified column
// names (Table.Name). Sets hasUnqualified on bare Ident or *.
func collectColsFromExpr(e PS.Expr, cols map[string]bool, hasUnqualified *bool) {
	walkExpr(e, func(node PS.Expr) {
		switch v := node.(type) {
		case *PS.QualifiedName:
			cols[v.Table+"."+v.Name] = true
		case *PS.Ident, *PS.StarExpr:
			*hasUnqualified = true
		}
	})
}

func (p *Planner) selectIndex(table, col string) (string, bool) {
	t, ok := p.catalog[table]
	if !ok {
		return "", false
	}
	for idxName, idxCols := range t.indexes {
		for _, c := range idxCols {
			if c == col {
				return idxName, true
			}
		}
	}
	return "", false
}

// extractViewAliases returns a set of column alias names from a
// view's SELECT columns. Used to detect when the outer query
// references view column aliases (REQ000702).
func extractViewAliases(cols []PS.Expr) map[string]bool {
	aliases := make(map[string]bool)
	for _, col := range cols {
		switch c := col.(type) {
		case *PS.AliasedExpr:
			if c.Alias != "" {
				aliases[c.Alias] = true
			}
		case *PS.Ident:
			aliases[c.Name] = true
		}
	}
	return aliases
}

// deriveJoinSchema builds the output column schema for a binary join.
// REQ001097: used at planning time to pre-compute sharedCols/
// sharedTypes/colIndex so the NLJ execution path skips the
// per-operator lazy schema build. Returns (nil, nil, nil) when
// the schema cannot be statically determined.
func deriveJoinSchema(left, right DT.Operator) ([]string, []LX.TokenType, map[string]int) {
	if left == nil || right == nil {
		return nil, nil, nil
	}
	leftCols := colsOf(left)
	rightCols := colsOf(right)
	if leftCols == nil || rightCols == nil {
		return nil, nil, nil
	}
	leftTypes := typesOf(left)
	rightTypes := typesOf(right)
	if leftTypes == nil || rightTypes == nil {
		return nil, nil, nil
	}
	if len(leftCols) != len(leftTypes) || len(rightCols) != len(rightTypes) {
		return nil, nil, nil
	}
	cols := make([]string, 0, len(leftCols)+len(rightCols))
	cols = append(cols, leftCols...)
	cols = append(cols, rightCols...)
	types := make([]LX.TokenType, 0, len(cols))
	types = append(types, leftTypes...)
	types = append(types, rightTypes...)
	idx := make(map[string]int, len(cols))
	for i, c := range cols {
		key := strings.ToLower(c)
		if _, exists := idx[key]; !exists {
			idx[key] = i
		}
	}
	return cols, types, idx
}

// colsOf extracts the column names from a known-shape operator.
// Returns nil if the schema is unknown (e.g. for valuesOp or
// computed projections).
func colsOf(op DT.Operator) []string {
	switch o := op.(type) {
	case *OP.SeqScan:
		if o.Schema() != nil {
			return o.Schema().Cols
		}
		return nil
	case *OP.IndexScan:
		if o.Schema() != nil {
			return o.Schema().Cols
		}
		return nil
	case *OP.NestedLoopJoin:
		return o.SharedCols()
	case *OP.HashJoin:
		return o.SharedCols()
	case *OP.HashCrossJoin:
		return o.SharedCols()
	case *OP.Filter:
		return colsOf(o.Child())
	case *OP.Project:
		return colsOf(o.Child())
	case *OP.Sort:
		return colsOf(o.Child())
	}
	return nil
}

// typesOf extracts the column types similarly to colsOf.
func typesOf(op DT.Operator) []LX.TokenType {
	switch o := op.(type) {
	case *OP.SeqScan:
		if o.Schema() != nil {
			return o.Schema().ColTypes
		}
		return nil
	case *OP.IndexScan:
		if o.Schema() != nil {
			return o.Schema().ColTypes
		}
		return nil
	case *OP.NestedLoopJoin:
		return o.SharedTypes()
	case *OP.HashJoin:
		return o.SharedTypes()
	case *OP.HashCrossJoin:
		return o.SharedTypes()
	case *OP.Filter:
		return typesOf(o.Child())
	case *OP.Project:
		return typesOf(o.Child())
	case *OP.Sort:
		return typesOf(o.Child())
	}
	return nil
}

func (p *Planner) planSelect(s *PS.Select) DT.Operator {
	// REQ000241: view resolution — expand view to underlying SELECT
	if viewSel := DT.LookupView(s.From); viewSel != nil {
		return p.resolveView(s, viewSel)
	}

	// REQ000357 (iter-27): SELECT without FROM clause (e.g. `SELECT 1+1`).
	// Create a OP.Values operator that evaluates expressions over a single
	// virtual row and returns exactly one result row. Also apply the WHERE
	// filter when present (REQ000458).
	//
	// REQ000830: when the SELECT contains aggregate functions (e.g.
	// `SELECT COUNT(*)`), we must use an Aggregate operator with a
	// single-row dummy source so COUNT(*) returns 1 (one implicit row)
	// instead of 0 (no rows to count).
	if s.From == "" && s.SubqueryFrom == nil {
		return p.planSelectNoFrom(s)
	}

	// REQ000709: subquery in FROM clause (derived table).
	// Plan the subquery and use its output as a virtual table.
	if s.SubqueryFrom != nil {
		return p.planSelectSubquery(s)
	}

	// REQ000727: sqlite_master virtual table
	if s.From == "sqlite_master" || s.From == "sqlite_schema" {
		return p.planSelectSqliteMaster(s)
	}

	// REQ000858: resolve column aliases in WHERE before creating filters.
	// SQLite allows SELECT aliases to be referenced in WHERE (e.g.
	// `SELECT v AS value FROM t WHERE value > 15`). Build an alias map
	// from the SELECT list and rewrite the WHERE expression.
	whereExpr := s.Where
	if aliasMap := buildSelectAliasMap(s.Cols); aliasMap != nil && s.Where != nil {
		whereExpr = resolveAliases(s.Where, aliasMap)
	}

	// REQ001074: constant folding — evaluate constant expressions at plan time
	// and simplify tautologies/contradictions.
	whereExpr = p.resolveAliasesAndFold(whereExpr)

	var scan DT.Operator
	if p.store != nil {
		scan, whereExpr = p.planSelectScan(s, whereExpr)
	}
	if scan == nil {
		scan = NewIndexOrSeqScan(s.From, whereExpr, p)
	}

	// REQ001106: bitmap heap scan for multi-index OR/AND predicates
	// REQ001107: covering-index detection (OP.IndexOnlyScan) on any scan path
	if scan != nil {
		if whereExpr != nil {
			if bitmap := p.tryBitmapHeapScan(s, whereExpr); bitmap != nil {
				scan = bitmap
			}
		}
		if _, ok := scan.(*OP.IndexScan); ok {
			if cover := p.tryIndexOnlyScan(s, whereExpr, scan); cover != nil {
				scan = cover
			}
		}
	}

	// REQ000156 (iter-27): cost-based scan selection. If the
	// planner produced a OP.SeqScan but an OP.IndexScan on the
	// predicate column would be cheaper, swap the scan.
	if whereExpr != nil && s.From != "" {
		if alt, ok := p.pickCheaperScan(s.From, whereExpr, scan); ok && alt != nil {
			scan = alt
		}
	}

	// REQ001080: column pruning — compute the set of columns actually
	// referenced by the query and pass it to the scan operator so it
	// only populates those columns. For star queries, unqualified
	// columns, or queries with window functions we skip pruning.
	if len(s.Joins) == 0 && !hasAnyWindowFunc(s.Cols) {
		if usedNames := collectReferencedColNames(s); usedNames != nil {
			if ss, ok := scan.(*OP.SeqScan); ok {
				ss.WithUsedCols(usedNames)
			}
		}
	}

	var current DT.Operator = scan

	// Set table alias on the scan operator so correlated subquery
	// eval can resolve qualified names like x.col. Must happen
	// BEFORE predicate pushdown (which wraps scan in a OP.Filter).
	if s.FromAlias != "" {
		if ss, ok := scan.(*OP.SeqScan); ok {
			ss.WithAlias(s.FromAlias)
		}
	}

	// REQ000XXX: predicate pushdown — split WHERE into per-table
	// conjuncts and push them down to individual table scans before
	// joins. This reduces intermediate row counts for cross joins.
	var pushedPredicates map[string][]PS.Expr
	var crossTablePredicates []PS.Expr
	if whereExpr != nil && (len(s.Joins) > 0 || s.From != "") {
		conjuncts := RE.SplitAnd(whereExpr)
		allTables := []string{s.From}
		for _, j := range s.Joins {
			allTables = append(allTables, j.Right)
		}
		pushedPredicates, crossTablePredicates = p.splitPredicatesByTable(conjuncts, allTables)
		// Push predicates for the first table onto its scan.
		// REQ000820: also set up point-lookup for IN-list predicates.
		if firstPreds := pushedPredicates[s.From]; len(firstPreds) > 0 {
			for _, pred := range firstPreds {
				current = OP.NewFilter(current, pred)
				tryApplyPointLookup(scan, pred)
			}
		}
	}

	// REQ000821: save the filtered scan after predicate pushdown
	// so bushy groups reuse the filter-wrapped operator.
	filteredScan := current

	// REQ000XXX: For multi-table implicit JOINs, extract equi-join
	// conditions from WHERE and use OP.HashJoin instead of OP.NestedLoopJoin.
	var crossTableConjuncts []PS.Expr
	if whereExpr != nil && len(s.Joins) > 0 {
		crossTableConjuncts = RE.SplitAnd(whereExpr)
		// REQ001077: transitive equality inference. For
		// `WHERE a = b AND b = c`, infer `a = c` so downstream
		// join planning can use any of the inferred equalities
		// as a join key.
		if inferred := p.inferTransitiveEqualities(crossTableConjuncts); len(inferred) > 0 {
			crossTableConjuncts = append(crossTableConjuncts, inferred...)
		}
	}

	// REQ000799: Join elimination — remove tables from the join
	// that are not referenced in SELECT/WHERE/ORDER BY/GROUP BY/HAVING.
	// Only eliminate when ALL columns in the query are fully qualified
	// (e.g. `t1.a`) so we can prove which tables are actually needed.
	// Unqualified columns (e.g. just `a`) prevent elimination since
	// we can't determine which table owns the column.
	extractedPreds := map[int]bool{}
	if len(s.Joins) > 0 {
		if refTables := collectReferencedTables(s); refTables != nil {
			filtered := s.Joins[:0]
			for _, j := range s.Joins {
				if refTables[j.Right] {
					filtered = append(filtered, j)
				} else if j.RightAlias != "" && refTables[j.RightAlias] {
					// REQ000835/836: when the same table is used
					// with different aliases (e.g. tab0 a, tab0 b),
					// check both the physical name and the alias.
					filtered = append(filtered, j)
				} else if joinOnReferences(j, j.Right, j.RightAlias, s.From, s.FromAlias) {
					// REQ001155: a join's ON clause may reference the
					// joined table even when SELECT/WHERE/ORDER/GROUP/
					// HAVING do not. The ON clause constrains which
					// rows survive the join, so dropping the join
					// changes the result set. Preserve the join when
					// its ON references either side (right name,
					// right alias, left name, left alias).
					filtered = append(filtered, j)
				}
			}
			s.Joins = filtered
		}
	}

	if len(s.Joins) > 0 {
		current = p.planSelectJoins(s, filteredScan, pushedPredicates, crossTableConjuncts, crossTablePredicates, extractedPreds)
	}

	if whereExpr != nil {
		// REQ000368: apply the WHERE on top of the (possibly
		// joined) operator, not on the bare scan. The previous
		// code used `NewFilter(scan, ...)` which discarded any
		// joins and produced wrong results for `FROM a JOIN b
		// WHERE ...`.
		// REQ000XXX: if predicate pushdown already applied some
		// conjuncts to scans, only apply the remaining cross-table
		// predicates here. Skip predicates already extracted by
		// OP.HashJoin (REQ000794) — they're already enforced and
		// re-applying them as Filters gives wrong results because
		// column prefixes change through the operator chain.
		if len(crossTablePredicates) > 0 {
			for i, c := range crossTablePredicates {
				if extractedPreds[i] {
					continue
				}
				current = OP.NewFilter(current, c)
			}
		} else if pushedPredicates == nil {
			// No predicate pushdown — apply full WHERE as before.
			conjuncts := RE.SplitAnd(whereExpr)
			current = OP.NewFilter(current, conjuncts[0])
			for _, c := range conjuncts[1:] {
				current = OP.NewFilter(current, c)
			}
		}
	}

	current = p.planAggregation(s, current)
	current = p.planOrdering(s, current)
	current = p.planLimitOffset(s, current)

	return current
}

// isViewMergeable returns true when the view's underlying SELECT is a
// simple single-table scan with no aggregation, DISTINCT, GROUP BY,
// ORDER BY, LIMIT, OFFSET, or HAVING, and the view's column list
// contains only simple column references (no computed expressions,
// function calls, aggregates, or *). In that case the view can be
// merged into the outer query — the outer SELECT reads directly from
// the view's underlying table, with the view's WHERE merged into the
// outer WHERE. REQ001078.
//
// Computed columns (e.g. `v * 2 AS doubled`) are NOT mergeable because
// the outer query references `doubled` which only exists as an alias
// after the view computes it — merging would expose raw columns
// instead of the alias.
func isViewMergeable(viewSel *PS.Select) bool {
	if viewSel == nil {
		return false
	}
	if viewSel.From == "" || viewSel.SubqueryFrom != nil {
		return false
	}
	if viewSel.Distinct {
		return false
	}
	if len(viewSel.GroupBy) > 0 {
		return false
	}
	if len(viewSel.OrderBy) > 0 {
		return false
	}
	if viewSel.Limit != nil || viewSel.Offset != nil {
		return false
	}
	if viewSel.Having != nil {
		return false
	}
	if hasAnyAggregate(viewSel.Cols) {
		return false
	}
	if !viewColsAreSimpleRefs(viewSel.Cols) {
		return false
	}
	return true
}

// viewColsAreSimpleRefs returns true when every entry in cols is
// either a QualifiedName, an Ident, or an AliasedExpr wrapping one
// of those. Rejects *, function calls, computed expressions, etc.
func viewColsAreSimpleRefs(cols []PS.Expr) bool {
	for _, c := range cols {
		switch v := c.(type) {
		case *PS.QualifiedName:
			continue
		case *PS.Ident:
			continue
		case *PS.AliasedExpr:
			switch v.Expr.(type) {
			case *PS.QualifiedName, *PS.Ident:
				continue
			}
			return false
		default:
			return false
		}
	}
	return true
}

// mergeViewIntoOuter produces a SELECT equivalent to (SELECT s.Cols FROM
// viewSel.From WHERE viewSel.Where AND s.Where ORDER BY ... LIMIT ...).
// Caller must verify the view is mergeable via isViewMergeable first.
// REQ001078.
func mergeViewIntoOuter(s *PS.Select, viewSel *PS.Select) *PS.Select {
	merged := *viewSel
	merged.Where = andExpr(viewSel.Where, s.Where)
	if len(s.Cols) > 0 {
		merged.Cols = s.Cols
	}
	merged.OrderBy = s.OrderBy
	merged.Limit = s.Limit
	merged.Offset = s.Offset
	merged.Distinct = s.Distinct
	merged.GroupBy = s.GroupBy
	merged.Having = s.Having
	merged.OffsetFirst = s.OffsetFirst
	merged.SubqueryFrom = nil
	return &merged
}

// resolveView handles view resolution — expand view to underlying SELECT.
// REQ000981: extracted from planSelect.
// REQ001078: when the view is a simple single-table SELECT (no agg,
// DISTINCT, etc.), merge it directly into the outer SELECT. Otherwise
// fall back to wrapping the view as a subquery.
func (p *Planner) resolveView(s *PS.Select, viewSel *PS.Select) DT.Operator {
	// REQ001078: view merging. When the view is mergeable, rewrite
	// the outer SELECT against the view's underlying table. This
	// eliminates the view indirection and lets predicate pushdown,
	// index selection, and column pruning apply to the combined
	// query.
	if isViewMergeable(viewSel) {
		return p.planSelect(mergeViewIntoOuter(s, viewSel))
	}

	// REQ000702: When the outer query references view column
	// aliases (e.g., SELECT doubled FROM v), we must wrap the
	// view as a subquery so the outer query projects over the
	// view's output columns.
	if len(s.Cols) > 0 {
		viewAliases := extractViewAliases(viewSel.Cols)
		needsWrap := false
		for _, col := range s.Cols {
			if id, ok := col.(*PS.Ident); ok {
				if viewAliases[id.Name] {
					needsWrap = true
					break
				}
			}
		}
		if needsWrap {
			// Plan the view's SELECT to get the underlying scan
			innerOp := p.planSelect(viewSel)
			// Apply the outer query's WHERE clause if present
			if s.Where != nil {
				innerOp = OP.NewFilter(innerOp, s.Where)
			}
			// OP.Project the outer query's columns over the view's output
			return OP.NewProject(innerOp, s.Cols)
		}
	}

	// Fallback: merge what we can (preserves existing behavior for
	// non-mergeable views — the view stays as the FROM target and
	// outer clauses wrap around it).
	merged := *viewSel
	if s.Where != nil {
		merged.Where = andExpr(viewSel.Where, s.Where)
	}
	if len(s.Cols) > 0 {
		merged.Cols = s.Cols
	}
	merged.OrderBy = s.OrderBy
	merged.Limit = s.Limit
	merged.Offset = s.Offset
	merged.Distinct = s.Distinct
	merged.GroupBy = s.GroupBy
	merged.Having = s.Having
	merged.OffsetFirst = s.OffsetFirst
	return p.planSelect(&merged)
}

// planSelectNoFrom handles SELECT without FROM clause (e.g. `SELECT 1+1`).
// REQ000981: extracted from planSelect.
func (p *Planner) planSelectNoFrom(s *PS.Select) DT.Operator {
	if hasAnyAggregate(s.Cols) {
		dummy := OP.NewValuesOp([]PS.Expr{&PS.NumberLiteral{Val: int64(1)}})
		var op DT.Operator = dummy
		if s.Where != nil {
			op = OP.NewFilter(op, s.Where)
		}
		agg := AG.NewAggregate(op, s.GroupBy, s.Cols)
		if s.Having != nil {
			return OP.NewFilter(agg, s.Having)
		}
		return agg
	}
	op := DT.Operator(OP.NewValuesOp(s.Cols))
	if s.Where != nil {
		op = OP.NewFilter(op, s.Where)
	}
	return op
}

// planSelectSubquery handles subquery in FROM clause (derived table).
// REQ000981: extracted from planSelect.
func (p *Planner) planSelectSubquery(s *PS.Select) DT.Operator {
	subSel, ok := s.SubqueryFrom.(*PS.Select)
	if !ok {
		return nil
	}
	// REQ001079: full subquery flattening. When the derived table
	// is a single-table SELECT with no aggregation/DISTINCT/GROUP
	// BY/ORDER BY/LIMIT/OFFSET, and the outer query references no
	// join-side columns, we can eliminate the SubqueryFrom nesting
	// by re-planning the outer SELECT directly against the inner
	// table. This reduces operator tree depth and lets predicate
	// pushdown, index selection, and column pruning apply to the
	// combined query.
	if op := p.tryFlattenSubqueryFrom(s, subSel); op != nil {
		return op
	}
	// REQ001072: predicate pushdown into subqueries. When the
	// outer WHERE references only columns from the subquery and
	// the subquery is flattenable (no aggregation/DISTINCT/GROUP
	// BY/LIMIT/OFFSET/ORDER BY), push the predicate into the
	// subquery's WHERE clause. This reduces intermediate rows.
	if s.Where != nil && isSubqueryFlattenable(subSel) {
		s.Where = pushPredicateIntoSubquery(s.Where, subSel)
	}
	subPlan := p.planSelect(subSel)
	var current DT.Operator = subPlan
	if s.Where != nil {
		current = OP.NewFilter(current, s.Where)
	}
	// REQ000859: handle aggregates in SubqueryFrom (e.g.
	// `SELECT MAX(v) FROM (SELECT v FROM t WHERE v < 30)`).
	// Without this, aggregates like MAX are evaluated per-row
	// instead of as a single-group aggregation.
	needsAggregate := hasAnyAggregate(s.Cols) || len(s.GroupBy) > 0
	if needsAggregate {
		groupCols := s.GroupBy
		aggsOnly, autoGroup, _ := splitSelectCols(s.Cols)
		aggExprs := aggsOnly
		if len(groupCols) == 0 {
			groupCols = autoGroup
		}
		agg := AG.NewAggregate(current, groupCols, aggExprs)
		current = agg
	}
	if len(s.Cols) > 0 && !isStarExpr(s.Cols) {
		if !needsAggregate {
			current = OP.NewProject(current, s.Cols)
		}
	}
	if s.Having != nil {
		current = OP.NewFilter(current, s.Having)
	}
	if len(s.OrderBy) > 0 {
		so := OP.NewSort(current, s.OrderBy)
		if p.pool != nil {
			so.WithPool(p.pool.(*UT.WorkerPool))
		}
		current = so
	}
	// OP.Limit is handled separately if needed
	return current
}

// planSelectSqliteMaster handles sqlite_master virtual table.
// REQ000981: extracted from planSelect.
func (p *Planner) planSelectSqliteMaster(s *PS.Select) DT.Operator {
	var scan DT.Operator = OP.NewSqliteMaster()
	if s.Where != nil {
		scan = OP.NewFilter(scan, s.Where)
	}
	return scan
}

// resolveAliasesAndFold resolves column aliases, folds constants, and
// eliminates common subexpressions in the WHERE clause.
// REQ000981: extracted from planSelect.
func (p *Planner) resolveAliasesAndFold(whereExpr PS.Expr) PS.Expr {
	if whereExpr == nil {
		return nil
	}
	// REQ001074: constant folding — evaluate constant expressions at plan time
	// and simplify tautologies/contradictions.
	whereExpr = foldConstants(whereExpr)
	// If folding produced a constant FALSE, the entire WHERE is a
	// contradiction — no rows will match.
	if whereExpr != nil {
		if isFalse(whereExpr) {
			whereExpr = &PS.BinaryExpr{
				Left:  &PS.NumberLiteral{Val: int64(0)},
				Op:    LX.T_EQ,
				Right: &PS.NumberLiteral{Val: int64(1)},
			}
		} else if isTrue(whereExpr) && isConstantExpr(whereExpr) {
			// REQ001074: constant TRUE tautology — remove WHERE entirely.
			whereExpr = nil
		}
	}
	// REQ001075: common subexpression elimination — remove duplicate
	// conjuncts from the WHERE clause. Only when whereExpr is still
	// non-nil and not a constant.
	if whereExpr != nil {
		whereExpr = eliminateCommonSubexpressions(whereExpr)
	}
	return whereExpr
}

// planSelectScan creates the scan operator (OP.IndexScan or OP.SeqScan) for
// the FROM table, trying index seeks first.
// REQ000981: extracted from planSelect.
func (p *Planner) planSelectScan(s *PS.Select, whereExpr PS.Expr) (DT.Operator, PS.Expr) {
	var scan DT.Operator
	remaining := whereExpr
	if p.store == nil {
		return nil, remaining
	}
	// Try OP.IndexScan first when the WHERE references an indexed column.
	// iter-22: prefer NewIndexScanWithIndex (real seek) over the
	// prefix-scan fallback when the predicate is an equality on
	// the indexed column AND the index is registered for writer
	// maintenance (i.e. the index keyspace is populated).
	if whereExpr != nil {
		// REQ001108: decompose WHERE into residual
		// (pushed into scan) and extra (outer OP.Filter).
		// Only decompose for real index seeks (EQ/range/LIKE).
		// The prefix-scan fallback (NewIndexScanWithStore) cannot
		// use decomposition because it returns ALL rows (table
		// prefix, not index prefix); the old OP.Filter behavior
		// must be preserved for that path.
		//
		// hasWriterIndex guard: the index must be registered
		// with DT.RegisteredIndexes for the writer to maintain
		// it. If the test populates the index manually (e.g.
		// idxDT.Store.Insert), hasWriterIndex returns false and
		// we use the prefix-scan fallback.
		if col, val, ok := indexedColumnEq(whereExpr); ok {
			idx, found := p.selectIndex(s.From, col)
			if found && hasWriterIndex(s.From, idx) {
				tableID, _ := DT.TableIDFor(s.From)
				if isc, err := OP.NewIndexScanWithIndex(p.store, tableID, s.From, idx, val, nil); err == nil {
					residual, extra := p.decomposeForIndexScan(whereExpr, col)
					if len(residual) > 0 {
						isc.WithResidual(residual)
					}
					if len(extra) > 0 {
						scan = OP.NewFilter(isc, rebuildAnd(extra))
						remaining = rebuildAnd(extra)
					} else {
						scan = isc
						remaining = nil
					}
				}
			}
		}
		// REQ000074 (iter-27): range seek for non-equality
		// predicates on an indexed column. Replaces the
		// prefix-scan fallback that the planner used before
		// for `col > X`, `col BETWEEN X AND Y`, etc.
		if scan == nil {
			if col, lo, loIncl, up, upIncl, ok := indexedColumnRange(whereExpr); ok {
				idx, found := p.selectIndex(s.From, col)
				if found && hasWriterIndex(s.From, idx) {
					tableID, _ := DT.TableIDFor(s.From)
					if isc, err := OP.NewIndexScanWithRange(p.store, tableID, s.From, idx, lo, loIncl, up, upIncl); err == nil {
						// REQ001108: decompose WHERE into residual
						// and extra.
						residual, extra := p.decomposeForIndexScan(whereExpr, col)
						if len(residual) > 0 {
							isc.WithResidual(residual)
						}
						if len(extra) > 0 {
							scan = OP.NewFilter(isc, rebuildAnd(extra))
							remaining = rebuildAnd(extra)
						} else {
							scan = isc
							remaining = nil
						}
					}
				}
			}
		}
		// REQ001070: LIKE prefix range seek on an indexed column.
		if scan == nil {
			if col, prefix, ok := indexedColumnLikePrefix(whereExpr); ok {
				idx, found := p.selectIndex(s.From, col)
				if found && hasWriterIndex(s.From, idx) {
					tableID, _ := DT.TableIDFor(s.From)
					upper := make([]byte, len(prefix)+1)
					copy(upper, prefix)
					upper[len(prefix)] = 0xff
					if isc, err := OP.NewIndexScanWithRange(p.store, tableID, s.From, idx, prefix, true, upper, false); err == nil {
						// REQ001108: decompose WHERE.
						residual, extra := p.decomposeForIndexScan(whereExpr, col)
						if len(residual) > 0 {
							isc.WithResidual(residual)
						}
						if len(extra) > 0 {
							scan = OP.NewFilter(isc, rebuildAnd(extra))
							remaining = rebuildAnd(extra)
						} else {
							scan = isc
							remaining = nil
						}
					}
				}
			}
		}
		if scan == nil {
			if col, ok := indexedColumn(whereExpr); ok {
				if idx, found := p.selectIndex(s.From, col); found {
					if isc, err := OP.NewIndexScanWithStore(p.store, s.From, idx); err == nil {
						// REQ001108: NewIndexScanWithStore is a table
						// prefix scan (returns ALL rows), not a real
						// index seek. Decomposition is not applicable;
						// the full WHERE must remain as OP.Filter.
						if whereExpr != nil {
							scan = OP.NewFilter(isc, whereExpr)
						} else {
							scan = isc
						}
					}
				}
			}
		}
	}
	if scan == nil {
		if ssc, err := OP.NewSeqScanWithStore(p.store, s.From); err == nil {
			scan = ssc
		}
	}
	return scan, remaining
}

// tryBitmapHeapScan combines multiple index conditions on
// distinct indexed columns into a OP.BitmapHeapScan. Returns nil if
// the predicate is not eligible (e.g. only one indexed column,
// or columns lack registered indexes). REQ001106.
//
// Detects the top-level OR-of-equality shape: at least two
// operands each carry an indexed-column equality on a distinct
// column whose index is registered. Each child becomes an
// OP.IndexScan; the result bitmap is fetched once per row via the
// heap.
func (p *Planner) tryBitmapHeapScan(s *PS.Select, whereExpr PS.Expr) DT.Operator {
	if s == nil || whereExpr == nil || p.store == nil {
		return nil
	}
	cols, lits, ok := extractOrIndexedEqColumns(whereExpr)
	if !ok || len(cols) < 2 {
		return nil
	}
	if len(cols) != len(lits) {
		return nil
	}
	children := make([]DT.Operator, 0, len(cols))
	for i, col := range cols {
		idx, found := p.selectIndex(s.From, col)
		if !found || !hasWriterIndex(s.From, idx) {
			return nil
		}
		tableID, _ := DT.TableIDFor(s.From)
		isc, err := OP.NewIndexScanWithIndex(p.store, tableID, s.From, idx, lits[i], nil)
		if err != nil {
			return nil
		}
		children = append(children, isc)
	}
	if len(children) < 2 {
		return nil
	}
	bhs := OP.NewBitmapHeapScan(s.From, p.store, children)
	if whereExpr != nil {
		return OP.NewFilter(bhs, whereExpr)
	}
	return bhs
}

// extractOrIndexedEqColumns recognises top-level OR whose
// branches are indexed-column equalities on distinct columns.
// Returns the columns and their indexed-key bytes, in order. Only
// the simplest shape — `col1 = lit1 OR col2 = lit2` (or
// OR-chains) — is recognised. Deeper expressions fall through
// to the OP.IndexScan/OP.SeqScan path.
func extractOrIndexedEqColumns(e PS.Expr) ([]string, [][]byte, bool) {
	b, ok := e.(*PS.BinaryExpr)
	if !ok || b.Op != LX.T_OR {
		// also handle top-level BinaryExpr that wraps a single AND-of-OR?
		// For Step 3b we keep scope tight: OR only.
		return nil, nil, false
	}
	branches := flattenOr(b)
	if len(branches) < 2 {
		return nil, nil, false
	}
	cols := make([]string, 0, len(branches))
	lits := make([][]byte, 0, len(branches))
	seen := make(map[string]struct{}, len(branches))
	for _, br := range branches {
		col, lit, ok := indexedColumnEq(br)
		if !ok {
			return nil, nil, false
		}
		if _, dup := seen[col]; dup {
			// Same column twice → simple OP.IndexScan path is enough;
			// bitmap doesn't help.
			return nil, nil, false
		}
		seen[col] = struct{}{}
		cols = append(cols, col)
		lits = append(lits, lit)
	}
	return cols, lits, true
}

// flattenOr returns the leaves of a top-level chain of OR
// BinaryExprs. The leaves preserve the order they appear in the
// predicate so the bitmap ordering is stable across calls.
func flattenOr(e PS.Expr) []PS.Expr {
	var out []PS.Expr
	var walk func(PS.Expr)
	walk = func(x PS.Expr) {
		if x == nil {
			return
		}
		b, ok := x.(*PS.BinaryExpr)
		if ok && b.Op == LX.T_OR {
			walk(b.Left)
			walk(b.Right)
			return
		}
		out = append(out, x)
	}
	walk(e)
	return out
}

// tryIndexOnlyScan wraps an OP.IndexScan in OP.IndexOnlyScan when the
// projected columns are entirely covered by the index columns
// (plus optionally the primary key). Returns nil if not
// eligible. REQ001107.
//
// Conservative guard: we refuse to wrap when the projection is
// empty or `*` (i.e. SELECT 1 or SELECT *). Those cases already
// work via OP.IndexScan — wrapping them in OP.IndexOnlyScan breaks
// correlated-subquery machinery that inspects the inner scan
// type. Only concrete column projections trigger the path.
func (p *Planner) tryIndexOnlyScan(s *PS.Select, whereExpr PS.Expr, scan DT.Operator) DT.Operator {
	if s == nil || scan == nil {
		return nil
	}
	isc, ok := scan.(*OP.IndexScan)
	if !ok || isc == nil {
		return nil
	}
	pk := p.tablePK(s.From)
	idxCols, ok := p.indexColumns(s.From, isc.Idx())
	if !ok {
		return nil
	}
	projected := projectColumns(s)
	if len(projected) == 0 {
		return nil
	}
	if !OP.IsCoveringIndex(projected, idxCols, pk) {
		return nil
	}
	_ = whereExpr
	return OP.NewIndexOnlyScan(isc)
}

// projectColumns returns the projected column names from a
// Select. StarExpr maps to nil so IsCoveringIndex rejects
// covering evaluation gracefully (it returns true only for
// empty projection).
func projectColumns(s *PS.Select) []string {
	if s == nil || len(s.Cols) == 0 {
		return nil
	}
	out := make([]string, 0, len(s.Cols))
	for _, c := range s.Cols {
		switch v := c.(type) {
		case *PS.StarExpr:
			return nil // any column → not covering
		case *PS.Ident:
			out = append(out, v.Name)
		default:
			return nil
		}
	}
	return out
}

// tablePK looks up the primary key column for a registered
// table. Empty string when unknown — IsCoveringIndex treats an
// empty pk as "no pk cover" and falls back to index-column
// coverage only.
func (p *Planner) tablePK(table string) string {
	if p.catalog == nil {
		return ""
	}
	if t, ok := p.catalog[table]; ok && t != nil {
		return t.pk
	}
	return ""
}

// indexColumns returns the columns a registered index covers
// and whether the index exists.
func (p *Planner) indexColumns(table, idx string) ([]string, bool) {
	if p.catalog == nil {
		return nil, false
	}
	t, ok := p.catalog[table]
	if !ok || t == nil {
		return nil, false
	}
	cols, ok := t.indexes[idx]
	if !ok {
		return nil, false
	}
	return append([]string(nil), cols...), true
}
func propagateLimitToNLJ(op DT.Operator, n int64) {
	switch t := op.(type) {
	case *OP.NestedLoopJoin:
		t.SetLimit(n)
	case *AD.AdaptiveOp:
		propagateLimitToNLJ(t.Inner, n)
	}
	type childer interface{ Child() DT.Operator }
	if c, ok := op.(childer); ok {
		propagateLimitToNLJ(c.Child(), n)
	}
	type leftRighter interface {
		LeftChild() DT.Operator
		RightChild() DT.Operator
	}
	if lr, ok := op.(leftRighter); ok {
		propagateLimitToNLJ(lr.LeftChild(), n)
		propagateLimitToNLJ(lr.RightChild(), n)
	}
}

// pkOrderMatches reports whether orderBy is a single ascending reference
// to the table's primary key column. When true, the planner can drop the
// OP.Sort operator and rely on the scan's natural key order.
func (p *Planner) pkOrderMatches(table string, orderBy []PS.OrderItem) bool {
	if len(orderBy) != 1 {
		return false
	}
	if orderBy[0].Desc {
		return false
	}
	ident, ok := orderBy[0].Expr.(*PS.Ident)
	if !ok {
		return false
	}
	if t, exists := p.catalog[table]; exists {
		return t.pk == ident.Name
	}
	// Fall back to the store schema if the planner catalog is unaware.
	if ss, ok := DT.SchemaFor(table); ok {
		return ss.Pk == ident.Name
	}
	return false
}

func splitSelectCols(cols []PS.Expr) (aggs, groupCols, other []PS.Expr) {
	if !hasAnyAggregate(cols) {
		return nil, nil, cols
	}
	for _, c := range cols {
		if DT.ContainsAggregate(c) {
			aggs = append(aggs, c)
			continue
		}
		if _, ok := c.(*PS.StarExpr); ok {
			continue
		}
		groupCols = append(groupCols, c)
	}
	return aggs, groupCols, nil
}

// isConstantExpr reports whether e is a constant expression (no column
// references). Used by constant folding (REQ001074).
func isConstantExpr(e PS.Expr) bool {
	if e == nil {
		return true
	}
	switch v := e.(type) {
	case *PS.NumberLiteral, *PS.FloatLiteral, *PS.StringLiteral,
		*PS.BoolLiteral, *PS.NullLiteral, *PS.Param:
		return true
	case *PS.Ident, *PS.QualifiedName:
		return false
	case *PS.UnaryExpr:
		return isConstantExpr(v.Operand)
	case *PS.BinaryExpr:
		return isConstantExpr(v.Left) && isConstantExpr(v.Right)
	case *PS.FunctionCall:
		for _, a := range v.Args {
			if !isConstantExpr(a) {
				return false
			}
		}
		return true
	case *PS.CastExpr:
		return isConstantExpr(v.Expr)
	}
	return false
}

// foldConstants simplifies constant expressions in a WHERE clause.
// It handles:
//   - `const = const` → evaluated to BoolLiteral (removes tautologies)
//   - `col + 0` → col
//   - `col * 1` → col
//   - `col - 0` → col
//   - `col / 1` → col
//
// Returns the simplified expression, or nil if the entire expression
// is a tautology (always true). REQ001074.
func foldConstants(e PS.Expr) PS.Expr {
	if e == nil {
		return nil
	}
	if b, ok := e.(*PS.BinaryExpr); ok {
		l := foldConstants(b.Left)
		r := foldConstants(b.Right)

		// col + 0 → col
		if LX.T_PLUS == b.Op && isSameColumn(l, r) && isZero(r) {
			return l
		}
		if LX.T_PLUS == b.Op && isSameColumn(l, r) && isZero(l) {
			return r
		}
		// col * 1 → col
		if LX.T_STAR == b.Op && isSameColumn(l, r) && isOne(r) {
			return l
		}
		if LX.T_STAR == b.Op && isSameColumn(l, r) && isOne(l) {
			return r
		}
		// col - 0 → col
		if LX.T_MINUS == b.Op && isSameColumn(l, r) && isZero(r) {
			return l
		}
		// col / 1 → col
		if LX.T_SLASH == b.Op && isSameColumn(l, r) && isOne(r) {
			return l
		}

		// If both sides are constant, eval at plan time.
		if isConstantExpr(l) && isConstantExpr(r) {
			v, err := EV.EvalValue(&PS.BinaryExpr{Left: l, Right: r, Op: b.Op}, nil, nil)
			if err == nil {
				return valueToLiteral(v)
			}
		}

		// Short-circuit: const AND FALSE → FALSE, const OR TRUE → TRUE
		if b.Op == LX.T_AND {
			// FALSE AND anything → FALSE
			if isFalse(l) || isFalse(r) {
				return &PS.BoolLiteral{Val: false}
			}
			// TRUE AND x → x
			if isTrue(l) && isConstantExpr(l) {
				return r
			}
			if isTrue(r) && isConstantExpr(r) {
				return l
			}
		}
		if b.Op == LX.T_OR {
			// TRUE OR anything → TRUE
			if isTrue(l) || isTrue(r) {
				return &PS.BoolLiteral{Val: true}
			}
			// FALSE OR x → x
			if isFalse(l) && isConstantExpr(l) {
				return r
			}
			if isFalse(r) && isConstantExpr(r) {
				return l
			}
		}

		// Rebuild with folded children.
		if l != b.Left || r != b.Right {
			cp := *b
			cp.Left = l
			cp.Right = r
			return &cp
		}
	}
	return e
}

// isSameColumn checks if l and r are the same column reference.
func isSameColumn(l, r PS.Expr) bool {
	if lid, ok := l.(*PS.Ident); ok {
		if rid, ok := r.(*PS.Ident); ok {
			return lid.Name == rid.Name
		}
	}
	if lq, ok := l.(*PS.QualifiedName); ok {
		if rq, ok := r.(*PS.QualifiedName); ok {
			return lq.Table == rq.Table && lq.Name == rq.Name
		}
	}
	return false
}

// isZero checks if an expression is the numeric literal 0.
func isZero(e PS.Expr) bool {
	if n, ok := e.(*PS.NumberLiteral); ok {
		return n.Val == 0
	}
	return false
}

// isOne checks if an expression is the numeric literal 1.
func isOne(e PS.Expr) bool {
	if n, ok := e.(*PS.NumberLiteral); ok {
		return n.Val == 1
	}
	return false
}

// isTrue checks if an expression is the boolean literal TRUE.
func isTrue(e PS.Expr) bool {
	if b, ok := e.(*PS.BoolLiteral); ok {
		return b.Val
	}
	// 1 can also be truthy in comparisons
	if n, ok := e.(*PS.NumberLiteral); ok {
		return n.Val != 0
	}
	return false
}

// isFalse checks if an expression is the boolean literal FALSE.
func isFalse(e PS.Expr) bool {
	if b, ok := e.(*PS.BoolLiteral); ok {
		return !b.Val
	}
	if n, ok := e.(*PS.NumberLiteral); ok {
		return n.Val == 0
	}
	return false
}

// valueToLiteral converts a Value back to a literal AST node.
func valueToLiteral(v DT.Value) PS.Expr {
	switch v.Kind {
	case KindNull:
		return &PS.NullLiteral{}
	case KindInt:
		return &PS.NumberLiteral{Val: v.I64}
	case KindFloat:
		return &PS.FloatLiteral{Val: v.F64}
	case KindText:
		return &PS.StringLiteral{Val: v.S}
	case KindBool:
		return &PS.BoolLiteral{Val: v.Bo}
	}
	return nil
}

// REQ001075: Common subexpression elimination (CSE).
// exprHash returns a structural hash for an expression to detect
// identical subexpressions in the WHERE clause.
func exprHash(e PS.Expr) string {
	if e == nil {
		return ""
	}
	switch v := e.(type) {
	case *PS.Ident:
		return "id:" + v.Name
	case *PS.QualifiedName:
		return "qn:" + v.Table + "." + v.Name
	case *PS.NumberLiteral:
		return fmt.Sprintf("num:%d", v.Val)
	case *PS.FloatLiteral:
		return fmt.Sprintf("flt:%g", v.Val)
	case *PS.StringLiteral:
		return "str:" + v.Val
	case *PS.BoolLiteral:
		return fmt.Sprintf("bool:%v", v.Val)
	case *PS.NullLiteral:
		return "null"
	case *PS.UnaryExpr:
		return fmt.Sprintf("un:%d:%s", v.Op, exprHash(v.Operand))
	case *PS.BinaryExpr:
		return fmt.Sprintf("bin:%d:%s:%s", v.Op, exprHash(v.Left), exprHash(v.Right))
	case *PS.FunctionCall:
		h := "fn:" + v.Name
		for _, a := range v.Args {
			h += ":" + exprHash(a)
		}
		return h
	case *PS.CastExpr:
		return fmt.Sprintf("cast:%d:%s", v.Type.Type, exprHash(v.Expr))
	case *PS.InExpr:
		// REQ0011XX: hash must include the target column and the
		// full list — without these, two distinct IN-list predicates
		// (e.g. `b4 IN (532,...)` and `d9 IN (808,...)`) hash to the
		// same value, causing eliminateCommonSubexpressions to
		// incorrectly drop one. That destroys cross-join predicate
		// pushdown and turns 5-table SELECTs into 10^10-row
		// Cartesian products that OOM the OP.HashJoin dataBuf.
		h := "in:" + exprHash(v.Expr) + ":["
		for _, it := range v.List {
			h += exprHash(it) + ","
		}
		return h + "]"
	case *PS.BetweenExpr:
		return fmt.Sprintf("btw:%s:%s:%s", exprHash(v.Expr), exprHash(v.Low), exprHash(v.High))
	case *PS.ListExpr:
		h := "list:["
		for _, it := range v.Items {
			h += exprHash(it) + ","
		}
		return h + "]"
	case *PS.AliasedExpr:
		return fmt.Sprintf("alias:%s:%s", v.Alias, exprHash(v.Expr))
	case *PS.AggregateFunc:
		return fmt.Sprintf("agg:%s:%s:%t", v.Name, exprHash(v.Arg), v.Distinct)
	case *PS.WindowFunc:
		return fmt.Sprintf("win:%s", v.Name)
	case *PS.CaseExpr:
		h := "case:" + exprHash(v.Expr) + ":["
		for _, w := range v.WhenList {
			h += "(" + exprHash(w.Cond) + "->" + exprHash(w.Then) + "),"
		}
		if v.Else != nil {
			h += "else=" + exprHash(v.Else)
		}
		return h + "]"
	case *PS.SubqueryExpr, *PS.ExistsExpr:
		// Subqueries have their own scope — hash by a stable tag so
		// they are not deduplicated against each other, but also do
		// not collapse to "%T" which would treat all subqueries as
		// identical.
		return fmt.Sprintf("subq:%T:%p", v, v)
	}
	return fmt.Sprintf("%T", e)
}

// eliminateCommonSubexpressions detects identical expressions in the
// WHERE clause and folds them so each is only evaluated once. For
// expressions that appear multiple times, the first occurrence is kept
// and subsequent occurrences reference the result of the first. REQ001075.
// For the MVP, this removes duplicate conjuncts from AND-connected clauses.
func eliminateCommonSubexpressions(where PS.Expr) PS.Expr {
	if where == nil {
		return nil
	}
	conjuncts := RE.SplitAnd(where)
	if len(conjuncts) <= 1 {
		return where
	}
	seen := make(map[string]bool, len(conjuncts))
	unique := make([]PS.Expr, 0, len(conjuncts))
	for _, c := range conjuncts {
		h := exprHash(c)
		if !seen[h] {
			seen[h] = true
			unique = append(unique, c)
		}
		// Skip duplicate — identical expression already present.
	}
	if len(unique) == 0 {
		return nil
	}
	if len(unique) == 1 {
		return unique[0]
	}
	result := unique[0]
	for _, u := range unique[1:] {
		result = &PS.BinaryExpr{
			Left:  result,
			Op:    LX.T_AND,
			Right: u,
		}
	}
	return result
}

// hasAnyAggregate checks if any column in the select list contains
// an aggregate function.
func hasAnyAggregate(cols []PS.Expr) bool {
	for _, c := range cols {
		if DT.ContainsAggregate(c) {
			return true
		}
	}
	return false
}

func hasAnyWindowFunc(cols []PS.Expr) bool {
	for _, c := range cols {
		if DT.ContainsWindowFunc(c) {
			return true
		}
	}
	return false
}

func isStarExpr(cols []PS.Expr) bool {
	if len(cols) != 1 {
		return false
	}
	_, ok := cols[0].(*PS.StarExpr)
	return ok
}

// NewIndexOrSeqScan picks an OP.IndexScan when the WHERE
// references a single column with an index on it;
// otherwise falls back to a OP.SeqScan. OP.IndexScan currently
// behaves like a OP.SeqScan for the in-memory source; the
// selection is the planner decision and the smoke test
// asserts which operator was chosen.
// ParallelThreshold is the minimum number of estimated table rows
// before the planner emits a ParallelSeqScan instead of OP.SeqScan.
// REQ001043.
const ParallelThreshold = 10000

func NewIndexOrSeqScan(table string, where PS.Expr, p *Planner) DT.Operator {
	// REQ001043: emit ParallelSeqScan when pool is available and
	// the in-memory table has enough rows.
	if p != nil && p.pool != nil {
		DT.TablesMu.RLock()
		src := DT.Tables[table]
		rowCount := len(src)
		DT.TablesMu.RUnlock()
		if rowCount >= ParallelThreshold {
			// Build schema from planner catalog (available at plan time)
			ti := p.catalog[table]
			if ti != nil && len(ti.cols) > 0 {
				schema := make([]string, len(ti.cols))
				types := make([]LX.TokenType, len(ti.cols))
				for k, ci := range ti.cols {
					schema[k] = ci.Name
					types[k] = LX.TokenType(ci.Typ)
				}
				if ss := OP.NewParallelSeqScanRow(src, schema, types, p.pool.(*UT.WorkerPool)); ss != nil {
					return ss
				}
			}
		}
		// REQ001051: detect IN-list on any column with > 10 values
		// and fan out filtered scans across workers.
		if where != nil {
			if colName, inValues, ok := extractInListValues(where); ok && len(inValues) >= 10 {
				ti := p.catalog[table]
				if ti != nil && len(ti.cols) > 0 && rowCount > 0 {
					schema := make([]string, len(ti.cols))
					types := make([]LX.TokenType, len(ti.cols))
					for k, ci := range ti.cols {
						schema[k] = ci.Name
						types[k] = LX.TokenType(ci.Typ)
					}
					return OP.NewParallelIndexRangeScan(src, schema, types, colName, inValues, p.pool.(*UT.WorkerPool))
				}
			}
		}
	}
	if p != nil && where != nil {
		if col, ok := indexedColumn(where); ok {
			if idx, found := p.selectIndex(table, col); found {
				if p.store != nil {
					if isc, err := OP.NewIndexScanWithStore(p.store, table, idx); err == nil {
						return isc
					}
				}
				return OP.NewIndexScan(table, idx, nil, nil)
			}
		}
		// REQ001068: check for equality predicate (col = ?) on an indexed column.
		if col, seekValue, ok := indexedColumnEq(where); ok {
			if idx, found := p.selectIndex(table, col); found {
				if hasWriterIndex(table, idx) {
					if p.store != nil {
						if tableID, ok := DT.TableIDFor(table); ok {
							if isc, err := OP.NewIndexScanWithIndex(p.store, tableID, table, idx, seekValue, nil); err == nil {
								return isc
							}
						}
					}
					return OP.NewIndexScan(table, idx, seekValue, nil)
				}
			}
		}
		// REQ001069: check for range predicate (col > ? / col < ? / BETWEEN) on an indexed column.
		if col, lower, lowerIncl, upper, upperIncl, ok := indexedColumnRange(where); ok {
			if idx, found := p.selectIndex(table, col); found {
				if hasWriterIndex(table, idx) {
					if p.store != nil {
						if tableID, ok := DT.TableIDFor(table); ok {
							if isc, err := OP.NewIndexScanWithRange(p.store, tableID, table, idx, lower, lowerIncl, upper, upperIncl); err == nil {
								return isc
							}
						}
					}
					return OP.NewIndexScan(table, idx, lower, upper)
				}
			}
		}
		// REQ001070: check for LIKE with constant prefix on an indexed column.
		// Uses OP.IndexScan with range [prefix, prefix+0xff) to seek to matching
		// entries, then the OP.Filter on top applies the full LIKE match.
		if col, prefix, ok := indexedColumnLikePrefix(where); ok {
			if idx, found := p.selectIndex(table, col); found {
				if hasWriterIndex(table, idx) {
					// Upper bound: prefix + 0xff (highest char) for prefix match.
					upper := make([]byte, len(prefix)+1)
					copy(upper, prefix)
					upper[len(prefix)] = 0xff
					if p.store != nil {
						if tableID, ok := DT.TableIDFor(table); ok {
							if isc, err := OP.NewIndexScanWithRange(p.store, tableID, table, idx, prefix, true, upper, false); err == nil {
								return isc
							}
						}
					}
					return OP.NewIndexScan(table, idx, prefix, upper)
				}
			}
		}
	}
	return OP.NewSeqScan(table)
}

// pickCheaperScan returns a cheaper scan alternative for the
// given WHERE predicate, if one exists. The function builds
// both a OP.SeqScan and an OP.IndexScan candidate and returns the
// lower-cost one. REQ000156 (iter-27).
// The cost model is simple but effective:
//   - OP.SeqScan: 1.0 unit per row
//   - OP.IndexScan: 0.1 unit per row, multiplied by predicate
//     selectivity (so a high-selectivity predicate on an
//     indexed column strongly prefers OP.IndexScan)
//
// If no index exists on the WHERE column, the function
// returns the original scan unchanged. If the cost of the
// index scan is not lower, the original scan is returned.
func (p *Planner) pickCheaperScan(table string, where PS.Expr, current DT.Operator) (DT.Operator, bool) {
	// REQ001106/107: don't downgrade a bitmap/index-only scan
	// back to a plain OP.IndexScan via the cost model — the new
	// operators are explicit planner choices, not cost fallback.
	if _, isBitmap := current.(*OP.BitmapHeapScan); isBitmap {
		return current, false
	}
	if _, isCover := current.(*OP.IndexOnlyScan); isCover {
		return current, false
	}
	// REQ000156 (iter-27): cost-based scan selection. The
	// function looks at the WHERE predicate to discover the
	// indexed column. We accept both simple equality
	// (`BinaryExpr col = lit`) and range predicates
	// (`BinaryExpr col > lit` / `BetweenExpr`).
	col, _ := indexedColumnOrRange(where)
	if col == "" {
		return current, false
	}
	idx, found := p.selectIndex(table, col)
	if !found {
		return current, false
	}
	// REQ000156 (iter-27): only swap to the index path if the
	// index is also registered for writer maintenance. This
	// avoids picking an OP.IndexScan whose keyspace has not been
	// backfilled (the existing planSelect code already uses
	// hasWriterIndex for the same reason).
	if !hasWriterIndex(table, idx) {
		return current, false
	}
	// Build a candidate OP.IndexScan.
	var indexScan DT.Operator
	if p.store != nil {
		if isc, err := OP.NewIndexScanWithStore(p.store, table, idx); err == nil {
			indexScan = isc
		}
	}
	if indexScan == nil {
		indexScan = OP.NewIndexScan(table, idx, nil, nil)
	}
	if indexScan == nil {
		return current, false
	}
	// Wrap both scans in a OP.Filter so the cost reflects the
	// post-filter work, matching how they will actually run.
	seqCandidate := OP.NewFilter(current, where)
	idxCandidate := OP.NewFilter(indexScan, where)
	seqCost := p.estimateCost(seqCandidate)
	idxCost := p.estimateCost(idxCandidate)
	if idxCost < seqCost {
		return indexScan, true
	}
	return current, false
}

// indexedColumnOrRange returns the indexed column name from a
// WHERE predicate, accepting both equality/range binary
// expressions and BETWEEN expressions. Returns "" if the
// predicate is not column-bounded.
func indexedColumnOrRange(e PS.Expr) (string, bool) {
	if e == nil {
		return "", false
	}
	if col, ok := indexedColumn(e); ok {
		return col, true
	}
	if col, _, _, _, _, ok := indexedColumnRange(e); ok {
		return col, true
	}
	return "", false
}

func indexedColumn(e PS.Expr) (string, bool) {
	switch v := e.(type) {
	case *PS.BinaryExpr:
		if l, ok := v.Left.(*PS.Ident); ok {
			return l.Name, true
		}
		if r, ok := v.Right.(*PS.Ident); ok {
			return r.Name, true
		}
		return indexedColumn(v.Left)
	}
	return "", false
}

// indexedColumnEq returns (columnName, encodedValue, true) if
// `e` is an equality comparison between an identifier and a
// literal (e.g. `col = 5` or `col = 'x'`). The encodedValue is
// the index key bytes (int64 big-endian for integers, raw
// string for strings).
// iter-22: used by the planner to enable real index seek via
// NewIndexScanWithIndex. REQ000252.
func indexedColumnEq(e PS.Expr) (string, []byte, bool) {
	b, ok := e.(*PS.BinaryExpr)
	if !ok {
		return "", nil, false
	}
	if b.Op != LX.T_EQ {
		return "", nil, false
	}
	// Pattern: Ident = Literal
	if l, ok := b.Left.(*PS.Ident); ok {
		if v, ok := encodeIndexValue(b.Right); ok {
			return l.Name, v, true
		}
	}
	// Pattern: Literal = Ident
	if r, ok := b.Right.(*PS.Ident); ok {
		if v, ok := encodeIndexValue(b.Left); ok {
			return r.Name, v, true
		}
	}
	return "", nil, false
}

// indexedColumnRange returns (columnName, lower, lowerInclusive,
// upper, upperInclusive, true) if `e` is a comparison on a single
// column with one or two literal bounds. Recognized shapes:
//
//	col > X   (lower exclusive, no upper)
//	col >= X  (lower inclusive, no upper)
//	col < X   (no lower, upper exclusive)
//	col <= X  (no lower, upper inclusive)
//	col BETWEEN X AND Y
//	  (lower inclusive, upper inclusive; both literals)
//
// REQ000074 (iter-27): used by the planner to enable real range
// seek via NewIndexScanWithRange. Replaces the prefix-scan
// fallback for non-equality predicates on indexed columns.
func indexedColumnRange(e PS.Expr) (string, []byte, bool, []byte, bool, bool) {
	switch v := e.(type) {
	case *PS.BetweenExpr:
		col, ok := v.Expr.(*PS.Ident)
		if !ok {
			return "", nil, false, nil, false, false
		}
		low, ok := encodeIndexValue(v.Low)
		if !ok {
			return "", nil, false, nil, false, false
		}
		high, ok := encodeIndexValue(v.High)
		if !ok {
			return "", nil, false, nil, false, false
		}
		return col.Name, low, true, high, true, true
	case *PS.BinaryExpr:
		// Recognize the comparison op
		lower, lowerIncl, upper, upperIncl, hasBounds, isCol := rangeBounds(v)
		if !isCol {
			return "", nil, false, nil, false, false
		}
		if !hasBounds {
			return "", nil, false, nil, false, false
		}
		colName := columnName(v)
		if colName == "" {
			return "", nil, false, nil, false, false
		}
		return colName, lower, lowerIncl, upper, upperIncl, true
	}
	return "", nil, false, nil, false, false
}

// rangeBounds pulls (lower, lowerInclusive, upper, upperInclusive)
// out of a single comparison. Returns isCol=true if a column is
// involved, and hasBounds=true if at least one bound is present.
func rangeBounds(b *PS.BinaryExpr) (lower []byte, lowerIncl bool, upper []byte, upperIncl bool, hasBounds bool, isCol bool) {
	// Pattern: Ident op Literal
	if l, ok := b.Left.(*PS.Ident); ok {
		if v, ok := encodeIndexValue(b.Right); ok {
			_ = l
			l, i, u, ii, h := rangeFromOp(b.Op, v)
			return l, i, u, ii, h, true
		}
		return nil, false, nil, false, false, true
	}
	// Pattern: Literal op Ident
	if r, ok := b.Right.(*PS.Ident); ok {
		if v, ok := encodeIndexValue(b.Left); ok {
			_ = r
			// Flip op direction
			flipped := flipOp(b.Op)
			l, i, u, ii, h := rangeFromOp(flipped, v)
			return l, i, u, ii, h, true
		}
		return nil, false, nil, false, false, true
	}
	return nil, false, nil, false, false, false
}

// rangeFromOp converts (op, literalValue) to (lower, lowerIncl,
// upper, upperIncl, hasBounds).
func rangeFromOp(op LX.TokenType, v []byte) (lower []byte, lowerIncl bool, upper []byte, upperIncl bool, hasBounds bool) {
	switch op {
	case LX.T_GT:
		return v, false, nil, false, true
	case LX.T_GE:
		return v, true, nil, false, true
	case LX.T_LT:
		return nil, false, v, false, true
	case LX.T_LE:
		return nil, false, v, true, true
	}
	return nil, false, nil, false, false
}

// flipOp mirrors a comparison: `5 < col` becomes `col > 5`.
// The token table uses distinct constants for each op, so we map
// each one explicitly.
func flipOp(op LX.TokenType) LX.TokenType {
	switch op {
	case LX.T_LT:
		return LX.T_GT
	case LX.T_LE:
		return LX.T_GE
	case LX.T_GT:
		return LX.T_LT
	case LX.T_GE:
		return LX.T_LE
	}
	return op
}

// columnName returns the column name from a comparison's column
// side, or "" if neither side is an Ident.
func columnName(b *PS.BinaryExpr) string {
	if l, ok := b.Left.(*PS.Ident); ok {
		return l.Name
	}
	if r, ok := b.Right.(*PS.Ident); ok {
		return r.Name
	}
	return ""
}

// indexedColumnLikePrefix detects LIKE expressions with a constant prefix.
// For `col LIKE 'abc%'`, returns ("col", []byte("abc"), true).
// For `col LIKE '%abc'` (wildcard first), returns ("", nil, false).
// Underscore (_) also ends the prefix since it matches any single char.
// REQ001070.
func indexedColumnLikePrefix(e PS.Expr) (string, []byte, bool) {
	if e == nil {
		return "", nil, false
	}
	b, ok := e.(*PS.BinaryExpr)
	if !ok {
		return "", nil, false
	}
	if b.Op != LX.T_LIKE && b.Op != LX.T_GLOB {
		return "", nil, false
	}
	// Column must be on the left side.
	ident, ok := b.Left.(*PS.Ident)
	if !ok {
		return "", nil, false
	}
	// Pattern must be a string literal.
	s, ok := b.Right.(*PS.StringLiteral)
	if !ok {
		return "", nil, false
	}
	prefix := extractLikePrefix(s.Val)
	if prefix == "" {
		return "", nil, false
	}
	return ident.Name, []byte(prefix), true
}

// extractLikePrefix returns the constant prefix before the first
// LIKE wildcard character (% or _). Returns "" if the pattern
// starts with a wildcard (no usable prefix).
func extractLikePrefix(pattern string) string {
	if pattern == "" {
		return ""
	}
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '%', '_':
			if i == 0 {
				return ""
			}
			return pattern[:i]
		}
	}
	// No wildcards — the entire pattern is a usable prefix.
	return pattern
}

// encodeIndexValue converts a literal expression into the byte
// form used by the index. Returns (value, true) on success.
func encodeIndexValue(e PS.Expr) ([]byte, bool) {
	switch v := e.(type) {
	case *PS.NumberLiteral:
		b := make([]byte, 8)
		u := uint64(v.Val)
		b[7] = byte(u)
		b[6] = byte(u >> 8)
		b[5] = byte(u >> 16)
		b[4] = byte(u >> 24)
		b[3] = byte(u >> 32)
		b[2] = byte(u >> 40)
		b[1] = byte(u >> 48)
		b[0] = byte(u >> 56)
		return b, true
	case *PS.StringLiteral:
		return []byte(v.Val), true
	case *PS.BoolLiteral:
		if v.Val {
			return []byte{1}, true
		}
		return []byte{0}, true
	}
	return nil, false
}

func limitInt64(e PS.Expr) (int64, bool) {
	switch v := e.(type) {
	case *PS.NumberLiteral:
		if v.Val < 0 {
			return 0, false
		}
		return v.Val, true
	case *PS.Param:
		_ = v
	}
	return 0, false
}

func (p *Planner) planInsert(s *PS.Insert) DT.Operator {
	// REQ000707: INSERT INTO t SELECT ...
	if s.Select != nil {
		selPlan, selErr := p.Plan(s.Select)
		if selErr == nil && selPlan != nil && selPlan.Root != nil {
			var op *Insert
			if p.store != nil {
				// REQ001129: store-backed INSERT...SELECT needs
				// a store-backed Insert operator.
				op, selErr = NewInsertWithStore(p.store, s.Table, s.Cols, nil, s.Returning, s.OnConflict)
				if selErr != nil {
					return nil
				}
			} else {
				op = NewInsert(s.Table, s.Cols, nil, s.Returning, s.OnConflict)
			}
			op.selectPlan = selPlan.Root
			propagatePlanner(selPlan.Root, p)
			return op
		}
	}

	var op *Insert
	if p.store != nil {
		var err error
		op, err = NewInsertWithStore(p.store, s.Table, s.Cols, s.Values, s.Returning, s.OnConflict)
		if err == nil {
			op.defaultValues = s.DefaultValues
			return op
		}
	}
	op = NewInsert(s.Table, s.Cols, s.Values, s.Returning, s.OnConflict)
	op.defaultValues = s.DefaultValues
	return op
}

func (p *Planner) planUpdate(s *PS.Update) DT.Operator {
	if DT.LookupView(s.Table) != nil {
		return NewUnsupportedOp(s, fmt.Sprintf("ex: cannot modify view %s", s.Table))
	}
	if p.store != nil {
		scan, err := OP.NewSeqScanWithStore(p.store, s.Table)
		if err == nil {
			filter := OP.NewFilter(scan, s.Where)
			op, err := NewUpdateWithStore(p.store, s.Table, s.Set, s.Where, filter, s.Returning)
			if err == nil {
				return op
			}
		}
	}
	scan := OP.NewSeqScan(s.Table)
	filter := OP.NewFilter(scan, s.Where)
	return NewUpdate(s.Table, s.Set, s.Where, filter, s.Returning)
}

func (p *Planner) planDelete(s *PS.Delete) DT.Operator {
	if DT.LookupView(s.Table) != nil {
		return NewUnsupportedOp(s, fmt.Sprintf("ex: cannot modify view %s", s.Table))
	}
	if p.store != nil {
		scan, err := OP.NewSeqScanWithStore(p.store, s.Table)
		if err == nil {
			filter := OP.NewFilter(scan, s.Where)
			op, err := NewDeleteWithStore(p.store, s.Table, s.Where, filter, s.Returning)
			if err == nil {
				return op
			}
		}
	}
	scan := OP.NewSeqScan(s.Table)
	filter := OP.NewFilter(scan, s.Where)
	return NewDelete(s.Table, s.Where, filter, s.Returning)
}

func (p *Planner) planCreateTable(s *PS.CreateTable) DT.Operator {
	if s.Select != nil {
		// CREATE TABLE AS SELECT: plan the inner SELECT and
		// wrap both in a CreateTable operator. REQ000520.
		innerPlan, innerErr := p.Plan(s.Select)
		if innerErr == nil && innerPlan != nil && innerPlan.Root != nil {
			return NewCreateTableAs(s, innerPlan.Root)
		}
	}
	return NewCreateTable(s)
}

func (p *Planner) planDropTable(s *PS.DropTable) DT.Operator {
	return NewDropTable(s)
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

	// Return an ExplainStmt operator that renders the plan
	return &ExplainStmtOp{
		mode:     s.Mode,
		format:   s.Format,
		planNode: planNode,
		root:     innerPlan.Root,
	}
}

func (p *Planner) planWith(w *PS.WithStmt) DT.Operator {
	for _, cte := range w.CTEs {
		if w.Recursive {
			if comp, ok := cte.Query.(*PS.CompoundStmt); ok {
				if recCTESubtree(comp.Right, cte.Name) {
					p.planRecursiveCTE(cte, comp)
					continue
				}
			}
		}

		ctePlan, err := p.Plan(cte.Query)
		if err != nil || ctePlan == nil || ctePlan.Root == nil {
			continue
		}

		var rows []DT.Row
		for {
			row, err := ctePlan.Root.Next(context.TODO())
			if err != nil {
				if err == DT.ErrNoRows {
					break
				}
				continue
			}
			rows = append(rows, row)
		}
		ctePlan.Root.Close()

		if len(cte.Cols) > 0 {
			for i := range rows {
				rows[i].Cols = cte.Cols
			}
		}
		DT.RegisterTable(cte.Name, rows)

		var colInfos []DT.ColInfo
		for _, c := range DT.Schemas[cte.Name] {
			colInfos = append(colInfos, DT.ColInfo{Name: c})
		}
		p.RegisterTable(cte.Name, colInfos, "")
	}

	innerPlan, err := p.Plan(w.Inner)
	if err != nil || innerPlan == nil || innerPlan.Root == nil {
		return OP.NewSeqScan("__cte_error__")
	}

	return innerPlan.Root
}

// planRecursiveCTE evaluates a recursive CTE inline. It executes the
// non-recursive arm (seed), determines canonical column names, then
// iterates the recursive arm until it produces 0 new rows. All
// accumulated rows are registered via RegisterTable for the inner
// query to consume.
func (p *Planner) planRecursiveCTE(cte *PS.CommonTableExpr, comp *PS.CompoundStmt) {
	ctx := context.TODO()

	// Plan and execute the non-recursive (seed) arm first. The seed
	// arm never references the CTE, so the CTE does not need to be
	// in the planner catalog yet.
	nonRecP, err := p.Plan(comp.Left)
	if err != nil || nonRecP == nil || nonRecP.Root == nil {
		DT.RegisterTable(cte.Name, nil)
		return
	}
	allRows := drainAllRows(ctx, nonRecP.Root)
	nonRecP.Root.Close()

	// Determine canonical column names from the CTE alias or the seed arm.
	canonicalCols := cte.Cols
	if len(canonicalCols) == 0 && len(allRows) > 0 {
		canonicalCols = allRows[0].Cols
	}
	if len(canonicalCols) > 0 {
		for i := range allRows {
			allRows[i].Cols = canonicalCols
		}
	}

	if len(allRows) == 0 {
		DT.RegisterTable(cte.Name, nil)
		return
	}

	// Register CTE in the planner catalog so the recursive arm can
	// be planned (OP.SeqScan for the CTE name needs catalog metadata).
	colInfos := make([]DT.ColInfo, len(canonicalCols))
	for i, cn := range canonicalCols {
		colInfos[i] = DT.ColInfo{Name: cn}
	}
	p.RegisterTable(cte.Name, colInfos, "")

	// Register seed rows so the inner query can see them.
	DT.RegisterTable(cte.Name, allRows)

	isUnion := comp.Op == PS.CompoundUnion
	iterRows := allRows
	compKey := pl.SerializeKey(comp.Right)

	// Safety limit: prevent infinite loops from malformed recursive CTEs.
	const maxRecIters = 10000
	for iter := 0; iter < maxRecIters; iter++ {
		// Bypass plan cache so re-planning produces a fresh operator tree.
		p.mu.Lock()
		delete(p.memo, compKey)
		p.mu.Unlock()

		// Feed only the previous iteration's rows to the recursive arm.
		DT.TablesMu.Lock()
		DT.Tables[cte.Name] = cloneRows(iterRows)
		if len(iterRows) > 0 {
			DT.Schemas[cte.Name] = iterRows[0].Cols
		}
		DT.TablesMu.Unlock()

		recP, err := p.Plan(comp.Right)
		if err != nil || recP == nil || recP.Root == nil {
			break
		}
		newRows := drainAllRows(ctx, recP.Root)
		recP.Root.Close()

		if len(newRows) == 0 {
			break
		}

		if len(canonicalCols) > 0 {
			for i := range newRows {
				newRows[i].Cols = canonicalCols
			}
		}

		if isUnion {
			newRows = dedupRecCTENewRows(newRows, allRows)
		}

		if len(newRows) == 0 {
			break
		}

		allRows = append(allRows, newRows...)
		iterRows = newRows

		DT.RegisterTable(cte.Name, allRows)
	}

	DT.RegisterTable(cte.Name, allRows)
}

// drainAllRows pulls all rows from op into a slice.
func drainAllRows(ctx context.Context, op DT.Operator) []DT.Row {
	var out []DT.Row
	for {
		row, err := op.Next(ctx)
		if err != nil {
			if err == DT.ErrNoRows {
				return out
			}
			return out
		}
		out = append(out, row)
	}
}

// cloneRows creates a deep copy of each DT.Row in the slice.
func cloneRows(rows []DT.Row) []DT.Row {
	out := make([]DT.Row, len(rows))
	for i, r := range rows {
		out[i] = DT.CloneRow(r)
	}
	return out
}

// dedupRecCTENewRows filters newRows to only those whose distinct key
// is not already present in allRows. Used for UNION (not UNION ALL)
// in recursive CTE evaluation.
func dedupRecCTENewRows(newRows, allRows []DT.Row) []DT.Row {
	seen := make(map[string]bool, len(allRows))
	for _, r := range allRows {
		seen[OP.DistinctKey(r)] = true
	}
	out := make([]DT.Row, 0, len(newRows))
	for _, r := range newRows {
		k := OP.DistinctKey(r)
		if !seen[k] {
			seen[k] = true
			out = append(out, r)
		}
	}
	return out
}

// recCTESubtree reports whether any SELECT in the Stmt subtree
// references a table with the given name. Used to detect recursive
// CTEs.
func recCTESubtree(stmt PS.Stmt, name string) bool {
	switch s := stmt.(type) {
	case *PS.Select:
		return recCTESelectRefs(s, name)
	case *PS.CompoundStmt:
		return recCTESubtree(s.Left, name) || recCTESubtree(s.Right, name)
	}
	return false
}

// recCTESelectRefs checks if a SELECT's FROM clause or any JOIN
// clause references the given table name.
func recCTESelectRefs(s *PS.Select, name string) bool {
	if strings.EqualFold(s.From, name) {
		return true
	}
	for _, j := range s.Joins {
		if strings.EqualFold(j.Right, name) {
			return true
		}
	}
	return false
}

// estimateRowCount provides a row count estimate for the given
// table+filter. Used by the planner to choose between
// streaming Aggregate and HashAggregate (REQ000196).
// In v1, this returns a conservative estimate: 0 for unknown
// tables (preferring streaming Aggregate) and 0 for store-
// backed tables. In-memory tables (via RegisterTable) have
// their row count available via package-level tables map.
// A future iteration can integrate histogram-based estimates
// (REQ000085) for more accuracy.
// REQ000787: now uses TableStats from the catalog when available.
func (p *Planner) estimateRowCount(table string, where PS.Expr) int {
	// REQ000780: return actual row count for in-memory tables.
	if rows, ok := DT.Tables[table]; ok {
		return len(rows)
	}
	// REQ000787: use statistics-driven estimate from catalog.
	if cat := DT.Catalog(); cat != nil {
		if ts := cat.TableStats(table); ts != nil && ts.RowCount > 0 {
			return int(ts.RowCount)
		}
	}
	return 100 // default estimate
}

// getTableRowCount returns the estimated number of rows in a table.
// Uses the global in-memory tables map first, then falls back to
// the statistics catalog, and finally to a default of 100.
func (p *Planner) getTableRowCount(table string) float64 {
	if rows, ok := DT.Tables[table]; ok {
		return float64(len(rows))
	}
	if cat := DT.Catalog(); cat != nil {
		if ts := cat.TableStats(table); ts != nil && ts.RowCount > 0 {
			return float64(ts.RowCount)
		}
	}
	// Check catalog for registered stats.
	tInfo, ok := p.catalog[table]
	if ok && len(tInfo.cols) > 0 {
		if p.statsCatalog != nil {
			for _, col := range tInfo.cols {
				if cs := p.statsCatalog.ColumnStatsByName(table, col.Name); cs != nil && cs.RowCount > 0 {
					return float64(cs.RowCount)
				}
			}
		}
	}
	return 100
}

// estimateJoinCost returns the estimated cost of joining two tables or
// table sets. The cost is based on the output row count of the join,
// adjusted by predicate selectivity:
//   - equi-join predicate:  selectivity = 0.1
//   - range predicate:      selectivity = 0.3
//   - other predicates:     selectivity = 0.5
//   - no predicates (CROSS JOIN): selectivity = 1.0
func (p *Planner) estimateJoinCost(leftRows, rightRows int, predicates []PS.Expr, hasIndex bool) float64 {
	indexFactor := 1.0
	if hasIndex {
		indexFactor = 0.2
	}

	if len(predicates) == 0 {
		// Cross join: full Cartesian product.
		return float64(leftRows) * float64(rightRows) * indexFactor
	}

	sel := 1.0
	for _, pred := range predicates {
		psel := p.joinPredSel(pred, 0)
		sel *= psel
	}
	cost := float64(leftRows) * float64(rightRows) * sel * indexFactor
	if cost < 1 {
		cost = 1
	}
	return cost
}

// estimateJoinPredicateSelectivity returns the selectivity of a single
// join predicate expression. rowCount is the estimated number of rows
// in the table the predicate applies to; used for IN-list selectivity
// scaling. Pass 0 to use the default NDV of 100.
func estimateJoinPredicateSelectivity(pred PS.Expr, rowCount float64) float64 {
	if pred == nil {
		return 1.0
	}
	// REQ000819: handle IN-list expressions: selectivity ≈ len(list)/rowCount.
	// When rowCount is unavailable, fall back to default NDV=100.
	if in, ok := pred.(*PS.InExpr); ok && len(in.List) > 0 {
		sel := estimateInListSelectivity(in.List, rowCount, nil, nil)
		return sel
	}
	bin, ok := pred.(*PS.BinaryExpr)
	if !ok {
		return 0.5
	}
	isColCol := isColumnColumnPair(bin.Left, bin.Right) || isColumnColumnPair(bin.Right, bin.Left)
	switch bin.Op {
	case LX.T_EQ:
		if isColCol {
			return 0.1
		}
		return 0.1
	case LX.T_LT, LX.T_LE, LX.T_GT, LX.T_GE:
		return 0.3
	default:
		return 0.5
	}
}

// REQ000948: NDV-based join predicate selectivity. Method variant of
// estimateJoinPredicateSelectivity that consults the stats catalog
// (ColumnStats.DistinctCount) for column-specific NDV values when
// available. Falls back to the package-level default constants when
// stats are missing.
//
// PostgreSQL reference (eqjoinsel): selectivity = (1 - null_frac) /
// max(ndv_left, ndv_right, 1). For range predicates, default is
// (1 - null_frac) / 3 (uniform distribution assumption).
func (p *Planner) joinPredSel(pred PS.Expr, rowCount float64) float64 {
	if pred == nil {
		return 1.0
	}
	// REQ000819: IN-list expressions. Use rowCount as NDV when available.
	// REQ001057b: when the column has MCV stats, prefer the
	// 1 - ∏(1 - pᵢ) formula over the uniform len/rowCount fallback.
	if in, ok := pred.(*PS.InExpr); ok && len(in.List) > 0 {
		var mcvs [][]byte
		var freqs []float64
		if p.statsCatalog != nil {
			// The IN-list target is the leftmost child (a column
			// reference). Resolve its (table, col) pair and look up
			// the column stats — but only when the target is a bare
			// column ref. Mixed targets (e.g. expr IN (…)) fall
			// through to the legacy formula.
			if colRef, ok := in.Expr.(*PS.Ident); ok {
				table, col := p.findTableForColumn(colRef.Name), colRef.Name
				if table != "" && col != "" {
					if cs := p.statsCatalog.ColumnStatsByName(table, col); cs != nil {
						mcvs = cs.MostCommonVals
						freqs = cs.MostCommonFreqs
					}
				}
			}
		}
		return estimateInListSelectivity(in.List, rowCount, mcvs, freqs)
	}
	bin, ok := pred.(*PS.BinaryExpr)
	if !ok {
		return 0.5
	}
	switch bin.Op {
	case LX.T_EQ:
		// REQ000948: equi-join (col = col) uses NDV of both sides.
		// REQ000948: equi-join (col = literal) uses NDV of the column.
		ndvL, ndvR := p.ndvFromExpr(bin.Left), p.ndvFromExpr(bin.Right)
		if ndvL > 0 && ndvR > 0 {
			if ndvL > ndvR {
				return 1.0 / ndvL
			}
			return 1.0 / ndvR
		}
		if ndvL > 0 {
			return 1.0 / ndvL
		}
		if ndvR > 0 {
			return 1.0 / ndvR
		}
		// REQ001095: when stats are unavailable, use the table's row
		// count as NDV proxy (col = literal hits 1/rows of the table).
		if rowCount > 0 {
			sel := 1.0 / rowCount
			if sel < 0.01 {
				sel = 0.01 // floor at 1% to avoid over-optimism
			}
			return sel
		}
		// No stats — fall back to default.
		return 0.1
	case LX.T_LT, LX.T_LE, LX.T_GT, LX.T_GE:
		// Range predicate: use (1 - null_frac) / 3 (uniform).
		nullFrac := p.nullFracFromExpr(bin.Left)
		if nullFrac < 0 {
			nullFrac = 0
		}
		return (1.0 - nullFrac) / 3.0
	default:
		return 0.5
	}
}

// estimateInListSelectivity computes the selectivity of an IN-list
// predicate using Most-Common-OP.Values stats when available.
//
// REQ001057b: matches CockroachDB / PostgreSQL semantics — when MCVs
// are known, the per-element frequency is used for matching values,
// and a uniform tail `(remaining list count) / (NDV - MCV count)`
// accounts for rare values. When MCVs are absent, the legacy
// uniform-distribution formula `min(1, len(list)/max(rowCount, 1))`
// is used.
//
// Inputs:
//   - list: the IN-list expressions (literals, parameters, etc.)
//   - rowCount: estimated number of rows in the column's table.
//     Used as the NDV denominator when no MCVs are available.
//     Pass 0 to fall back to the default NDV=100.
//   - mcvs / freqs: parallel slices of most-common values and their
//     frequencies. May be nil (legacy path).
//
// Returns selectivity in (0, 1]. A selectivity of 1.0 means the
// predicate matches everything; a value clamped to 1 means every row
// is selected.
func estimateInListSelectivity(list []PS.Expr, rowCount float64, mcvs [][]byte, freqs []float64) float64 {
	if len(list) == 0 {
		return 1.0
	}
	// MCV-aware path: for each IN-list literal, look it up in MCVs.
	// Match: pᵢ = freqs[j]. No match: contribute (1 / max(1, NDV - |MCVs|))
	// to the rare-value tail.
	if len(mcvs) > 0 && len(mcvs) == len(freqs) {
		// Build a small lookup map. Linear scan is fine for the
		// typical |MCVs| ≤ 100 and |list| ≤ 1000 budget.
		freqByVal := make(map[string]float64, len(mcvs))
		for i, v := range mcvs {
			freqByVal[string(v)] = freqs[i]
		}
		// NDV estimate: use rowCount if positive, else fall back
		// to the count of distinct MCVs plus a 1.0/NDV-tail term.
		// We approximate the rare-value uniform frequency as
		// max(0, (1 - sum(MCV freqs))) / max(1, NDV - |MCVs|).
		// NDV itself is unknown from MCVs alone, so we use
		// rowCount as the universe.
		var matched int
		var probNotMatched float64 = 1.0
		var tailCount int
		for _, item := range list {
			// REQ001057b: extract literal bytes from the IN-list
			// element. Most IN-list items are *PS.Literal or
			// *PS.IntegerLit / *PS.FloatLit / *PS.StringLit; we
			// only know the AST shape from PS.Ident (column) /
			// unknown. Treat any non-MCV match as tail.
			key, ok := inListLiteralKey(item)
			if !ok {
				tailCount++
				continue
			}
			if f, hit := freqByVal[key]; hit {
				probNotMatched *= (1.0 - f)
				matched++
			} else {
				tailCount++
			}
		}
		// Rare-value tail: uniform over (NDV - |MCVs|).
		var tailSel float64
		if tailCount > 0 {
			ndv := rowCount
			if ndv <= 0 {
				ndv = 100
			}
			rareN := ndv - float64(len(mcvs))
			if rareN < 1 {
				rareN = 1
			}
			tailSel = float64(tailCount) / rareN
			if tailSel > 1.0 {
				tailSel = 1.0
			}
		}
		sel := (1.0 - probNotMatched) + tailSel
		if sel > 1.0 {
			sel = 1.0
		}
		if sel < 0 {
			sel = 0
		}
		return sel
	}
	// Legacy path: uniform distribution.
	ndv := rowCount
	if ndv <= 0 {
		ndv = 100
	}
	sel := float64(len(list)) / ndv
	if sel > 1.0 {
		sel = 1.0
	}
	return sel
}

// inListLiteralKey returns a canonical byte representation of an
// IN-list literal for MCV lookup, plus an "ok" flag. Only literal
// expressions are supported; column references and complex
// expressions are not (treated as tail).
//
// REQ001057b: this avoids importing PS.Literal-specific types — the
// PS AST exposes a single Literal interface; concrete types are
// *PS.StringLit, *PS.IntegerLit, *PS.FloatLit, etc. We probe a small
// set of likely field names.
func inListLiteralKey(item PS.Expr) (string, bool) {
	switch v := item.(type) {
	case *PS.StringLiteral:
		return "S:" + v.Val, true
	case *PS.NumberLiteral:
		return fmt.Sprintf("I:%d", v.Val), true
	case *PS.FloatLiteral:
		return fmt.Sprintf("F:%v", v.Val), true
	case *PS.BoolLiteral:
		return fmt.Sprintf("B:%v", v.Val), true
	}
	return "", false
}

// ndvFromExpr returns the DistinctCount (NDV) of the column referenced
// by expr, or -1 if NDV is unavailable. Handles Ident and QualifiedName
// column references; returns -1 for literals, function calls, etc.
func (p *Planner) ndvFromExpr(expr PS.Expr) float64 {
	if p.statsCatalog == nil {
		return -1
	}
	table, col := p.tableColFromExpr(expr)
	if table == "" || col == "" {
		return -1
	}
	stats := p.statsCatalog.ColumnStatsByName(table, col)
	if stats == nil || stats.DistinctCount <= 0 {
		return -1
	}
	return float64(stats.DistinctCount)
}

// nullFracFromExpr returns the null fraction (NullCount/RowCount) of
// the column referenced by expr, or -1 if unavailable.
func (p *Planner) nullFracFromExpr(expr PS.Expr) float64 {
	if p.statsCatalog == nil {
		return -1
	}
	table, col := p.tableColFromExpr(expr)
	if table == "" || col == "" {
		return -1
	}
	stats := p.statsCatalog.ColumnStatsByName(table, col)
	if stats == nil || stats.RowCount <= 0 {
		return -1
	}
	return float64(stats.NullCount) / float64(stats.RowCount)
}

// tableColFromExpr extracts the (table, column) pair from a column
// reference expression. Supports PS.Ident (unqualified) and
// PS.QualifiedName (table.col). Returns ("", "") for other expr types.
func (p *Planner) tableColFromExpr(expr PS.Expr) (string, string) {
	switch e := expr.(type) {
	case *PS.Ident:
		// Unqualified column — look up via findTableForColumn.
		return p.findTableForColumn(e.Name), e.Name
	case *PS.QualifiedName:
		// table.col — explicit.
		if e.Table != "" {
			return e.Table, e.Name
		}
		if e.Name != "" {
			return "", e.Name
		}
	}
	return "", ""
}

// isColumnColumnPair returns true when both sides of the expression
// are column references (Ident or QualifiedName). Used to detect
// equi-join predicates like t1.a = t2.b.
func isColumnColumnPair(a, b PS.Expr) bool {
	_, aIsCol := a.(*PS.Ident)
	_, aIsQn := a.(*PS.QualifiedName)
	_, bIsCol := b.(*PS.Ident)
	_, bIsQn := b.(*PS.QualifiedName)
	return (aIsCol || aIsQn) && (bIsCol || bIsQn)
}

// n3JoinOrdering implements a simplified N3 (N-nearest-neighbor)
// algorithm inspired by SQLite's NGQP. It finds a near-optimal join
// order by maintaining a heap of the N=12 best partial plans and
// extending them step by step.
//
// REQ000883: table row counts are reduced by single-table predicate
// selectivity before cost estimation. Tables with highly selective
// single-table predicates (e.g., `IN (101,103)` reducing 100→2 rows)
// are joined first, minimizing intermediate result sizes.
//
// Parameters:
//   - baseTable: the FROM-clause table (leftmost in the join tree)
//   - joinTables: list of (table name, join clause) pairs to join
//   - wherePredicates: cross-table WHERE conjuncts for selectivity
//   - pushedPredicates: per-table predicates already pushed down to scans
//
// Returns the ordered list of table names that minimizes estimated cost.
// REQ000946: n3JoinOrdering now returns (order, cost) so the caller
// (n3JoinOrderingMultiStart) can compare costs across different
// starting baseTable choices. The cost is the sum of per-step
// joinCost estimates for the cheapest complete plan in the heap.
func (p *Planner) n3JoinOrdering(baseTable string, joinTables []joinTableInfo, wherePredicates []PS.Expr, pushedPredicates map[string][]PS.Expr) ([]string, float64) {
	k := len(joinTables)
	if k == 0 {
		return []string{baseTable}, p.getTableRowCount(baseTable)
	}
	if k == 1 {
		return []string{baseTable, joinTables[0].name}, p.getTableRowCount(baseTable) + p.getTableRowCount(joinTables[0].name)
	}

	// REQ000883/REQ000909: pre-compute per-table selectivity from
	// single-table WHERE predicates (both pushed-down and cross-table
	// that reference a single table). This allows the N3 algorithm to
	// prefer joining tables first that have highly selective filters.
	tableSelectivity := make(map[string]float64)
	for _, jt := range joinTables {
		sel := 1.0
		rowCnt := p.getTableRowCount(jt.name)
		for _, pred := range wherePredicates {
			if p.canPushDown(pred, jt.name) {
				psel := p.joinPredSel(pred, rowCnt)
				sel *= psel
			}
		}
		tableSelectivity[jt.name] = sel
	}
	// REQ000909: also factor in pushed-down predicates for each table
	// (these are the single-table predicates that were already pushed
	// to OP.SeqScan/OP.IndexScan before n3JoinOrdering is called).
	if pushedPredicates != nil {
		for _, jt := range joinTables {
			if preds, ok := pushedPredicates[jt.name]; ok && len(preds) > 0 {
				sel := tableSelectivity[jt.name]
				rowCnt := p.getTableRowCount(jt.name)
				for _, pred := range preds {
					psel := p.joinPredSel(pred, rowCnt)
					sel *= psel
				}
				tableSelectivity[jt.name] = sel
			}
		}
	}

	// Build initial heap: one partial plan per table (extending baseTable).
	type partial struct {
		tablesSet map[string]bool
		order     []string
		cost      float64
		rows      float64
	}

	heap := make([]partial, 0, n3HeapMaxSize)

	baseRows := p.getTableRowCount(baseTable)
	// REQ000909: reduce base table rows by its own single-table selectivity.
	if pushedPredicates != nil {
		if preds, ok := pushedPredicates[baseTable]; ok && len(preds) > 0 {
			sel := 1.0
			for _, pred := range preds {
				psel := p.joinPredSel(pred, baseRows)
				sel *= psel
			}
			r := baseRows * sel
			if r < 1 {
				r = 1
			}
			baseRows = r
		}
	}

	for _, jt := range joinTables {
		preds := p.findPredicatesForPair(baseTable, jt.name, wherePredicates)
		hasIdx := p.hasIndexOnTable(jt.name)
		rightRows := p.getTableRowCount(jt.name)
		// REQ000883: reduce right row count by single-table selectivity.
		if sel, ok := tableSelectivity[jt.name]; ok {
			r := rightRows * sel
			if r < 1 {
				r = 1
			}
			rightRows = r
		}
		joinCost := p.estimateJoinCost(int(baseRows), int(rightRows), preds, hasIdx)
		resultRows := p.joinResultRows(baseRows, rightRows, preds)
		heap = append(heap, partial{
			tablesSet: map[string]bool{baseTable: true, jt.name: true},
			order:     []string{baseTable, jt.name},
			cost:      baseRows + joinCost,
			rows:      resultRows,
		})
	}

	// Iteratively extend partial plans with the cheapest remaining table.
	for step := 1; step < k; step++ {
		// Determine which tables are still missing from each plan.
		allTables := make([]string, 0, k)
		for _, jt := range joinTables {
			allTables = append(allTables, jt.name)
		}

		type candidate struct {
			idx       int
			cost      float64
			order     []string
			tablesSet map[string]bool
			rows      float64
		}
		nextHeap := make([]candidate, 0, n3HeapMaxSize)

		// REQ000947: bestCost is the minimum cost among the current
		// partial plans (heap[0]). Candidates whose cost exceeds
		// bestCost × n3PruneMultiplier are pruned. For the first
		// step, the partial plans are all the base-pair candidates
		// in heap, so we look up the minimum from heap.
		var bestCost float64
		if len(heap) > 0 {
			bestCost = heap[0].cost
			for _, pp := range heap[1:] {
				if pp.cost < bestCost {
					bestCost = pp.cost
				}
			}
		}
		pruneThreshold := bestCost * n3PruneMultiplier

		// REQ001096: memoize findPredicatesForSet keyed by
		// (sorted joined-set, candidate). The N3 inner loop calls
		// this O(heap × tables) times; the same (joined-set, tbl)
		// pair recurs across heap entries, so the cache turns the
		// inner-loop predicate scan from O(predicates) to O(1).
		predCache := make(map[string][]PS.Expr, len(heap)*len(allTables))

		for _, pp := range heap {
			// Which tables are not yet joined?
			for _, tbl := range allTables {
				if pp.tablesSet[tbl] {
					continue
				}
				// REQ001096: cache lookup by sorted tables-set + tbl.
				cacheKey := n3PredCacheKey(pp.tablesSet, tbl)
				preds, ok := predCache[cacheKey]
				if !ok {
					preds = p.findPredicatesForSet(pp.tablesSet, tbl, wherePredicates)
					predCache[cacheKey] = preds
				}
				hasIdx := p.hasIndexOnTable(tbl)
				rightRows := p.getTableRowCount(tbl)
				// REQ000883: reduce right row count by single-table selectivity.
				if sel, ok := tableSelectivity[tbl]; ok {
					r := rightRows * sel
					if r < 1 {
						r = 1
					}
					rightRows = r
				}
				joinCost := p.estimateJoinCost(int(pp.rows), int(rightRows), preds, hasIdx)
				newCost := pp.cost + joinCost
				newRows := p.joinResultRows(pp.rows, rightRows, preds)

				newOrder := make([]string, len(pp.order)+1)
				copy(newOrder, pp.order)
				newOrder[len(pp.order)] = tbl

				newSet := make(map[string]bool, len(pp.tablesSet)+1)
				for k := range pp.tablesSet {
					newSet[k] = true
				}
				newSet[tbl] = true

				cand := candidate{
					idx:       0,
					cost:      newCost,
					order:     newOrder,
					tablesSet: newSet,
					rows:      newRows,
				}

				// REQ000947: prune candidates whose cost exceeds
				// bestCost × n3PruneMultiplier. Skip both the
				// append path and the replace path.
				if bestCost > 0 && cand.cost > pruneThreshold {
					continue
				}

				// Insert into nextHeap, keep top N.
				if len(nextHeap) < n3HeapMaxSize {
					nextHeap = append(nextHeap, cand)
					// Bubble up (min-heap by cost).
					for i := len(nextHeap) - 1; i > 0; i-- {
						parent := (i - 1) / 2
						if nextHeap[i].cost >= nextHeap[parent].cost {
							break
						}
						nextHeap[i], nextHeap[parent] = nextHeap[parent], nextHeap[i]
					}
				} else if cand.cost < nextHeap[0].cost {
					nextHeap[0] = cand
					// Sink down.
					i := 0
					for {
						smallest := i
						left := 2*i + 1
						right := 2*i + 2
						if left < len(nextHeap) && nextHeap[left].cost < nextHeap[smallest].cost {
							smallest = left
						}
						if right < len(nextHeap) && nextHeap[right].cost < nextHeap[smallest].cost {
							smallest = right
						}
						if smallest == i {
							break
						}
						nextHeap[i], nextHeap[smallest] = nextHeap[smallest], nextHeap[i]
						i = smallest
					}
				}
			}
		}

		// Convert candidates back to partial plans.
		heap = make([]partial, len(nextHeap))
		for i, c := range nextHeap {
			heap[i] = partial{
				tablesSet: c.tablesSet,
				order:     c.order,
				cost:      c.cost,
				rows:      c.rows,
			}
		}
		if len(heap) == 0 {
			break
		}
	}

	// Return the cheapest complete plan.
	if len(heap) == 0 {
		// Fallback: return original FROM clause order when N3
		// cannot find any valid join order.
		order := make([]string, 0, 1+len(joinTables))
		order = append(order, baseTable)
		for _, jt := range joinTables {
			order = append(order, jt.name)
		}
		return order, 0
	}
	best := heap[0]
	for _, pp := range heap[1:] {
		if pp.cost < best.cost {
			best = pp
		}
	}
	// REQ000914: guard against empty order slice — some code paths
	// (e.g. self-joins with aliases) can produce a non-empty heap
	// entry with a zero-length order. Fall back to raw joinTables order.
	if len(best.order) == 0 {
		order := make([]string, 0, 1+len(joinTables))
		order = append(order, baseTable)
		for _, jt := range joinTables {
			order = append(order, jt.name)
		}
		return order, best.cost
	}
	return best.order, best.cost
}

// REQ000946: n3JoinOrderingMultiStart runs the N3 algorithm from
// every candidate base table (the leftmost table in FROM plus each
// subsequent table) and returns the lowest-cost plan. This matches
// SQLite's NGQP "best-of-many-starting-points" strategy.
//
// For a query with K FROM-tables, the cost is K × K² × H evaluations
// where H=n3HeapMaxSize=24. With K=8 this is ~1536 evaluations —
// negligible compared to execution time.
//
// candidateBase is the FROM-list leftmost table (the planner's
// default base). The full candidate set is candidateBase plus every
// other table in joinTables. For each candidate, all other tables
// (the candidate base itself plus every entry in joinTables) form
// the "remaining" set passed to n3JoinOrdering.
//
// The returned order is normalized to start with candidateBase (the
// FROM-list leftmost) — this is required by the join construction
// code in planSelect, which uses s.From as the scan base and looks
// up other tables via joinMap. The relative order of the remaining
// tables follows the cheapest plan.
func (p *Planner) n3JoinOrderingMultiStart(candidateBase string, joinTables []joinTableInfo, wherePredicates []PS.Expr, pushedPredicates map[string][]PS.Expr) []string {
	// REQ000926: preserve duplicate table names in allNames. When the
	// same physical table appears multiple times in the join list
	// (e.g. `FROM tab1 a, tab1 b` self-join), each entry is a
	// distinct reference and must be kept separately in the result
	// order so the join construction produces a full cross product.
	allNames := make([]string, 0, 1+len(joinTables))
	if candidateBase != "" {
		allNames = append(allNames, candidateBase)
	}
	for _, jt := range joinTables {
		allNames = append(allNames, jt.name)
	}
	// REQ000926: short-circuit when the result is already the trivial
	// single-table case. The dedupe check is for the candidate list
	// (different starting bases), not the result.
	hasNonBase := false
	for _, n := range allNames[1:] {
		hasNonBase = true
		_ = n
		break
	}
	if !hasNonBase {
		return allNames
	}
	// candidates: every DISTINCT name as a possible base. Duplicate
	// entries (self-join) collapse to one candidate for planning
	// purposes — the actual join construction uses the full order.
	candidates := make([]string, 0, len(allNames))
	seen := make(map[string]bool)
	for _, n := range allNames {
		if !seen[n] {
			seen[n] = true
			candidates = append(candidates, n)
		}
	}
	bestOrder := []string(nil)
	bestCost := -1.0
	for _, base := range candidates {
		// rest = all joinTables entries whose name != base.
		// Plus, if candidateBase is not in joinTables and is not
		// the current base, include it as a joinTable entry (so
		// the resulting order includes it).
		rest := make([]joinTableInfo, 0, len(joinTables))
		hasBase := false
		for _, jt := range joinTables {
			if jt.name == base {
				hasBase = true
				continue
			}
			rest = append(rest, jt)
		}
		if base != candidateBase && !hasBase {
			// The current base is not candidateBase and is not
			// listed in joinTables — add it as a join entry so the
			// resulting order includes all tables.
			rest = append(rest, joinTableInfo{name: base})
		}
		order, cost := p.n3JoinOrdering(base, rest, wherePredicates, pushedPredicates)
		if bestCost < 0 || cost < bestCost {
			bestCost = cost
			bestOrder = order
		}
	}
	if bestOrder == nil {
		bestOrder = allNames
		return bestOrder
	}

	// REQ000926: ensure the result order has the same number of
	// entries as the input. n3JoinOrdering deduplicates by table
	// name in tablesSet, so for self-joins the order can be shorter
	// than expected. Append any missing duplicates from allNames.
	wantLen := len(allNames)
	if len(bestOrder) < wantLen {
		have := make(map[string]int)
		for _, n := range bestOrder {
			have[n]++
		}
		need := make(map[string]int)
		for _, n := range allNames {
			need[n]++
		}
		for n, count := range need {
			for have[n] < count {
				bestOrder = append(bestOrder, n)
				have[n]++
			}
		}
	}
	// REQ000946: normalize the returned order to start with
	// candidateBase (s.From) so the join construction code that
	// uses s.From as the scan base works correctly.
	if len(bestOrder) > 0 && bestOrder[0] != candidateBase {
		baseIdx := -1
		for i, t := range bestOrder {
			if t == candidateBase {
				baseIdx = i
				break
			}
		}
		if baseIdx > 0 {
			normalized := make([]string, 0, len(bestOrder))
			normalized = append(normalized, candidateBase)
			for i, t := range bestOrder {
				if i != baseIdx {
					normalized = append(normalized, t)
				}
			}
			bestOrder = normalized
		} else if baseIdx < 0 {
			normalized := make([]string, 0, 1+len(bestOrder))
			normalized = append(normalized, candidateBase)
			normalized = append(normalized, bestOrder...)
			bestOrder = normalized
		}
	}

	return bestOrder
}

// filteredRowCount estimates the number of rows that remain after
// applying all single-table predicates on the given table.
// REQ001095: used by exhaustiveJoinOrder to sort tables by
// selectivity so the most-filtered table is joined first.
func filteredRowCount(table string, predicates []PS.Expr, p *Planner) float64 {
	rows := p.getTableRowCount(table)
	for _, pred := range predicates {
		if p.canPushDown(pred, table) {
			psel := p.joinPredSel(pred, rows)
			rows *= psel
		}
	}
	if rows < 1 {
		rows = 1
	}
	return rows
}

// REQ001071: exhaustiveJoinOrder tries all permutations of join tables
// (keeping baseTable fixed as first) and picks the one with minimum
// estimated cost. This is optimal for small joins (≤4 join tables,
// i.e. 24 permutations max) where N3's heuristic may miss the true
// best order. For larger joins, N3 is preferred.
func (p *Planner) exhaustiveJoinOrder(baseTable string, joinTables []joinTableInfo, predicates []PS.Expr) []string {
	k := len(joinTables)
	if k == 0 {
		return []string{baseTable}
	}
	// Extract names, keeping baseTable fixed at position 0.
	names := make([]string, k)
	for i, jt := range joinTables {
		names[i] = jt.name
	}

	// Evaluate the original order as the baseline.
	order := make([]string, 0, 1+k)
	order = append(order, baseTable)
	order = append(order, names...)
	bestOrder := make([]string, len(order))
	copy(bestOrder, order)
	bestCost := p.estimateJoinOrderCost(order, predicates)

	// Generate all permutations of the join tables using Heap's algorithm.
	indices := make([]int, k)
	perm := make([]string, 0, 1+k)
	for i := 0; i < k; {
		if indices[i] < i {
			perm = append(perm[:0], baseTable)
			if i%2 == 0 {
				names[0], names[i] = names[i], names[0]
			} else {
				names[indices[i]], names[i] = names[i], names[indices[i]]
			}
			perm = append(perm, names...)
			cost := p.estimateJoinOrderCost(perm, predicates)
			if cost < bestCost {
				bestCost = cost
				copy(bestOrder, perm)
			}
			indices[i]++
			i = 0
		} else {
			indices[i] = 0
			i++
		}
	}
	return bestOrder
}

func (p *Planner) estimateJoinOrderCost(order []string, predicates []PS.Expr) float64 {
	if len(order) == 0 {
		return 0
	}
	cost := p.getTableRowCount(order[0])
	joined := map[string]bool{order[0]: true}
	for i := 1; i < len(order); i++ {
		next := order[i]
		nextRows := p.getTableRowCount(next)
		// Apply single-table predicate selectivity.
		for _, pred := range predicates {
			if p.canPushDown(pred, next) {
				psel := p.joinPredSel(pred, nextRows)
				nextRows *= psel
			}
		}
		// Cross-table selectivity: if there's an equi-join predicate
		// between joined tables and the next table, assume some reduction.
		selectivity := 1.0
		for _, pred := range predicates {
			tables := p.extractTablesFromExpr(pred)
			if tables[next] {
				hasJoined := false
				for t := range tables {
					if joined[t] {
						hasJoined = true
						break
					}
				}
				if hasJoined {
					psel := p.joinPredSel(pred, nextRows)
					if psel < selectivity {
						selectivity = psel
					}
				}
			}
		}
		intermediate := cost * nextRows * selectivity
		cost += intermediate
		joined[next] = true
	}
	return cost
}

// joinTableInfo holds a table name and its associated JoinClause
// for use in N3 join ordering.
type joinTableInfo struct {
	name string
	join PS.JoinClause
}

// n3PredCacheKey returns a deterministic string key for the
// (joined-set, candidate) pair. REQ001096: sorts the set entries
// so different iteration orders produce the same key.
func n3PredCacheKey(joined map[string]bool, candidate string) string {
	keys := make([]string, 0, len(joined)+1)
	for k := range joined {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte(0)
	}
	b.WriteString(candidate)
	return b.String()
}

// findPredicatesForPair finds the subset of WHERE predicates that
// reference both leftTable and rightTable (cross-table predicates
// between a specific pair).
func (p *Planner) findPredicatesForPair(leftTable, rightTable string, predicates []PS.Expr) []PS.Expr {
	var result []PS.Expr
	for _, pred := range predicates {
		tables := p.extractTablesFromExpr(pred)
		if len(tables) == 2 && tables[leftTable] && tables[rightTable] {
			result = append(result, pred)
		}
	}
	return result
}

// findPredicatesForSet finds WHERE predicates that cross between the
// already-joined tables and the candidate table.
func (p *Planner) findPredicatesForSet(joined map[string]bool, candidate string, predicates []PS.Expr) []PS.Expr {
	var result []PS.Expr
	for _, pred := range predicates {
		tables := p.extractTablesFromExpr(pred)
		if len(tables) < 2 {
			continue
		}
		hasCandidate := tables[candidate]
		hasJoined := false
		for t := range tables {
			if t != candidate && joined[t] {
				hasJoined = true
				break
			}
		}
		if hasCandidate && hasJoined {
			result = append(result, pred)
		}
	}
	return result
}

// hasIndexOnTable checks whether the table has any registered index.
func (p *Planner) hasIndexOnTable(table string) bool {
	tInfo, ok := p.catalog[table]
	if !ok {
		return false
	}
	return len(tInfo.indexes) > 0
}

// joinResultRows estimates the number of output rows from a join.
func (p *Planner) joinResultRows(leftRows, rightRows float64, predicates []PS.Expr) float64 {
	if len(predicates) == 0 {
		return leftRows * rightRows
	}
	sel := 1.0
	for _, pred := range predicates {
		sel *= p.joinPredSel(pred, 0)
	}
	result := leftRows * rightRows * sel
	if result < 1 {
		result = 1
	}
	return result
}

// groupBushyJoins detects independent equi-join pairs in the join
// order and groups them for bushy plan execution. A pair of tables
// is "independent" when their equi-join keys share no columns.
// REQ000821.
func groupBushyJoins(baseTable string, joinOrder []string, crossTablePredicates []PS.Expr) [][]string {
	k := len(joinOrder)
	if k <= 3 {
		// 2-3 tables: left-deep is fine, no bushy benefit.
		return [][]string{joinOrder}
	}

	// Extract equi-join column sets for each consecutive pair in the order.
	type pairKey struct {
		left  string
		right string
	}
	pairKeys := map[pairKey][]string{}
	// REQ001113: track which tables each table is equi-joined with, so
	// we can detect transitive dependencies (e.g. a3=b9 AND a1=d9 →
	// t3 and t1 both equi-join to t9, so they must be in the same group).
	equiJoinTables := map[string]map[string]bool{}
	for _, pred := range crossTablePredicates {
		bin, ok := pred.(*PS.BinaryExpr)
		if !ok || bin.Op != LX.T_EQ {
			continue
		}
		lTable, lCol := extractTableColumn(bin.Left)
		rTable, rCol := extractTableColumn(bin.Right)
		if lTable == "" || rTable == "" {
			continue
		}
		pk := pairKey{lTable, rTable}
		pairKeys[pk] = append(pairKeys[pk], lCol+"="+rCol)
		if equiJoinTables[lTable] == nil {
			equiJoinTables[lTable] = map[string]bool{}
		}
		equiJoinTables[lTable][rTable] = true
		if equiJoinTables[rTable] == nil {
			equiJoinTables[rTable] = map[string]bool{}
		}
		equiJoinTables[rTable][lTable] = true
	}

	// Check for independent pairs: (A,B) and (C,D) where the equi-join
	// columns of (A,B) don't overlap with those of (C,D).
	// For simplicity, we look for the pattern where baseTable is joined
	// to two different tables on different columns — classic star join.
	groups := [][]string{{baseTable}}
	for i := 1; i < k; i++ {
		tbl := joinOrder[i]
		// Check if this table's equi-join key with any already-grouped
		// table is independent. If yes, start a new bushy group.
		independent := true
		targetGroup := len(groups) - 1 // default: last group
		for gi, existing := range groups {
			for _, et := range existing {
				pk := pairKey{et, tbl}
				if _, found := pairKeys[pk]; found {
					independent = false
					targetGroup = gi
					break
				}
				pk = pairKey{tbl, et}
				if _, found := pairKeys[pk]; found {
					independent = false
					targetGroup = gi
					break
				}
			}
			if !independent {
				break
			}
		}
		// REQ001113: also check transitive dependency via shared
		// equi-join table. If the candidate table equi-joins to any
		// table that is also equi-joined by a table in an existing
		// group, they are transitively dependent. E.g. a3=b9 AND
		// a1=d9: t3 equi-joins to t9, t1 equi-joins to t9, so
		// t3 and t1 must be in the same group.
		if independent {
			tblJoins := equiJoinTables[tbl]
			if len(tblJoins) > 0 {
				for gi, existing := range groups {
					for _, et := range existing {
						etJoins := equiJoinTables[et]
						// Check if the candidate and existing table share
						// a common equi-join table (transitive dependency).
						for shared := range tblJoins {
							if etJoins[shared] {
								independent = false
								targetGroup = gi
								break
							}
						}
						if !independent {
							break
						}
					}
					if !independent {
						break
					}
				}
			}
		}
		if independent && len(groups[len(groups)-1]) >= 2 {
			groups = append(groups, []string{tbl})
		} else {
			groups[targetGroup] = append(groups[targetGroup], tbl)
		}
	}

	if len(groups) == 1 {
		return [][]string{joinOrder}
	}
	return groups
}

// extractTableColumn extracts (table, column) from an expression
// that is an Ident or QualifiedName. For bare Idents (implicit
// comma-join columns like "d6"), resolves the table via the SLT
// naming convention (d6 => t6.d) so groupBushyJoins can detect
// cross-table equi-join dependencies. REQ001113.
func extractTableColumn(e PS.Expr) (string, string) {
	switch v := e.(type) {
	case *PS.QualifiedName:
		return v.Table, v.Name
	case *PS.Ident:
		// Resolve bare column to its owning table via SLT naming
		// convention (e.g. "d6" => table "t6", column "d").
		if tbl := findTableInSchemas(v.Name); tbl != "" {
			return tbl, v.Name
		}
		return "", v.Name
	}
	return "", ""
}

func (p *Planner) ParseAndPlan(sql string) (*pl.PlanResult, error) {
	parser := PS.NewParser(sql)
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
	return NewCreateIndex(s)
}

// planDropIndex removes a secondary index. iter-22.
func (p *Planner) planDropIndex(s *PS.DropIndexStmt) DT.Operator {
	return NewDropIndex(s)
}

// planPragma handles PRAGMA statements. REQ000261.
func (p *Planner) planPragma(s *PS.PragmaStmt) DT.Operator {
	switch s.Name {
	case "integrity_check":
		return UT.NewIntegrityCheckWithStore(p.store)
	case "cache_size", "journal_mode", "synchronous", "user_version":
		// REQ000242: return pragma value as a single-row result
		return OP.NewPragmaResult(s.Name, s.Value)
	case "foreign_keys", "foreign_key_check":
		// REQ000905/REQ000906: these are handled by the Pragma operator
		// which needs access to the store for FK introspection.
		return NewPragma(s).WithStore(p.store)
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
	if p.maxMemoryPerQuery > 0 {
		cop.WithMemoryLimit(p.maxMemoryPerQuery)
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
func isSubqueryFlattenable(s *PS.Select) bool {
	if s == nil {
		return false
	}
	if hasAnyAggregate(s.Cols) {
		return false
	}
	if s.Distinct {
		return false
	}
	if len(s.GroupBy) > 0 {
		return false
	}
	if s.Limit != nil || s.Offset != nil {
		return false
	}
	if len(s.OrderBy) > 0 {
		return false
	}
	return true
}

// pushPredicateIntoSubquery pushes outer WHERE predicates that reference
// only subquery columns into the subquery's own WHERE clause. Returns the
// remaining predicates (those that cannot be pushed down).
// REQ001072.
func pushPredicateIntoSubquery(outerWhere PS.Expr, subSel *PS.Select) PS.Expr {
	if outerWhere == nil || subSel == nil {
		return outerWhere
	}
	// Extract subquery column names.
	subCols := make(map[string]bool)
	for _, col := range subSel.Cols {
		switch c := col.(type) {
		case *PS.AliasedExpr:
			subCols[c.Alias] = true
		case *PS.Ident:
			subCols[c.Name] = true
		case *PS.StarExpr:
			// SELECT * — all columns are available, so all predicates
			// can potentially be pushed. Return the original WHERE
			// as-is and merge it into the subquery.
			mergeWhereIntoSubquery(outerWhere, subSel)
			return nil
		}
	}
	// Split outer WHERE into conjuncts and check each one.
	conjuncts := RE.SplitAnd(outerWhere)
	var pushable []PS.Expr
	var remaining []PS.Expr
	for _, c := range conjuncts {
		if referencesOnlySubqueryCols(c, subCols) {
			pushable = append(pushable, c)
		} else {
			remaining = append(remaining, c)
		}
	}
	// Merge pushable conjuncts into the subquery's WHERE.
	for _, p := range pushable {
		if subSel.Where != nil {
			subSel.Where = &PS.BinaryExpr{
				Left:  subSel.Where,
				Op:    LX.T_AND,
				Right: p,
			}
		} else {
			subSel.Where = p
		}
	}
	// Return remaining predicates as the outer WHERE.
	if len(remaining) == 0 {
		return nil
	}
	result := remaining[0]
	for _, r := range remaining[1:] {
		result = &PS.BinaryExpr{
			Left:  result,
			Op:    LX.T_AND,
			Right: r,
		}
	}
	return result
}

// tryFlattenSubqueryFrom attempts to fully flatten a FROM-clause derived
// table by rewriting the outer SELECT against the inner table directly.
// REQ001079: `SELECT * FROM (SELECT x FROM t) WHERE x > 10` becomes
// `SELECT x FROM t WHERE x > 10`. Returns nil if flattening is not safe.
//
// Flattening is safe when:
//   - subSel is flattenable (no aggregation/DISTINCT/GROUP BY/ORDER BY/LIMIT/OFFSET)
//   - subSel.From is a real table (not another derived table)
//   - outer SELECT has no JOINs (single-table flattening only)
//   - outer Cols and WHERE do not reference the subquery alias
//     (e.g. `sub.x`) — those need alias resolution before flattening
func (p *Planner) tryFlattenSubqueryFrom(s *PS.Select, subSel *PS.Select) DT.Operator {
	if !isSubqueryFlattenable(subSel) {
		return nil
	}
	if subSel.From == "" || subSel.SubqueryFrom != nil {
		return nil
	}
	if len(s.Joins) > 0 {
		return nil
	}
	subAlias := s.From
	if subAlias == "" || subAlias == "$$subquery$$" {
		subAlias = ""
	}
	if subAlias != "" {
		// Outer cols or WHERE that reference `sub.X` need alias
		// rewriting to use subSel's underlying column names. Skip
		// flatten for now — REQ001072 pushdown still applies.
		if s.Where != nil && exprReferencesTable(s.Where, subAlias) {
			return nil
		}
		for _, c := range s.Cols {
			if colExprReferencesTable(c, subAlias) {
				return nil
			}
		}
	}
	// Build the flattened SELECT: SELECT s.Cols FROM subSel.From
	// WHERE subSel.Where AND s.Where.
	flat := &PS.Select{}
	*flat = *subSel
	flat.Cols = s.Cols
	flat.FromAlias = subAlias
	flat.Where = andExpr(subSel.Where, s.Where)
	flat.OrderBy = s.OrderBy
	flat.Limit = s.Limit
	flat.Offset = s.Offset
	flat.Distinct = s.Distinct
	flat.GroupBy = s.GroupBy
	flat.Having = s.Having
	flat.OffsetFirst = s.OffsetFirst
	// Clear the SubqueryFrom marker — flatten has absorbed it.
	flat.SubqueryFrom = nil
	return p.planSelect(flat)
}

// andExpr returns a AND b as a fresh BinaryExpr. Returns a when b is nil,
// b when a is nil, nil when both are nil.
func andExpr(a, b PS.Expr) PS.Expr {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	return &PS.BinaryExpr{Op: LX.T_AND, Left: a, Right: b}
}

// exprReferencesTable returns true if e contains a QualifiedName or
// AliasedExpr whose Table matches `tbl`. Used by REQ001079 to decide
// whether outer WHERE/Cols can be flattened without alias rewriting.
func exprReferencesTable(e PS.Expr, tbl string) bool {
	if e == nil || tbl == "" {
		return false
	}
	hit := false
	walkExpr(e, func(n PS.Expr) {
		if hit {
			return
		}
		switch v := n.(type) {
		case *PS.QualifiedName:
			if v.Table == tbl {
				hit = true
			}
		case *PS.AliasedExpr:
			if colExprReferencesTable(v.Expr, tbl) {
				hit = true
			}
		}
	})
	return hit
}

// colExprReferencesTable is the variant used for top-level SELECT col
// entries (which are bare Expr values, not wrapped in another Expr).
func colExprReferencesTable(c PS.Expr, tbl string) bool {
	if c == nil || tbl == "" {
		return false
	}
	switch v := c.(type) {
	case *PS.QualifiedName:
		return v.Table == tbl
	case *PS.AliasedExpr:
		return colExprReferencesTable(v.Expr, tbl)
	case *PS.BinaryExpr:
		return colExprReferencesTable(v.Left, tbl) || colExprReferencesTable(v.Right, tbl)
	}
	return false
}

// canonicalColRef extracts a column reference identifier from an expression.
// Returns "Table.Name" for qualified names or "Name" for bare identifiers.
// Returns empty string if the expression is not a column reference.
func canonicalColRef(e PS.Expr) string {
	switch v := e.(type) {
	case *PS.QualifiedName:
		if v.Table != "" {
			return v.Table + "." + v.Name
		}
		return v.Name
	case *PS.Ident:
		return v.Name
	}
	return ""
}

// colRefFromCanonical reconstructs a column reference expression from
// the canonical "Table.Name" form (or just "Name" for unqualified cols).
func colRefFromCanonical(canonical string) PS.Expr {
	if idx := strings.Index(canonical, "."); idx >= 0 {
		return &PS.QualifiedName{Table: canonical[:idx], Name: canonical[idx+1:]}
	}
	return &PS.Ident{Name: canonical}
}

// inferTransitiveEqualities builds equivalence classes from
// column-to-column equality predicates and emits inferred equalities
// for every pair within each equivalence class. REQ001077: WHERE
// a = b AND b = c implies a = c, so the planner can use any inferred
// equality as a join key or index condition. Already-existing equalities
// are not re-emitted.
func (p *Planner) inferTransitiveEqualities(conjuncts []PS.Expr) []PS.Expr {
	parent := make(map[string]string)
	var find func(string) string
	find = func(x string) string {
		if _, ok := parent[x]; !ok {
			parent[x] = x
		}
		if parent[x] != x {
			parent[x] = find(parent[x])
		}
		return parent[x]
	}
	union := func(a, b string) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[ra] = rb
		}
	}
	for _, e := range conjuncts {
		be, ok := e.(*PS.BinaryExpr)
		if !ok || be.Op != LX.T_EQ {
			continue
		}
		l := canonicalColRef(be.Left)
		r := canonicalColRef(be.Right)
		if l == "" || r == "" || l == r {
			continue
		}
		union(l, r)
	}
	// Collect existing equalities so we don't re-emit them.
	existing := make(map[string]bool)
	for _, e := range conjuncts {
		be, ok := e.(*PS.BinaryExpr)
		if !ok || be.Op != LX.T_EQ {
			continue
		}
		l := canonicalColRef(be.Left)
		r := canonicalColRef(be.Right)
		if l != "" && r != "" {
			existing[l+"\x00"+r] = true
			existing[r+"\x00"+l] = true
		}
	}
	// Group columns by equivalence class root.
	groups := make(map[string][]string)
	for col := range parent {
		root := find(col)
		groups[root] = append(groups[root], col)
	}
	var inferred []PS.Expr
	for _, members := range groups {
		if len(members) < 2 {
			continue
		}
		for i := 0; i < len(members); i++ {
			for j := i + 1; j < len(members); j++ {
				a, b := members[i], members[j]
				if existing[a+"\x00"+b] {
					continue
				}
				existing[a+"\x00"+b] = true
				inferred = append(inferred, &PS.BinaryExpr{
					Op:    LX.T_EQ,
					Left:  colRefFromCanonical(a),
					Right: colRefFromCanonical(b),
				})
			}
		}
	}
	return inferred
}

// mergeWhereIntoSubquery merges outerWhere into the subquery's WHERE.
// Used when the subquery has SELECT * (all columns available).
func mergeWhereIntoSubquery(outerWhere PS.Expr, subSel *PS.Select) {
	if outerWhere == nil || subSel == nil {
		return
	}
	if subSel.Where != nil {
		subSel.Where = &PS.BinaryExpr{
			Left:  subSel.Where,
			Op:    LX.T_AND,
			Right: outerWhere,
		}
	} else {
		subSel.Where = outerWhere
	}
}

// referencesOnlySubqueryCols checks if an expression references only
// columns from the given set. Returns false for multi-table references.
func referencesOnlySubqueryCols(e PS.Expr, subCols map[string]bool) bool {
	if e == nil {
		return true
	}
	// Walk the expression and collect all column references.
	cols := collectIdentsFromExpr(e)
	if len(cols) == 0 {
		// Constant expression (e.g., 1=1) — always pushable.
		return true
	}
	for _, c := range cols {
		if !subCols[c] {
			return false
		}
	}
	return true
}

// collectIdentsFromExpr returns all Ident names referenced in an expression.
func collectIdentsFromExpr(e PS.Expr) []string {
	var result []string
	walkExpr(e, func(inner PS.Expr) {
		if id, ok := inner.(*PS.Ident); ok {
			result = append(result, id.Name)
		}
	})
	return result
}

// operatorProducesSorted returns true when op is guaranteed to emit
// rows sorted ascending on the given key columns. REQ001102: used by
// the planner to detect when MergeJoin is applicable. Currently
// recognized: OP.Sort with matching keys. OP.IndexScan recognition is
// deferred to a future iteration because the planner doesn't expose
// the indexed column name through the operator interface.
func operatorProducesSorted(op DT.Operator, keys []string) bool {
	if op == nil || len(keys) == 0 {
		return false
	}
	if s, ok := op.(*OP.Sort); ok {
		// Match OP.Sort's keys (in order) against the join keys.
		if len(s.Keys()) < len(keys) {
			return false
		}
		for i, k := range keys {
			sortExpr := s.Keys()[i].Expr
			switch v := sortExpr.(type) {
			case *PS.QualifiedName:
				if v.Name != k && v.Table+"."+v.Name != k {
					return false
				}
			case *PS.Ident:
				if v.Name != k {
					return false
				}
			default:
				return false
			}
		}
		return true
	}
	return false
}

// tryMergeJoin returns a MergeJoin operator if BOTH the left and right
// sides are already sorted on the equi-join keys; otherwise nil.
// REQ001102. Left/Right/Full outer joins are supported via WithKind.
func (p *Planner) tryMergeJoin(left, right DT.Operator, leftTbl, rightTbl string, leftKeys, rightKeys []string, kind OP.JoinKind) DT.Operator {
	if len(leftKeys) == 0 || len(rightKeys) == 0 {
		return nil
	}
	if len(leftKeys) != len(rightKeys) {
		return nil
	}
	if !operatorProducesSorted(left, leftKeys) {
		return nil
	}
	if !operatorProducesSorted(right, rightKeys) {
		return nil
	}
	mj := OP.NewMergeJoin(left, right, leftTbl, rightTbl, leftKeys, rightKeys)
	mj.WithKind(kind)
	return mj
}

// planAggregation handles aggregate selection (HashAggregate vs streaming
// Aggregate) and HAVING clause application.
// REQ000981: extracted from planSelect.
func (p *Planner) planAggregation(s *PS.Select, current DT.Operator) DT.Operator {
	needsAggregate := hasAnyAggregate(s.Cols) || len(s.GroupBy) > 0
	groupCols := s.GroupBy
	var aggExprs []PS.Expr
	if !needsAggregate {
		if s.Having != nil {
			return OP.NewFilter(current, s.Having)
		}
		return current
	}
	aggsOnly, autoGroup, _ := splitSelectCols(s.Cols)
	aggExprs = aggsOnly
	if len(groupCols) == 0 {
		groupCols = autoGroup
	}
	estimatedRows := p.estimateRowCount(s.From, s.Where)
	if estimatedRows >= HashAggregateThreshold {
		agg := AG.NewHashAggregate(current, groupCols, aggExprs)
		if isStarExpr(s.Cols) {
			agg.SetExpandStar()
		}
		current = agg
	} else {
		agg := AG.NewAggregate(current, groupCols, aggExprs)
		if isStarExpr(s.Cols) {
			agg.SetExpandStar()
		}
		current = agg
	}
	if s.Having != nil {
		current = OP.NewFilter(current, s.Having)
	}
	return current
}

// planOrdering handles ORDER BY resolution, OP.Sort operator creation,
// window functions, projection, and DISTINCT.
// REQ000981: extracted from planSelect.
func (p *Planner) planOrdering(s *PS.Select, current DT.Operator) DT.Operator {
	if len(s.OrderBy) > 0 {
		selectExprs := make([]PS.Expr, 0, len(s.Cols))
		for _, col := range s.Cols {
			if ae, ok := col.(*PS.AliasedExpr); ok {
				selectExprs = append(selectExprs, ae.Expr)
			} else {
				selectExprs = append(selectExprs, col)
			}
		}
		for i := range s.OrderBy {
			if nl, ok := s.OrderBy[i].Expr.(*PS.NumberLiteral); ok {
				pos := int(nl.Val)
				if pos >= 1 && pos <= len(selectExprs) {
					s.OrderBy[i].Expr = cloneExpr(selectExprs[pos-1])
				}
			}
		}
	}
	if len(s.OrderBy) > 0 {
		if !p.pkOrderMatches(s.From, s.OrderBy) {
			sort := OP.NewSort(current, s.OrderBy)
			if p.pool != nil {
				sort.WithPool(p.pool.(*UT.WorkerPool))
			}
			current = sort
		}
	}
	needsWindow := hasAnyWindowFunc(s.Cols)
	if needsWindow {
		for _, col := range s.Cols {
			if wf, ok := col.(*PS.WindowFunc); ok {
				args := make([]PS.Expr, len(wf.Args))
				copy(args, wf.Args)
				cols := []string{"*"}
				winOp := AG.NewWindowOperator(current, wf.Name, args, wf.Over, cols)
				current = winOp
			}
		}
	}
	if len(s.Cols) > 0 && !isStarExpr(s.Cols) && !hasAnyAggregate(s.Cols) && !needsWindow {
		current = OP.NewProject(current, s.Cols)
	}
	if needsWindow && len(s.Cols) > 0 && !isStarExpr(s.Cols) {
		current = OP.NewProject(current, s.Cols)
	}
	if s.Distinct && !hasAnyAggregate(s.Cols) {
		current = OP.NewDistinct(current)
	}
	return current
}

// planLimitOffset handles LIMIT, OFFSET, and FETCH FIRST wrapping.
// REQ000981: extracted from planSelect.
func (p *Planner) planLimitOffset(s *PS.Select, current DT.Operator) DT.Operator {
	if s.Limit == nil && s.FetchFirst != nil {
		if s.FetchFirst.Count != nil {
			s.Limit = s.FetchFirst.Count
		} else {
			s.Limit = &PS.NumberLiteral{Val: int64(1)}
		}
	}
	if s.OffsetFirst {
		if s.Limit != nil {
			n, ok := limitInt64(s.Limit)
			if !ok {
				return nil
			}
			current = OP.NewLimit(current, n)
			propagateLimitToNLJ(current, n)
		}
		if s.Offset != nil {
			n, ok := limitInt64(s.Offset)
			if ok && n > 0 {
				current = OP.NewOffset(current, n)
			}
		}
	} else {
		if s.Offset != nil {
			n, ok := limitInt64(s.Offset)
			if ok && n > 0 {
				current = OP.NewOffset(current, n)
			}
		}
		if s.Limit != nil {
			n, ok := limitInt64(s.Limit)
			if !ok {
				return nil
			}
			current = OP.NewLimit(current, n)
			propagateLimitToNLJ(current, n)
		}
	}
	return current
}

// planSelectJoins handles join planning: N3 join ordering, bushy join tree
// construction, and join operator creation. REQ000981: extracted from planSelect.
func (p *Planner) planSelectJoins(s *PS.Select, filteredScan DT.Operator, pushedPredicates map[string][]PS.Expr, crossTableConjuncts, crossTablePredicates []PS.Expr, extractedPreds map[int]bool) DT.Operator {
	joinInfos := make([]joinTableInfo, 0, len(s.Joins))
	joinClauses := make([]PS.JoinClause, 0, len(s.Joins))
	for _, j := range s.Joins {
		if j.Kind != "INNER" && j.Kind != "LEFT" && j.Kind != "RIGHT" && j.Kind != "FULL" && j.Kind != "CROSS" {
			continue
		}
		joinInfos = append(joinInfos, joinTableInfo{name: j.Right, join: j})
		joinClauses = append(joinClauses, j)
	}
	costPredicates := crossTablePredicates
	if costPredicates == nil && s.Where != nil {
		costPredicates = RE.SplitAnd(s.Where)
	}
	const reorderJoinsLimit = 8
	joinOrder := []string(nil)
	if len(joinInfos) <= 4 {
		joinOrder = p.exhaustiveJoinOrder(s.From, joinInfos, costPredicates)
	} else if len(joinInfos) <= reorderJoinsLimit {
		joinOrder = p.n3JoinOrderingMultiStart(s.From, joinInfos, costPredicates, pushedPredicates)
	} else {
		joinOrder, _ = p.n3JoinOrdering(s.From, joinInfos, costPredicates, pushedPredicates)
	}
	var projectedCols []string
	if refCols := collectReferencedColumns(s); refCols != nil {
		projectedCols = make([]string, 0, len(refCols))
		for c := range refCols {
			projectedCols = append(projectedCols, c)
		}
	}
	joinGroups := groupBushyJoins(s.From, joinOrder, crossTableConjuncts)
	type groupResult struct {
		op    DT.Operator
		tbl   string
		set   map[string]bool
		preds []PS.Expr
	}
	var groupOps []groupResult
	// REQ001113: track equi-join predicates consumed by earlier groups
	// so subsequent groups don't re-extract them. Without this, a
	// predicate like a1=d9 consumed within group {t9,t3,t1} would be
	// re-extracted by the merge phase, causing the final WHERE filter
	// to skip it and produce 0 rows.
	consumedPreds := map[PS.Expr]bool{}
	for gi, group := range joinGroups {
		baseTable := group[0]
		var current DT.Operator
		var leftTbl string
		groupCounts := make(map[string]int, len(group))
		for _, t := range group {
			groupCounts[t]++
		}
		joinedTables := map[string]bool{}
		// OP.Filter out predicates already consumed by earlier groups.
		// Also filter to only include predicates where both sides
		// reference tables in the current group — cross-group equi-join
		// keys must be left for the merge phase. REQ001113.
		groupTableSet := map[string]bool{}
		for _, t := range group {
			groupTableSet[t] = true
		}
		var localConjuncts []PS.Expr
		for _, c := range crossTableConjuncts {
			if consumedPreds[c] {
				continue
			}
			// Check if both sides of the predicate reference only
			// tables in the current group.
			tables := p.extractTablesFromExpr(c)
			if len(tables) == 0 {
				// Constant expression — include it.
				localConjuncts = append(localConjuncts, c)
				continue
			}
			allInGroup := true
			for t := range tables {
				if !groupTableSet[t] {
					allInGroup = false
					break
				}
			}
			if allInGroup {
				localConjuncts = append(localConjuncts, c)
			}
		}
		initialConjuncts := localConjuncts
		tableOccurrence := make(map[string]int, len(group))
		joinClauseIdx := make(map[string]int, len(joinClauses))
		for ci, jc := range joinClauses {
			joinClauseIdx[jc.Right] = ci
		}
		if gi == 0 {
			current = filteredScan
			leftTbl = s.From
			if s.FromAlias != "" {
				leftTbl = s.FromAlias
			}
			joinedTables[s.From] = true
		} else {
			var baseOp DT.Operator = OP.NewSeqScan(baseTable)
			if ssc, err := OP.NewSeqScanWithStore(p.store, baseTable); err == nil {
				baseOp = ssc
			}
			if baseCi, ok := joinClauseIdx[baseTable]; ok {
				jc := joinClauses[baseCi]
				if jc.RightAlias != "" {
					if ss, ok := baseOp.(*OP.SeqScan); ok {
						ss.WithAlias(jc.RightAlias)
					}
					leftTbl = jc.RightAlias
				}
			}
			if basePreds := pushedPredicates[baseTable]; len(basePreds) > 0 {
				for _, pred := range basePreds {
					tryApplyPointLookup(baseOp, pred)
					baseOp = OP.NewFilter(baseOp, pred)
				}
			}
			current = baseOp
			if leftTbl == "" {
				leftTbl = baseTable
			}
			joinedTables[baseTable] = true
		}
		for ti, tbl := range group {
			if ti == 0 {
				continue
			}
			occ := tableOccurrence[tbl]
			tableOccurrence[tbl]++
			ci, ok := joinClauseIdx[tbl]
			if !ok {
				continue
			}
			joinedCounts := make(map[string]int, len(group))
			for t := range joinedTables {
				joinedCounts[t]++
			}
			if occ > 0 {
				jcCount := 0
				for _, jc := range joinClauses {
					if jc.Right == tbl {
						jcCount++
					}
				}
				if joinedCounts[tbl] >= jcCount {
					continue
				}
			}
			j := joinClauses[ci]
			if j.Right != tbl {
				continue
			}
			kind := OP.JoinKind(j.Kind)
			rightTbl := j.Right
			if j.RightAlias != "" {
				rightTbl = j.RightAlias
			}
			var rightScan DT.Operator = OP.NewSeqScan(j.Right)
			if ssc, err := OP.NewSeqScanWithStore(p.store, j.Right); err == nil {
				rightScan = ssc
			}
			if j.RightAlias != "" {
				if ss, ok := rightScan.(*OP.SeqScan); ok {
					ss.WithAlias(j.RightAlias)
				}
			}
			if rightPreds := pushedPredicates[j.Right]; len(rightPreds) > 0 {
				for _, pred := range rightPreds {
					tryApplyPointLookup(rightScan, pred)
					rightScan = OP.NewFilter(rightScan, pred)
				}
			}
			var joinOp DT.Operator
			if (kind == OP.JoinKindInner || kind == OP.JoinKindCross) && len(localConjuncts) > 0 {
				lk, rk, remaining := p.extractEquiJoinKeys(localConjuncts, joinedTables, j.Right)
				if len(lk) > 0 {
					for _, orig := range localConjuncts {
						found := false
						for _, rem := range remaining {
							if orig == rem {
								found = true
								break
							}
						}
						if !found {
							for pi, cp := range crossTablePredicates {
								if cp == orig {
									extractedPreds[pi] = true
								}
							}
						}
					}
					joinOp = OP.NewHashJoin(current, rightScan, leftTbl, rightTbl, lk, rk, 0)
					if p.joinBufferSize > 0 {
						if hj, ok := joinOp.(*OP.HashJoin); ok {
							hj.WithJoinBufferSize(p.joinBufferSize)
						}
					}
					if projectedCols != nil {
						if hj, ok := joinOp.(*OP.HashJoin); ok {
							hj.WithProjection(projectedCols)
						}
					}
					localConjuncts = remaining
				}
			}
			if joinOp == nil {
				if kind == OP.JoinKindInner && j.On != nil {
					if lk, rk, ok := p.extractSingleOnEquiKey(j.On, leftTbl, rightTbl); ok {
						joinOp = OP.NewHashCrossJoin(current, rightScan, leftTbl, rightTbl, lk, rk)
						if projectedCols != nil {
							if hcj, ok := joinOp.(*OP.HashCrossJoin); ok {
								hcj.WithProjection(projectedCols)
							}
						}
					}
				}
				if joinOp == nil && len(localConjuncts) == 0 {
					// REQ001102: try MergeJoin when both sides are
					// already sorted on the join keys. Falls back to
					// NLJ if not applicable.
					if lk, rk, ok := p.extractSingleOnEquiKey(j.On, leftTbl, rightTbl); ok && j.On != nil {
						if mj := p.tryMergeJoin(current, rightScan, leftTbl, rightTbl, []string{lk}, []string{rk}, kind); mj != nil {
							joinOp = mj
						}
					}
				}
				if joinOp == nil {
					var on func(outer, inner *DT.Row) (bool, error)
					if j.On != nil {
						pred := j.On
						on = func(outer, inner *DT.Row) (bool, error) {
							v, err := EV.EvalValue(pred, inner, nil)
							if err != nil {
								return false, err
							}
							return DT.IsValueTruthy(v), nil
						}
					}
					nlj := OP.NewNestedLoopJoin(current, rightScan, leftTbl, rightTbl, on, kind)
					if projectedCols != nil {
						nlj.WithProjection(projectedCols)
					}
					_ = deriveJoinSchema
					joinOp = nlj
				}
			}
			current = joinOp
			joinedTables[tbl] = true
			leftTbl = rightTbl
		}
		if current != nil {
			groupOps = append(groupOps, groupResult{
				op:    current,
				tbl:   leftTbl,
				set:   joinedTables,
				preds: localConjuncts,
			})
		}
		// Record predicates consumed by this group so subsequent groups
		// don't re-extract them.
		for _, c := range initialConjuncts {
			found := false
			for _, r := range localConjuncts {
				if c == r {
					found = true
					break
				}
			}
			if !found {
				consumedPreds[c] = true
			}
		}
	}
	leftTbl := ""
	joinedTables := map[string]bool{}
	var current DT.Operator
	for i, gr := range groupOps {
		if i == 0 {
			current = gr.op
			leftTbl = gr.tbl
			for t := range gr.set {
				joinedTables[t] = true
			}
			continue
		}
		var joinOp DT.Operator
		if len(gr.preds) > 0 {
			var lk, rk []string
			var remaining []PS.Expr
			for t := range gr.set {
				if joinedTables[t] {
					continue
				}
				lk2, rk2, rem := p.extractEquiJoinKeys(gr.preds, joinedTables, t)
				if len(lk2) > 0 {
					lk, rk, remaining = lk2, rk2, rem
					break
				}
			}
			if len(lk) == 0 {
				lk, rk, remaining = p.extractEquiJoinKeys(gr.preds, joinedTables, gr.tbl)
			}
			_ = remaining
			if len(lk) > 0 {
				joinOp = OP.NewHashJoin(current, gr.op, leftTbl, gr.tbl, lk, rk, 0)
				if p.joinBufferSize > 0 {
					if hj, ok := joinOp.(*OP.HashJoin); ok {
						hj.WithJoinBufferSize(p.joinBufferSize)
					}
				}
				if projectedCols != nil {
					if hj, ok := joinOp.(*OP.HashJoin); ok {
						hj.WithProjection(projectedCols)
					}
				}
			}
		}
		if joinOp == nil {
			nlj := OP.NewNestedLoopJoin(current, gr.op, leftTbl, gr.tbl, nil, OP.JoinKindCross)
			if projectedCols != nil {
				nlj.WithProjection(projectedCols)
			}
			joinOp = nlj
		}
		current = joinOp
		leftTbl = gr.tbl
		for t := range gr.set {
			joinedTables[t] = true
		}
	}
	return current
}

// collectColRefs gathers all unqualified column names referenced
// in the expression. For index-decomposition we only need the
// names; we don't trace qualified/table-prefixed refs yet.
func collectColRefs(e PS.Expr) []string {
	if e == nil {
		return nil
	}
	switch n := e.(type) {
	case *PS.Ident:
		return []string{n.Name}
	case *PS.BinaryExpr:
		return append(collectColRefs(n.Left), collectColRefs(n.Right)...)
	case *PS.UnaryExpr:
		return collectColRefs(n.Operand)
	case *PS.AliasedExpr:
		return collectColRefs(n.Expr)
	case *PS.CastExpr:
		return collectColRefs(n.Expr)
	case *PS.FunctionCall:
		var refs []string
		for _, a := range n.Args {
			refs = append(refs, collectColRefs(a)...)
		}
		return refs
	case *PS.BetweenExpr:
		return append(
			append(collectColRefs(n.Expr), collectColRefs(n.Low)...),
			collectColRefs(n.High)...)
	case *PS.InExpr:
		var refs []string
		refs = append(refs, collectColRefs(n.Expr)...)
		for _, a := range n.List {
			refs = append(refs, collectColRefs(a)...)
		}
		return refs
	case *PS.ListExpr:
		var refs []string
		for _, a := range n.Items {
			refs = append(refs, collectColRefs(a)...)
		}
		return refs
	case *PS.CaseExpr:
		var refs []string
		refs = append(refs, collectColRefs(n.Expr)...)
		for _, w := range n.WhenList {
			refs = append(refs, collectColRefs(w.Cond)...)
			refs = append(refs, collectColRefs(w.Then)...)
		}
		refs = append(refs, collectColRefs(n.Else)...)
		return refs
	}
	return nil
}

// rebuildAnd constructs a left-deep AND tree from a slice of
// expressions. An empty/nil slice returns nil. A single-element
// slice returns that element unwrapped.
func rebuildAnd(exprs []PS.Expr) PS.Expr {
	if len(exprs) == 0 {
		return nil
	}
	if len(exprs) == 1 {
		return exprs[0]
	}
	acc := exprs[0]
	for i := 1; i < len(exprs); i++ {
		acc = &PS.BinaryExpr{Op: LX.T_AND, Left: acc, Right: exprs[i]}
	}
	return acc
}

// decomposeForIndexScan splits the WHERE expression into three
// groups relative to the scanColumn that is used as the index
// seek key. The split is:
//
//	indexable — conjuncts that reference ONLY scanColumn AND are
//	             already matched by the index seek (e.g. col=5
//	             or col BETWEEN 1 AND 10). These are NOT added
//	             as residual or filter; the seek handles them.
//	residual  — conjuncts that reference ONLY scanColumn but are
//	             NOT the primary seek condition (e.g. col>0 or
//	             col IS NOT NULL). These supplement the seek.
//	extra     — conjuncts that reference other columns or
//	             multiple columns. These must stay in an outer
//	             OP.Filter because the index path cannot evaluate
//	             them.
//
// The first indexable conjunct that carries a bound (eq or
// range) is selected as the seek condition; subsequent ones
// become residual.
//
// REQ001108.
func (p *Planner) decomposeForIndexScan(whereExpr PS.Expr, scanCol string) (residual, extra []PS.Expr) {
	conjuncts := RE.SplitAnd(whereExpr)
	if len(conjuncts) == 0 {
		return nil, nil
	}
	// The seek condition is the first indexable conjunct with
	// a concrete bound. We detect it by checking indexedColumnEq
	// and indexedColumnRange patterns. Everything else is extra
	// or residual.
	seekFound := false
	for _, c := range conjuncts {
		refs := collectColRefs(c)
		scanColOnly := len(refs) == 1 && refs[0] == scanCol
		if !scanColOnly {
			extra = append(extra, c)
			continue
		}
		if !seekFound {
			if col, _, ok := indexedColumnEq(c); ok && col == scanCol {
				seekFound = true
				continue
			}
			if col, _, _, _, _, ok := indexedColumnRange(c); ok && col == scanCol {
				seekFound = true
				continue
			}
		}
		residual = append(residual, c)
	}
	return residual, extra
}
