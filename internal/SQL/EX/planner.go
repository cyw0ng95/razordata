package EX

import (
	"bytes"
	"fmt"
	"sync"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
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
	// statsCatalog provides access to column statistics for
	// histogram-based selectivity estimation. REQ000085.
	statsCatalog StatsCatalog
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

// NewPlannerWithStats returns a planner with store and stats catalog
// access for histogram-based selectivity estimation. REQ000085.
func NewPlannerWithStats(store Store, statsCatalog StatsCatalog) *Planner {
	return &Planner{
		memo:         make(map[string]*plan),
		catalog:      make(map[string]*tableInfo),
		store:        store,
		statsCatalog: statsCatalog,
	}
}

// SetStatsCatalog wires a stats catalog into an existing planner.
// REQ000085.
func (p *Planner) SetStatsCatalog(statsCatalog StatsCatalog) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.statsCatalog = statsCatalog
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
	case *PS.DropTable:
		root = p.planDropTable(s)
	case *PS.CreateIndexStmt:
		root = p.planCreateIndex(s)
	case *PS.DropIndexStmt:
		root = p.planDropIndex(s)
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
		// REQ000156 (iter-27): the cost depends on the scan
		// mode. Real index seek (indexMode=true) is the
		// cheapest; range seek is slightly more expensive;
		// full prefix read is the most expensive of the
		// index paths but still cheaper than SeqScan.
		if v.indexMode {
			return 0.05
		}
		return 0.1
	case *Filter:
		return p.estimateCost(v.child) * p.estimatePredicateSelectivity(v.predicate)
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
		if isColumnLiteralPair(v.Left, v.Right) || isColumnLiteralPair(v.Right, v.Left) {
			switch v.Op {
			case int(LX.T_EQ):
				return 0.1
			}
		}
	}
	return 0.5
}

