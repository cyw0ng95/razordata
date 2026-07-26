package EX

import (
	"context"
	"testing"
)

func TestREQ001730_SumDistinctConstant(t *testing.T) {
	ResetForTest(t)
	ctx := context.Background()
	ex := NewExecutor()
	defer ex.Close()

	mustExec(t, ex, ctx, "CREATE TABLE tab0(pk INTEGER, col0 INTEGER, col1 INTEGER, col2 INTEGER, col3 INTEGER, col4 INTEGER, col5 INTEGER)")
	mustExec(t, ex, ctx, "INSERT INTO tab0 VALUES (0,73,61,94,99,44,76)")
	mustExec(t, ex, ctx, "INSERT INTO tab0 VALUES (1,77,17,14,74,90,81)")
	mustExec(t, ex, ctx, "INSERT INTO tab0 VALUES (2,50,10,20,30,40,50)")

	// SUM(DISTINCT 28) should be 28, not 84 (=3*28).
	// DISTINCT must apply to the constant expression result.
	rows, err := ex.QueryAll(ctx, "SELECT ALL SUM ( DISTINCT 28 ) FROM tab0")
	if err != nil {
		t.Fatalf("query error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	val := rows[0].Data[0].I64
	if val != 28 {
		t.Fatalf("SUM(DISTINCT 28): expected 28, got %d", val)
	}
}
