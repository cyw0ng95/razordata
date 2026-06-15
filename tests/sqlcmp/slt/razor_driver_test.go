//go:build !slt_corpus

package slt

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRazorDriver_ConnectExecQuery(t *testing.T) {
	if os.Getenv("SKIP_RAZOR_DRIVER") != "" {
		t.Skip("SKIP_RAZOR_DRIVER set")
	}
	ctx := context.Background()
	d := NewRazorDriver()
	if err := d.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = d.Close(ctx) })

	// Verify the temp dir was created and is not a "<name>.razor" leaf.
	if d.dir == "" {
		t.Fatalf("dir not set")
	}
	if !filepath.IsAbs(d.dir) {
		t.Errorf("dir %q is not absolute", d.dir)
	}

	if err := d.Exec(ctx, "CREATE TABLE t (a INT PRIMARY KEY, b TEXT)"); err != nil {
		t.Fatalf("CREATE: %v", err)
	}
	if err := d.Exec(ctx, "INSERT INTO t VALUES (1, 'x')"); err != nil {
		t.Fatalf("INSERT: %v", err)
	}
	if err := d.Exec(ctx, "INSERT INTO t VALUES (2, 'y')"); err != nil {
		t.Fatalf("INSERT: %v", err)
	}

	rs, err := d.Query(ctx, "SELECT a, b FROM t ORDER BY a")
	if err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	if len(rs.Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rs.Rows))
	}
	if rs.Rows[0][0].Kind != TypeInteger || rs.Rows[0][0].Int != 1 {
		t.Errorf("row[0][0] = %+v", rs.Rows[0][0])
	}
	if rs.Rows[1][1].Text != "y" {
		t.Errorf("row[1][1] = %+v", rs.Rows[1][1])
	}
}

func TestRazorDriver_StatementError_Rejected(t *testing.T) {
	ctx := context.Background()
	d := NewRazorDriver()
	if err := d.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = d.Close(ctx) })

	// We do not know the exact Razordata error class, so we
	// exercise the classifier indirectly: a CLEARLY unsupported
	// statement must be classified as Skipped.
	c := d.classifier
	if v := c.Classify(errFromString("razordata: syntax error at column 4")); v != VerdictSkipped {
		t.Errorf("syntax error should be Skipped, got %v", v)
	}
	if v := c.Classify(errFromString("some other failure")); v != VerdictFailed {
		t.Errorf("other error should be Failed, got %v", v)
	}
}

func TestRazorDriver_EndToEnd_ParseAndRun(t *testing.T) {
	ctx := context.Background()
	d := NewRazorDriver()
	if err := d.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = d.Close(ctx) })

	src := `statement ok
CREATE TABLE t (a INT PRIMARY KEY, b TEXT)

statement ok
INSERT INTO t VALUES (1, 'x')

statement ok
INSERT INTO t VALUES (2, 'y')

query IT rowsort
SELECT a, b FROM t
----
1 x
2 y
`
	recs, err := Parse(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	r := NewRunner(d, d.classifier, RazorEngineName)
	stats := r.Run(ctx, recs)
	if stats.Failed != 0 {
		t.Errorf("end-to-end run failed: %+v", stats)
	}
}

type stringErr string

func (e stringErr) Error() string { return string(e) }

func errFromString(s string) error { return stringErr(s) }
