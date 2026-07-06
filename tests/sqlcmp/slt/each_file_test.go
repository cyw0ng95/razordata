//go:build slt_corpus

package slt

import (
	"fmt"
	"strings"
)

// diagnoseFailures formats cached diagnostic output from the runner's first
// pass. No re-execution of queries is needed — the runner captures diff
// output in Stats.FailureContext during runQuery.
func diagnoseFailures(stats Stats, n int) string {
	if n <= 0 {
		return ""
	}
	var b strings.Builder
	for i, fc := range stats.FailureContext {
		if i >= n {
			break
		}
		fmt.Fprintf(&b, "  L%-5d %s\n    SQL: %s\n    DIAG: %s\n",
			fc.Line, fc.Kind, truncate(fc.SQL, 200), fc.Diag)
	}
	return b.String()
}

// truncate clamps s to max characters with an ellipsis suffix.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
