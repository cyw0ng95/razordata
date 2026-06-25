package EX

import (
	"context"
	"testing"
)

func TestReq934_NegatedFullQuery(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	ctx := context.Background()
	ex := NewExecutor()
	// 5 column table like the aggregates corpus
	ex.RegisterTable("tab0", []string{"col0", "col1", "col2"})
	ex.Exec(ctx, "INSERT INTO tab0 VALUES (1, 10, 100)")
	ex.Exec(ctx, "INSERT INTO tab0 VALUES (2, 20, 200)")
	ex.Exec(ctx, "INSERT INTO tab0 VALUES (3, 30, 300)")

	// Try aggregate-like queries with IN and negated columns
	tests := []string{
		// Basic negated IN
		"SELECT col0 FROM tab0 WHERE -col2 IN (-100)",
		// IN with computed values
		"SELECT col0 FROM tab0 WHERE -col2 IN (-col0 * 100)",
		// NOT IN with negated column
		"SELECT col0 FROM tab0 WHERE -col0 NOT IN (-1, -2)",
		// NOT -col NOT IN (-col)
		"SELECT col0 FROM tab0 WHERE NOT -col2 NOT IN (-col0)",
		// Full REQ934 pattern
		"SELECT col0 FROM tab0 WHERE NOT -+col2 NOT IN (-col0)",
	}
	for _, sql := range tests {
		_, err := ex.QueryAll(ctx, sql)
		if err != nil {
			t.Errorf("FAIL %s: %v", sql, err)
		} else {
			t.Logf("OK   %s", sql)
		}
	}
}
