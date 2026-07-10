package JN

import (
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func MultiStart(candidateBase string, joinTables []JoinTableInfo, wherePredicates []PS.Expr, pushedPredicates map[string][]PS.Expr, sp StatsProvider) []string {
	allNames := make([]string, 0, 1+len(joinTables))
	if candidateBase != "" {
		allNames = append(allNames, candidateBase)
	}
	for _, jt := range joinTables {
		allNames = append(allNames, jt.Name)
	}
	hasNonBase := false
	for _, n := range allNames[1:] {
		hasNonBase = true
		_ = n
		break
	}
	if !hasNonBase {
		return allNames
	}
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
		rest := make([]JoinTableInfo, 0, len(joinTables))
		hasBase := false
		for _, jt := range joinTables {
			if jt.Name == base {
				hasBase = true
				continue
			}
			rest = append(rest, jt)
		}
		if base != candidateBase && !hasBase {
			rest = append(rest, JoinTableInfo{Name: base})
		}
		order, cost := N3(base, rest, wherePredicates, pushedPredicates, sp)
		if bestCost < 0 || cost < bestCost {
			bestCost = cost
			bestOrder = order
		}
	}
	if bestOrder == nil {
		return allNames
	}

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