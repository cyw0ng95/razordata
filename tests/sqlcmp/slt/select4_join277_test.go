//go:build slt_corpus

package slt

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestSelect4_Join277_HashMismatch is the regression test for the
// single failing case in select4.test (L39784). The query is an
// 8-table join with a mix of equi-joins and IN-list filters; under
// SLT hashing, the expected result is 168 cells hashing to
// 34325f84dd0efa600c0be4e8e0770bc3 (verified against sqlite3).
//
// As of the original bug capture the engine returns 112 cells
// (14 rows × 8 cols) instead of 21 rows × 8 cols. This test
// replays the full corpus setup up to L39784, runs the failing
// query through the RazorDriver, and asserts both the cell count
// and the MD5 hash so a regression is unambiguous.
//
// Run with:
//
//	go test -tags slt_corpus -run TestSelect4_Join277_HashMismatch ./tests/sqlcmp/slt/...
func TestSelect4_Join277_HashMismatch(t *testing.T) {
	root := corpusRoot()
	if _, err := os.Stat(root); err != nil {
		t.Skipf("corpus not present at %s; initialize submodule to enable: %v", root, err)
	}

	driver := NewRazorDriver()
	if err := driver.Connect(context.Background()); err != nil {
		t.Fatalf("driver.Connect: %v", err)
	}
	t.Cleanup(func() { _ = driver.Close(context.Background()) })

	f, err := os.Open(filepath.Join(root, "select4.test"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	recs, err := Parse(f)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// Replay every "statement ok" record up to L39784 (the
	// failing case). The corpus inserts ~1000 rows across
	// t1..t9; this is the same workload the SLT runner applies
	// before evaluating the query.
	//
	// Setup statements may fail for engine-side reasons
	// (unsupported syntax, unsupported DDL, etc.). Match the
	// SLT runner's tolerance: classify the error and skip past
	// it instead of aborting the test. Only catastrophic
	// failures (driver disconnect, panic) abort.
	ctx := context.Background()
	const failingLine = 39784
	classifier := &RazorClassifier{}
	executed, skipped, failed := 0, 0, 0
	for i := range recs {
		rec := &recs[i]
		if rec.Line >= failingLine {
			break
		}
		if rec.Kind != RecordStatementOK {
			continue
		}
		err := driver.Exec(ctx, rec.SQL)
		switch {
		case err == nil:
			executed++
		case classifier.Classify(err) == VerdictSkipped:
			skipped++
		default:
			failed++
			t.Logf("setup stmt at L%d failed: %v", rec.Line, err)
		}
	}
	t.Logf("replayed setup from select4.test up to L%d: ok=%d skip=%d fail=%d",
		failingLine, executed, skipped, failed)
	if executed == 0 {
		t.Fatalf("no setup statements succeeded; the corpus is required for this test")
	}

	// Failing query — copied verbatim from select4.test L39784.
	const query = `SELECT x5, e6+c6, d1, c8, e9+108, a7, a3+149+a5, e4+358
  FROM t3, t7, t4, t9, t5, t1, t8, t6
 WHERE b4=d6
   AND 168=e7
   AND a3 in (515,190,306,513,959,788,198)
   AND e8=c9
   AND 625=c5
   AND a9 in (28,11,739,102,413,389)
   AND a1=d8
   AND d6 in (277,256,469,924,846,729,901,186)`

	rs, err := driver.Query(ctx, query)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}

	// 8 columns per row × 21 rows = 168 cells (SQLite reference).
	// The original bug returned 14 rows = 112 cells, which fails
	// the hash check below.
	const wantRows = 21
	const wantHash = "34325f84dd0efa600c0be4e8e0770bc3"
	if len(rs.Rows) != wantRows {
		// Log first few rows for debugging
		t.Errorf("row count: got %d, want %d", len(rs.Rows), wantRows)
		n := len(rs.Rows)
		if n > 21 {
			n = 21
		}
		for i := 0; i < n; i++ {
			t.Logf("  row %d: %v", i, rs.Rows[i])
		}
	}
	gotHash := resultHash(rs, RowSort)
	if gotHash != wantHash {
		t.Errorf("hash mismatch: got %s, want %s (rowsort)", gotHash, wantHash)
		rows := rs.Rows
		idx := make([]int, len(rows))
		for i := range idx {
			idx[i] = i
		}
		sort.Slice(idx, func(i, j int) bool {
			a, b := rows[idx[i]], rows[idx[j]]
			for k := 0; k < len(a) && k < len(b); k++ {
				if c := valueLess(a[k], b[k]); c != 0 {
					return c < 0
				}
			}
			return len(a) < len(b)
		})
		t.Logf("Sorted output (kind:string):")
		for _, i := range idx {
			parts := make([]string, len(rows[i]))
			for j, cell := range rows[i] {
				parts[j] = fmt.Sprintf("%d:%s", cell.Kind, cell.String())
			}
			t.Logf("  %s", strings.Join(parts, "\t"))
		}
	}
}