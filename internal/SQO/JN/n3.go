package JN

import (
	"slices"
	"strings"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

type partial struct {
	tablesSet map[string]bool
	order     []string
	cost      float64
	rows      float64
}

type candidate struct {
	idx       int
	cost      float64
	order     []string
	tablesSet map[string]bool
	rows      float64
}

func N3(baseTable string, joinTables []JoinTableInfo, wherePredicates []PS.Expr, pushedPredicates map[string][]PS.Expr, sp StatsProvider) ([]string, float64) {
	k := len(joinTables)
	if k == 0 {
		return []string{baseTable}, sp.TableRowCount(baseTable)
	}
	if k == 1 {
		return []string{baseTable, joinTables[0].Name}, sp.TableRowCount(baseTable) + sp.TableRowCount(joinTables[0].Name)
	}

	tableSelectivity := make(map[string]float64)
	for _, jt := range joinTables {
		sel := 1.0
		rowCnt := sp.TableRowCount(jt.Name)
		for _, pred := range wherePredicates {
			if sp.CanPushDown(pred, jt.Name) {
				psel := sp.JoinPredSel(pred, rowCnt)
				sel *= psel
			}
		}
		tableSelectivity[jt.Name] = sel
	}
	if pushedPredicates != nil {
		for _, jt := range joinTables {
			if preds, ok := pushedPredicates[jt.Name]; ok && len(preds) > 0 {
				sel := tableSelectivity[jt.Name]
				rowCnt := sp.TableRowCount(jt.Name)
				for _, pred := range preds {
					psel := sp.JoinPredSel(pred, rowCnt)
					sel *= psel
				}
				tableSelectivity[jt.Name] = sel
			}
		}
	}

	heap := make([]partial, 0, N3HeapMaxSize)

	baseRows := sp.TableRowCount(baseTable)
	if pushedPredicates != nil {
		if preds, ok := pushedPredicates[baseTable]; ok && len(preds) > 0 {
			sel := 1.0
			for _, pred := range preds {
				psel := sp.JoinPredSel(pred, baseRows)
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
		preds := findPredicatesForPair(baseTable, jt.Name, wherePredicates, sp)
		hasIdx := sp.HasIndex(jt.Name)
		rightRows := sp.TableRowCount(jt.Name)
		if sel, ok := tableSelectivity[jt.Name]; ok {
			r := rightRows * sel
			if r < 1 {
				r = 1
			}
			rightRows = r
		}
		joinCost := sp.JoinCost(int(baseRows), int(rightRows), preds, hasIdx)
		resultRows := sp.JoinResultRows(baseRows, rightRows, preds)
		heap = append(heap, partial{
			tablesSet: map[string]bool{baseTable: true, jt.Name: true},
			order:     []string{baseTable, jt.Name},
			cost:      baseRows + joinCost,
			rows:      resultRows,
		})
	}

	for step := 1; step < k; step++ {
		allTables := make([]string, 0, k)
		for _, jt := range joinTables {
			allTables = append(allTables, jt.Name)
		}

		nextHeap := make([]candidate, 0, N3HeapMaxSize)

		var bestCost float64
		if len(heap) > 0 {
			bestCost = heap[0].cost
			for _, pp := range heap[1:] {
				if pp.cost < bestCost {
					bestCost = pp.cost
				}
			}
		}
		pruneThreshold := bestCost * N3PruneMultiplier

		predCache := make(map[string][]PS.Expr, len(heap)*len(allTables))

		for _, pp := range heap {
			for _, tbl := range allTables {
				if pp.tablesSet[tbl] {
					continue
				}
				cacheKey := n3PredCacheKey(pp.tablesSet, tbl)
				preds, ok := predCache[cacheKey]
				if !ok {
					preds = findPredicatesForSet(pp.tablesSet, tbl, wherePredicates, sp)
					predCache[cacheKey] = preds
				}
				hasIdx := sp.HasIndex(tbl)
				rightRows := sp.TableRowCount(tbl)
				if sel, ok := tableSelectivity[tbl]; ok {
					r := rightRows * sel
					if r < 1 {
						r = 1
					}
					rightRows = r
				}
				joinCost := sp.JoinCost(int(pp.rows), int(rightRows), preds, hasIdx)
				newCost := pp.cost + joinCost
				newRows := sp.JoinResultRows(pp.rows, rightRows, preds)

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

				if bestCost > 0 && cand.cost > pruneThreshold {
					continue
				}

				if len(nextHeap) < N3HeapMaxSize {
					nextHeap = append(nextHeap, cand)
					for i := len(nextHeap) - 1; i > 0; i-- {
						parent := (i - 1) / 2
						if nextHeap[i].cost >= nextHeap[parent].cost {
							break
						}
						nextHeap[i], nextHeap[parent] = nextHeap[parent], nextHeap[i]
					}
				} else if cand.cost < nextHeap[0].cost {
					nextHeap[0] = cand
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

	if len(heap) == 0 {
		order := make([]string, 0, 1+len(joinTables))
		order = append(order, baseTable)
		for _, jt := range joinTables {
			order = append(order, jt.Name)
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
			order = append(order, jt.Name)
		}
		return order, best.cost
	}
	return best.order, best.cost
}

func FilteredRowCount(table string, predicates []PS.Expr, sp StatsProvider) float64 {
	rows := sp.TableRowCount(table)
	for _, pred := range predicates {
		if sp.CanPushDown(pred, table) {
			psel := sp.JoinPredSel(pred, rows)
			rows *= psel
		}
	}
	if rows < 1 {
		rows = 1
	}
	return rows
}

func ExhaustiveJoinOrder(baseTable string, joinTables []JoinTableInfo, predicates []PS.Expr, sp StatsProvider) []string {
	k := len(joinTables)
	if k == 0 {
		return []string{baseTable}
	}
	names := make([]string, k)
	for i, jt := range joinTables {
		names[i] = jt.Name
	}

	order := make([]string, 0, 1+k)
	order = append(order, baseTable)
	order = append(order, names...)
	bestOrder := make([]string, len(order))
	copy(bestOrder, order)
	bestCost := EstimateJoinOrderCost(order, predicates, sp)

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
			cost := EstimateJoinOrderCost(perm, predicates, sp)
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

func EstimateJoinOrderCost(order []string, predicates []PS.Expr, sp StatsProvider) float64 {
	if len(order) == 0 {
		return 0
	}
	cost := sp.TableRowCount(order[0])
	joined := map[string]bool{order[0]: true}
	for i := 1; i < len(order); i++ {
		next := order[i]
		nextRows := sp.TableRowCount(next)
		for _, pred := range predicates {
			if sp.CanPushDown(pred, next) {
				psel := sp.JoinPredSel(pred, nextRows)
				nextRows *= psel
			}
		}
		selectivity := 1.0
		for _, pred := range predicates {
			tables := sp.ExtractTablesFromExpr(pred)
			if tables[next] {
				hasJoined := false
				for t := range tables {
					if joined[t] {
						hasJoined = true
						break
					}
				}
				if hasJoined {
					psel := sp.JoinPredSel(pred, nextRows)
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

func findPredicatesForPair(leftTable, rightTable string, predicates []PS.Expr, sp StatsProvider) []PS.Expr {
	var result []PS.Expr
	for _, pred := range predicates {
		tables := sp.ExtractTablesFromExpr(pred)
		if len(tables) == 2 && tables[leftTable] && tables[rightTable] {
			result = append(result, pred)
		}
	}
	return result
}

func findPredicatesForSet(joined map[string]bool, candidate string, predicates []PS.Expr, sp StatsProvider) []PS.Expr {
	var result []PS.Expr
	for _, pred := range predicates {
		tables := sp.ExtractTablesFromExpr(pred)
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

func IsConnectedGraph(joinOrder []string, crossTablePredicates []PS.Expr) bool {
	if len(joinOrder) <= 1 {
		return true
	}
	adj := map[string]map[string]bool{}
	for _, pred := range crossTablePredicates {
		bin, ok := pred.(*PS.BinaryExpr)
		if !ok || bin.Op != LX.T_EQ {
			continue
		}
		lTable, _ := ExtractTableColumn(bin.Left)
		rTable, _ := ExtractTableColumn(bin.Right)
		if lTable == "" || rTable == "" || lTable == rTable {
			continue
		}
		if adj[lTable] == nil {
			adj[lTable] = map[string]bool{}
		}
		adj[lTable][rTable] = true
		if adj[rTable] == nil {
			adj[rTable] = map[string]bool{}
		}
		adj[rTable][lTable] = true
	}
	visited := map[string]bool{}
	queue := []string{joinOrder[0]}
	visited[joinOrder[0]] = true
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for nb := range adj[cur] {
			if !visited[nb] {
				visited[nb] = true
				queue = append(queue, nb)
			}
		}
	}
	for _, tbl := range joinOrder {
		if !visited[tbl] {
			return false
		}
	}
	return true
}

func ExtractTableColumn(e PS.Expr) (string, string) {
	switch v := e.(type) {
	case *PS.QualifiedName:
		return v.Table, v.Name
	case *PS.Ident:
		return "", v.Name
	}
	return "", ""
}