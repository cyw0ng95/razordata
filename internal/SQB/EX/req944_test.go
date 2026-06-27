package EX

import (
	"context"
	"testing"
)

func TestChain_DistinctAggregates_UnaryChain(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ctx := context.Background()

	ex := NewExecutor()
	ex.RegisterTable("tab2", []string{"col0", "col1", "col2"})
	ex.Exec(ctx, "INSERT INTO tab2 VALUES (64, 77, 40)")
	ex.Exec(ctx, "INSERT INTO tab2 VALUES (75, 67, 58)")
	ex.Exec(ctx, "INSERT INTO tab2 VALUES (46, 51, 23)")

	// -COUNT(DISTINCT +col0) - ++SUM(DISTINCT -(-11)) + 32
	// COUNT(DISTINCT +col0) over {64,75,46} = 3
	// SUM(DISTINCT -(-11)) = SUM(DISTINCT 11) = 11
	// -3 - 11 + 32 = 18
	rs, err := ex.QueryAll(ctx, "SELECT - COUNT(DISTINCT + col0) - + + SUM(DISTINCT - ( - 11 ) ) + 32 FROM tab2 AS cor0")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rs) != 1 {
		t.Fatalf("got %d rows, want 1", len(rs))
	}
	got, ok := rs[0].Data[0].ToAny().(int64)
	if !ok {
		t.Fatalf("result type %T, want int64", rs[0].Data[0].ToAny())
	}
	if got != 18 {
		t.Errorf("got %d, want 18", got)
	}

	// Also test the loose-space variant
	rs, err = ex.QueryAll(ctx, "SELECT - COUNT ( DISTINCT + col0 ) - + + SUM ( DISTINCT - ( - 11 ) ) + 32 FROM tab2 AS cor0")
	if err != nil {
		t.Fatalf("query (loose): %v", err)
	}
	got, ok = rs[0].Data[0].ToAny().(int64)
	if !ok {
		t.Fatalf("result type %T, want int64", rs[0].Data[0].ToAny())
	}
	if got != 18 {
		t.Errorf("loose: got %d, want 18", got)
	}
}