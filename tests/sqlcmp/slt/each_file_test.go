//go:build slt_corpus

package slt

import (
	"context"
	"fmt"
	"strings"
)

// diagnoseFailures re-runs the records and prints the first n
// failed ones. The runner is sequential and tolerant; a single
// failure does not abort the run, so we re-iterate the records
// ourselves to surface the failing SQL alongside its verdict.
// Only RecordQuery records are re-run — RecordStatementOK records
// are skipped to avoid side effects (e.g., duplicate INSERTs,
// "table already exists" errors) that would corrupt the diagnosis.
func diagnoseFailures(ctx context.Context, driver Driver, recs []Record, n int) string {
	if n <= 0 {
		return ""
	}
	var b strings.Builder
	failed := 0
	for i := range recs {
		if failed >= n {
			break
		}
		rec := &recs[i]
		if rec.Kind != RecordQuery {
			continue
		}
		var err error
		rs, qerr := driver.Query(ctx, rec.SQL)
		if qerr == nil {
			if diff := DiffResultSets(rs, rec); diff != "" {
				err = fmt.Errorf("result mismatch:\n%s", diff)
			}
		} else {
			err = qerr
		}
		if err == nil {
			rs = nil
			continue
		}
		failed++
		fmt.Fprintf(&b, "  L%-5d %s\n    SQL: %s\n    ERR: %v\n",
			rec.Line, rec.Kind, truncate(rec.SQL, 200), err)
		rs = nil
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
