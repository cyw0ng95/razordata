package JN

import (
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func EstimateJoinCost(leftRows, rightRows int, predicates []PS.Expr, hasIndex bool) float64 {
	if leftRows <= 0 {
		leftRows = 1
	}
	if rightRows <= 0 {
		rightRows = 1
	}
	if hasIndex {
		return float64(leftRows + rightRows)
	}
	return float64(leftRows * rightRows)
}

func JoinResultRows(leftRows, rightRows float64, predicates []PS.Expr) float64 {
	if len(predicates) == 0 {
		return leftRows * rightRows
	}
	sel := 1.0
	for range predicates {
		sel *= 0.5
	}
	result := leftRows * rightRows * sel
	if result < 1 {
		result = 1
	}
	return result
}