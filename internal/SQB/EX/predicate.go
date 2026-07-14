package EX

import (
	"strings"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	OP "github.com/cyw0ng95/razordata/internal/SQB/OP"
	CO "github.com/cyw0ng95/razordata/internal/SQO/CO"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// predicateCost returns a heuristic cost for evaluating a predicate
// expression. Lower cost = evaluated first. REQ001248.
//
// Cost scale:
//   - Ident/Literal = 1 (trivial column read / constant)
//   - Comparison (=, !=, <, <=, >, >=) = 2 + children
//   - Arithmetic (+, -, *, /, etc.) = 3 + children
//   - Unary NOT = 1 + child; unary arithmetic = 2 + child
//   - Function call = 5 + children
//   - InExpr = 5 + children
//   - BETWEEN = 4 + children
//   - Cast = 3 + child
//   - CASE = 3 + branches
//   - LIKE/GLOB = 10 + children (expensive string matching)
//   - EXISTS/Subquery = 100 (expensive)
//   - AND = sum of children (reordered for short-circuit)
//   - OR = sum of children
func predicateCost(e PS.Expr) int {
	return CO.Cost(e)
}

func funcArgCost(args []PS.Expr) int {
	return CO.FuncArgCost(args)
}

func caseExprCost(e *PS.CaseExpr) int {
	return CO.CaseExprCost(e)
}

// reorderIndices returns indices 0..n-1 sorted by the cost of
// predicates[indices[i]] ascending. Equal-cost predicates preserve
// original order (stable sort). REQ001248.
//
// Returns nil when n <= 1 (no reordering needed).
func reorderIndices(predicates []PS.Expr) []int {
	return CO.ReorderIndices(predicates)
}

func orderSlice(predicates []PS.Expr, indices []int) []PS.Expr {
	return CO.OrderSlice(predicates, indices)
}

func (p *Planner) splitAnd(expr PS.Expr) []PS.Expr {
	if p.splitAndCache == nil {
		p.splitAndCache = make(map[uintptr][]PS.Expr, 8)
	}
	return CO.SplitAnd(expr, p.splitAndCache)
}

// InvalidateCache clears the plan cache. REQ000846: called when DTL
// changes the schema (CREATE/DROP/ALTER TABLE) so cached plans that
// reference the old schema are not reused.
func extractColumnLiteral(v *PS.BinaryExpr) (string, []byte, bool) {
	return CO.ExtractColumnLiteral(v)
}

func literalToBytes(e PS.Expr) ([]byte, bool) {
	return CO.LiteralToBytes(e)
}

func extractColumnLiteralExpr(e PS.Expr) (string, []byte, bool) {
	return CO.ExtractColumnLiteralExpr(e)
}

func (p *Planner) estimatePredicateSelectivity(e PS.Expr) float64 {
	return CO.EstimatePredicateSelectivity(e, p.findTableForColumn, p.statsCatalog, estimateSelectivityWithStats)
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
	CO.WalkExpr(e, fn)
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
	return CO.SplitAlphaNum(s)
}

// resolveTableForColumn tries to resolve a column name using the
// SLT naming convention: "e8" => column "e" of table "t8".
// Must be called under tablesMu.RLock.
func resolveTableForColumn(col string) string {
	return CO.ResolveTableForColumn(col, DT.Schemas)
}

// findTableInSchemas searches the in-memory schemas (populated
// by CREATE TABLE) to find which table owns the given column.
// Returns empty string if not found.
func findTableInSchemas(col string) string {
	DT.TablesMu.RLock()
	defer DT.TablesMu.RUnlock()
	return CO.FindTableInSchemas(col, DT.Schemas)
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

// tryApplyPointLookup checks if pred is a col IN (literal, ...),
// col = literal, or same-column OR-chain of equalities, and if so,
// sets up SeqScan point-lookup so we skip the row-by-row filter
// and read only matching rows directly.
// REQ000820 + REQ001218.
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
	// REQ001218: same-column OR-chain of equalities — synthesize
	// the equivalent IN-list and use point-lookup.
	col, values, ok = extractOrChainEquality(pred)
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
// are literals. Handles both `*PS.InExpr` (parser output for
// `col IN (...)`) and `*PS.BinaryExpr{Op: T_IN}` (legacy form).
// REQ001218: now also accepts the InExpr form that the parser
// actually produces — the previous BinaryExpr-only check made
// point-lookup for IN-lists dead code.
func extractInListValues(pred PS.Expr) (string, []any, bool) {
	var col *PS.Ident
	var items []PS.Expr
	switch p := pred.(type) {
	case *PS.InExpr:
		ident, ok := p.Expr.(*PS.Ident)
		if !ok {
			return "", nil, false
		}
		col = ident
		items = p.List
	case *PS.BinaryExpr:
		if p.Op != LX.T_IN {
			return "", nil, false
		}
		ident, ok := p.Left.(*PS.Ident)
		if !ok {
			return "", nil, false
		}
		col = ident
		list, ok := p.Right.(*PS.ListExpr)
		if !ok {
			return "", nil, false
		}
		items = list.Items
	default:
		return "", nil, false
	}
	if len(items) == 0 {
		return "", nil, false
	}
	values := make([]any, 0, len(items))
	for _, item := range items {
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

// extractOrChainEquality extracts (columnName, values, ok) from a
// same-column OR-chain of equality predicates, e.g.
// `(e8=180 OR e8=333 OR e8=38 OR e8=349)` or
// `(180=e8 OR 333=e8 OR e8=38 OR e8=349)` (literals can appear on
// either side). Returns (colName, [val1, val2, ...], true) when all
// leaves are `col = literal` on the same column; otherwise
// ("", nil, false). REQ001218.
func extractOrChainEquality(pred PS.Expr) (string, []any, bool) {
	leaves := flattenOr(pred)
	if len(leaves) < 2 {
		// single equality: leave it to extractSingleEquality
		return "", nil, false
	}
	var colName string
	values := make([]any, 0, len(leaves))
	for _, leaf := range leaves {
		c, v, ok := extractEqualityAnySide(leaf)
		if !ok {
			return "", nil, false
		}
		if colName == "" {
			colName = c
		} else if !strings.EqualFold(colName, c) {
			return "", nil, false
		}
		values = append(values, v)
	}
	return colName, values, true
}

// extractEqualityAnySide extracts (columnName, value, ok) from
// `col = literal` OR `literal = col`. Both operands may be the
// column reference. Used by extractOrChainEquality.
func extractEqualityAnySide(pred PS.Expr) (string, any, bool) {
	bin, ok := pred.(*PS.BinaryExpr)
	if !ok || bin.Op != LX.T_EQ {
		return "", nil, false
	}
	if col, ok := bin.Left.(*PS.Ident); ok {
		if v, ok := literalValue(bin.Right); ok {
			return col.Name, v, true
		}
	}
	if col, ok := bin.Right.(*PS.Ident); ok {
		if v, ok := literalValue(bin.Left); ok {
			return col.Name, v, true
		}
	}
	return "", nil, false
}

// literalValue extracts a typed value from a literal expression.
// Returns (val, true) for NumberLiteral/StringLiteral/BoolLiteral,
// (_, false) otherwise.
func literalValue(e PS.Expr) (any, bool) {
	switch v := e.(type) {
	case *PS.NumberLiteral:
		return v.Val, true
	case *PS.StringLiteral:
		return v.Val, true
	case *PS.BoolLiteral:
		return v.Val, true
	}
	return nil, false
}

// extractSingleEquality extracts (columnName, value, ok) from a
// predicate of the form "col = literal". Accepts either side as the
// column reference (`col = literal` or `literal = col`).
func extractSingleEquality(pred PS.Expr) (string, any, bool) {
	bin, ok := pred.(*PS.BinaryExpr)
	if !ok || bin.Op != LX.T_EQ {
		return "", nil, false
	}
	if col, ok := bin.Left.(*PS.Ident); ok {
		if v, ok := literalValue(bin.Right); ok {
			return col.Name, v, true
		}
	}
	if col, ok := bin.Right.(*PS.Ident); ok {
		if v, ok := literalValue(bin.Left); ok {
			return col.Name, v, true
		}
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
