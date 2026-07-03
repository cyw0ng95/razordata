//go:build debug

package OP

import (
	jd "github.com/cyw0ng95/razordata/internal/DBG/JD"
)

func filterDebugPredicate(expr string, rowID uint64, passed bool) {
	if tr := jd.GetJoinTracer(); tr != nil {
		tr.Predicate("Filter", expr, rowID, 0, passed)
	}
}
