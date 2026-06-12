//go:build edge_probe

package slt

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestEdge_LargeStrings probes text-typed edge sizes.
func TestEdge_LargeStrings(t *testing.T) {
	ctx := context.Background()
	d := NewRazorDriver()
	if err := d.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close(ctx) })

	if err := d.Exec(ctx, "CREATE TABLE t (id INT PRIMARY KEY, s TEXT)"); err != nil {
		t.Fatal(err)
	}

	// Boundary sizes: empty, 1 byte, 4 KiB (one block),
	// 8 KiB (spans 2 blocks), 64 KiB (16 blocks),
	// 1 MiB (lots of blocks).
	sizes := []int{0, 1, 4096, 8192, 65536, 1 << 20, 100 << 20}
	for i, n := range sizes {
		s := strings.Repeat("x", n)
		// Parameter binding not available in this driver; we
		// build the SQL by quoting. For n=0 use '' literal.
		lit := "'" + s + "'"
		stmt := "INSERT INTO t VALUES (" + itoaEdge(i+1) + ", " + lit + ")"
		if err := d.Exec(ctx, stmt); err != nil {
			fmt.Fprintf(os.Stderr, "DBG: INSERT size=%d err: %v\n", n, err)
			continue
		}
		fmt.Fprintf(os.Stderr, "DBG: INSERT size=%d: ok\n", n)
		rs, err := d.Query(ctx, "SELECT s FROM t WHERE id = "+itoaEdge(i+1))
		if err != nil {
			t.Logf("SELECT size=%d err: %v", n, err)
			continue
		}
		if len(rs.Rows) != 1 {
			fmt.Fprintf(os.Stderr, "DBG: size=%d: row count %d\n", n, len(rs.Rows))
			continue
		}
		gotLen := int64(len(rs.Rows[0][0].Text))
		if gotLen != int64(n) {
			t.Errorf("size=%d: stored %d bytes, want %d", n, gotLen, n)
		} else {
			fmt.Fprintf(os.Stderr, "DBG: size=%d: round-trip OK (len=%d)\n", n, gotLen)
		}
		_ = d
		_ = ctx
	}
}
