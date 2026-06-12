//go:build slt_corpus

package slt

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// corpusRoot is the directory containing the SQLLogicTest files.
// Resolved relative to the test's working directory; CI checks
// out the submodule under tests/sqlcmp/corpus/, so the default
// is "corpus/test/". Override with RAZOR_SLT_ROOT for ad-hoc
// runs against a different corpus layout.
func corpusRoot() string {
	if v := os.Getenv("RAZOR_SLT_ROOT"); v != "" {
		return v
	}
	return "corpus/test"
}

// TestSQLLogicTest_CorpusSubset runs the curated PR subset and
// fails if the pass rate falls below corpusSubsetThreshold.
//
// The test is gated by the `slt_corpus` build tag so default
// `go test ./...` does not require the corpus submodule. The
// full corpus is not exercised here; it is reserved for the
// nightly CI workflow (REQ000335).
func TestSQLLogicTest_CorpusSubset(t *testing.T) {
	if testing.Short() {
		t.Skip("slt: skipping corpus in short mode")
	}
	root := corpusRoot()
	if _, err := os.Stat(root); err != nil {
		t.Skipf("corpus not present at %s; initialize submodule to enable: %v", root, err)
	}
	files, err := resolveSubset(root, corpusSubset)
	if err != nil {
		t.Fatalf("resolve subset: %v", err)
	}
	if len(files) == 0 {
		t.Skipf("no subset files resolved under %s", root)
	}
	sort.Strings(files)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	driver := NewRazorDriver()
	if err := driver.Connect(ctx); err != nil {
		t.Fatalf("driver.Connect: %v", err)
	}
	t.Cleanup(func() { _ = driver.Close(ctx) })

	var (
		total     Stats
		perFile   = make([]fileStat, 0, len(files))
		startTime = time.Now()
	)
	for _, path := range files {
		stat := runFile(ctx, driver, path)
		perFile = append(perFile, stat)
		mergeStats(&total, stat.stats)
	}
	rate := passRate(total)
	t.Logf("slt: ran %d files in %s: %d pass / %d fail / %d skip / %d parse-err (rate=%.2f%%)",
		len(files), time.Since(startTime).Truncate(time.Millisecond),
		total.Passed, total.Failed, total.Skipped, total.ParseErrors,
		rate*100)
	if rate < corpusSubsetThreshold {
		t.Errorf("slt: pass rate %.2f%% below threshold %.2f%%", rate*100, corpusSubsetThreshold*100)
	}
}

// runFile parses one .test file and runs the driver through
// it. Errors opening the file are reported as a synthetic
// "all skipped" stat; this is benign when the corpus is
// filtered (e.g. some directories may be empty).
func runFile(ctx context.Context, driver *RazorDriver, path string) fileStat {
	f, err := os.Open(path)
	if err != nil {
		return fileStat{path: path, stats: Stats{Skipped: 1, Total: 1}}
	}
	defer f.Close()
	recs, err := Parse(f)
	if err != nil {
		return fileStat{path: path, stats: Stats{ParseErrors: 1, Total: 1}}
	}
	runner := NewRunner(driver, driver.classifier)
	stats := runner.Run(ctx, recs)
	return fileStat{path: path, stats: stats}
}

// resolveSubset turns the curated list (with optional leading
// "_" directories) into a flat slice of .test file paths
// that exist on disk. Missing files are silently dropped;
// the runner treats them as "no work to do".
func resolveSubset(root string, entries []string) ([]string, error) {
	var out []string
	for _, e := range entries {
		full := filepath.Join(root, e)
		info, err := os.Stat(full)
		if err != nil {
			continue
		}
		if info.IsDir() {
			err := filepath.WalkDir(full, func(path string, d os.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if !d.IsDir() && strings.HasSuffix(path, ".test") {
					out = append(out, path)
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
			continue
		}
		out = append(out, full)
	}
	return out, nil
}

func mergeStats(dst *Stats, src Stats) {
	dst.Total += src.Total
	dst.Passed += src.Passed
	dst.Failed += src.Failed
	dst.Skipped += src.Skipped
	dst.ParseErrors += src.ParseErrors
	if src.Duration > dst.Duration {
		dst.Duration = src.Duration
	}
}

func passRate(s Stats) float64 {
	denom := s.Passed + s.Failed
	if denom == 0 {
		return 0
	}
	return float64(s.Passed) / float64(denom)
}
