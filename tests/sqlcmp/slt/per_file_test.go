//go:build slt_corpus

package slt

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSLT_PerFile discovers every .test file under the corpus root
// and runs each as an isolated subtest with its own engine. This is
// the primary ergonomic entry point for SLT development:
//
//	Run all files:
//	    go test -v -run TestSLT_PerFile ./tests/sqlcmp/slt/...
//
//	Run one file:
//	    go test -v -run 'TestSLT_PerFile/select4' ./tests/sqlcmp/slt/...
//
//	Run files matching a pattern:
//	    go test -v -run 'TestSLT_PerFile/evidence' ./tests/sqlcmp/slt/...
//
// Override corpus location:
//
//	RAZOR_SLT_ROOT=/path/to/corpus/test go test -v -run TestSLT_PerFile ./tests/sqlcmp/slt/...
//
// Short mode skips the full corpus scan:
//
//	go test -short -run TestSLT_PerFile ./tests/sqlcmp/slt/...
func TestSLT_PerFile(t *testing.T) {
	if testing.Short() {
		t.Skip("slt: skipping per-file corpus in short mode")
	}
	root := corpusRoot()
	if _, err := os.Stat(root); err != nil {
		t.Skipf("corpus not present at %s; initialize submodule: %v", root, err)
	}

	files := discoverTestFiles(t, root)
	if len(files) == 0 {
		t.Skipf("no .test files found under %s", root)
	}
	t.Logf("discovered %d .test files under %s", len(files), root)

	for _, rel := range files {
		rel := rel
		// Use the relative path (without corpus root prefix) as the
		// subtest name so -run patterns are intuitive:
		//   -run 'TestSLT_PerFile/select4'
		//   -run 'TestSLT_PerFile/evidence/slt_lang_update'
		name := filepath.ToSlash(rel)
		t.Run(name, func(t *testing.T) {
			full := filepath.Join(root, rel)
			timeout := perFileTimeout(t, full)

			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()

			driver := NewRazorDriver()
			if err := driver.Connect(ctx); err != nil {
				t.Fatalf("driver.Connect: %v", err)
			}
			t.Cleanup(func() { _ = driver.Close(context.Background()) })

			f, err := os.Open(full)
			if err != nil {
				t.Fatalf("open %s: %v", full, err)
			}
			defer f.Close()

			recs, err := Parse(f)
			if err != nil {
				t.Fatalf("parse %s: %v", full, err)
			}

			runner := NewRunner(driver, driver.classifier, RazorEngineName)
			stats := runner.Run(ctx, recs)

			t.Logf("slt[%s]: pass=%d fail=%d skip=%d parse-err=%d total=%d dur=%s",
				name, stats.Passed, stats.Failed, stats.Skipped,
				stats.ParseErrors, stats.Total, time.Duration(stats.Duration))

			if len(stats.Slowest) > 0 {
				t.Logf("slt[%s] slowest %d records:", name, len(stats.Slowest))
				for i, s := range stats.Slowest {
					t.Logf("  #%d L%d %s [%s] %s  dur=%s", i+1, s.Line, s.Kind, s.Label, s.SQL, s.Time)
				}
			}

			if stats.Failed > 0 {
				diag := diagnoseFailures(ctx, driver, recs, 20)
				if diag != "" {
					t.Logf("first failures:\n%s", diag)
				}
				t.Errorf("%s: %d/%d records failed", name, stats.Failed, stats.Total)
			}
		})
	}
}

// discoverTestFiles walks the corpus root and returns all .test file
// paths relative to root, sorted for deterministic ordering.
func discoverTestFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip inaccessible entries
		}
		if !d.IsDir() && strings.HasSuffix(path, ".test") {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return nil
			}
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk corpus: %v", err)
	}
	return files
}

// perFileTimeout returns a timeout scaled to the file's line count.
// Small evidence files (<100 lines) get 5s; medium files get 15s;
// large select files (>10K lines) get 60s. This avoids the
// one-size-fits-all 30s that was either too generous for tiny files
// or too tight for massive ones.
// REQ001056: very large files (>500KB) get 120s to accommodate
// cross-join queries that dominate select4.test.
func perFileTimeout(t *testing.T, path string) time.Duration {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		return 30 * time.Second // fallback
	}
	// Rough heuristic: 1000 lines ≈ 15s. Clamp to [5s, 60s].
	// Very large corpus files (select4.test at 1.2MB) get extra time.
	size := info.Size()
	switch {
	case size < 2_000:
		return 5 * time.Second
	case size < 50_000:
		return 15 * time.Second
	case size < 500_000:
		return 60 * time.Second
	default:
		return 120 * time.Second
	}
}
