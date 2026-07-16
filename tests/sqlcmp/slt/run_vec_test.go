//go:build slt_vec

package slt

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
	"strings"
)

// TestSLT_Vectorized_Select1_4_AllPass runs the select1-select4 corpus
// with vectorized mode enabled via PRAGMA. REQ001449.
func TestSLT_Vectorized_Select1_4_AllPass(t *testing.T) {
	if testing.Short() {
		t.Skip("slt: skipping vectorized corpus in short mode")
	}
	root := corpusRoot()
	if _, err := os.Stat(root); err != nil {
		t.Skipf("corpus not present at %s; initialize submodule to enable: %v", root, err)
	}

	files := []string{"select1.test", "select2.test", "select3.test", "select4.test"}
	var allPassed bool
	var failedFiles []string

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	driver := NewRazorDriver()
	if err := driver.Connect(ctx); err != nil {
		t.Fatalf("driver.Connect: %v", err)
	}
	t.Cleanup(func() { _ = driver.Close(ctx) })

	// Enable vectorized mode
	if err := driver.Exec(ctx, "PRAGMA vectorized_mode = on"); err != nil {
		t.Fatalf("PRAGMA vectorized_mode = on: %v", err)
	}

	for _, fname := range files {
		path := filepath.Join(root, fname)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Logf("skipping %s (not found)", fname)
			continue
		}
		stat := runFile(ctx, driver, path)
		if stat.stats.Failed > 0 {
			failedFiles = append(failedFiles, fmt.Sprintf("%s: %d/%d failed", fname, stat.stats.Failed, stat.stats.Total))
		}
		allPassed = stat.stats.Failed == 0
		t.Logf("%s: %d total, %d passed, %d failed, %d skipped",
			fname, stat.stats.Total, stat.stats.Passed, stat.stats.Failed, stat.stats.Skipped)
	}

	if len(failedFiles) > 0 {
		sort.Strings(failedFiles)
		t.Errorf("vectorized mode failures:\n  %s", strings.Join(failedFiles, "\n  "))
	}
	if !allPassed {
		t.Log("some files had failures; see individual results above")
	}
}
