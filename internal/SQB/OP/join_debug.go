//go:build debug

package OP

import (
	jd "github.com/cyw0ng95/razordata/internal/DBG/JD"
)

func nljDebugRowFlow(table string, rowID uint64, entering bool) {
	if tr := jd.GetJoinTracer(); tr != nil {
		tr.RowFlow("NestedLoopJoin", table, rowID, entering)
	}
}

func nljDebugPredicate(expr string, leftRowID, rightRowID uint64, passed bool) {
	if tr := jd.GetJoinTracer(); tr != nil {
		tr.Predicate("NestedLoopJoin", expr, leftRowID, rightRowID, passed)
	}
}

func nljDebugStrategy(chosen, reason string, cost float64) {
	if tr := jd.GetJoinTracer(); tr != nil {
		tr.Strategy("NestedLoopJoin", chosen, reason, cost)
	}
}

func nljDebugCorrelation(stage int, tables []string, rowCount int64) {
	if tr := jd.GetJoinTracer(); tr != nil {
		tr.Correlation(stage, tables, rowCount)
	}
}
