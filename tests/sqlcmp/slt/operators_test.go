//go:build edge_probe

package slt

import (
	"context"
	"fmt"
	"os"
	"testing"
)

// TestEdge_SQLOperators enumerates SQLite-standard operators
// the parser should accept. Each is reported as OK
// (compiled and ran) or ERR (with the engine error).
func TestEdge_SQLOperators(t *testing.T) {
	ctx := context.Background()
	d := NewRazorDriver()
	if err := d.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close(ctx) })

	if err := d.Exec(ctx, "CREATE TABLE t (id INT PRIMARY KEY, a INT, b INT, s TEXT)"); err != nil {
		t.Fatal(err)
	}
	if err := d.Exec(ctx, "INSERT INTO t VALUES (1, 5, 3, 'hello')"); err != nil {
		t.Fatal(err)
	}
	if err := d.Exec(ctx, "INSERT INTO t VALUES (2, NULL, NULL, 'world')"); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		desc, query string
	}{
		// Arithmetic
		{"add", "SELECT a + b FROM t WHERE id = 1"},
		{"sub", "SELECT a - b FROM t WHERE id = 1"},
		{"mul", "SELECT a * b FROM t WHERE id = 1"},
		{"div", "SELECT a / b FROM t WHERE id = 1"},
		{"mod", "SELECT a % b FROM t WHERE id = 1"},
		// Comparison
		{"eq", "SELECT a = b FROM t WHERE id = 1"},
		{"ne", "SELECT a != b FROM t WHERE id = 1"},
		{"lt", "SELECT a < b FROM t WHERE id = 1"},
		{"gt", "SELECT a > b FROM t WHERE id = 1"},
		{"le", "SELECT a <= b FROM t WHERE id = 1"},
		{"ge", "SELECT a >= b FROM t WHERE id = 1"},
		// Logical
		{"and", "SELECT (a > 0) AND (b > 0) FROM t WHERE id = 1"},
		{"or", "SELECT (a > 0) OR (b < 0) FROM t WHERE id = 1"},
		{"not_eq", "SELECT NOT (a = b) FROM t WHERE id = 1"},
		// IS
		{"is_null", "SELECT a IS NULL FROM t WHERE id = 2"},
		{"is_not_null", "SELECT a IS NOT NULL FROM t WHERE id = 2"},
		// IN
		{"in_list", "SELECT a IN (1, 2, 3, 5) FROM t WHERE id = 1"},
		// BETWEEN
		{"between", "SELECT a BETWEEN 1 AND 10 FROM t WHERE id = 1"},
		// LIKE
		{"like", "SELECT s LIKE 'h%' FROM t WHERE id = 1"},
		// CASE
		{"case_when", "SELECT CASE WHEN a > 0 THEN 'pos' ELSE 'neg' END FROM t WHERE id = 1"},
		// COALESCE
		{"coalesce", "SELECT COALESCE(a, 0) FROM t WHERE id = 2"},
		// NULLIF
		{"nullif", "SELECT NULLIF(a, 0) FROM t WHERE id = 1"},
		// CAST
		{"cast", "SELECT CAST(a AS TEXT) FROM t WHERE id = 1"},
	}
	for _, c := range cases {
		rs, err := d.Query(ctx, c.query)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERR  %-12s %q: %v\n", c.desc, c.query, err)
			continue
		}
		fmt.Fprintf(os.Stderr, "OK   %-12s %d rows\n", c.desc, len(rs.Rows))
	}
}
