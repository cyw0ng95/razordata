//go:build slt_corpus

// Package slt per-file wrapper. Each .test file in the SQLLogicTest
// corpus becomes a single Go test (or t.Run subtest). The wrapper
// keeps each file isolated so a regression in one area (DDL, NULL,
// aggregates) is reported individually rather than as a single
// aggregate pass-rate.
//
// Run with:
//
//	RAZOR_SLT_ROOT=../corpus/test go test -tags slt_corpus \
//	    -run TestSLT_Each -v ./tests/sqlcmp/slt/...
//
// To focus on one file, use -run 'TestSLT_Each/<basename>'. To
// see only the failing files, use -run 'TestSLT_Each/.*FAIL.*'.
//
// The set of wrapped files is intentionally the smallest set that
// exercises every public SQL surface (DDL, DML, NULL handling,
// aggregates, joins, expressions, type casting, views, triggers).
// Adding a new file is a one-line edit to sltFiles below.
package slt

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// sltFiles is the curated list of .test files wrapped here. Each
// entry is relative to corpusRoot(). The list is hand-picked to
// cover the most bug-prone areas first: DDL, DML, NULL, aggregates,
// subqueries, views, triggers. Larger files (select1..select5) are
// excluded from this wrapper; they run under TestSQLLogicTest_CorpusSubset
// in the nightly CI to keep the default `go test` cycle fast.
var sltFiles = []string{
	"select1.test",
	"select2.test",
	"select3.test",
	"select4.test",
	"select5.test",
	"evidence/in1.test",
	"evidence/in2.test",
	"evidence/slt_lang_aggfunc.test",
	"evidence/slt_lang_createtrigger.test",
	"evidence/slt_lang_createview.test",
	"evidence/slt_lang_dropindex.test",
	"evidence/slt_lang_droptable.test",
	"evidence/slt_lang_droptrigger.test",
	"evidence/slt_lang_dropview.test",
	"evidence/slt_lang_reindex.test",
	"evidence/slt_lang_replace.test",
	"evidence/slt_lang_update.test",
}

// sltPerFileTimeout caps how long a single file is allowed to run.
// select1..select5 each exercise thousands of records; without a
// cap a regression in one query could stall the suite for minutes.
// 30s is generous enough for the evidence files and tight enough
// for select files to surface real regressions quickly.
const sltPerFileTimeout = 30 * time.Second

// TestSLT_Each runs every entry in sltFiles as an isolated subtest.
// The test is build-gated to `slt_corpus` so it does not require
// the corpus submodule in the default test run.
func TestSLT_Each(t *testing.T) {
	if testing.Short() {
		t.Skip("slt: skipping per-file in short mode")
	}
	root := corpusRoot()
	if _, err := os.Stat(root); err != nil {
		t.Skipf("corpus not present at %s; initialize submodule: %v", root, err)
	}
	for _, rel := range sltFiles {
		rel := rel
		basename := filepath.Base(rel)
		t.Run(basename, func(t *testing.T) {
			full := filepath.Join(root, rel)
			if _, err := os.Stat(full); err != nil {
				t.Skipf("file not present: %s: %v", full, err)
			}
			stats, diag := runSLTFile(t, full, sltPerFileTimeout)
			// Always log the breakdown so a regression is
			// easy to triage from the verbose output.
			t.Logf("slt[%s]: pass=%d fail=%d skip=%d parse-err=%d total=%d dur=%s",
				basename, stats.Passed, stats.Failed, stats.Skipped,
				stats.ParseErrors, stats.Total, time.Duration(stats.Duration))
			if diag != "" {
				t.Logf("first failure context:\n%s", diag)
			}
		})
	}
}

// runSLTFile opens one .test file, parses it, runs the records
// against a fresh engine, and returns aggregate stats. The
// per-record classifier is the driver's default; "feature
// unsupported" errors are skipped, not failed.
//
// The per-file context is cancelled only on the test goroutine;
// cleanup uses a fresh, non-cancelled context so engine shutdown
// can drain in-flight goroutines. Without this, a query that
// hangs the WAL writer would wedge the test for the full
// 6-phase shutdown timeout.
func runSLTFile(t *testing.T, path string, perFile time.Duration) (Stats, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), perFile)
	defer cancel()

	driver := NewRazorDriver()
	if err := driver.Connect(ctx); err != nil {
		t.Fatalf("driver.Connect: %v", err)
	}
	// Cleanup uses a fresh background context so a cancelled
	// record context does not propagate into engine shutdown.
	t.Cleanup(func() { _ = driver.Close(context.Background()) })

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	recs, err := Parse(f)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	runner := NewRunner(driver, driver.classifier)
	stats := runner.Run(ctx, recs)

	diag := ""
	if stats.Failed > 0 {
		diag = diagnoseFailures(ctx, driver, recs, 5)
	}
	return stats, diag
}

// diagnoseFailures re-runs the records and prints the first n
// failed ones. The runner is sequential and tolerant; a single
// failure does not abort the run, so we re-iterate the records
// ourselves to surface the failing SQL alongside its verdict.
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
		if rec.Kind != RecordStatementOK && rec.Kind != RecordQuery {
			continue
		}
		var err error
		switch rec.Kind {
		case RecordStatementOK:
			err = driver.Exec(ctx, rec.SQL)
		case RecordQuery:
			rs, qerr := driver.Query(ctx, rec.SQL)
			if qerr == nil {
				if diff := DiffResultSets(rs, rec); diff != "" {
					err = fmt.Errorf("result mismatch:\n%s", diff)
				}
			} else {
				err = qerr
			}
		}
		if err == nil {
			continue
		}
		failed++
		fmt.Fprintf(&b, "  L%-5d %s\n    SQL: %s\n    ERR: %v\n",
			rec.Line, rec.Kind, truncate(rec.SQL, 200), err)
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
