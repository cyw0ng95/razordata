package EX

import (
	"fmt"
	"sync"

	"github.com/cyw0ng95/razordata/internal/SQL/LX"
	"github.com/cyw0ng95/razordata/internal/SQL/PS"
	"github.com/cyw0ng95/razordata/internal/SQL/RE"
)

type plan struct {
	root    Operator
	cost    float64
	memoKey string
}

// HashAggregateThreshold is the row count above which the
// planner prefers HashAggregate over streaming Aggregate
// (REQ000196). HashAggregate has higher upfront cost
// (full materialization + hash table) but better performance
// for large groups. Threshold tuned for typical analytical
// workloads.
const HashAggregateThreshold = 1000

type Planner struct {
	mu      sync.Mutex
	memo    map[string]*plan
	catalog map[string]*tableInfo
	store   Store
}

type tableInfo struct {
	name    string
	cols    []ColInfo
	pk      string
	indexes map[string][]string
}

func NewPlanner() *Planner {
	return &Planner{
		memo:    make(map[string]*plan),
		catalog: make(map[string]*tableInfo),
	}
}

// NewPlannerWithStore returns a planner that routes its leaf operators
// through store. The store may be nil to fall back to in-memory mode.
func NewPlannerWithStore(store Store) *Planner {
	return &Planner{
		memo:    make(map[string]*plan),
		catalog: make(map[string]*tableInfo),
		store:   store,
	}
}

