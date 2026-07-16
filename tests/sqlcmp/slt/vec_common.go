// vec_common.go provides shared helpers for vectorized SLT tests.
//go:build slt_vec

package slt

import (
	"context"
	"os"
	"path/filepath"
)

// corpusRoot is the directory containing the SQLLogicTest files.
func corpusRoot() string {
	return filepath.Join("..", "corpus", "test")
}

// runFile executes all records in a single .test file and returns
// per-record pass/fail/skip statistics.
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
	runner := NewRunner(driver, driver.classifier, RazorEngineName)
	stats := runner.Run(ctx, recs)
	return fileStat{path: path, stats: stats}
}
