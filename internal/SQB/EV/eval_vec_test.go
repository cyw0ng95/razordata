package EV

import (
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// makeIntBatch creates an int64 batch for test purposes.
// If sel is non-nil, the batch's Sel is set to the given selection vector.
func makeIntBatch(sel []uint16) *UT.Batch {
	b := UT.GetBatch(1)
	b.Cols[0].Name = "x"
	if sel != nil {
		b.Sel = sel
	}
	return b
}

// TestEvalBatchExpr_DivisionByZero verifies that int64 division by zero
// produces NULL in the output column rather than a panic (C1).
func TestEvalBatchExpr_DivisionByZero(t *testing.T) {
	// Batch: one column "x" with values [10, 0, 5, 0]
	n := 4
	b := UT.GetBatch(1)
	for i := 0; i < n; i++ {
		v := int64(10 - i*5) // 10, 5, 0, -5 (row order: 10, 5, 0, -5)
		if i == 1 {
			v = 0 // row 1 has value 0
		}
		if i == 3 {
			v = -5 // row 3 has value -5
		}
		b.AppendRow(0, LX.T_INT_KW, v, false)
		b.AdvanceSize()
	}
	b.Cols[0].Name = "x"
	b.SetColMap(map[string]int{"x": 0})

	// Expression: x / 0
	expr := &PS.BinaryExpr{
		Op:    LX.T_SLASH,
		Left:  &PS.Ident{Name: "x"},
		Right: &PS.NumberLiteral{Val: 0},
	}

	// Must not panic.
	col := EvalBatchExpr(expr, b, nil)

	if col.Type != LX.T_INT_KW && col.Type != LX.T_BIGINT {
		t.Fatalf("expected int type, got %v", col.Type)
	}
	// All rows should be NULL (division by zero)
	if col.Nulls == nil {
		t.Fatal("expected all rows NULL for division by zero, but Nulls is nil")
	}
	for i := 0; i < n; i++ {
		if !col.Nulls[i] {
			t.Errorf("row %d: expected NULL for division by zero, got value %d", i, col.Data.Ints[i])
		}
	}
}

// TestEvalBatchExpr_ComparisonWithNull verifies that comparison operators
// produce NULL, not false, when either operand is NULL (C2).
func TestEvalBatchExpr_ComparisonWithNull(t *testing.T) {
	n := 4
	b := UT.GetBatch(1)
	vals := []int64{1, 10, 3, 20}
	nulls := []bool{false, true, false, true} // rows 1 and 3 are NULL
	for i := 0; i < n; i++ {
		b.AppendRow(0, LX.T_INT_KW, vals[i], nulls[i])
		b.AdvanceSize()
	}
	b.Cols[0].Name = "x"
	b.SetColMap(map[string]int{"x": 0})

	// Expression: x > 5
	expr := &PS.BinaryExpr{
		Op:    LX.T_GT,
		Left:  &PS.Ident{Name: "x"},
		Right: &PS.NumberLiteral{Val: 5},
	}

	col := EvalBatchExpr(expr, b, nil)

	if col.Type != LX.T_BOOL {
		t.Fatalf("expected bool type, got %v", col.Type)
	}

	// Expect: row 0 (1) → false, row 1 (NULL) → NULL, row 2 (3) → false, row 3 (NULL) → NULL
	expectedBools := []bool{false, false, false, false}
	expectedNulls := []bool{false, true, false, true}

	for i := 0; i < n; i++ {
		isNull := col.Nulls != nil && i < len(col.Nulls) && col.Nulls[i]
		if isNull != expectedNulls[i] {
			t.Errorf("row %d: expected NULL=%v, got NULL=%v", i, expectedNulls[i], isNull)
		}
		if !isNull && col.Data.Bools[i] != expectedBools[i] {
			t.Errorf("row %d: expected bool=%v, got %v", i, expectedBools[i], col.Data.Bools[i])
		}
	}
}

// TestEvalBatchExpr_ConcatNonString verifies that concatenation of non-text
// columns falls back to row-at-a-time evaluation (I2).
func TestEvalBatchExpr_ConcatNonString(t *testing.T) {
	n := 3
	b := UT.GetBatch(2)
	// Column 0: int64 "x"
	// Column 1: text "z"
	for i := 0; i < n; i++ {
		b.AppendRow(0, LX.T_INT_KW, int64(i), false)
		b.AppendRow(1, LX.T_TEXT, "s", false)
		b.AdvanceSize()
	}
	b.Cols[0].Name = "x"
	b.Cols[1].Name = "z"
	b.SetColMap(map[string]int{"x": 0, "z": 1})

	// Expression: x || z  (int64 || text)
	expr := &PS.BinaryExpr{
		Op:    LX.T_CONCAT,
		Left:  &PS.Ident{Name: "x"},
		Right: &PS.Ident{Name: "z"},
	}

	col := EvalBatchExpr(expr, b, nil)
	// The fallback should produce correct results. If it used the batch concat
	// kernel, non-text column "x" would silently produce "" and the result
	// would be just "s" for all rows. With the fix, the result should be
	// something like "0s", "1s", "2s" (via row-eval string conversion).
	// We verify it's not the wrong batch-kernel output by checking at least
	// one row has content from both sides.
	if col.Data.Strs == nil || len(col.Data.Strs) < n {
		t.Fatal("expected string column output")
	}
	// Row 0 should contain "0" and "s" — not just "s"
	for i := 0; i < n; i++ {
		if col.Data.Strs[i] == "s" {
			t.Errorf("row %d: concat result is just 's', expected left-side value too (e.g. '%ds')", i, i)
		}
	}
}