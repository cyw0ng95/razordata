package EX

import (
	"context"
	"testing"
)

func TestREQ001194_MultiColumnUnaryAggregate(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ctx := context.Background()
	ex := NewExecutor()

	rs, err := ex.QueryAll(ctx, "SELECT - MAX( - 76 ), - 15")
	if err != nil {
		t.Fatalf("query error: %v", err)
	}
	if len(rs) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rs))
	}

	v1 := rs[0].Data[0].ToAny()
	v2 := rs[0].Data[1].ToAny()
	if v1 != int64(76) {
		t.Errorf("column 0: got %v, want 76", v1)
	}
	if v2 != int64(-15) {
		t.Errorf("column 1: got %v, want -15", v2)
	}
}

func TestREQ001194_DistinctUnaryExpr(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ctx := context.Background()
	ex := NewExecutor()

	rs, err := ex.QueryAll(ctx, "SELECT DISTINCT 32 * + + 59 * + ( + + 41 ) - - 9")
	if err != nil {
		t.Fatalf("query error: %v", err)
	}
	if len(rs) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rs))
	}

	v := rs[0].Data[0].ToAny()
	// 32 * 59 * 41 + 9 = 32 * 59 * 41 + 9 = 32 * 2419 + 9
	// Actually parse: 32 * + + 59 * + ( + + 41 ) - - 9
	// = 32 * (+ (+ 59)) * (+ (+ (+ 41))) - (-9)
	// = 32 * 59 * 41 - (-9) = 77408 + 9 = 77417
	if v != int64(77417) {
		t.Errorf("got %v, want 77417", v)
	}
}

func TestREQ001194_PureNonAggregateInAggregateList(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ctx := context.Background()
	ex := NewExecutor()

	rs, err := ex.QueryAll(ctx, "SELECT MAX(1), 2")
	if err != nil {
		t.Fatalf("query error: %v", err)
	}
	if len(rs) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rs))
	}

	v1 := rs[0].Data[0].ToAny()
	v2 := rs[0].Data[1].ToAny()
	if v1 != int64(1) {
		t.Errorf("column 0: got %v, want 1", v1)
	}
	if v2 != int64(2) {
		t.Errorf("column 1: got %v, want 2", v2)
	}
}
