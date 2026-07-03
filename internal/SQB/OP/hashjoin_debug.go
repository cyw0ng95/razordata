//go:build debug

package OP

import jd "github.com/cyw0ng95/razordata/internal/DBG/JD"

func hashJoinDebugRowFlow(table string, rowID uint64, entering bool) {
	if tr := jd.GetJoinTracer(); tr != nil {
		tr.RowFlow("HashJoin", table, rowID, entering)
	}
}

func hashJoinDebugPredicate(expr string, leftRowID, rightRowID uint64, passed bool) {
	if tr := jd.GetJoinTracer(); tr != nil {
		tr.Predicate("HashJoin", expr, leftRowID, rightRowID, passed)
	}
}

func hashJoinDebugStrategy(chosen, reason string, cost float64) {
	if tr := jd.GetJoinTracer(); tr != nil {
		tr.Strategy("HashJoin", chosen, reason, cost)
	}
}

func hashJoinDebugCorrelation(stage int, tables []string, rowCount int64) {
	if tr := jd.GetJoinTracer(); tr != nil {
		tr.Correlation(stage, tables, rowCount)
	}
}
