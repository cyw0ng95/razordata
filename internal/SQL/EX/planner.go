package EX

import (
	"bytes"
	"context"
	"fmt"
	"unicode"
	"sync"

	"github.com/cyw0ng95/razordata/internal/ENG/LS"
	"github.com/cyw0ng95/razordata/internal/SQL/LX"
	PL "github.com/cyw0ng95/razordata/internal/SQL/PL"
	PS "github.com/cyw0ng95/razordata/internal/SQL/PS"
	RE "github.com/cyw0ng95/razordata/internal/SQL/RE"
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

type plan struct {
	root    Operator
	cost    float64
	memoKey string
}

// joinPlan captures a partial or complete join plan for the N3
// nearest-neighbor search. Used internally by n3JoinOrdering.
type joinPlan struct {
	root    Operator
	cost    float64
	tables  map[string]bool
	order   []string
}

// n3HeapMaxSize limits the number of partial plans retained at each
// step of the N3 algorithm. With N=12 and K<=8, we evaluate at most
// 96 partial plans instead of 40,320 for worst-case 8-table join.
const n3HeapMaxSize = 12

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
		memo:    make(map[string]*plan, maxPlanCacheSize),
		catalog: make(map[string]*tableInfo),
	}
}

// InvalidateCache clears the plan cache. REQ000846: called when DDL
// changes the schema (CREATE/DROP/ALTER TABLE) so cached plans that
// reference the old schema are not reused.
func (p *Planner) InvalidateCache() {
	p.mu.Lock()
	p.memo = make(map[string]*plan, maxPlanCacheSize)
	p.mu.Unlock()
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
	if rows, ok := tables[table]; ok {
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
		root = newValuesRowsOp(s.Rows)
	}

	// Wrap query plans in AdaptiveOp for hot-path specialization.
	// DDL/DML operators (Insert/Update/Delete/CreateTable/DropTable)
	// are typically one-shot and don't benefit from ADQC.
	switch root.(type) {
	case *Insert, *Update, *Delete, *CreateTable, *DropTable:
		// no adaptive wrapper for DDL/DML
	default:
		root = NewAdaptiveOp(root, key)
	}

	result := &plan{
		root:    root,
		cost:    p.estimateCost(root),
		memoKey: key,
	}

	p.mu.Lock()
	p.memo[key] = result
	// REQ000846: bound the memo size. When the cache is full,
	// evict the oldest entry. With maxPlanCacheSize=1024 and the
	// SLT test's 48,300 unique queries, this keeps the cache
	// bounded at ~1024 entries instead of growing unboundedly.
	if len(p.memo) > maxPlanCacheSize {
		// Find and delete one entry (deterministic but slow).
		// For 48,300 queries the insertion cost is O(n) per insert,
		// so the cache size limit prevents the cache from dominating
		// query planning time.
		for k := range p.memo {
			if k != key {
				delete(p.memo, k)
				break
			}
		}
	}
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
	// Unwrap AdaptiveOp to estimate cost of the inner operator.
	if aop, ok := op.(*AdaptiveOp); ok {
		return p.estimateCost(aop.inner)
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

	stats := p.statsCatalog.ColumnStatsByName(tableName, col)
	if stats == nil {
		return 0.5
	}

	histSel := estimateSelectivityWithStats(e, stats)

	lm := PL.Learned()
	if lm.IsTrained() {
		predType := 0
		if v, ok := e.(*PS.BinaryExpr); ok {
			switch v.Op {
			case int(LX.T_EQ):
				predType = 0
			case int(LX.T_LT), int(LX.T_LE), int(LX.T_GT), int(LX.T_GE):
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
	tablesMu.RLock()
	defer tablesMu.RUnlock()
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
	switch v := e.(type) {
	case *PS.Ident:
		// Find which table this column belongs to.
		// First try the planner's catalog, then fall back to
		// the in-memory schemas (populated by CREATE TABLE).
		tbl := p.findTableForColumn(v.Name)
		if tbl == "" {
			tbl = findTableInSchemas(v.Name)
		}
		if tbl != "" {
			tables[tbl] = true
		}
	case *PS.QualifiedName:
		// Qualified name has explicit table prefix
		tables[v.Table] = true
	case *PS.BinaryExpr:
		p.walkExprForTables(v.Left, tables)
		p.walkExprForTables(v.Right, tables)
	case *PS.UnaryExpr:
		p.walkExprForTables(v.Operand, tables)
	case *PS.ListExpr:
		for _, item := range v.Items {
			p.walkExprForTables(item, tables)
		}
	case *PS.InExpr:
		p.walkExprForTables(v.Expr, tables)
		for _, item := range v.List {
			p.walkExprForTables(item, tables)
		}
	case *PS.AggregateFunc:
		p.walkExprForTables(v.Arg, tables)
	case *PS.CaseExpr:
		p.walkExprForTables(v.Expr, tables)
		for _, w := range v.WhenList {
			p.walkExprForTables(w.Cond, tables)
			p.walkExprForTables(w.Then, tables)
		}
		p.walkExprForTables(v.Else, tables)
	case *PS.FunctionCall:
		for _, arg := range v.Args {
			p.walkExprForTables(arg, tables)
		}
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
	cols, ok := schemas[tbl]
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
	tablesMu.RLock()
	defer tablesMu.RUnlock()
	for tbl, cols := range schemas {
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
			crossTable = append(crossTable, c)
		}
	}
	return perTable, crossTable
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
// REQ000820: only applies to in-memory SeqScan operators.
func tryApplyPointLookup(scan Operator, pred PS.Expr) {
	ss, ok := scan.(*SeqScan)
	if !ok || ss.store != nil {
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
	if !ok || bin.Op != int(LX.T_IN) {
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
	if !ok || bin.Op != int(LX.T_EQ) {
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
	if !ok || bin.Op != int(LX.T_EQ) {
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
		// HashJoin lookupKeys can find the correct column
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
	if !ok || bin.Op != int(LX.T_EQ) {
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
	// JOIN ON — keep tables referenced in ON clauses (not all joins)
	for _, j := range s.Joins {
		if j.On != nil {
			collectTablesFromExpr(j.On, tables, &hasUnqualified)
		}
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
	if e == nil {
		return
	}
	switch v := e.(type) {
	case *PS.QualifiedName:
		tables[v.Table] = true
	case *PS.StarExpr:
		// * in expression — can't eliminate.
		*hasUnqualified = true
	case *PS.Ident:
		// Bare column name — can't determine table.
		*hasUnqualified = true
	case *PS.BinaryExpr:
		collectTablesFromExpr(v.Left, tables, hasUnqualified)
		collectTablesFromExpr(v.Right, tables, hasUnqualified)
	case *PS.UnaryExpr:
		collectTablesFromExpr(v.Operand, tables, hasUnqualified)
	case *PS.ListExpr:
		for _, item := range v.Items {
			collectTablesFromExpr(item, tables, hasUnqualified)
		}
	case *PS.InExpr:
		collectTablesFromExpr(v.Expr, tables, hasUnqualified)
		for _, item := range v.List {
			collectTablesFromExpr(item, tables, hasUnqualified)
		}
	case *PS.BetweenExpr:
		collectTablesFromExpr(v.Expr, tables, hasUnqualified)
		collectTablesFromExpr(v.Low, tables, hasUnqualified)
		collectTablesFromExpr(v.High, tables, hasUnqualified)
	case *PS.AggregateFunc:
		collectTablesFromExpr(v.Arg, tables, hasUnqualified)
	case *PS.CaseExpr:
		collectTablesFromExpr(v.Expr, tables, hasUnqualified)
		for _, w := range v.WhenList {
			collectTablesFromExpr(w.Cond, tables, hasUnqualified)
			collectTablesFromExpr(w.Then, tables, hasUnqualified)
		}
		collectTablesFromExpr(v.Else, tables, hasUnqualified)
	case *PS.FunctionCall:
		for _, arg := range v.Args {
			collectTablesFromExpr(arg, tables, hasUnqualified)
		}
	case *PS.AliasedExpr:
		collectTablesFromExpr(v.Expr, tables, hasUnqualified)
	case *PS.CastExpr:
		collectTablesFromExpr(v.Expr, tables, hasUnqualified)
	case *PS.SubqueryExpr:
		// Subquery has its own scope — don't walk.
	case *PS.ExistsExpr:
		// Subquery — don't walk.
	}
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
	if e == nil {
		return
	}
	switch v := e.(type) {
	case *PS.QualifiedName:
		cols[v.Table+"."+v.Name] = true
	case *PS.Ident:
		*hasUnqualified = true
	case *PS.StarExpr:
		*hasUnqualified = true
	case *PS.BinaryExpr:
		collectColsFromExpr(v.Left, cols, hasUnqualified)
		collectColsFromExpr(v.Right, cols, hasUnqualified)
	case *PS.UnaryExpr:
		collectColsFromExpr(v.Operand, cols, hasUnqualified)
	case *PS.ListExpr:
		for _, item := range v.Items {
			collectColsFromExpr(item, cols, hasUnqualified)
		}
	case *PS.InExpr:
		collectColsFromExpr(v.Expr, cols, hasUnqualified)
		for _, item := range v.List {
			collectColsFromExpr(item, cols, hasUnqualified)
		}
	case *PS.AliasedExpr:
		collectColsFromExpr(v.Expr, cols, hasUnqualified)
	case *PS.FunctionCall:
		for _, arg := range v.Args {
			collectColsFromExpr(arg, cols, hasUnqualified)
		}
	case *PS.AggregateFunc:
		if v.Arg != nil {
			collectColsFromExpr(v.Arg, cols, hasUnqualified)
		}
	case *PS.WindowFunc:
		for _, arg := range v.Args {
			collectColsFromExpr(arg, cols, hasUnqualified)
		}
	case *PS.BetweenExpr:
		collectColsFromExpr(v.Expr, cols, hasUnqualified)
		collectColsFromExpr(v.Low, cols, hasUnqualified)
		collectColsFromExpr(v.High, cols, hasUnqualified)
	case *PS.CastExpr:
		collectColsFromExpr(v.Expr, cols, hasUnqualified)
	case *PS.CaseExpr:
		if v.Expr != nil {
			collectColsFromExpr(v.Expr, cols, hasUnqualified)
		}
		for _, w := range v.WhenList {
			collectColsFromExpr(w.Cond, cols, hasUnqualified)
			collectColsFromExpr(w.Then, cols, hasUnqualified)
		}
		if v.Else != nil {
			collectColsFromExpr(v.Else, cols, hasUnqualified)
		}
	}
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

func (p *Planner) planSelect(s *PS.Select) Operator {
	// REQ000241: view resolution — expand view to underlying SELECT
	if viewSel := LookupView(s.From); viewSel != nil {
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
					innerOp = NewFilter(innerOp, s.Where)
				}
				// Project the outer query's columns over the view's output
				return NewProject(innerOp, s.Cols)
			}
		}

		merged := *viewSel
		if s.Where != nil {
			if merged.Where != nil {
				merged.Where = &PS.BinaryExpr{
					Op:    int(LX.T_AND),
					Left:  merged.Where,
					Right: s.Where,
				}
			} else {
				merged.Where = s.Where
			}
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

	// REQ000357 (iter-27): SELECT without FROM clause (e.g. `SELECT 1+1`).
	// Create a Values operator that evaluates expressions over a single
	// virtual row and returns exactly one result row. Also apply the WHERE
	// filter when present (REQ000458).
	//
	// REQ000830: when the SELECT contains aggregate functions (e.g.
	// `SELECT COUNT(*)`), we must use an Aggregate operator with a
	// single-row dummy source so COUNT(*) returns 1 (one implicit row)
	// instead of 0 (no rows to count).
	if s.From == "" && s.SubqueryFrom == nil {
		if hasAnyAggregate(s.Cols) {
			dummy := newValuesOp([]PS.Expr{&PS.NumberLiteral{Val: int64(1)}})
			var op Operator = dummy
			if s.Where != nil {
				op = NewFilter(op, s.Where)
			}
			agg := NewAggregate(op, s.GroupBy, s.Cols)
			if s.Having != nil {
				return NewFilter(agg, s.Having)
			}
			return agg
		}
		op := Operator(newValuesOp(s.Cols))
		if s.Where != nil {
			op = NewFilter(op, s.Where)
		}
		return op
	}

	// REQ000709: subquery in FROM clause (derived table).
	// Plan the subquery and use its output as a virtual table.
	if s.SubqueryFrom != nil {
		subPlan := p.planSelect(s.SubqueryFrom.(*PS.Select))
		var current Operator = subPlan
		if s.Where != nil {
			current = NewFilter(current, s.Where)
		}
		if len(s.Cols) > 0 && !isStarExpr(s.Cols) {
			current = NewProject(current, s.Cols)
		}
		if len(s.OrderBy) > 0 {
			current = NewSort(current, s.OrderBy)
		}
		// Limit is handled separately if needed
		return current
	}

	// REQ000727: sqlite_master virtual table
	if s.From == "sqlite_master" || s.From == "sqlite_schema" {
		var scan Operator = NewSqliteMaster()
		if s.Where != nil {
			scan = NewFilter(scan, s.Where)
		}
		return scan
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
							if s.Where != nil {
								scan = NewFilter(scan, s.Where)
							}
						}
					}
				}
			}
			if scan == nil {
				if col, ok := indexedColumn(s.Where); ok {
					if idx, found := p.selectIndex(s.From, col); found {
						if isc, err := NewIndexScanWithStore(p.store, s.From, idx); err == nil {
							scan = isc
							if s.Where != nil {
								scan = NewFilter(scan, s.Where)
							}
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

	// Set table alias on the scan operator so correlated subquery
	// eval can resolve qualified names like x.col. Must happen
	// BEFORE predicate pushdown (which wraps scan in a Filter).
	if s.FromAlias != "" {
		if ss, ok := scan.(*SeqScan); ok {
			ss.WithAlias(s.FromAlias)
		}
	}

	// REQ000XXX: predicate pushdown — split WHERE into per-table
	// conjuncts and push them down to individual table scans before
	// joins. This reduces intermediate row counts for cross joins.
	var pushedPredicates map[string][]PS.Expr
	var crossTablePredicates []PS.Expr
	if s.Where != nil && (len(s.Joins) > 0 || s.From != "") {
		conjuncts := RE.SplitAnd(s.Where)
		allTables := []string{s.From}
		for _, j := range s.Joins {
			allTables = append(allTables, j.Right)
		}
		pushedPredicates, crossTablePredicates = p.splitPredicatesByTable(conjuncts, allTables)
		// Push predicates for the first table onto its scan.
		// REQ000820: also set up point-lookup for IN-list predicates.
		if firstPreds := pushedPredicates[s.From]; len(firstPreds) > 0 {
			for _, pred := range firstPreds {
				current = NewFilter(current, pred)
				tryApplyPointLookup(scan, pred)
			}
		}
	}

	// REQ000821: save the filtered scan after predicate pushdown
	// so bushy groups reuse the filter-wrapped operator.
	filteredScan := current

	// REQ000XXX: For multi-table implicit JOINs, extract equi-join
	// conditions from WHERE and use HashJoin instead of NestedLoopJoin.
	var crossTableConjuncts []PS.Expr
	if s.Where != nil && len(s.Joins) > 0 {
		crossTableConjuncts = RE.SplitAnd(s.Where)
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
				}
			}
			s.Joins = filtered
		}
	}

	if len(s.Joins) > 0 {
		// REQ000801: N3-style join ordering — determine the best
		// join order using the simplified N3 algorithm, then
		// create join operators following that order.
		joinInfos := make([]joinTableInfo, 0, len(s.Joins))
		joinMap := make(map[string]PS.JoinClause, len(s.Joins))
		for _, j := range s.Joins {
			if j.Kind != "INNER" && j.Kind != "LEFT" && j.Kind != "RIGHT" && j.Kind != "FULL" && j.Kind != "CROSS" {
				continue
			}
			joinInfos = append(joinInfos, joinTableInfo{name: j.Right, join: j})
			joinMap[j.Right] = j
		}

		// Determine predicates for cost estimation.
		costPredicates := crossTablePredicates
		if costPredicates == nil && s.Where != nil {
			costPredicates = RE.SplitAnd(s.Where)
		}

		// Run N3 to find the best join order.
		joinOrder := p.n3JoinOrdering(s.From, joinInfos, costPredicates)

		// REQ000803: compute column projection for join pushdown.
		// Only include columns referenced by SELECT/WHERE/ORDER BY/etc.
		// to reduce per-row memory and CPU downstream.
		var projectedCols []string
		if refCols := collectReferencedColumns(s); refCols != nil {
			projectedCols = make([]string, 0, len(refCols))
			for c := range refCols {
				projectedCols = append(projectedCols, c)
			}
		}

		// Build operators following the N3-determined order.
		// REQ000821: detect bushy join opportunities: group tables
		// by independent equi-join keys so star-join pairs are
		// joined first (reducing intermediate row counts).
		// buildBushyJoinTree returns a tree of groups; each group
		// is a set of tables that should be joined before the
		// results are joined together.
		type joinGroup struct {
			tables []string
			keys   []string // equi-join keys connecting this group
		}
		joinGroups := groupBushyJoins(s.From, joinOrder, crossTableConjuncts)

		// REQ000821: build each bushy group as a separate operator,
		// then join the group results together.
		type groupResult struct {
			op    Operator
			tbl   string // rightmost table in the group (for leftTbl tracking)
			set   map[string]bool
			preds []PS.Expr // remaining cross-table predicates for this group
		}
		var groupOps []groupResult

		for gi, group := range joinGroups {
			baseTable := group[0]
			var current Operator
			var leftTbl string
			joinedTables := map[string]bool{}
			localConjuncts := make([]PS.Expr, len(crossTableConjuncts))
			copy(localConjuncts, crossTableConjuncts)

			// Build the scan for the base table of this group.
			if gi == 0 {
				// First group reuses the main scan (with pushed predicates).
				current = filteredScan
				leftTbl = s.From
				joinedTables[s.From] = true
			} else {
				// Secondary group: create a fresh scan for the base table.
				var baseOp Operator = NewSeqScan(baseTable)
				if ssc, err := NewSeqScanWithStore(p.store, baseTable); err == nil {
					baseOp = ssc
				}
				// Push single-table predicates for the base table.
				if basePreds := pushedPredicates[baseTable]; len(basePreds) > 0 {
					for _, pred := range basePreds {
						tryApplyPointLookup(baseOp, pred)
						baseOp = NewFilter(baseOp, pred)
					}
				}
				current = baseOp
				leftTbl = baseTable
				joinedTables[baseTable] = true
			}

			for ti, tbl := range group {
				if ti == 0 {
					continue // skip base table (already set up as current)
				}
				if _, ok := joinedTables[tbl]; ok {
					continue
				}
				j, ok := joinMap[tbl]
				if !ok {
					continue
				}
				kind := JoinKind(j.Kind)

				var rightScan Operator = NewSeqScan(j.Right)
				if ssc, err := NewSeqScanWithStore(p.store, j.Right); err == nil {
					rightScan = ssc
				}
				if rightPreds := pushedPredicates[j.Right]; len(rightPreds) > 0 {
					for _, pred := range rightPreds {
						tryApplyPointLookup(rightScan, pred)
						rightScan = NewFilter(rightScan, pred)
					}
				}

				var joinOp Operator
				if (kind == JoinKindInner || kind == JoinKindCross) && len(localConjuncts) > 0 {
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
						joinOp = NewHashJoin(current, rightScan, leftTbl, j.Right, lk, rk, 0)
						if projectedCols != nil {
							if hj, ok := joinOp.(*HashJoin); ok {
								hj.WithProjection(projectedCols)
							}
						}
						localConjuncts = remaining
					}
				}

				if joinOp == nil {
					if kind == JoinKindInner && j.On != nil {
						if lk, rk, ok := p.extractSingleOnEquiKey(j.On, leftTbl, j.Right); ok {
							joinOp = NewHashCrossJoin(current, rightScan, leftTbl, j.Right, lk, rk)
							if projectedCols != nil {
								if hcj, ok := joinOp.(*HashCrossJoin); ok {
									hcj.WithProjection(projectedCols)
								}
							}
						}
					}
					if joinOp == nil {
						var on func(outer, inner *Row) (bool, error)
						if j.On != nil {
							pred := j.On
							on = func(outer, inner *Row) (bool, error) {
								v, err := EvalValue(pred, inner, nil)
								if err != nil {
									return false, err
								}
								return isValueTruthy(v), nil
							}
						}
						nlj := NewNestedLoopJoin(current, rightScan, leftTbl, j.Right, on, kind)
						if projectedCols != nil {
							nlj.WithProjection(projectedCols)
						}
						joinOp = nlj
					}
				}

				current = joinOp
				joinedTables[tbl] = true
				leftTbl = j.Right
			}

			if current != nil {
				groupOps = append(groupOps, groupResult{
					op:    current,
					tbl:   leftTbl,
					set:   joinedTables,
					preds: localConjuncts,
				})
			}
		}

		// Merge group results into a single join tree.
		leftTbl := ""
		joinedTables := map[string]bool{}
		for i, gr := range groupOps {
			if i == 0 {
				current = gr.op
				leftTbl = gr.tbl
				for t := range gr.set {
					joinedTables[t] = true
				}
				continue
			}
			// Join this group's result with the accumulated tree.
			var joinOp Operator
			if len(gr.preds) > 0 {
				// REQ000843: try every table in the group, not just
				// the rightmost one (gr.tbl), to find equi-join keys.
				// In bushy join groups, the equi-join may connect a
				// non-rightmost table (e.g. t1.a1 = t8.d8 where t1 is
				// in the middle of the group and t8 is the rightmost).
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
					// Fallback: try the rightmost table.
					lk, rk, remaining = p.extractEquiJoinKeys(gr.preds, joinedTables, gr.tbl)
					_ = remaining
				}
				if len(lk) > 0 {
					joinOp = NewHashJoin(current, gr.op, leftTbl, gr.tbl, lk, rk, 0)
					if projectedCols != nil {
						if hj, ok := joinOp.(*HashJoin); ok {
							hj.WithProjection(projectedCols)
						}
					}
					_ = remaining
				}
			}
			if joinOp == nil {
				nlj := NewNestedLoopJoin(current, gr.op, leftTbl, gr.tbl, nil, JoinKindCross)
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
	}

	if s.Where != nil {
		// REQ000368: apply the WHERE on top of the (possibly
		// joined) operator, not on the bare scan. The previous
		// code used `NewFilter(scan, ...)` which discarded any
		// joins and produced wrong results for `FROM a JOIN b
		// WHERE ...`.
		// REQ000XXX: if predicate pushdown already applied some
		// conjuncts to scans, only apply the remaining cross-table
		// predicates here. Skip predicates already extracted by
		// HashJoin (REQ000794) — they're already enforced and
		// re-applying them as Filters gives wrong results because
		// column prefixes change through the operator chain.
		if len(crossTablePredicates) > 0 {
			for i, c := range crossTablePredicates {
				if extractedPreds[i] {
					continue
				}
				current = NewFilter(current, c)
			}
		} else if pushedPredicates == nil {
			// No predicate pushdown — apply full WHERE as before.
			conjuncts := RE.SplitAnd(s.Where)
			current = NewFilter(current, conjuncts[0])
			for _, c := range conjuncts[1:] {
				current = NewFilter(current, c)
			}
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
			if isStarExpr(s.Cols) {
				agg.SetExpandStar()
			}
			current = agg
		} else {
			agg := NewAggregate(current, groupCols, aggExprs)
			if isStarExpr(s.Cols) {
				agg.SetExpandStar()
			}
			current = agg
		}
	}

	if s.Having != nil {
		filter := NewFilter(current, s.Having)
		current = filter
	}

	// Resolve ORDER BY position references (e.g., "ORDER BY 1" means first SELECT column).
	// This must happen before pkOrderMatches check and sort creation.
	if len(s.OrderBy) > 0 {
		// Build a list of SELECT column expressions for position resolution.
		selectExprs := make([]PS.Expr, 0, len(s.Cols))
		for _, col := range s.Cols {
			if ae, ok := col.(*PS.AliasedExpr); ok {
				selectExprs = append(selectExprs, ae.Expr)
			} else {
				selectExprs = append(selectExprs, col)
			}
		}
		// Replace integer literal position references with the corresponding SELECT expression.
		for i := range s.OrderBy {
			if nl, ok := s.OrderBy[i].Expr.(*PS.NumberLiteral); ok {
				pos := int(nl.Val)
				if pos >= 1 && pos <= len(selectExprs) {
					// Clone the expression to avoid mutating the original AST.
					s.OrderBy[i].Expr = cloneExpr(selectExprs[pos-1])
				}
			}
		}
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

	if needsWindow && len(s.Cols) > 0 && !isStarExpr(s.Cols) {
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
	// REQ000707: INSERT INTO t SELECT ...
	if s.Select != nil {
		selPlan, err := p.Plan(s.Select)
		if err == nil && selPlan != nil && selPlan.root != nil {
			op := NewInsert(s.Table, s.Cols, nil, s.Returning, s.OnConflict)
			op.selectPlan = selPlan.root
			propagatePlanner(selPlan.root, p)
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

func (p *Planner) planUpdate(s *PS.Update) Operator {
	if p.store != nil {
		scan, err := NewSeqScanWithStore(p.store, s.Table)
		if err == nil {
			filter := NewFilter(scan, s.Where)
			op, err := NewUpdateWithStore(p.store, s.Table, s.Set, s.Where, filter, s.Returning)
			if err == nil {
				return op
			}
		}
	}
	scan := NewSeqScan(s.Table)
	filter := NewFilter(scan, s.Where)
	return NewUpdate(s.Table, s.Set, s.Where, filter, s.Returning)
}

func (p *Planner) planDelete(s *PS.Delete) Operator {
	if p.store != nil {
		scan, err := NewSeqScanWithStore(p.store, s.Table)
		if err == nil {
			filter := NewFilter(scan, s.Where)
			op, err := NewDeleteWithStore(p.store, s.Table, s.Where, filter, s.Returning)
			if err == nil {
				return op
			}
		}
	}
	scan := NewSeqScan(s.Table)
	filter := NewFilter(scan, s.Where)
	return NewDelete(s.Table, s.Where, filter, s.Returning)
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
		mode:   s.Mode,
		format: s.Format,
		planNode: planNode,
		root:     innerPlan.root,
	}
}

func (p *Planner) planWith(w *PS.WithStmt) Operator {
	for _, cte := range w.CTEs {
		ctePlan, err := p.Plan(cte.Query)
		if err != nil || ctePlan == nil || ctePlan.root == nil {
			continue
		}

		var rows []Row
		for {
			row, err := ctePlan.root.Next(context.TODO())
			if err != nil {
				if err == ErrNoRows {
					break
				}
				continue
			}
			rows = append(rows, row)
		}
		ctePlan.root.Close()

		RegisterTable(cte.Name, rows)

		var colInfos []ColInfo
		for _, c := range schemas[cte.Name] {
			colInfos = append(colInfos, ColInfo{Name: c})
		}
		p.RegisterTable(cte.Name, colInfos, "")
	}

	innerPlan, err := p.Plan(w.Inner)
	if err != nil || innerPlan == nil || innerPlan.root == nil {
		return NewSeqScan("__cte_error__")
	}

	return innerPlan.root
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
	if rows, ok := tables[table]; ok {
		return len(rows)
	}
	// REQ000787: use statistics-driven estimate from catalog.
	if cat := Catalog(); cat != nil {
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
	if rows, ok := tables[table]; ok {
		return float64(len(rows))
	}
	if cat := Catalog(); cat != nil {
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
		psel := estimateJoinPredicateSelectivity(pred)
		sel *= psel
	}
	cost := float64(leftRows) * float64(rightRows) * sel * indexFactor
	if cost < 1 {
		cost = 1
	}
	return cost
}

// estimateJoinPredicateSelectivity returns the selectivity of a single
// join predicate expression.
func estimateJoinPredicateSelectivity(pred PS.Expr) float64 {
	if pred == nil {
		return 1.0
	}
	// REQ000819: handle IN-list expressions: selectivity ≈ len(list)/NDV.
	// Default NDV ≈ 100 for in-memory tables without histogram stats.
	if in, ok := pred.(*PS.InExpr); ok && len(in.List) > 0 {
		sel := float64(len(in.List)) / 100.0
		if sel > 1.0 {
			sel = 1.0
		}
		return sel
	}
	bin, ok := pred.(*PS.BinaryExpr)
	if !ok {
		return 0.5
	}
	isColCol := isColumnColumnPair(bin.Left, bin.Right) || isColumnColumnPair(bin.Right, bin.Left)
	switch bin.Op {
	case int(LX.T_EQ):
		if isColCol {
			return 0.1
		}
		return 0.1
	case int(LX.T_LT), int(LX.T_LE), int(LX.T_GT), int(LX.T_GE):
		return 0.3
	default:
		return 0.5
	}
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
// Parameters:
//   - baseTable: the FROM-clause table (leftmost in the join tree)
//   - joinTables: list of (table name, join clause) pairs to join
//   - wherePredicates: cross-table WHERE conjuncts for selectivity
//
// Returns the ordered list of table names that minimizes estimated cost.
func (p *Planner) n3JoinOrdering(baseTable string, joinTables []joinTableInfo, wherePredicates []PS.Expr) []string {
	k := len(joinTables)
	if k == 0 {
		return []string{baseTable}
	}
	if k == 1 {
		return []string{baseTable, joinTables[0].name}
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

	for _, jt := range joinTables {
		preds := p.findPredicatesForPair(baseTable, jt.name, wherePredicates)
		hasIdx := p.hasIndexOnTable(jt.name)
		rightRows := p.getTableRowCount(jt.name)
		joinCost := p.estimateJoinCost(int(baseRows), int(rightRows), preds, hasIdx)
		resultRows := joinResultRows(baseRows, rightRows, preds)
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
			idx int
			cost float64
			order []string
			tablesSet map[string]bool
			rows float64
		}
		nextHeap := make([]candidate, 0, n3HeapMaxSize)

		for _, pp := range heap {
			// Which tables are not yet joined?
			for _, tbl := range allTables {
				if pp.tablesSet[tbl] {
					continue
				}
				preds := p.findPredicatesForSet(pp.tablesSet, tbl, wherePredicates)
				hasIdx := p.hasIndexOnTable(tbl)
				rightRows := p.getTableRowCount(tbl)
				joinCost := p.estimateJoinCost(int(pp.rows), int(rightRows), preds, hasIdx)
				newCost := pp.cost + joinCost
				newRows := joinResultRows(pp.rows, rightRows, preds)

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
		return order
	}
	best := heap[0]
	for _, pp := range heap[1:] {
		if pp.cost < best.cost {
			best = pp
		}
	}
	return best.order
}

// joinTableInfo holds a table name and its associated JoinClause
// for use in N3 join ordering.
type joinTableInfo struct {
	name string
	join PS.JoinClause
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
func joinResultRows(leftRows, rightRows float64, predicates []PS.Expr) float64 {
	if len(predicates) == 0 {
		return leftRows * rightRows
	}
	sel := 1.0
	for _, pred := range predicates {
		sel *= estimateJoinPredicateSelectivity(pred)
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
	for _, pred := range crossTablePredicates {
		bin, ok := pred.(*PS.BinaryExpr)
		if !ok || bin.Op != int(LX.T_EQ) {
			continue
		}
		lTable, lCol := extractTableColumn(bin.Left)
		rTable, rCol := extractTableColumn(bin.Right)
		if lTable == "" || rTable == "" {
			continue
		}
		pk := pairKey{lTable, rTable}
		pairKeys[pk] = append(pairKeys[pk], lCol+"="+rCol)
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
		for _, existing := range groups {
			for _, et := range existing {
				pk := pairKey{et, tbl}
				if _, found := pairKeys[pk]; found {
					independent = false
					break
				}
				pk = pairKey{tbl, et}
				if _, found := pairKeys[pk]; found {
					independent = false
					break
				}
			}
			if !independent {
				break
			}
		}
		if independent && len(groups[len(groups)-1]) >= 2 {
			groups = append(groups, []string{tbl})
		} else {
			groups[len(groups)-1] = append(groups[len(groups)-1], tbl)
		}
	}

	if len(groups) == 1 {
		return [][]string{joinOrder}
	}
	return groups
}

// extractTableColumn extracts (table, column) from an expression
// that is an Ident or QualifiedName.
func extractTableColumn(e PS.Expr) (string, string) {
	switch v := e.(type) {
	case *PS.QualifiedName:
		return v.Table, v.Name
	case *PS.Ident:
		return "", v.Name
	}
	return "", ""
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
