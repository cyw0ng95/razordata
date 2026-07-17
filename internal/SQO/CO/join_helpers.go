package CO

import (
	"slices"
	"strings"

	LX "github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// N3PredCacheKey builds a stable cache key for the N3 join ordering.
// REQ000883, REQ001441.
func N3PredCacheKey(joined map[string]bool, candidate string) string {
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

// IsConnectedGraph checks if all tables in joinOrder are reachable
// via equi-join predicates. REQ000883, REQ001441.
func IsConnectedGraph(joinOrder []string, crossTablePredicates []PS.Expr, extractTableCol func(PS.Expr) (string, string)) bool {
	if len(joinOrder) <= 1 {
		return true
	}
	adj := map[string]map[string]bool{}
	for _, pred := range crossTablePredicates {
		bin, ok := pred.(*PS.BinaryExpr)
		if !ok || bin.Op != LX.T_EQ {
			continue
		}
		lTable, _ := extractTableCol(bin.Left)
		rTable, _ := extractTableCol(bin.Right)
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

// GroupBushyJoins detects independent equi-join pairs and groups them.
// REQ000821, REQ001441.
func GroupBushyJoins(baseTable string, joinOrder []string, crossTablePredicates []PS.Expr, extractTableCol func(PS.Expr) (string, string)) [][]string {
	k := len(joinOrder)
	if k <= 3 {
		return [][]string{joinOrder}
	}
	if IsConnectedGraph(joinOrder, crossTablePredicates, extractTableCol) {
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
		lTable, lCol := extractTableCol(bin.Left)
		rTable, rCol := extractTableCol(bin.Right)
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

	// REQ001452: compute transitive closure of equi-join adjacency.
	// Tables connected through a chain of equi-join predicates are
	// considered joinable, preventing N3 from exploring dead-end
	// partial plans.
	reachableFrom := makeTransitiveClosure(equiJoinTables)
	groups := [][]string{{baseTable}}
	for i := 1; i < k; i++ {
		tbl := joinOrder[i]
		independent := true
		targetGroup := len(groups) - 1
		for gi, existing := range groups {
			for _, et := range existing {
				// REQ001452: use transitive closure for group membership
				// decision — tables reachable via any chain of equi-join
				// predicates belong in the same group.
				if reachableFrom[tbl] != nil && reachableFrom[tbl][et] {
					independent = false
					targetGroup = gi
					break
				}
				// Fallback to direct pairKeys for backward compat.
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

// ExtractTableColumn extracts (table, column) from an Ident or
// QualifiedName. findTableInSchemas is a callback for resolving
// bare Idents (SLT naming convention). REQ001113, REQ001441.
func ExtractTableColumn(e PS.Expr, findTableInSchemas func(string) string) (string, string) {
	switch v := e.(type) {
	case *PS.QualifiedName:
		return v.Table, v.Name
	case *PS.Ident:
		if tbl := findTableInSchemas(v.Name); tbl != "" {
			return tbl, v.Name
		}
		return "", v.Name
	}
	return "", ""
}

// ProjectColNames extracts column names from projection expressions.
// REQ001442.
func ProjectColNames(cols []PS.Expr) []string {
	names := make([]string, 0, len(cols))
	for _, c := range cols {
		switch e := c.(type) {
		case *PS.Ident:
			names = append(names, e.Name)
		case *PS.QualifiedName:
			names = append(names, e.Table+"."+e.Name)
		case *PS.AliasedExpr:
			names = append(names, e.Alias)
		default:
			names = append(names, "")
		}
	}
	return names
}

// ResolveExprSlots walks an expression tree and sets SlotIdx on each
// Ident and QualifiedName node by looking up the column name in schema.
// REQ001442.
func ResolveExprSlots(expr PS.Expr, schema []string) {
	if expr == nil || len(schema) == 0 {
		return
	}
	switch e := expr.(type) {
	case *PS.Ident:
		for i, name := range schema {
			if strings.EqualFold(name, e.Name) {
				e.SlotIdx = i
				return
			}
		}
	case *PS.QualifiedName:
		full := e.Table + "." + e.Name
		for i, name := range schema {
			if strings.EqualFold(name, full) {
				e.SlotIdx = i
				return
			}
		}
	case *PS.BinaryExpr:
		ResolveExprSlots(e.Left, schema)
		ResolveExprSlots(e.Right, schema)
	case *PS.UnaryExpr:
		ResolveExprSlots(e.Operand, schema)
	case *PS.CastExpr:
		ResolveExprSlots(e.Expr, schema)
	case *PS.CaseExpr:
		ResolveExprSlots(e.Expr, schema)
		for _, w := range e.WhenList {
			ResolveExprSlots(w.Cond, schema)
			ResolveExprSlots(w.Then, schema)
		}
		ResolveExprSlots(e.Else, schema)
	case *PS.AggregateFunc:
		ResolveExprSlots(e.Arg, schema)
		ResolveExprSlots(e.Separator, schema)
	case *PS.FunctionCall:
		for _, a := range e.Args {
			ResolveExprSlots(a, schema)
		}
	case *PS.InExpr:
		ResolveExprSlots(e.Expr, schema)
		for _, el := range e.List {
			ResolveExprSlots(el, schema)
		}
	case *PS.BetweenExpr:
		ResolveExprSlots(e.Expr, schema)
		ResolveExprSlots(e.Low, schema)
		ResolveExprSlots(e.High, schema)
	case *PS.SubqueryExpr, *PS.ExistsExpr:
	}
}

// makeTransitiveClosure computes the transitive closure of an
// equi-join adjacency map. For each table, returns the set of all
// tables reachable via any chain of equi-join predicates.
// REQ001452.
func makeTransitiveClosure(adj map[string]map[string]bool) map[string]map[string]bool {
	closure := make(map[string]map[string]bool, len(adj))
	for t := range adj {
		closure[t] = make(map[string]bool)
	}
	for t := range adj {
		if _, ok := closure[t]; !ok {
			closure[t] = map[string]bool{t: true}
			continue
		}
		visited := map[string]bool{}
		var dfs func(string)
		dfs = func(cur string) {
			for next := range adj[cur] {
				if !visited[next] {
					visited[next] = true
					closure[t][next] = true
					dfs(next)
				}
			}
		}
		closure[t][t] = true
		dfs(t)
	}
	return closure
}