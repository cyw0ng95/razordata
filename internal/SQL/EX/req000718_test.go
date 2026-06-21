package EX

import (
	"context"
	"strconv"
	"testing"
)

// REQ000718: SQLite allows `expr IN tableName` as shorthand for
// `expr IN (SELECT * FROM tableName)`. This is the test for the
// executor end; the parser side is exercised by parsing the query.
func TestREQ000718_INTableNameNoParens(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t1", []string{"x"})

	ctx := context.Background()
	for _, v := range []int{1, 2, 3, 4, 5} {
		if _, err := ex.Exec(ctx, "INSERT INTO t1 VALUES ("+strconv.Itoa(v)+")"); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	// Without parens — parser should rewrite to IN (SELECT * FROM t1).
	rows, err := ex.QueryAll(ctx, "SELECT x FROM t1 WHERE x IN t1 ORDER BY x")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 5 {
		t.Errorf("expected 5 rows, got %d", len(rows))
	}

	// Sanity: same query with parens should also work.
	rows2, err := ex.QueryAll(ctx, "SELECT x FROM t1 WHERE x IN (SELECT * FROM t1) ORDER BY x")
	if err != nil {
		t.Fatalf("query parens: %v", err)
	}
	if len(rows2) != 5 {
		t.Errorf("parens form: expected 5 rows, got %d", len(rows2))
	}
}
