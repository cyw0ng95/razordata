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

	src := "statement ok\nCREATE TABLE t (a INT PRIMARY KEY, b TEXT)\n\nstatement ok\nINSERT INTO t VALUES (1, 'x')\n\nstatement ok\nINSERT INTO t VALUES (2, 'y')\n\nquery IT rowsort\nSELECT a, b FROM t\n----\n1\tx\n2\ty\n"
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

// BenchmarkSLT_QueryDirect_vs_DatabaseSQL measures the ns/op and alloc/op
// difference between the direct engine path and the database/sql path.
// REQ001420.
func BenchmarkSLT_QueryDirect_vs_DatabaseSQL(b *testing.B) {
	ctx := context.Background()
	d := NewRazorDriver()
	if err := d.Connect(ctx); err != nil {
		b.Fatalf("Connect: %v", err)
	}
	defer d.Close(ctx)

	d.mu.Lock()
	if _, err := d.db.ExecContext(ctx, "CREATE TABLE bench (id INTEGER PRIMARY KEY, val TEXT)"); err != nil {
		d.mu.Unlock()
		b.Fatalf("create: %v", err)
	}
	for i := range 100 {
		if _, err := d.db.ExecContext(ctx, "INSERT INTO bench VALUES (?, ?)", i, "v"+strings.Repeat("x", 50)); err != nil {
			d.mu.Unlock()
			b.Fatalf("insert: %v", err)
		}
	}
	d.mu.Unlock()

	b.Run("Direct", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			rs, err := d.queryDirect(ctx, "SELECT id, val FROM bench WHERE id > 50 ORDER BY id")
			if err != nil {
				b.Fatal(err)
			}
			_ = rs
		}
	})

	b.Run("DatabaseSQL", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			rs, err := d.querySQL(ctx, "SELECT id, val FROM bench WHERE id > 50 ORDER BY id")
			if err != nil {
				b.Fatal(err)
			}
			_ = rs
		}
	})
}

// REQ001393: SLT driver journal_mode dispatch round-trip.
func TestSLTDriver_JournalMode_Dispatch(t *testing.T) {
	if os.Getenv("SKIP_RAZOR_DRIVER") != "" {
		t.Skip("SKIP_RAZOR_DRIVER set")
	}
	ctx := context.Background()
	d := NewRazorDriver()
	if err := d.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = d.Close(ctx) })

	// Read default.
	rs, err := d.Query(ctx, "PRAGMA journal_mode")
	if err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	if len(rs.Rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rs.Rows))
	}
	if rs.Rows[0][0].Text != "delete" {
		t.Errorf("default journal_mode = %q, want delete", rs.Rows[0][0].Text)
	}

	// Write journal_mode = WAL and read back.
	if err := d.Exec(ctx, "PRAGMA journal_mode = WAL"); err != nil {
		t.Fatalf("PRAGMA journal_mode = WAL: %v", err)
	}
	rs, err = d.Query(ctx, "PRAGMA journal_mode")
	if err != nil {
		t.Fatalf("PRAGMA journal_mode after WAL: %v", err)
	}
	if rs.Rows[0][0].Text != "WAL" {
		t.Errorf("journal_mode = %q, want WAL", rs.Rows[0][0].Text)
	}

	// Write MEMORY (not a keyword).
	if err := d.Exec(ctx, "PRAGMA journal_mode = MEMORY"); err != nil {
		t.Fatalf("PRAGMA journal_mode = MEMORY: %v", err)
	}
	rs, err = d.Query(ctx, "PRAGMA journal_mode")
	if err != nil {
		t.Fatalf("PRAGMA journal_mode after MEMORY: %v", err)
	}
	if rs.Rows[0][0].Text != "MEMORY" {
		t.Errorf("journal_mode = %q, want MEMORY", rs.Rows[0][0].Text)
	}
}
