package EX

import (
	CO "github.com/cyw0ng95/razordata/internal/SQO/CO"
	"context"
	"fmt"
	"strings"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	OP "github.com/cyw0ng95/razordata/internal/SQB/OP"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	RE "github.com/cyw0ng95/razordata/internal/SQF/RE"
)

func (p *Planner) decorrelateExists(existsExpr *PS.ExistsExpr, outerTable string, outerScan DT.Operator) (DT.Operator, bool) {
	subq, ok := existsExpr.Subquery.(*PS.Select)
	if !ok {
		return nil, false
	}
	// Must be a single-table subquery
	if subq.From == "" || len(subq.Joins) > 0 || subq.SubqueryFrom != nil {
		return nil, false
	}
	// No aggregates, DISTINCT, GROUP BY, HAVING, LIMIT, ORDER BY, window funcs
	if subq.Distinct || len(subq.GroupBy) > 0 || subq.Having != nil || subq.Limit != nil || len(subq.OrderBy) > 0 {
		return nil, false
	}
	if hasAnyWindowFunc(subq.Cols) {
		return nil, false
	}
	// Check SELECT list for aggregate functions
	for _, col := range subq.Cols {
		if col != nil && hasAggFunc(col) {
			return nil, false
		}
	}
	// Inner WHERE must exist and contain at least one correlation predicate
	if subq.Where == nil {
		return nil, false
	}
	innerConjuncts := RE.SplitAnd(subq.Where)
	if len(innerConjuncts) == 0 {
		return nil, false
	}
	// Find a correlation predicate: inner.col <op> outer.col or vice versa
	var correlationPred PS.Expr
	innerTable := subq.From
	for _, c := range innerConjuncts {
		bin, ok := c.(*PS.BinaryExpr)
		if !ok {
			continue
		}
		// Check left=Ident + right=QualifiedName(outer)
		if _, ok := bin.Left.(*PS.Ident); ok {
			if qn, ok := bin.Right.(*PS.QualifiedName); ok && qn.Table == outerTable {
				correlationPred = bin
				break
			}
		}
		// Check left=QualifiedName(outer) + right=Ident
		if qn, ok := bin.Left.(*PS.QualifiedName); ok && qn.Table == outerTable {
			if _, ok := bin.Right.(*PS.Ident); ok {
				correlationPred = bin
				break
			}
		}
		// Both sides QualifiedName: one outer, one inner
		if lqn, ok := bin.Left.(*PS.QualifiedName); ok {
			if rqn, ok := bin.Right.(*PS.QualifiedName); ok {
				if (lqn.Table == outerTable && rqn.Table == innerTable) ||
					(lqn.Table == innerTable && rqn.Table == outerTable) {
					correlationPred = bin
					break
				}
			}
		}
	}
	if correlationPred == nil {
		return nil, false
	}
	// Build inner scan with non-correlation filters applied
	var innerScan DT.Operator
	if p.store != nil {
		ssc, err := OP.NewSeqScanWithStore(p.store, innerTable)
		if err == nil {
			innerScan = ssc
		}
	}
	if innerScan == nil {
		innerScan = NewIndexOrSeqScan(innerTable, nil, p)
	}
	if order := CO.ReorderIndices(innerConjuncts); order != nil {
		innerConjuncts = CO.OrderSlice(innerConjuncts, order)
	}
	for _, c := range innerConjuncts {
		if c != correlationPred {
			innerScan = OP.NewFilter(innerScan, c, nil)
		}
	}
	// Build correlation ON predicate function
	on := buildCorrelationFunc(correlationPred.(*PS.BinaryExpr), innerTable, outerTable)
	// Create semi-join: outerScan (probe) × innerScan (build, first-match)
	semiJoin := OP.NewNestedLoopJoin(outerScan, innerScan, outerTable, innerTable, on, OP.JoinKindSemi)
	return semiJoin, true
}

// buildCorrelationFunc creates the ON predicate function for the semi-join
// from the extracted correlation predicate expression. The returned closure
// is used by NestedLoopJoin to test left-right row pairs at runtime.
func buildCorrelationFunc(bin *PS.BinaryExpr, innerTable, outerTable string) func(outer, inner *DT.Row) (bool, error) {
	op := bin.Op
	// Extract column names from both sides of the comparison
	var outerCol, innerCol string
	switch l := bin.Left.(type) {
	case *PS.QualifiedName:
		if l.Table == outerTable {
			outerCol = l.Name
		} else {
			innerCol = l.Name
		}
	case *PS.Ident:
		innerCol = l.Name
	}
	switch r := bin.Right.(type) {
	case *PS.QualifiedName:
		if r.Table == outerTable {
			outerCol = r.Name
		} else {
			innerCol = r.Name
		}
	case *PS.Ident:
		innerCol = r.Name
	}

	return func(outer, inner *DT.Row) (bool, error) {
		// Find column indices by scanning Cols slices
		outerIdx := -1
		for i, c := range outer.Cols {
			if strings.EqualFold(c, outerCol) || strings.HasSuffix(c, "."+outerCol) {
				outerIdx = i
				break
			}
		}
		innerIdx := -1
		for i, c := range inner.Cols {
			if strings.EqualFold(c, innerCol) || strings.HasSuffix(c, "."+innerCol) {
				innerIdx = i
				break
			}
		}
		if outerIdx < 0 || innerIdx < 0 {
			return false, fmt.Errorf("semi-join: cannot find column %s/%s in semi-join rows", outerCol, innerCol)
		}
		outerVal := outer.Data[outerIdx]
		innerVal := inner.Data[innerIdx]
		cmp := DT.CompareValue(outerVal, innerVal)
		switch op {
		case LX.T_EQ:
			return cmp == 0, nil
		case LX.T_NE:
			return cmp != 0, nil
		case LX.T_LT:
			return cmp < 0, nil
		case LX.T_LE:
			return cmp <= 0, nil
		case LX.T_GT:
			return cmp > 0, nil
		case LX.T_GE:
			return cmp >= 0, nil
		default:
			return false, fmt.Errorf("semi-join: unsupported operator %v", op)
		}
	}
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
				innerOp = OP.NewFilter(innerOp, s.Where, nil)
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

	cteTraceSeed(cte.Name, len(allRows))

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
	// Use the same key computation as p.Plan to correctly bypass plan cache.
	normalized, _ := pl.NormalizeForMemo(comp.Right)
	compKey := pl.SerializeKey(normalized)

	// Safety limit: prevent infinite loops from malformed recursive CTEs.
	const maxRecIters = 10000
	maxReached := false
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

		cteTraceIteration(cte.Name, iter+1, len(iterRows), len(newRows))

		allRows = append(allRows, newRows...)
		iterRows = newRows

		DT.RegisterTable(cte.Name, allRows)
		maxReached = (iter == maxRecIters-1)
	}

	if maxReached {
		cteTraceMaxIterations(cte.Name)
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
	CO.WalkExpr(e, func(n PS.Expr) {
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
		return &PS.QualifiedName{Table: canonical[:idx], Name: canonical[idx+1:], SlotIdx: -1}
	}
	return &PS.Ident{Name: canonical, SlotIdx: -1}
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
	CO.WalkExpr(e, func(inner PS.Expr) {
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
