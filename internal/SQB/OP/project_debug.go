//go:build debug

package OP

import (
	jd "github.com/cyw0ng95/razordata/internal/DBG/JD"
)

func projectDebugOffset(operator string, expected, actual int, colName string) {
	if tr := jd.GetJoinTracer(); tr != nil {
		tr.ColumnOffset(operator, expected, actual, colName)
	}
}
