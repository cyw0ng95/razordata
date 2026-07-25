package EV

import (
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// TestEvalBatchExpr_UnaryNot verifies REQ001991: vectorized NOT in CASE
// WHEN conditions. The dispatch in EvalBatchExpr must handle *PS.UnaryExpr
// and return a UT.Column without falling back to row-at-a-time.
func TestEvalBatchExpr_UnaryNot(t *testing.T) {
	b := UT.GetBatch(1)
	for i := 0; i < 4; i++ {
		b.AppendRow(0, LX.T_INT_KW, int64(i), false)
		b.AdvanceSize()
	}
	b.Cols[0].Name = "x"
	b.SetColMap(map[string]int{"x": 0})

	// Expression: NOT (x > 1)  →  where x > 1 is true at i=2,3
	// x > 1: [false, false, true, true]
	// NOT:   [true, true, false, false]
	expr := &PS.UnaryExpr{
		Op: LX.T_NOT,
		Operand: &PS.BinaryExpr{
			Op:    LX.T_GT,
			Left:  &PS.Ident{Name: "x"},
			Right: &PS.NumberLiteral{Val: 1},
		},
	}

	col := EvalBatchExpr(expr, b, nil)
	if col.Type != LX.T_BOOL {
		t.Fatalf("expected T_BOOL, got %v", col.Type)
	}
	want := []bool{true, true, false, false}
	for i, w := range want {
		if col.Data.Bools[i] != w {
			t.Errorf("row %d: got %v want %v", i, col.Data.Bools[i], w)
		}
	}
}

// TestEvalBatchExpr_UnaryMinus verifies REQ001991: vectorized T_MINUS
// produces negative values per row, not a row-fallback.
func TestEvalBatchExpr_UnaryMinus(t *testing.T) {
	b := UT.GetBatch(1)
	for i := 0; i < 4; i++ {
		b.AppendRow(0, LX.T_INT_KW, int64(i+1), false) // 1,2,3,4
		b.AdvanceSize()
	}
	b.Cols[0].Name = "x"
	b.SetColMap(map[string]int{"x": 0})

	// Expression: -x → -1, -2, -3, -4
	expr := &PS.UnaryExpr{
		Op:      LX.T_MINUS,
		Operand: &PS.Ident{Name: "x"},
	}

	col := EvalBatchExpr(expr, b, nil)
	if col.Type != LX.T_INT_KW && col.Type != LX.T_BIGINT {
		t.Fatalf("expected int type, got %v", col.Type)
	}
	want := []int64{-1, -2, -3, -4}
	for i, w := range want {
		if col.Data.Ints[i] != w {
			t.Errorf("row %d: got %d want %d", i, col.Data.Ints[i], w)
		}
	}
}

// TestEvalBatchExpr_UnaryMinusFloat verifies REQ001991: T_MINUS works
// on float64 batches.
func TestEvalBatchExpr_UnaryMinusFloat(t *testing.T) {
	b := UT.GetBatch(1)
	for i := 0; i < 3; i++ {
		b.AppendRow(0, LX.T_FLOAT_KW, float64(i+1)*1.5, false)
		b.AdvanceSize()
	}
	b.Cols[0].Name = "x"
	b.SetColMap(map[string]int{"x": 0})

	expr := &PS.UnaryExpr{
		Op:      LX.T_MINUS,
		Operand: &PS.Ident{Name: "x"},
	}
	col := EvalBatchExpr(expr, b, nil)
	if col.Type != LX.T_FLOAT_KW {
		t.Fatalf("expected T_FLOAT_KW, got %v", col.Type)
	}
	want := []float64{-1.5, -3.0, -4.5}
	for i, w := range want {
		if col.Data.Floats[i] != w {
			t.Errorf("row %d: got %v want %v", i, col.Data.Floats[i], w)
		}
	}
}

// TestEvalBatchExpr_UnaryMinusNull verifies REQ001991: T_MINUS
// preserves NULL propagation. NULL input row → NULL output row.
func TestEvalBatchExpr_UnaryMinusNull(t *testing.T) {
	b := UT.GetBatch(1)
	// Row 0: x=5, Row 1: x=NULL, Row 2: x=7
	// Note: AppendRow uses b.Size for the Nulls slot, so we use
	// AdvanceSize to advance b.Size between rows.
	b.AppendRow(0, LX.T_INT_KW, int64(5), false)
	b.AdvanceSize()
	b.AppendRow(0, LX.T_INT_KW, int64(0), true)
	b.AdvanceSize()
	b.AppendRow(0, LX.T_INT_KW, int64(7), false)
	b.AdvanceSize()
	b.Cols[0].Name = "x"
	b.SetColMap(map[string]int{"x": 0})

	expr := &PS.UnaryExpr{
		Op:      LX.T_MINUS,
		Operand: &PS.Ident{Name: "x"},
	}
	col := EvalBatchExpr(expr, b, nil)
	// Row 1 should be NULL.
	if col.Nulls == nil {
		t.Fatal("expected Nulls slice, got nil")
	}
	if !col.Nulls[1] {
		t.Errorf("row 1: expected NULL, got value %d", col.Data.Ints[1])
	}
	if col.Data.Ints[0] != -5 {
		t.Errorf("row 0: expected -5, got %d", col.Data.Ints[0])
	}
	if col.Data.Ints[2] != -7 {
		t.Errorf("row 2: expected -7, got %d", col.Data.Ints[2])
	}
}

// TestEvalBatchExpr_InExprSubqueryFallsBack verifies REQ001990's
// safety fallback: when the planner is unavailable, the dispatch
// gracefully degrades to row-at-a-time evalRowFallbackColumn instead
// of crashing. REQ001990 hard requirement: never panic on missing
// planner.
func TestEvalBatchExpr_InExprSubqueryFallsBack(t *testing.T) {
	b := UT.GetBatch(1)
	for i := 0; i < 3; i++ {
		b.AppendRow(0, LX.T_INT_KW, int64(i+1), false)
		b.AdvanceSize()
	}
	b.Cols[0].Name = "x"
	b.SetColMap(map[string]int{"x": 0})

	expr := &PS.InExpr{
		Expr: &PS.Ident{Name: "x"},
		Subquery: &PS.Select{
			Cols: []PS.Expr{&PS.Ident{Name: "y"}},
		},
	}
	// No ExecCtx.Planner → getSubqueryPlanner returns nil → fallback.
	col := EvalBatchExpr(expr, b, nil)
	// Should return a column (possibly empty) without crashing.
	if col.Type == 0 && len(col.Data.Bools) == 0 && len(col.Data.Ints) == 0 {
		t.Fatal("fallback returned zero-value column with no data")
	}
}