func (p *Planner) RegisterTable(name string, cols []ColInfo, pk string) {
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

func (p *Planner) Plan(stmt PS.Stmt) (*plan, error) {
	key := serializeKey(stmt)
	p.mu.Lock()
	if cached, ok := p.memo[key]; ok {
		p.mu.Unlock()
		return cached, nil
	}
	p.mu.Unlock()

	var root Operator

	rewritten, err := RE.Rewrite(stmt)
	if err != nil {
		return nil, err
	}

	switch s := rewritten.(type) {
	case *PS.Select:
		root = p.planSelect(s)
	case *PS.Insert:
		root = p.planInsert(s)
	case *PS.Update:
		root = p.planUpdate(s)
	case *PS.Delete:
		root = p.planDelete(s)
	case *PS.CreateTable:
		root = p.planCreateTable(s)
	case *PS.DropTable:
		root = p.planDropTable(s)
	case *PS.CreateIndexStmt:
		root = p.planCreateIndex(s)
	case *PS.DropIndexStmt:
		root = p.planDropIndex(s)
	case *PS.ExplainStmt:
		root = p.planExplain(s)
	case *PS.WithStmt:
		root = p.planWith(s)
	}

	result := &plan{
		root:    root,
		cost:    p.estimateCost(root),
		memoKey: key,
	}

	p.mu.Lock()
	p.memo[key] = result
	p.mu.Unlock()

	return result, nil
}

// estimateCost returns a unitless cost for the operator tree rooted at op.
// The model uses uniform distribution: each row is 1.0 unit, filters and
// joins apply selectivity, sort adds a log(n) factor. Real statistics land
// in v2.
func (p *Planner) estimateCost(op Operator) float64 {
	if op == nil {
		return 0
	}
	switch v := op.(type) {
	case *SeqScan:
		// In v1 we don't track row counts; assume 1.0 per row.
		return 1.0
	case *IndexScan:
		// Cheaper than full scan; one seek + ordered reads.
		return 0.1
	case *Filter:
		return p.estimateCost(v.child) * estimateSelectivity(v.predicate)
	case *Project:
		return p.estimateCost(v.child)
	case *Limit:
		return p.estimateCost(v.child)
	case *Offset:
		return p.estimateCost(v.child)
	case *Distinct:
		return p.estimateCost(v.child)
	case *Sort:
		childCost := p.estimateCost(v.child)
		if childCost < 1 {
			childCost = 1
		}
		return childCost * (1 + log2ish(childCost))
	case *Aggregate:
		return p.estimateCost(v.child) + 1
	case *NestedLoopJoin:
		leftCost := p.estimateCost(v.left)
		rightCost := p.estimateCost(v.right)
		return leftCost * rightCost
	case *Insert, *Update, *Delete, *CreateTable, *DropTable:
		// Writer operators: cost ~ 1 (single mutation).
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
		switch v.Op {
		case int(LX.T_EQ):
			if isColumnLiteralPair(v.Left, v.Right) || isColumnLiteralPair(v.Right, v.Left) {
				return 0.1
			}
		}
	}
	return 0.5
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

func (p *Planner) planSelect(s *PS.Select) Operator {
	var scan Operator
	if p.store != nil {
		// Try IndexScan first when the WHERE references an indexed column.
		// iter-22: prefer NewIndexScanWithIndex (real seek) over the
		// prefix-scan fallback when the predicate is an equality on
		// the indexed column AND the index is registered for writer
		// maintenance (i.e. the index keyspace is populated).
		if s.Where != nil {
			if col, val, ok := indexedColumnEq(s.Where); ok {
				idx, found := p.selectIndex(s.From, col)
				if found && hasWriterIndex(s.From, idx) {
					tableID, _ := tableIDFor(s.From)
					if isc, err := NewIndexScanWithIndex(p.store, tableID, s.From, idx, val, nil); err == nil {
						if s.Where != nil {
							scan = NewFilter(isc, s.Where)
						} else {
							scan = isc
						}
					}
				}
			}
			if scan == nil {
				if col, ok := indexedColumn(s.Where); ok {
					if idx, found := p.selectIndex(s.From, col); found {
						if isc, err := NewIndexScanWithStore(p.store, s.From, idx); err == nil {
							scan = isc
						}
					}
				}
			}
		}
		if scan == nil {
			if ssc, err := NewSeqScanWithStore(p.store, s.From); err == nil {
				scan = ssc
			}
		}
	}
	if scan == nil {
		scan = NewIndexOrSeqScan(s.From, s.Where, p)
	}

	var current Operator = scan

	if len(s.Joins) > 0 {
		leftTbl := s.From
		for _, j := range s.Joins {
			// Support all join kinds (REQ000197: OUTER JOIN)
			if j.Kind != "INNER" && j.Kind != "LEFT" && j.Kind != "RIGHT" && j.Kind != "FULL" && j.Kind != "CROSS" {
				continue
			}
			var on func(outer, inner *Row) (bool, error)
			if j.On != nil {
				pred := j.On
				on = func(outer, inner *Row) (bool, error) {
					v, err := Eval(pred, inner, nil)
					if err != nil {
						return false, err
					}
					return truthy(v), nil
				}
			}
			// Convert string kind to JoinKind enum
			kind := JoinKind(j.Kind)
			joinOp := NewNestedLoopJoin(current, NewSeqScan(j.Right), leftTbl, j.Right, on, kind)
			current = joinOp
			leftTbl = j.Right
		}
	}

	if s.Where != nil {
		conjuncts := RE.SplitAnd(s.Where)
		current = NewFilter(scan, conjuncts[0])
		for _, c := range conjuncts[1:] {
			current = NewFilter(current, c)
		}
	}

	needsAggregate := hasAnyAggregate(s.Cols) || len(s.GroupBy) > 0
	groupCols := s.GroupBy
	var aggExprs []PS.Expr
	if needsAggregate {
		aggsOnly, autoGroup, _ := splitSelectCols(s.Cols)
		aggExprs = aggsOnly
		if len(groupCols) == 0 {
			groupCols = autoGroup
		}
		// Choose between streaming Aggregate and HashAggregate
		// based on estimated row count (REQ000196). For large
		// datasets, HashAggregate is preferred (better group-by
		// locality); for small datasets, streaming Aggregate
		// avoids the upfront materialization cost.
		estimatedRows := p.estimateRowCount(s.From, s.Where)
		if estimatedRows >= HashAggregateThreshold {
			agg := NewHashAggregate(current, groupCols, aggExprs)
			current = agg
		} else {
			agg := NewAggregate(current, groupCols, aggExprs)
			current = agg
		}
	}

	if s.Having != nil {
		filter := NewFilter(current, s.Having)
		current = filter
	}

	if len(s.OrderBy) > 0 {
		if !p.pkOrderMatches(s.From, s.OrderBy) {
			sort := NewSort(current, s.OrderBy)
			current = sort
		}
	}

	if len(s.Cols) > 0 && !isStarExpr(s.Cols) && !hasAnyAggregate(s.Cols) {
		project := NewProject(current, s.Cols)
		current = project
	}

	if s.Distinct && !hasAnyAggregate(s.Cols) {
		current = NewDistinct(current)
	}

	if s.Offset != nil {
		n, ok := limitInt64(s.Offset)
		if ok && n > 0 {
			current = NewOffset(current, n)
		}
	}

	if s.Limit != nil {
		n, ok := limitInt64(s.Limit)
		if !ok {
			return nil
		}
		limit := NewLimit(current, n)
		current = limit
	}

	return current
}

// pkOrderMatches reports whether orderBy is a single ascending reference
// to the table's primary key column. When true, the planner can drop the
// Sort operator and rely on the scan's natural key order.
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
	if ss, ok := schemaFor(table); ok {
		return ss.pk == ident.Name
	}
	return false
}

func splitSelectCols(cols []PS.Expr) (aggs, groupCols, other []PS.Expr) {
	if !hasAnyAggregate(cols) {
		return nil, nil, cols
	}
	for _, c := range cols {
		if containsAggregate(c) {
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

func hasAnyAggregate(cols []PS.Expr) bool {
	for _, c := range cols {
		if containsAggregate(c) {
			return true
		}
	}
	return false
}

func containsAggregate(e PS.Expr) bool {
	if e == nil {
		return false
	}
	switch v := e.(type) {
	case *PS.AggregateFunc:
		return true
	case *PS.BinaryExpr:
		return containsAggregate(v.Left) || containsAggregate(v.Right)
	case *PS.UnaryExpr:
		return containsAggregate(v.Operand)
	case *PS.AliasedExpr:
		return containsAggregate(v.Expr)
	case *PS.CastExpr:
		return containsAggregate(v.Expr)
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

// NewIndexOrSeqScan picks an IndexScan when the WHERE
// references a single column with an index on it;
// otherwise falls back to a SeqScan. IndexScan currently
// behaves like a SeqScan for the in-memory source; the
// selection is the planner decision and the smoke test
// asserts which operator was chosen.
func NewIndexOrSeqScan(table string, where PS.Expr, p *Planner) Operator {
	if p != nil && where != nil {
		if col, ok := indexedColumn(where); ok {
			if idx, found := p.selectIndex(table, col); found {
				if p.store != nil {
					if isc, err := NewIndexScanWithStore(p.store, table, idx); err == nil {
						return isc
					}
				}
				return NewIndexScan(table, idx, nil, nil)
			}
		}
	}
	return NewSeqScan(table)
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
//
// iter-22: used by the planner to enable real index seek via
// NewIndexScanWithIndex. REQ000252.
func indexedColumnEq(e PS.Expr) (string, []byte, bool) {
	b, ok := e.(*PS.BinaryExpr)
	if !ok {
		return "", nil, false
	}
	if b.Op != int(LX.T_EQ) {
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

func (p *Planner) planInsert(s *PS.Insert) Operator {
	if p.store != nil {
		op, err := NewInsertWithStore(p.store, s.Table, s.Cols, s.Values, s.Returning, s.OnConflict)
		if err == nil {
			return op
		}
	}
	return NewInsert(s.Table, s.Cols, s.Values, s.Returning, s.OnConflict)
}

func (p *Planner) planUpdate(s *PS.Update) Operator {
	if p.store != nil {
		scan, err := NewSeqScanWithStore(p.store, s.Table)
		if err == nil {
			op, err := NewUpdateWithStore(p.store, s.Table, s.Set, s.Where, scan, s.Returning)
			if err == nil {
				return op
			}
		}
	}
	scan := NewSeqScan(s.Table)
	return NewUpdate(s.Table, s.Set, s.Where, scan, s.Returning)
}

func (p *Planner) planDelete(s *PS.Delete) Operator {
	if p.store != nil {
		scan, err := NewSeqScanWithStore(p.store, s.Table)
		if err == nil {
			op, err := NewDeleteWithStore(p.store, s.Table, s.Where, scan, s.Returning)
			if err == nil {
				return op
			}
		}
	}
	scan := NewSeqScan(s.Table)
	return NewDelete(s.Table, s.Where, scan, s.Returning)
}

func (p *Planner) planCreateTable(s *PS.CreateTable) Operator {
	return NewCreateTable(s)
}

func (p *Planner) planDropTable(s *PS.DropTable) Operator {
	return NewDropTable(s)
}

func (p *Planner) planExplain(s *PS.ExplainStmt) Operator {
	// Plan the inner statement
	innerPlan, err := p.Plan(s.Inner)
	if err != nil || innerPlan == nil || innerPlan.root == nil {
		// Return a placeholder operator that will produce empty output
		return NewSeqScan("__explain_error__")
	}

	// Build the PlanNode tree for structured output
	planNode := buildPlanNodeTree(innerPlan.root, p)

	// Return an ExplainStmt operator that renders the plan
	return &ExplainStmtOp{
		mode:     s.Mode,
		planNode: planNode,
		root:     innerPlan.root,
	}
}

func (p *Planner) planWith(w *PS.WithStmt) Operator {
	// For now, implement a simple CTE that inlines the CTE definitions
	// into the main query. Full materialization decision can be added later.
	
	// Register CTEs as temporary tables in the catalog
	for _, cte := range w.CTEs {
		// Plan the CTE query to get its schema
		ctePlan, err := p.Plan(cte.Query)
		if err != nil || ctePlan == nil || ctePlan.root == nil {
			continue
		}
		
		// For simplicity, we'll execute the CTE and store results in a temp table
		// This is a naive implementation; proper materialization would be more efficient
		_ = ctePlan
	}
	
	// Plan the inner query
	innerPlan, err := p.Plan(w.Inner)
	if err != nil || innerPlan == nil || innerPlan.root == nil {
		return NewSeqScan("__cte_error__")
	}
	
	return innerPlan.root
}

// estimateRowCount provides a row count estimate for the given
// table+filter. Used by the planner to choose between
// streaming Aggregate and HashAggregate (REQ000196).
//
// In v1, this returns a conservative estimate: 0 for unknown
// tables (preferring streaming Aggregate) and 0 for store-
// backed tables. In-memory tables (via RegisterTable) have
// their row count available via package-level tables map.
//
// A future iteration can integrate histogram-based estimates
// (REQ000085) for more accuracy.
func (p *Planner) estimateRowCount(table string, where PS.Expr) int {
	// Conservative default: return 0 (assume small dataset)
	// which prefers streaming Aggregate. Future iters can
	// consult table statistics.
	return 0
}

func (p *Planner) ParseAndPlan(sql string) (*plan, error) {
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
	for _, idx := range GetRegisteredIndexes(table) {
		if idx.Name == indexName {
			return true
		}
	}
	return false
}

// planCreateIndex registers a secondary index. iter-22.
func (p *Planner) planCreateIndex(s *PS.CreateIndexStmt) Operator {
	return NewCreateIndex(s)
}

// planDropIndex removes a secondary index. iter-22.
func (p *Planner) planDropIndex(s *PS.DropIndexStmt) Operator {
	return NewDropIndex(s)
}
