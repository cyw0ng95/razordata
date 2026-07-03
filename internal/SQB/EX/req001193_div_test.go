package EX

import (
	"context"
	"testing"
)

// REQ001193: DIV operator — integer division with double-negative syntax.
func TestReq001193_DivEval(t *testing.T) {
	ResetForTest(t)
	ctx := context.Background()
	ex := NewExecutor()

	tests := []struct {
		sql    string
		expect int64
	}{
		{"SELECT 20 DIV 4", 5},
		{"SELECT 20 DIV - - 96", 0},    // 20 DIV 96 = 0
		{"SELECT 20 DIV -3", -6},       // 20 / -3 = -6 (trunc toward zero)
		{"SELECT -20 DIV 3", -6},       // -20 / 3 = -6
		{"SELECT 100 DIV 3", 33},       // 100 / 3 = 33
		{"SELECT 7 DIV 2", 3},          // 7 / 2 = 3
		{"SELECT 0 DIV 5", 0},          // 0 / 5 = 0
	}
	for _, tt := range tests {
		t.Run(tt.sql, func(t *testing.T) {
			rows, err := ex.QueryAll(ctx, tt.sql)
			if err != nil {
				t.Fatalf("query %q: %v", tt.sql, err)
			}
			if len(rows) != 1 {
				t.Fatalf("expected 1 row, got %d", len(rows))
			}
			got := rows[0].Data[0].I64
			if got != tt.expect {
				t.Errorf("%s = %d, want %d", tt.sql, got, tt.expect)
			}
		})
	}

	// NULL propagation.
	rows, err := ex.QueryAll(ctx, "SELECT NULL DIV 5")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 1 || !rows[0].Data[0].IsNull() {
		t.Errorf("NULL DIV 5 should return NULL")
	}

	// Division by zero returns NULL.
	rows, err = ex.QueryAll(ctx, "SELECT 10 DIV 0")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 1 || !rows[0].Data[0].IsNull() {
		t.Errorf("10 DIV 0 should return NULL")
	}
}
