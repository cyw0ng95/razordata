//go:build slt_corpus

package slt

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestSLT_NonSelect_StatementFail captures RecordStatementOK/Error
// failures that the query-only TestSLT_NonSelect_FullDiff misses.
// Walks each non-select1..5 file, replays records, and reports any
// statement whose success/error verdict is wrong.
//
// REQ001058-style: documents a logic bug where the executor returns
// an unexpected error or accepts an unexpected success for DDL.
func TestSLT_NonSelect_StatementFail(t *testing.T) {
	root := corpusRoot()
	all := collectAllTests(t, root)
	target := os.Getenv("RAZOR_SLT_FILES")
	filtered := all[:0]
	for _, p := range all {
		base := basename(p)
		if base == "select1.test" || base == "select2.test" ||
			base == "select3.test" || base == "select4.test" ||
			base == "select5.test" {
			continue
		}
		if target != "" && !contains(p, target) {
			continue
		}
		filtered = append(filtered, p)
	}
	t.Logf("scanning %d files", len(filtered))

	for _, full := range filtered {
		runFileStatement(t, full)
	}
}

func runFileStatement(t *testing.T, full string) {
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
	driver := NewRazorDriver()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := driver.Connect(ctx); err != nil {
		t.Logf("[%s] connect err: %v", full, err)
		return
	}
	defer driver.Close(context.Background())

	var stmtFails []string
	for i := range recs {
		rec := &recs[i]
		if rec.Kind != RecordStatementOK && rec.Kind != RecordStatementError {
			continue
		}
		err := driver.Exec(ctx, rec.SQL)
		wantErr := rec.Kind == RecordStatementError
		gotErr := err != nil
		if gotErr != wantErr {
			stmtFails = append(stmtFails,
				formatFail(rec, err, wantErr))
		}
	}
	t.Logf("[%s] stmt-fail-lines=%d", full, len(stmtFails))
	for _, s := range stmtFails {
		t.Logf("  %s", s)
	}
}

func formatFail(rec *Record, err error, wantErr bool) string {
	verdict := "OK"
	if wantErr {
		verdict = "ERROR"
	}
	got := "ok"
	if err != nil {
		got = err.Error()
	}
	return truncate("L"+lineItoa(rec.Line)+" want="+verdict+" got="+got+"\n    SQL: "+rec.SQL, 300)
}

func lineItoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func contains(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && (s == sub ||
		(len(s) > len(sub) && (s[:len(sub)+1] == sub+"/" || contains(s[1:], sub))))
}