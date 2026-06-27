//go:build slt_corpus

package slt

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// TestSLT_NonSelect_FullDiff runs every .test file under
// corpus/test that is NOT select1..5 and reports FULL diffs for
// every RecordQuery failure. This bypasses the 5-record cap of
// `diagnoseFailures` so we can catalogue every distinct bug
// pattern in a single run.
//
// Used to surface new logic-bug REQs.
func TestSLT_NonSelect_FullDiff(t *testing.T) {
	root := corpusRoot()
	target := os.Getenv("RAZOR_SLT_FILES")
	if target == "" {
		target = "evidence"
	}

	// Walk the corpus, filter to "non-select1..5" .test files.
	all := collectAllTests(t, root)
	filtered := all[:0]
	for _, p := range all {
		base := basename(p)
		if base == "select1.test" || base == "select2.test" ||
			base == "select3.test" || base == "select4.test" ||
			base == "select5.test" {
			continue
		}
		if target != "" && !strings.Contains(p, "/"+target+"/") && p != target {
			continue
		}
		filtered = append(filtered, p)
	}
	t.Logf("scanning %d non-select files under %s", len(filtered), target)

	driver := NewRazorDriver()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if err := driver.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer driver.Close(context.Background())

	for _, full := range filtered {
		runOneFile(t, ctx, driver, full)
	}
}

func runOneFile(t *testing.T, ctx context.Context, driver Driver, full string) {
	f, err := os.Open(full)
	if err != nil {
		t.Logf("[%s] open err: %v", full, err)
		return
	}
	defer f.Close()
	recs, err := Parse(f)
	if err != nil {
		t.Logf("[%s] parse err: %v", full, err)
		return
	}
	// Need a fresh driver per file so state from one file doesn't
	// pollute the next — different files declare different tables.
	driver2 := NewRazorDriver()
	fctx, fcancel := context.WithTimeout(ctx, 60*time.Second)
	defer fcancel()
	if err := driver2.Connect(fctx); err != nil {
		t.Logf("[%s] connect err: %v", full, err)
		return
	}
	runner := NewRunner(driver2, driver2.classifier, RazorEngineName)
	stats := runner.Run(fctx, recs)
	driver2.Close(context.Background())

	t.Logf("[%s] pass=%d fail=%d skip=%d total=%d",
		full, stats.Passed, stats.Failed, stats.Skipped, stats.Total)

	if stats.Failed == 0 {
		return
	}
	// Re-run each RecordQuery and log full diff.
	var failLines []int
	for i := range recs {
		rec := &recs[i]
		if rec.Kind != RecordQuery {
			continue
		}
		// Need driver2 again — re-create for diagnostics.
		d2 := NewRazorDriver()
		if err := d2.Connect(fctx); err != nil {
			continue
		}
		// Replay all preceding records to set up state.
		setupRecs := make([]Record, 0, i)
		for j := 0; j <= i; j++ {
			setupRecs = append(setupRecs, recs[j])
		}
		runner2 := NewRunner(d2, d2.classifier, RazorEngineName)
		runner2.Run(fctx, setupRecs)
		// Now query the target record fresh.
		qctx, qcancel := context.WithTimeout(fctx, 5*time.Second)
		rs, qerr := d2.Query(qctx, rec.SQL)
		qcancel()
		if qerr != nil {
			failLines = append(failLines, rec.Line)
			t.Logf("  L%d QUERY ERR: %v", rec.Line, qerr)
		} else if diff := DiffResultSets(rs, rec); diff != "" {
			failLines = append(failLines, rec.Line)
			t.Logf("  L%d MISMATCH: %s\n    SQL: %s",
				rec.Line, diff, truncate(rec.SQL, 250))
		}
		d2.Close(context.Background())
	}
	t.Logf("[%s] failing lines: %v", full, failLines)
}

func basename(p string) string {
	i := strings.LastIndexAny(p, "/\\")
	if i < 0 {
		return p
	}
	return p[i+1:]
}

func collectAllTests(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := walkDir(root, &out)
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	return out
}

func walkDir(root string, out *[]string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, e := range entries {
		full := root + "/" + e.Name()
		if e.IsDir() {
			if err := walkDir(full, out); err != nil {
				return err
			}
			continue
		}
		if strings.HasSuffix(e.Name(), ".test") {
			*out = append(*out, full)
		}
	}
	return nil
}

var _ = fmt.Sprintf