// estimateSelectivityWithStats computes selectivity using column
// histograms when available, falling back to uniform distribution.
// REQ000085.
//
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
			case int(LX.T_EQ):
				return estimateEqSelectivity(stats, lit)
			case int(LX.T_LT), int(LX.T_LE):
				return estimateRangeSelectivity(stats, nil, lit, false)
			case int(LX.T_GT), int(LX.T_GE):
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

	stats := p.statsCatalog.GetStatsByName(tableName, col)
	if stats == nil {
		return 0.5
	}

	return estimateSelectivityWithStats(e, stats)
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
	// REQ000241: view resolution — expand view to underlying SELECT
	if viewSel := LookupView(s.From); viewSel != nil {
		return p.planSelect(viewSel)
	}

	// REQ000357 (iter-27): SELECT without FROM clause (e.g. `SELECT 1+1`).
	// Create a Values operator that evaluates expressions over a single
	// virtual row and returns exactly one result row.
	if s.From == "" {
		return newValuesOp(s.Cols)
	}

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
			// REQ000074 (iter-27): range seek for non-equality
			// predicates on an indexed column. Replaces the
			// prefix-scan fallback that the planner used before
			// for `col > X`, `col BETWEEN X AND Y`, etc.
			if scan == nil {
				if col, lo, loIncl, up, upIncl, ok := indexedColumnRange(s.Where); ok {
					idx, found := p.selectIndex(s.From, col)
					if found && hasWriterIndex(s.From, idx) {
						tableID, _ := tableIDFor(s.From)
						if isc, err := NewIndexScanWithRange(p.store, tableID, s.From, idx, lo, loIncl, up, upIncl); err == nil {
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

	// REQ000156 (iter-27): cost-based scan selection. If the
	// planner produced a SeqScan but an IndexScan on the
	// predicate column would be cheaper, swap the scan.
	if s.Where != nil && s.From != "" {
		if alt, ok := p.pickCheaperScan(s.From, s.Where, scan); ok && alt != nil {
			scan = alt
		}
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
			// REQ000368: prefer the store-backed SeqScan for
			// the right side so joins over engine tables
			// actually see the rows. Falls back to the
			// in-memory SeqScan if the right table isn't
			// registered for storage.
			var rightScan Operator = NewSeqScan(j.Right)
			if ssc, err := NewSeqScanWithStore(p.store, j.Right); err == nil {
				rightScan = ssc
			}
			joinOp := NewNestedLoopJoin(current, rightScan, leftTbl, j.Right, on, kind)
			current = joinOp
			leftTbl = j.Right
		}
	}

	if s.Where != nil {
		// REQ000368: apply the WHERE on top of the (possibly
		// joined) operator, not on the bare scan. The previous
		// code used `NewFilter(scan, ...)` which discarded any
		// joins and produced wrong results for `FROM a JOIN b
		// WHERE ...`.
		conjuncts := RE.SplitAnd(s.Where)
		current = NewFilter(current, conjuncts[0])
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

	needsWindow := hasAnyWindowFunc(s.Cols)
	if needsWindow {
		for _, col := range s.Cols {
			if wf, ok := col.(*PS.WindowFunc); ok {
				args := make([]PS.Expr, len(wf.Args))
				copy(args, wf.Args)
				cols := make([]string, 0)
				cols = append(cols, "*")
				winOp := NewWindowOperator(current, wf.Name, args, wf.Over, cols)
				current = winOp
			}
		}
	}

	if len(s.Cols) > 0 && !isStarExpr(s.Cols) && !hasAnyAggregate(s.Cols) && !needsWindow {
		project := NewProject(current, s.Cols)
		current = project
	}

	if s.Distinct && !hasAnyAggregate(s.Cols) {
		current = NewDistinct(current)
	}

	// REQ000521: LIMIT/OFFSET wrapping order depends on which
	// keyword appeared first in the SQL. The parser tracks this
	// in s.OffsetFirst.
	if s.OffsetFirst {
		// OFFSET m LIMIT n → Limit wraps scan, then Offset wraps that
		if s.Limit != nil {
			n, ok := limitInt64(s.Limit)
			if !ok {
				return nil
			}
			current = NewLimit(current, n)
		}
		if s.Offset != nil {
			n, ok := limitInt64(s.Offset)
			if ok && n > 0 {
				current = NewOffset(current, n)
			}
		}
	} else {
		// LIMIT n OFFSET m (standard) → Offset wraps scan, then Limit wraps that
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
			current = NewLimit(current, n)
		}
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
	case *PS.WindowFunc:
		return false
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

func hasAnyWindowFunc(cols []PS.Expr) bool {
	for _, c := range cols {
		if containsWindowFunc(c) {
			return true
		}
	}
	return false
}

func containsWindowFunc(e PS.Expr) bool {
	if e == nil {
		return false
	}
	switch v := e.(type) {
	case *PS.WindowFunc:
		return true
	case *PS.BinaryExpr:
		return containsWindowFunc(v.Left) || containsWindowFunc(v.Right)
	case *PS.UnaryExpr:
		return containsWindowFunc(v.Operand)
	case *PS.AliasedExpr:
		return containsWindowFunc(v.Expr)
	case *PS.CastExpr:
		return containsWindowFunc(v.Expr)
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

// pickCheaperScan returns a cheaper scan alternative for the
// given WHERE predicate, if one exists. The function builds
// both a SeqScan and an IndexScan candidate and returns the
// lower-cost one. REQ000156 (iter-27).
//
// The cost model is simple but effective:
//   - SeqScan: 1.0 unit per row
//   - IndexScan: 0.1 unit per row, multiplied by predicate
//     selectivity (so a high-selectivity predicate on an
//     indexed column strongly prefers IndexScan)
//
// If no index exists on the WHERE column, the function
// returns the original scan unchanged. If the cost of the
// index scan is not lower, the original scan is returned.
func (p *Planner) pickCheaperScan(table string, where PS.Expr, current Operator) (Operator, bool) {
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
	// avoids picking an IndexScan whose keyspace has not been
	// backfilled (the existing planSelect code already uses
	// hasWriterIndex for the same reason).
	if !hasWriterIndex(table, idx) {
		return current, false
	}
	// Build a candidate IndexScan.
	var indexScan Operator
	if p.store != nil {
		if isc, err := NewIndexScanWithStore(p.store, table, idx); err == nil {
			indexScan = isc
		}
	}
	if indexScan == nil {
		indexScan = NewIndexScan(table, idx, nil, nil)
	}
	if indexScan == nil {
		return current, false
	}
	// Wrap both scans in a Filter so the cost reflects the
	// post-filter work, matching how they will actually run.
	seqCandidate := NewFilter(current, where)
	idxCandidate := NewFilter(indexScan, where)
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
func rangeFromOp(op int, v []byte) (lower []byte, lowerIncl bool, upper []byte, upperIncl bool, hasBounds bool) {
	switch op {
	case int(LX.T_GT):
		return v, false, nil, false, true
	case int(LX.T_GE):
		return v, true, nil, false, true
	case int(LX.T_LT):
		return nil, false, v, false, true
	case int(LX.T_LE):
		return nil, false, v, true, true
	}
	return nil, false, nil, false, false
}

// flipOp mirrors a comparison: `5 < col` becomes `col > 5`.
// The token table uses distinct constants for each op, so we map
// each one explicitly.
func flipOp(op int) int {
	switch op {
	case int(LX.T_LT):
		return int(LX.T_GT)
	case int(LX.T_LE):
		return int(LX.T_GE)
	case int(LX.T_GT):
		return int(LX.T_LT)
	case int(LX.T_GE):
		return int(LX.T_LE)
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
	if s.Select != nil {
		// CREATE TABLE AS SELECT: plan the inner SELECT and
		// wrap both in a CreateTable operator. REQ000520.
		innerPlan, innerErr := p.Plan(s.Select)
		if innerErr == nil && innerPlan != nil && innerPlan.root != nil {
			return NewCreateTableAs(s, innerPlan.root)
		}
	}
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
// Note: the planner's cost-based selection (REQ000156) is
// keyed off ex.RegisterIndex, not CREATE INDEX. CREATE INDEX
// does not backfill existing rows into the index keyspace, so
// the planner cannot rely on the index for query plans
// triggered by CREATE INDEX.
func (p *Planner) planCreateIndex(s *PS.CreateIndexStmt) Operator {
	return NewCreateIndex(s)
}

// planDropIndex removes a secondary index. iter-22.
func (p *Planner) planDropIndex(s *PS.DropIndexStmt) Operator {
	return NewDropIndex(s)
}

// planPragma handles PRAGMA statements. REQ000261.
func (p *Planner) planPragma(s *PS.PragmaStmt) Operator {
	switch s.Name {
	case "integrity_check":
		return NewIntegrityCheckWithStore(p.store)
	case "cache_size", "journal_mode", "synchronous", "user_version":
		// REQ000242: return pragma value as a single-row result
		return NewPragmaResult(s.Name, s.Value)
	default:
		return NewSeqScan("__pragma_unknown__")
	}
}

// planAnalyze collects table statistics. REQ000258.
func (p *Planner) planAnalyze(s *PS.AnalyzeStmt) Operator {
	return NewAnalyze(s)
}

// planVacuum reclaims storage. REQ000257.
func (p *Planner) planVacuum(s *PS.VacuumStmt) Operator {
	return NewVacuumWithStore(s, p.store)
}

// planCompound dispatches a UNION/UNION ALL/INTERSECT/EXCEPT
// statement. REQ000383.
func (p *Planner) planCompound(s *PS.CompoundStmt) Operator {
	left := p.planSubStmt(s.Left)
	right := p.planSubStmt(s.Right)
	op := NewCompoundOp(left, right, s.Op, s.OrderBy, s.Limit, s.Offset)
	return op
}

// planSubStmt is a sub-dispatcher for the inner Stmt of a
// CompoundStmt. REQ000383.
func (p *Planner) planSubStmt(stmt PS.Stmt) Operator {
	switch s := stmt.(type) {
	case *PS.Select:
		return p.planSelect(s)
	case *PS.CompoundStmt:
		return p.planCompound(s)
	}
	return nil
}
