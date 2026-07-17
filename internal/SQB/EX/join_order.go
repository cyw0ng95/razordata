package EX

import (
	CO "github.com/cyw0ng95/razordata/internal/SQO/CO"
	LX "github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func (p *Planner) n3JoinOrdering(baseTable string, joinTables []joinTableInfo, wherePredicates []PS.Expr, pushedPredicates map[string][]PS.Expr) ([]string, float64) {
	k := len(joinTables)
	if k == 0 {
		return []string{baseTable}, p.getTableRowCount(baseTable)
	}
	if k == 1 {
		return []string{baseTable, joinTables[0].name}, p.getTableRowCount(baseTable) + p.getTableRowCount(joinTables[0].name)
	}

	// Pre-compute allTables, rowCounts, and table indices for bitmask cache keys.
	allTables := make([]string, 0, k)
	tableIndex := make(map[string]int, k+1)
	tableRowCounts := make(map[string]float64, k+1)
	tableSelectivity := make(map[string]float64)
	hasDuplicates := false
	seenNames := make(map[string]bool, k+1)

	// baseTable gets index 0.
	tableIndex[baseTable] = 0
	seenNames[baseTable] = true
	tableRowCounts[baseTable] = p.getTableRowCount(baseTable)
	baseRows := tableRowCounts[baseTable]

	for _, jt := range joinTables {
		n := jt.name
		allTables = append(allTables, n)
		if seenNames[n] {
			hasDuplicates = true
		}
		seenNames[n] = true
		tableIndex[n] = len(tableIndex)
		tableRowCounts[n] = p.getTableRowCount(n)
	}

	// REQ000883/REQ000909: pre-compute per-table selectivity from
	// single-table WHERE predicates (both pushed-down and cross-table
	// that reference a single table). This allows the N3 algorithm to
	// prefer joining tables first that have highly selective filters.
	for _, jt := range joinTables {
		sel := 1.0
		rowCnt := tableRowCounts[jt.name]
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
				rowCnt := tableRowCounts[jt.name]
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
		rightRows := tableRowCounts[jt.name]
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

	// Pre-compute the bitmask for the baseTable alone.
	baseMask := uint64(1) << tableIndex[baseTable]

	// Iteratively extend partial plans with the cheapest remaining table.
	for step := 1; step < k; step++ {
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
		// bestCost × n3PruneMultiplier are pruned.
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
		// (joined-set, candidate). Use uint64 bitmask when table
		// names are unique; fall back to string keys for self-joins.
		var predCacheStr map[string][]PS.Expr
		var predCacheBits map[uint64][]PS.Expr
		if hasDuplicates {
			predCacheStr = make(map[string][]PS.Expr, len(heap)*len(allTables))
		} else {
			predCacheBits = make(map[uint64][]PS.Expr, len(heap)*len(allTables))
		}

		for _, pp := range heap {
			// Pre-compute the bitmask for this partial plan's joined set.
			var joinedMask uint64
			if !hasDuplicates {
				joinedMask = baseMask
				for tbl := range pp.tablesSet {
					if idx, ok := tableIndex[tbl]; ok {
						joinedMask |= uint64(1) << idx
					}
				}
			}

			for _, tbl := range allTables {
				if pp.tablesSet[tbl] {
					continue
				}
				// Predicate cache lookup.
				var preds []PS.Expr
				if hasDuplicates {
					cacheKey := CO.N3PredCacheKey(pp.tablesSet, tbl)
					var ok bool
					preds, ok = predCacheStr[cacheKey]
					if !ok {
						preds = p.findPredicatesForSet(pp.tablesSet, tbl, wherePredicates)
						predCacheStr[cacheKey] = preds
					}
				} else {
					candIdx, _ := tableIndex[tbl]
					bitsKey := (joinedMask << 8) | uint64(candIdx)
					var ok bool
					preds, ok = predCacheBits[bitsKey]
					if !ok {
						preds = p.findPredicatesForSet(pp.tablesSet, tbl, wherePredicates)
						predCacheBits[bitsKey] = preds
					}
				}
				hasIdx := p.hasIndexOnTable(tbl)
				rightRows := tableRowCounts[tbl]
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

				// REQ000947: prune candidates whose cost exceeds
				// bestCost × n3PruneMultiplier. Check BEFORE allocations.
				if bestCost > 0 && newCost > pruneThreshold {
					continue
				}

				// Only allocate for surviving candidates.
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
		// REQ001453: selectivity-weighted cost estimation. Multiply
		// intermediate rows by NDV-derived selectivity for equi-join
		// predicates, preferring plans that join highly-selective
		// predicates early. Fallback to 0.5 when no stats available.
		joinSel := 0.5 // default for unknown selectivity
		for _, pred := range predicates {
			bin, ok := pred.(*PS.BinaryExpr)
			if !ok || bin.Op != LX.T_EQ {
				continue
			}
			ndvL := p.ndvFromExpr(bin.Left)
			ndvR := p.ndvFromExpr(bin.Right)
			if ndvL > 0 && ndvR > 0 {
				joinSel = 1.0 / float64(int(ndvL) + int(ndvR) - 1)
				break
			}
		}
		intermediate := cost * nextRows * selectivity * joinSel
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

// extractTableColumn extracts (table, column) from an expression
// that is an Ident or QualifiedName. For bare Idents (implicit
// comma-join columns like "d6"), resolves the table via the SLT
// naming convention (d6 => t6.d) so groupBushyJoins can detect
// cross-table equi-join dependencies. REQ001113.
func extractTableColumn(e PS.Expr) (string, string) {
	return CO.ExtractTableColumn(e, findTableInSchemas)
}
