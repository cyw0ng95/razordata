package EX

import (
	"strings"

	AD "github.com/cyw0ng95/razordata/internal/SQB/AD"
	AG "github.com/cyw0ng95/razordata/internal/SQB/AG"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
	OP "github.com/cyw0ng95/razordata/internal/SQB/OP"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

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
				// REQ001229: projection pushdown — compute column indices
				// so cloneRow decodes only the requested columns.
				if tblCols := tableSchema(ss.Table()); tblCols != nil {
					indices := make([]int, 0, len(usedNames))
					for _, name := range usedNames {
						for j, col := range tblCols {
							if strings.EqualFold(col, name) {
								indices = append(indices, j)
								break
							}
						}
					}
					if len(indices) > 0 {
						ss.RequestedCols = indices
					}
				}
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
	// REQ001235: track EXISTS conjuncts that have been decorrelated into
	// semi-joins, so they are skipped by the normal filter path.
	existsReplaced := make(map[int]bool)
	// REQ001235: pointer-based set of actually-replaced ExistsExpr nodes
	// so the cross-table predicate loop skips only those, not non-decorrelated EXISTS.
	existsReplacedPtr := make(map[*PS.ExistsExpr]bool)
	if whereExpr != nil && (len(s.Joins) > 0 || s.From != "") {
		conjuncts := p.splitAnd(whereExpr)
		// REQ001235: correlated EXISTS decorrelation → semi-join.
		// Scan conjuncts before predicate pushdown. If a conjunct is a
		// correlated EXISTS subquery that can be rewritten as a semi-join,
		// replace it now and mark the conjunct index as replaced.
		for i, c := range conjuncts {
			if existsExpr, ok := c.(*PS.ExistsExpr); ok && existsExpr.Subquery != nil {
				if replacement, ok := p.decorrelateExists(existsExpr, s.From, scan); ok {
					existsReplaced[i] = true
					existsReplacedPtr[existsExpr] = true
					// Wrap the current operator as the left side of the
					// semi-join. Multiple EXISTS may chain.
					current = replacement
				}
			}
		}
		allTables := []string{s.From}
		for _, j := range s.Joins {
			allTables = append(allTables, j.Right)
		}
		pushedPredicates, crossTablePredicates = p.splitPredicatesByTable(conjuncts, allTables)
		// Push predicates for the first table onto its scan.
		// REQ000820: also set up point-lookup for IN-list predicates.
		// REQ001248: reorder predicates by ascending cost so cheap
		// predicates short-circuit before expensive ones.
		if firstPreds := pushedPredicates[s.From]; len(firstPreds) > 0 {
			if order := reorderIndices(firstPreds); order != nil {
				firstPreds = orderSlice(firstPreds, order)
			}
			for _, pred := range firstPreds {
				current = OP.NewFilter(current, pred, nil)
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
		// REQ000XXX: For multi-table implicit JOINs, extract equi-join
		// conditions from WHERE and use OP.HashJoin instead of OP.NestedLoopJoin.
		crossTableConjuncts = p.splitAnd(whereExpr)
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
		// REQ001235: skip EXISTS conjuncts already decorrelated.
		// Use a pointer-based set of replaced ExistsExpr nodes so we
		// don't skip non-decorrelated EXISTS (bare-name correlations).
		if len(crossTablePredicates) > 0 {
			// REQ001248: reorder by ascending cost so cheap
			// predicates short-circuit before expensive ones.
			if order := reorderIndices(crossTablePredicates); order != nil {
				crossTablePredicates = orderSlice(crossTablePredicates, order)
			}
			// Build pointer-based skip sets after reordering (indices
			// no longer match extractedPreds). extractedPreds used
			// indices into the original crossTablePredicates slice
			// before reordering; now use the expressions themselves.
			extractedPtrs := make(map[PS.Expr]bool)
			for i, c := range crossTablePredicates {
				if extractedPreds[i] {
					extractedPtrs[c] = true
				}
			}
			for _, c := range crossTablePredicates {
				// Skip predicates already extracted by hash joins.
				if extractedPtrs[c] {
					continue
				}
				if ee, ok := c.(*PS.ExistsExpr); ok && existsReplacedPtr[ee] {
					// Skip EXISTS conjuncts already decorrelated.
					continue
				}
				current = OP.NewFilter(current, c, nil)
			}
		} else if pushedPredicates == nil {
			// No predicate pushdown — apply full WHERE as before.
			// REQ001235: skip EXISTS conjuncts already decorrelated.
			conjuncts := p.splitAnd(whereExpr)
			// REQ001248: reorder by ascending cost.
			if order := reorderIndices(conjuncts); order != nil {
				conjuncts = orderSlice(conjuncts, order)
			}
			// Reorder changes indices; rebuild a pointer-based set
			// for replaced existsExpr nodes that survives reordering.
			// existsReplaced was keyed by index in the original
			// conjuncts slice (line 142). After reordering the
			// indices no longer match, so we skip by pointer instead.
			existsReplacedPt := make(map[*PS.ExistsExpr]bool)
			for _, c := range conjuncts {
				if ee, ok := c.(*PS.ExistsExpr); ok && existsReplacedPtr[ee] {
					existsReplacedPt[ee] = true
				}
			}
			for _, c := range conjuncts {
				if ee, ok := c.(*PS.ExistsExpr); ok && existsReplacedPt[ee] {
					continue
				}
				current = OP.NewFilter(current, c, nil)
			}
		}
	}

	current = p.planAggregation(s, current)
	current = p.planOrdering(s, current)
	current = p.planLimitOffset(s, current)

	// REQ001231: Filter-Project fusion — detect Project(Filter(scan))
	// and replace with FilterProject(scan) to eliminate one virtual call
	// per row. Skip when expressions contain subqueries or aggregates
	// (those need the full eval machinery).
	current = fuseFilterProject(current)

	return current
}

// fuseFilterProject detects Project(Filter(child)) and fuses them into
// FilterProject(child). This eliminates one virtual call per row by
// evaluating both predicate and projection in a single Next() loop.
// Skips fusion when either the predicate or any projection expression
// contains subqueries or aggregates. REQ001231.
func fuseFilterProject(op DT.Operator) DT.Operator {
	proj, ok := op.(*OP.Project)
	if !ok {
		return op
	}
	filter, ok := proj.Child().(*OP.Filter)
	if !ok {
		return op
	}
	// Skip if predicate or projection expressions contain subqueries
	// or aggregates — those need the separate eval machinery.
	if containsSubqueryOrAggregate(filter.Predicate()) {
		return op
	}
	for _, c := range proj.Cols() {
		if containsSubqueryOrAggregate(c) {
			return op
		}
	}
	return OP.NewFilterProject(filter.Child(), filter.Predicate(), proj.Cols())
}

// containsSubqueryOrAggregate checks whether an expression tree contains
// any SubqueryExpr, ExistsExpr, or AggregateFunc node. REQ001231.
func containsSubqueryOrAggregate(e PS.Expr) bool {
	if e == nil {
		return false
	}
	has := false
	walkExpr(e, func(n PS.Expr) {
		switch n.(type) {
		case *PS.SubqueryExpr, *PS.ExistsExpr, *PS.AggregateFunc:
			has = true
		}
	})
	return has
}

// existsReplacement records a decorrelated EXISTS conjunct that should
// replace the original Filter(scan) with a semi-join operator.
type existsReplacement struct {
	index       int
	replacement DT.Operator
}

// decorrelateExists checks if an EXISTS expression is a correlated
// subquery suitable for semi-join rewrite. Returns (semiJoinOp, true)
// when the rewrite is safe, (nil, false) to fall back to per-row eval.
//
// Detection criteria:
//  1. EXISTS subquery is a single-table SELECT (no joins, aggregates, etc.)
//  2. WHERE clause contains a correlation predicate: inner.col <op> outer.col
//  3. No OR, GROUP BY, HAVING, DISTINCT, LIMIT, ORDER BY, window functions
func (p *Planner) planSelectNoFrom(s *PS.Select) DT.Operator {
	if hasAnyAggregate(s.Cols) {
		dummy := OP.NewValuesOp([]PS.Expr{&PS.NumberLiteral{Val: int64(1)}})
		var op DT.Operator = dummy
		if s.Where != nil {
			op = OP.NewFilter(op, s.Where, nil)
		}
		agg := AG.NewAggregate(op, s.GroupBy, s.Cols)
		if s.Having != nil {
			return OP.NewFilter(agg, s.Having, nil)
		}
		return agg
	}
	op := DT.Operator(OP.NewValuesOp(s.Cols))
	if s.Where != nil {
		op = OP.NewFilter(op, s.Where, nil)
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
		current = OP.NewFilter(current, s.Where, nil)
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
		current = OP.NewFilter(current, s.Having, nil)
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
		scan = OP.NewFilter(scan, s.Where, nil)
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
						scan = OP.NewFilter(isc, rebuildAnd(extra), nil)
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
							scan = OP.NewFilter(isc, rebuildAnd(extra), nil)
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
							scan = OP.NewFilter(isc, rebuildAnd(extra), nil)
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
							scan = OP.NewFilter(isc, whereExpr, nil)
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
			return OP.NewFilter(current, s.Having, nil)
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
		current = OP.NewFilter(current, s.Having, nil)
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

			// REQ001230: detect Sort + Limit pattern → replace with TopNSort.
			// Only applies to simple ORDER BY + LIMIT (no offset wrapping).
			if s.Offset == nil || s.OffsetFirst {
				if lim, ok := current.(*OP.Limit); ok {
					inner := lim.Child()
					if sort, ok := inner.(*OP.Sort); ok {
						current = OP.NewTopNSort(sort.Child(), sort.Keys(), n)
					}
				}
			}
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
		costPredicates = p.splitAnd(s.Where)
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
			// REQ001248: reorder by ascending cost so cheap
			// predicates short-circuit before expensive ones.
			if order := reorderIndices(basePreds); order != nil {
				basePreds = orderSlice(basePreds, order)
			}
			for _, pred := range basePreds {
				tryApplyPointLookup(baseOp, pred)
				baseOp = OP.NewFilter(baseOp, pred, nil)
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
			// REQ001248: reorder by ascending cost.
			if order := reorderIndices(rightPreds); order != nil {
				rightPreds = orderSlice(rightPreds, order)
			}
			for _, pred := range rightPreds {
                tryApplyPointLookup(rightScan, pred)
                rightScan = OP.NewFilter(rightScan, pred, nil)
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
	conjuncts := p.splitAnd(whereExpr)
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
