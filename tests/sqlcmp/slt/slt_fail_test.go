//go:build slt_corpus

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

func TestSLT_DetectSelect4Failures(t *testing.T) {
	root := corpusRoot()
	full := filepath.Join(root, "select4.test")
	if _, err := os.Stat(full); err != nil {
		t.Skipf("select4.test not present: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	driver := NewRazorDriver()
	if err := driver.Connect(ctx); err != nil {
		t.Fatalf("driver.Connect: %v", err)
	}
	t.Cleanup(func() { _ = driver.Close(context.Background()) })
	f, err := os.Open(full)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	recs, err := Parse(f)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	runner := NewRunner(driver, driver.classifier, RazorEngineName)
	stats := runner.Run(ctx, recs)
	t.Logf("slt[select4]: pass=%d fail=%d skip=%d parse-err=%d total=%d dur=%s",
		stats.Passed, stats.Failed, stats.Skipped,
		stats.ParseErrors, stats.Total, time.Duration(stats.Duration))

	if stats.Failed == 0 {
		return
	}
	failed := 0
	for i := range recs {
		rec := &recs[i]
		if rec.Kind != RecordQuery {
			continue
		}
		rs, qerr := driver.Query(ctx, rec.SQL)
		var err error
		if qerr == nil {
			if diff := DiffResultSets(rs, rec); diff != "" {
				err = fmt.Errorf("result mismatch: %s", truncate(diff, 200))
			}
		} else {
			err = qerr
		}
		if err != nil {
			failed++
			sql := strings.ReplaceAll(truncate(rec.SQL, 200), "\n", " ")
			t.Logf("FAIL L%-5d label=%-15s sql=%s", rec.Line, rec.Label, sql)
			t.Logf("  err=%v", err)
		}
		rs = nil
		if failed >= 5 {
			break
		}
	}
	t.Logf("total failures logged: %d", failed)
}
