package EX

import (
	CO "github.com/cyw0ng95/razordata/internal/SQO/CO"
	"strings"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	OP "github.com/cyw0ng95/razordata/internal/SQB/OP"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func cloneExpr(e PS.Expr) PS.Expr {
	if e == nil {
		return nil
	}
	switch x := e.(type) {
	case *PS.Ident:
		return &PS.Ident{Name: x.Name, SlotIdx: x.SlotIdx}
	case *PS.QualifiedName:
		return &PS.QualifiedName{Table: x.Table, Name: x.Name, SlotIdx: x.SlotIdx}
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

func collectReferencedColNames(s *PS.Select) []string {
	cols := make(map[string]bool)
	addCol := func(e PS.Expr) {
		CO.WalkExpr(e, func(node PS.Expr) {
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
		CO.WalkExpr(e, func(node PS.Expr) {
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
	CO.WalkExpr(e, func(node PS.Expr) {
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
	CO.WalkExpr(j.On, func(node PS.Expr) {
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
	CO.WalkExpr(e, func(node PS.Expr) {
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

// selectIndexHinted is like selectIndex but respects an INDEXED BY hint.
// REQ001371: when hint is non-empty, only returns the named index if it
// matches the column; otherwise returns false.
func (p *Planner) selectIndexHinted(table, col, hint string) (string, bool) {
	if hint != "" {
		return hint, hasWriterIndex(table, hint) && p.indexHasColumn(table, hint, col)
	}
	return p.selectIndex(table, col)
}

func (p *Planner) indexHasColumn(table, idxName, col string) bool {
	t, ok := p.catalog[table]
	if !ok {
		return false
	}
	idxCols, ok := t.indexes[idxName]
	if !ok {
		return false
	}
	for _, c := range idxCols {
		if c == col {
			return true
		}
	}
	return false
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
func deriveJoinSchema(left, right DT.Operator, leftTbl, rightTbl string) ([]string, []LX.TokenType, map[string]int) {
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
	// REQ001656: simulate the NLJ runtime prefixing. At runtime,
	// the NLJ prefixes bare column names with leftTbl/rightTbl
	// (via prefixCols). The pre-built shared schema must match
	// so that WithSharedSchema produces the same Cols/ColIndex the
	// runtime would. Without this, qualified references like
	// "tab1.col1" can't be found in the ColIndex because the
	// unaliased tab1's columns are bare "col1" and collide with
	// the base table's "col1".
	if !hasAnyPrefixCols(leftCols) && leftTbl != "" {
		leftCols = prefixColNames(leftCols, leftTbl)
	}
	if !hasAnyPrefixCols(rightCols) && rightTbl != "" {
		rightCols = prefixColNames(rightCols, rightTbl)
	}
	cols := make([]string, 0, len(leftCols)+len(rightCols))
	cols = append(cols, leftCols...)
	cols = append(cols, rightCols...)
	types := make([]LX.TokenType, 0, len(cols))
	types = append(types, leftTypes...)
	types = append(types, rightTypes...)
	idx := make(map[string]int, len(cols)*2)
	for i, c := range cols {
		key := LX.NormalizeIdent(c)
		if _, exists := idx[key]; !exists {
			idx[key] = i
		}
		// REQ001656: also register the bare column name (without
		// the "table." prefix) so that unqualified Ident references
		// like "col1" can be resolved at runtime via Lookup.
		// The bare name maps to the first occurrence, which matches
		// SQLite's behavior of resolving ambiguous bare names to
		// the first table in the FROM clause.
		if dot := strings.LastIndex(c, "."); dot >= 0 {
			bare := c[dot+1:]
			bkey := LX.NormalizeIdent(bare)
			if _, exists := idx[bkey]; !exists {
				idx[bkey] = i
			}
		}
	}
	return cols, types, idx
}

// prefixColNames returns a copy of cols with "alias." prepended to each.
func prefixColNames(cols []string, alias string) []string {
	out := make([]string, len(cols))
	prefix := alias + "."
	for i, c := range cols {
		out[i] = prefix + c
	}
	return out
}

// hasAnyPrefixCols returns true if any column name contains a "."
// (indicating it's already table- or alias-prefixed).
func hasAnyPrefixCols(cols []string) bool {
	for _, c := range cols {
		if strings.Contains(c, ".") {
			return true
		}
	}
	return false
}

// colsOf extracts the column names from a known-shape operator.
// Returns nil if the schema is unknown (e.g. for valuesOp or
// computed projections).
func colsOf(op DT.Operator) []string {
	switch o := op.(type) {
	case *OP.SeqScan:
		if o.Schema() != nil {
			// REQ001656: when an alias is set, the SeqScan produces
			// alias-prefixed column names (e.g. "cor0.col0"). Return
			// the prefixed names so downstream operators (NLJ, HashJoin)
			// build a combined schema with proper qualified names for
			// slot resolution and ColIndex lookup. Without this, the
			// NLJ's shared schema has bare names like
			// [col0 col1 col2 col0 col1 col2], and qualified references
			// like cor0.col2 can't be resolved — they fall back to the
			// first matching bare name (always the left/base table).
			if alias := o.Alias(); alias != "" {
				return prefixColNames(o.Schema().Cols, alias)
			}
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

func hasAnyAggregate(cols []PS.Expr) bool {
	for _, c := range cols {
		if DT.ContainsAggregate(c) {
			return true
		}
	}
	return false
}

// collectAggregates returns all AggregateFunc nodes found in e (deduplicated by lookup key).
func collectAggregates(e PS.Expr) []*PS.AggregateFunc {
	var result []*PS.AggregateFunc
	seen := make(map[string]bool)
	var walk func(PS.Expr)
	walk = func(expr PS.Expr) {
		if expr == nil {
			return
		}
		switch v := expr.(type) {
		case *PS.AggregateFunc:
			key := DT.AggregateLookupKey(v)
			if !seen[key] {
				seen[key] = true
				result = append(result, v)
			}
		case *PS.WindowFunc:
			return
		case *PS.BinaryExpr:
			walk(v.Left)
			walk(v.Right)
		case *PS.UnaryExpr:
			walk(v.Operand)
		case *PS.AliasedExpr:
			walk(v.Expr)
		case *PS.CastExpr:
			walk(v.Expr)
		case *PS.FunctionCall:
			for _, a := range v.Args {
				walk(a)
			}
		case *PS.CaseExpr:
			walk(v.Expr)
			for _, w := range v.WhenList {
				walk(w.Cond)
				walk(w.Then)
			}
			walk(v.Else)
		case *PS.BetweenExpr:
			walk(v.Expr)
			walk(v.Low)
			walk(v.High)
		case *PS.InExpr:
			walk(v.Expr)
			for _, item := range v.List {
				walk(item)
			}
		default:
			DT.ContainsAggregate(expr)
		}
	}
	walk(e)
	return result
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
