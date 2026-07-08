//go:build slt_corpus

package slt

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestSLT_WalMode_Basic runs the locally-managed wal_mode.test file
// (not in the read-only corpus submodule) and reports pass/fail stats.
// REQ001391: verifies end-to-end WAL-mode semantics.
func TestSLT_WalMode_Basic(t *testing.T) {
	path := filepath.Join("..", "wal_mode.slt")
	f, err := os.Open(path)
	if err != nil {
		t.Skipf("wal_mode.test not found at %s: %v", path, err)
	}
	defer f.Close()

	recs, err := Parse(f)
	if err != nil {
		t.Fatalf("parse wal_mode.test: %v", err)
	}
	if len(recs) == 0 {
		t.Fatal("wal_mode.test: no records parsed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	driver := NewRazorDriver()
	if err := driver.Connect(ctx); err != nil {
		t.Fatalf("driver.Connect: %v", err)
	}
	t.Cleanup(func() { _ = driver.Close(ctx) })

	runner := NewRunner(driver, driver.classifier, RazorEngineName)
	stats := runner.Run(ctx, recs)

	t.Logf("wal_mode.test: %d pass / %d fail / %d skip (total=%d)",
		stats.Passed, stats.Failed, stats.Skipped, stats.Total)

	if stats.Passed == 0 {
		t.Error("wal_mode.test: expected at least 1 passing test")
	}
	if stats.Failed > 0 {
		t.Errorf("wal_mode.test: %d failures — expected 0", stats.Failed)
	}
}