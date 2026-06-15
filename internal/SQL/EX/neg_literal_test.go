package EX

import (
	"context"
	"testing"
)

// TestNegativeLiteralRegression verifies the fix for REQ000443b:
// `SELECT -a FROM t1` must return the negated column value, not
// `ex: eval error`. The original bug was case-sensitivity in Row.Lookup
// (parser uppercased identifiers but the column store kept the original
// case) and missing column-name extraction for UnaryExpr in Project.Next.
func TestNegativeLiteralRegression(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t1", []string{"a", "b"})
	ctx := context.Background()
	if _, err := ex.Exec(ctx, "INSERT INTO t1 VALUES (10, 20)"); err != nil {
		t.Fatalf("insert: %v", err)
	}

	rows, err := ex.QueryAll(ctx, "SELECT -a FROM t1")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if v, ok := rows[0].Data[0].(int64); !ok || v != -10 {
		t.Errorf("expected -10, got %v (type %T)", rows[0].Data[0], rows[0].Data[0])
	}

	// Chained unary minus must also work.
	rows2, err := ex.QueryAll(ctx, "SELECT -(-a) FROM t1")
	if err != nil {
		t.Fatalf("chained unary minus: %v", err)
	}
	if len(rows2) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows2))
	}
	if v, ok := rows2[0].Data[0].(int64); !ok || v != 10 {
		t.Errorf("expected 10, got %v", rows2[0].Data[0])
	}

	// Case-insensitive: column "A" should resolve to "a" in the row.
	rows3, err := ex.QueryAll(ctx, "SELECT -A FROM t1")
	if err != nil {
		t.Fatalf("uppercase ident: %v", err)
	}
	if v, ok := rows3[0].Data[0].(int64); !ok || v != -10 {
		t.Errorf("expected -10, got %v", rows3[0].Data[0])
	}
}
