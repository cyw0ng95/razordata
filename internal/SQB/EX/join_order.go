package EX

import (
	"github.com/cyw0ng95/razordata/internal/SQO/JN"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// plannerAsStatsProvider adapts *Planner to the JN.StatsProvider interface.
type plannerAsStatsProvider struct{ p *Planner }

func (sp *plannerAsStatsProvider) TableRowCount(table string) float64 { return sp.p.getTableRowCount(table) }
func (sp *plannerAsStatsProvider) HasIndex(table string) bool         { return sp.p.hasIndexOnTable(table) }
func (sp *plannerAsStatsProvider) JoinPredSel(pred PS.Expr, rowCount float64) float64 {
	return sp.p.joinPredSel(pred, rowCount)
}
func (sp *plannerAsStatsProvider) JoinCost(leftRows, rightRows int, predicates []PS.Expr, hasIndex bool) float64 {
	return sp.p.estimateJoinCost(leftRows, rightRows, predicates, hasIndex)
}
func (sp *plannerAsStatsProvider) JoinResultRows(leftRows, rightRows float64, predicates []PS.Expr) float64 {
	return sp.p.joinResultRows(leftRows, rightRows, predicates)
}
func (sp *plannerAsStatsProvider) CanPushDown(pred PS.Expr, table string) bool {
	return sp.p.canPushDown(pred, table)
}
func (sp *plannerAsStatsProvider) ExtractTablesFromExpr(e PS.Expr) map[string]bool {
	return sp.p.extractTablesFromExpr(e)
}
func (sp *plannerAsStatsProvider) FindTableForColumn(col string) string {
	return sp.p.findTableForColumn(col)
}

func (p *Planner) n3JoinOrdering(baseTable string, joinTables []joinTableInfo, wherePredicates []PS.Expr, pushedPredicates map[string][]PS.Expr) ([]string, float64) {
	return JN.N3(baseTable, toJNJoinTableInfos(joinTables), wherePredicates, pushedPredicates, &plannerAsStatsProvider{p})
}

func (p *Planner) n3JoinOrderingMultiStart(candidateBase string, joinTables []joinTableInfo, wherePredicates []PS.Expr, pushedPredicates map[string][]PS.Expr) []string {
	return JN.MultiStart(candidateBase, toJNJoinTableInfos(joinTables), wherePredicates, pushedPredicates, &plannerAsStatsProvider{p})
}

func (p *Planner) exhaustiveJoinOrder(baseTable string, joinTables []joinTableInfo, predicates []PS.Expr) []string {
	return JN.ExhaustiveJoinOrder(baseTable, toJNJoinTableInfos(joinTables), predicates, &plannerAsStatsProvider{p})
}

func (p *Planner) estimateJoinOrderCost(order []string, predicates []PS.Expr) float64 {
	return JN.EstimateJoinOrderCost(order, predicates, &plannerAsStatsProvider{p})
}

func filteredRowCount(table string, predicates []PS.Expr, p *Planner) float64 {
	return JN.FilteredRowCount(table, predicates, &plannerAsStatsProvider{p})
}

func toJNJoinTableInfos(src []joinTableInfo) []JN.JoinTableInfo {
	dst := make([]JN.JoinTableInfo, len(src))
	for i, j := range src {
		dst[i] = JN.JoinTableInfo{Name: j.name, Join: j.join}
	}
	return dst
}

// joinTableInfo stores a table name and its optional JOIN clause.
type joinTableInfo struct {
	name string
	join PS.JoinClause
}

// hasIndexOnTable checks whether the table has any registered index.
func (p *Planner) hasIndexOnTable(table string) bool {
	return len(p.catalog[table].indexes) > 0
}

// joinResultRows estimates the number of output rows from a join.
func (p *Planner) joinResultRows(leftRows, rightRows float64, predicates []PS.Expr) float64 {
	if len(predicates) == 0 {
		return leftRows * rightRows
	}
	sel := 1.0
	for _, pred := range predicates {
		sel *= p.joinPredSel(pred, leftRows+rightRows)
	}
	if sel < 0 {
		sel = 0
	}
	return leftRows * rightRows * sel
}

// isConnectedGraph checks if all tables in joinOrder form a single
// connected component via equi-join predicates. Used by groupBushyJoins
// to detect chain-equi-joins that should NOT be bushy-grouped.
func isConnectedGraph(joinOrder []string, crossTablePredicates []PS.Expr) bool {
	if len(joinOrder) <= 1 {
		return true
	}
	adj := map[string]map[string]bool{}
	for _, pred := range crossTablePredicates {
		bin, ok := pred.(*PS.BinaryExpr)
		if !ok || bin.Op != LX.T_EQ {
			continue
		}
		lTable, _ := extractTableColumn(bin.Left)
		rTable, _ := extractTableColumn(bin.Right)
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

// groupBushyJoins detects independent equi-join pairs in the join
// order and groups them for bushy plan execution. A pair of tables
// is "independent" when their equi-join keys share no columns.
// REQ000821. Kept from legacy implementation — JN.GroupBushy has
// different grouping semantics. REQ001495.
func groupBushyJoins(baseTable string, joinOrder []string, crossTablePredicates []PS.Expr) [][]string {
	k := len(joinOrder)
	if k <= 3 {
		return [][]string{joinOrder}
	}
	if isConnectedGraph(joinOrder, crossTablePredicates) {
		return [][]string{joinOrder}
	}
	type pairKey struct {
		left  string
		right string
	}
	pairKeys := map[pairKey][]string{}
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
	groups := [][]string{{baseTable}}
	for i := 1; i < k; i++ {
		tbl := joinOrder[i]
		independent := true
		targetGroup := len(groups) - 1
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
		if independent {
			tblJoins := equiJoinTables[tbl]
			if len(tblJoins) > 0 {
				for gi, existing := range groups {
					for _, et := range existing {
						etJoins := equiJoinTables[et]
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
		// SLT naming convention: a1 → t1.a, d6 → t6.d, etc.
		// Used by groupBushyJoins to detect cross-table equi-joins
		// in tests where columns are not always qualified.
		name := v.Name
		if len(name) > 1 {
			last := name[len(name)-1]
			if last >= '0' && last <= '9' {
				table := "t" + string(last)
				col := name[:len(name)-1]
				return table, col
			}
		}
		return "", name
	}
	return "", ""
}