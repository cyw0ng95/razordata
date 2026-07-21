//go:build slt_corpus

package slt

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// TestSLT_InList runs every case in includedCases within a single
// go-test invocation. A shared RazorDriver is reused via Reset()
// between files (REQ001454), keeping startup overhead to one-time
// cost instead of paying ~200 ms process + compilation per file.
//
// Run all cases:
//
//	go test -tags slt_corpus -run TestSLT_InList ./tests/sqlcmp/slt/
//
// Run a subset by label:
//
//	go test -tags slt_corpus -run 'TestSLT_InList/in1' -v ./tests/sqlcmp/slt/
//
// Quick-fail on first error: once any case fails, remaining cases
// are skipped (not executed, but still shown as skipped in -v).
func TestSLT_InList(t *testing.T) {
	root := corpusRoot()
	if _, err := os.Stat(root); err != nil {
		t.Skipf("corpus not present at %s; initialize submodule: %v", root, err)
	}

	driver := NewRazorDriver()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	if err := driver.Connect(ctx); err != nil {
		t.Fatalf("driver.Connect: %v", err)
	}
	t.Cleanup(func() { _ = driver.Close(context.Background()) })

	var aborted atomic.Bool // set to true when first failure occurs

	for _, ic := range includedCases {
		ic := ic
		t.Run(ic.Label, func(t *testing.T) {
			// Quick-fail: if a prior case already failed, skip
			// remaining cases without executing queries.
			if aborted.Load() {
				t.Skip("skipped due to prior failure (quick-fail)")
				return
			}

			fullPath := filepath.Join(root, ic.Path+".test")
			if _, err := os.Stat(fullPath); err != nil {
				t.Errorf("file missing: %s", fullPath)
				return
			}

			fileCtx, fileCancel := context.WithTimeout(ctx, time.Duration(ic.Timeout)*time.Second)
			defer fileCancel()

			if err := driver.Reset(fileCtx); err != nil {
				t.Errorf("driver.Reset: %v", err)
				return
			}

			f, err := os.Open(fullPath)
			if err != nil {
				t.Errorf("open %s: %v", fullPath, err)
				return
			}
			defer f.Close()

			recs, err := Parse(f)
			if err != nil {
				t.Errorf("parse %s: %v", ic.Path, err)
				return
			}

			runner := NewRunner(driver, driver.classifier, RazorEngineName)
			t0 := time.Now()
			stats := runner.Run(fileCtx, recs)
			dur := time.Since(t0)

			t.Logf("slt[%s]: pass=%d fail=%d skip=%d parse-err=%d total=%d dur=%s",
				ic.Path, stats.Passed, stats.Failed, stats.Skipped,
				stats.ParseErrors, stats.Total, dur.Round(time.Microsecond))

			if len(stats.Slowest) > 0 && t.Failed() {
				t.Logf("slowest %d records:", len(stats.Slowest))
				for i, s := range stats.Slowest {
					t.Logf("  #%d L%d %s [%s] %s  dur=%s",
						i+1, s.Line, s.Kind, s.Label, s.SQL, s.Time)
				}
			}

			if stats.Failed > 0 {
				diag := diagnoseFailures(stats, 500)
				if diag != "" {
					t.Logf("first failures:\n%s", diag)
				}
				t.Errorf("%s: %d/%d records failed", ic.Path, stats.Failed, stats.Total)
				// Signal other goroutines to abort.
				aborted.Store(true)
			}
		})
	}
}
