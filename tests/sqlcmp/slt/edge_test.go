//go:build edge_probe

package slt

import (
	"context"
	"fmt"
	"testing"
)

// TestEdge_Expressions is a one-off boundary probe.
// Build tag keeps it out of the default run; invoke with
//   go test -tags edge_probe -run TestEdge ./tests/sqlcmp/slt/...
func TestEdge_Expressions(t *testing.T) {
	ctx := context.Background()
	d := NewRazorDriver()
	if err := d.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close(ctx) })

	setup := []string{
		"CREATE TABLE t (id INT PRIMARY KEY, v INT)",
		"INSERT INTO t VALUES (1, 10)",
		"INSERT INTO t VALUES (2, NULL)",
		"INSERT INTO t VALUES (3, 0)",
	}
	for _, s := range setup {
		if err := d.Exec(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	queries := []string{
		"SELECT COUNT(*), COUNT(v), SUM(v), AVG(v), MIN(v), MAX(v) FROM t",
		"SELECT id, v, v+1, v*0, v/1, v-1 FROM t ORDER BY id",
		"SELECT v IS NULL, v IS NOT NULL, v = 0, v != 0 FROM t ORDER BY id",
		"SELECT id & 1, id | 1, id ^ 1 FROM t ORDER BY id",
		"SELECT NOT 0, NOT 1, -1, +1, ~1 FROM t LIMIT 1",
		"SELECT CAST(id AS TEXT), CAST(v AS INT) FROM t ORDER BY id",
		"SELECT id || 'x' FROM t ORDER BY id",
		"SELECT CASE WHEN v > 0 THEN 'pos' WHEN v < 0 THEN 'neg' ELSE 'zero' END FROM t ORDER BY id",
		"SELECT id, ROW_NUMBER() OVER (ORDER BY id) FROM t",
	}
	for _, q := range queries {
		rs, err := d.Query(ctx, q)
		if err != nil {
			t.Logf("ERR %q: %v", q, err)
			continue
		}
		t.Logf("OK  %q: %d rows", q, len(rs.Rows))
		for _, row := range rs.Rows {
			cells := make([]string, len(row))
			for i, c := range row {
				cells[i] = c.String()
			}
			t.Logf("    %v", cells)
		}
	}
	_ = fmt.Sprintf
}
