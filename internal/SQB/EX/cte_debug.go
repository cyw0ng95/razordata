//go:build debug

package EX

import (
	cd "github.com/cyw0ng95/razordata/internal/DBG/CD"
	ct "github.com/cyw0ng95/razordata/internal/DBG/CT"
)

// cteTraceSeed records the seed execution of a recursive CTE.
func cteTraceSeed(name string, numRows int) {
	if tr := cd.GetCTETracer(); tr != nil {
		tr.Seed(name, numRows)
	}
	ct.GlobalStats.CteSeedRows.Add(int64(numRows))
}

// cteTraceIteration records a recursive CTE iteration.
func cteTraceIteration(name string, iter int, rowsIn, rowsOut int) {
	if tr := cd.GetCTETracer(); tr != nil {
		tr.Iteration(name, iter, rowsIn, rowsOut, nil)
	}
	ct.GlobalStats.CteTotalIterations.Add(1)
	ct.GlobalStats.CteArmRows.Add(int64(rowsOut))
}

// cteTraceMaxIterations records that a CTE reached the maximum iteration limit.
func cteTraceMaxIterations(name string) {
	if tr := cd.GetCTETracer(); tr != nil {
		tr.MaxIterationsReached(name)
	}
	ct.GlobalStats.CteMaxItersReached.Add(1)
}