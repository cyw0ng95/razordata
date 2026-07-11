package JN

import (
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

type pairKey struct {
	left  string
	right string
}

func GroupBushy(baseTable string, joinOrder []string, crossTablePredicates []PS.Expr) [][]string {
	k := len(joinOrder)
	if k <= 3 {
		return [][]string{joinOrder}
	}

	if IsConnectedGraph(joinOrder, crossTablePredicates) {
		return [][]string{joinOrder}
	}

	pairKeys := map[pairKey][]string{}
	equiJoinTables := map[string]map[string]bool{}
	for _, pred := range crossTablePredicates {
		bin, ok := pred.(*PS.BinaryExpr)
		if !ok || bin.Op != LX.T_EQ {
			continue
		}
		lTable, lCol := ExtractTableColumn(bin.Left)
		rTable, rCol := ExtractTableColumn(bin.Right)
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

// StarSchemaCostInflation detects star-schema patterns and returns a
// cost multiplier for the dimension table scan. A star schema is
// detected when a table equi-joins to the base table but not to any
// other table in the join order. Based on SQLite v3.49.0 star-schema
// detection. REQ001505.
func StarSchemaCostInflation(tbl, baseTable string, joinOrder []string, crossTablePredicates []PS.Expr) float64 {
	// Count how many tables the candidate equi-joins to.
	eqCount := 0
	withBase := false
	for _, pred := range crossTablePredicates {
		bin, ok := pred.(*PS.BinaryExpr)
		if !ok || bin.Op != LX.T_EQ {
			continue
		}
		lTable, _ := ExtractTableColumn(bin.Left)
		rTable, _ := ExtractTableColumn(bin.Right)
		if lTable == tbl && rTable != "" {
			eqCount++
			if rTable == baseTable {
				withBase = true
			}
		}
		if rTable == tbl && lTable != "" {
			eqCount++
			if lTable == baseTable {
				withBase = true
			}
		}
	}
	// Star-schema dimension: equi-joins only to the base table, not to
	// other tables. Inflate cost by 3.0x.
	if withBase && eqCount == 1 {
		return 3.0
	}
	return 1.0
}