package EV

import (
	"fmt"
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

// TestBatchToRow_PreservesNullColumns verifies REQ001210: batchToRow
// must include pure-NULL columns in the reconstructed row. Before the
// fix, NULL-only columns were silently dropped because all Data slices
// were nil, causing scalar functions to see missing columns instead of
// NULL and return 0 instead of NULL.
func TestBatchToRow_PreservesNullColumns(t *testing.T) {
	// Build a batch with two columns: "id" (int) and "val" (pure NULL).
	b := UT.GetBatch(2)
	b.Cols[0].Name = "id"
	b.Cols[1].Name = "val"
	// Row 0: id=1, val=NULL
	b.AppendRow(0, LX.T_INT_KW, int64(1), false)
	b.AppendRow(1, LX.T_NULL, nil, true)
	b.AdvanceSize()
	b.Pooled = false

	row := batchToRow(b, 0)

	// The reconstructed row must have both columns.
	if len(row.Cols) != 2 {
		t.Fatalf("batchToRow dropped columns: got %d cols, want 2 (cols=%v)", len(row.Cols), row.Cols)
	}
	// val must be NULL, not missing.
	valIdx := -1
	for i, c := range row.Cols {
		if c == "val" {
			valIdx = i
			break
		}
	}
	if valIdx < 0 {
		t.Fatal("batchToRow dropped the pure-NULL 'val' column")
	}
	if row.Data[valIdx].Kind != KindNull {
		t.Errorf("val column should be NULL, got kind=%v val=%v", row.Data[valIdx].Kind, row.Data[valIdx].ToAny())
	}
	// id must be 1.
	if row.Data[0].I64 != 1 {
		t.Errorf("id should be 1, got %v", row.Data[0].ToAny())
	}
}

// TestEvalBatch_8Wide compares the 8-wide path against the
// 4-wide path to ensure they produce identical selection
// vectors. REQ000310.
// makeTextBatch creates a batch with a text column "z" for testing.
func makeTextBatch(n int) *UT.Batch {
	b := UT.GetBatch(1)
	for i := 0; i < n; i++ {
		b.AppendRow(0, LX.T_TEXT, fmt.Sprintf("value%d", i), false)
		b.AdvanceSize()
	}
	b.Cols[0].Name = "z"
	b.SetColMap(map[string]int{"z": 0})
	return b
}

func TestEvalBatchExpr_Abs_Int64(t *testing.T) {
	n := 4
	b := UT.GetBatch(1)
	vals := []int64{-3, 0, 7, -8}
	nulls := []bool{false, false, false, true}
	for i := 0; i < n; i++ {
		b.AppendRow(0, LX.T_INT_KW, vals[i], nulls[i])
		b.AdvanceSize()
	}
	b.Cols[0].Name = "x"
	b.SetColMap(map[string]int{"x": 0})

	expr := &PS.FunctionCall{Name: "abs", Args: []PS.Expr{&PS.Ident{Name: "x"}}}
	col := EvalBatchExpr(expr, b, nil)

	if col.Type != LX.T_INT_KW {
		t.Fatalf("expected int type, got %v", col.Type)
	}
	expected := []int64{3, 0, 7, 0}
	expectNull := []bool{false, false, false, true}
	for i := 0; i < n; i++ {
		isNull := col.Nulls != nil && i < len(col.Nulls) && col.Nulls[i]
		if isNull != expectNull[i] {
			t.Errorf("row %d: expected NULL=%v, got %v", i, expectNull[i], isNull)
		}
		if !isNull && col.Data.Ints[i] != expected[i] {
			t.Errorf("row %d: expected %d, got %d", i, expected[i], col.Data.Ints[i])
		}
	}
}

func TestEvalBatchExpr_Abs_Float(t *testing.T) {
	n := 3
	b := UT.GetBatch(1)
	vals := []float64{-3.5, 0.0, 4.2}
	for i := 0; i < n; i++ {
		b.AppendRow(0, LX.T_FLOAT_KW, vals[i], false)
		b.AdvanceSize()
	}
	b.Cols[0].Name = "x"
	b.SetColMap(map[string]int{"x": 0})

	expr := &PS.FunctionCall{Name: "abs", Args: []PS.Expr{&PS.Ident{Name: "x"}}}
	col := EvalBatchExpr(expr, b, nil)

	if col.Type != LX.T_FLOAT_KW {
		t.Fatalf("expected float type, got %v", col.Type)
	}
	expected := []float64{3.5, 0.0, 4.2}
	for i := 0; i < n; i++ {
		if col.Data.Floats[i] != expected[i] {
			t.Errorf("row %d: expected %f, got %f", i, expected[i], col.Data.Floats[i])
		}
	}
}

func TestEvalBatchExpr_Length(t *testing.T) {
	n := 3
	b := makeTextBatch(n)
	expr := &PS.FunctionCall{Name: "length", Args: []PS.Expr{&PS.Ident{Name: "z"}}}
	col := EvalBatchExpr(expr, b, nil)

	if col.Type != LX.T_INT_KW {
		t.Fatalf("expected int type, got %v", col.Type)
	}
	expected := []int64{6, 6, 6} // "value0"=6, "value1"=6, "value2"=6
	for i := 0; i < n; i++ {
		if col.Data.Ints[i] != expected[i] {
			t.Errorf("row %d: expected %d, got %d", i, expected[i], col.Data.Ints[i])
		}
	}
}

func TestEvalBatchExpr_Upper(t *testing.T) {
	n := 2
	b := UT.GetBatch(1)
	b.AppendRow(0, LX.T_TEXT, "hello", false)
	b.AdvanceSize()
	b.AppendRow(0, LX.T_TEXT, "World", false)
	b.AdvanceSize()
	b.Cols[0].Name = "z"
	b.SetColMap(map[string]int{"z": 0})

	expr := &PS.FunctionCall{Name: "upper", Args: []PS.Expr{&PS.Ident{Name: "z"}}}
	col := EvalBatchExpr(expr, b, nil)

	if col.Type != LX.T_TEXT {
		t.Fatalf("expected text type, got %v", col.Type)
	}
	expected := []string{"HELLO", "WORLD"}
	for i := 0; i < n; i++ {
		if col.Data.Strs[i] != expected[i] {
			t.Errorf("row %d: expected %q, got %q", i, expected[i], col.Data.Strs[i])
		}
	}
}

func TestEvalBatchExpr_Lower(t *testing.T) {
	n := 2
	b := UT.GetBatch(1)
	b.AppendRow(0, LX.T_TEXT, "HELLO", false)
	b.AdvanceSize()
	b.AppendRow(0, LX.T_TEXT, "World", false)
	b.AdvanceSize()
	b.Cols[0].Name = "z"
	b.SetColMap(map[string]int{"z": 0})

	expr := &PS.FunctionCall{Name: "lower", Args: []PS.Expr{&PS.Ident{Name: "z"}}}
	col := EvalBatchExpr(expr, b, nil)

	if col.Type != LX.T_TEXT {
		t.Fatalf("expected text type, got %v", col.Type)
	}
	expected := []string{"hello", "world"}
	for i := 0; i < n; i++ {
		if col.Data.Strs[i] != expected[i] {
			t.Errorf("row %d: expected %q, got %q", i, expected[i], col.Data.Strs[i])
		}
	}
}

func TestEvalBatchExpr_Mod(t *testing.T) {
	n := 4
	b := UT.GetBatch(2)
	leftVals := []int64{10, 7, 5, 3}
	rightVals := []int64{3, 0, 2, 7}
	nulls := []bool{false, false, false, true}
	for i := 0; i < n; i++ {
		b.AppendRow(0, LX.T_INT_KW, leftVals[i], false)
		b.AppendRow(1, LX.T_INT_KW, rightVals[i], nulls[i] || (i == 1))
		b.AdvanceSize()
	}
	b.Cols[0].Name = "a"
	b.Cols[1].Name = "b"
	b.SetColMap(map[string]int{"a": 0, "b": 1})

	// MOD(a, b)
	expr := &PS.FunctionCall{Name: "mod", Args: []PS.Expr{
		&PS.Ident{Name: "a"},
		&PS.Ident{Name: "b"},
	}}
	col := EvalBatchExpr(expr, b, nil)

	expected := []int64{1, 0, 1, 0}
	expectNull := []bool{false, true, false, true}
	for i := 0; i < n; i++ {
		isNull := col.Nulls != nil && i < len(col.Nulls) && col.Nulls[i]
		if isNull != expectNull[i] {
			t.Errorf("row %d: expected NULL=%v, got %v", i, expectNull[i], isNull)
		}
		if !isNull && col.Data.Ints[i] != expected[i] {
			t.Errorf("row %d: expected %d, got %d", i, expected[i], col.Data.Ints[i])
		}
	}
}

func TestEvalBatchExpr_Sign_Int(t *testing.T) {
	n := 4
	b := UT.GetBatch(1)
	vals := []int64{-5, 0, 8, -1}
	nulls := []bool{false, false, false, true}
	for i := 0; i < n; i++ {
		b.AppendRow(0, LX.T_INT_KW, vals[i], nulls[i])
		b.AdvanceSize()
	}
	b.Cols[0].Name = "x"
	b.SetColMap(map[string]int{"x": 0})

	expr := &PS.FunctionCall{Name: "sign", Args: []PS.Expr{&PS.Ident{Name: "x"}}}
	col := EvalBatchExpr(expr, b, nil)

	expected := []int64{-1, 0, 1, 0}
	expectNull := []bool{false, false, false, true}
	for i := 0; i < n; i++ {
		isNull := col.Nulls != nil && i < len(col.Nulls) && col.Nulls[i]
		if isNull != expectNull[i] {
			t.Errorf("row %d: expected NULL=%v, got %v", i, expectNull[i], isNull)
		}
		if !isNull && col.Data.Ints[i] != expected[i] {
			t.Errorf("row %d: expected %d, got %d", i, expected[i], col.Data.Ints[i])
		}
	}
}

func TestEvalBatchExpr_Function_Fallback(t *testing.T) {
	// Unknown function should fall back to row-at-a-time.
	n := 2
	b := makeTextBatch(n)
	expr := &PS.FunctionCall{Name: "nonexistent", Args: []PS.Expr{&PS.Ident{Name: "z"}}}
	col := EvalBatchExpr(expr, b, nil)
	_ = col
}

func TestEvalBatchExpr_Abs_MinInt64_ReturnsNull(t *testing.T) {
	b := UT.GetBatch(1)
	b.AppendRow(0, LX.T_INT_KW, int64(-9223372036854775808), false)
	b.AdvanceSize()
	b.AppendRow(0, LX.T_INT_KW, int64(5), false)
	b.AdvanceSize()
	b.Cols[0].Name = "x"
	b.SetColMap(map[string]int{"x": 0})

	expr := &PS.FunctionCall{Name: "abs", Args: []PS.Expr{&PS.Ident{Name: "x"}}}
	col := EvalBatchExpr(expr, b, nil)

	if col.Nulls == nil || !col.Nulls[0] {
		t.Error("expected ABS(MinInt64) to return NULL")
	}
	if col.Nulls != nil && len(col.Nulls) > 1 && col.Nulls[1] {
		t.Error("expected ABS(5) to not be NULL")
	}
	if !col.Nulls[0] || col.Data.Ints[1] != 5 {
		t.Errorf("expected row 1=5, got %d", col.Data.Ints[1])
	}
}

func TestEvalBatchExpr_Length_Null(t *testing.T) {
	b := UT.GetBatch(1)
	b.AppendRow(0, LX.T_TEXT, "hello", false)
	b.AdvanceSize()
	b.AppendRow(0, LX.T_TEXT, "", true) // NULL
	b.AdvanceSize()
	b.Cols[0].Name = "z"
	b.SetColMap(map[string]int{"z": 0})

	expr := &PS.FunctionCall{Name: "length", Args: []PS.Expr{&PS.Ident{Name: "z"}}}
	col := EvalBatchExpr(expr, b, nil)

	if col.Data.Ints[0] != 5 {
		t.Errorf("row 0: expected 5, got %d", col.Data.Ints[0])
	}
	if col.Nulls == nil || !col.Nulls[1] {
		t.Error("expected row 1 to be NULL")
	}
}

func TestEvalBatchExpr_OctetLength(t *testing.T) {
	b := UT.GetBatch(1)
	b.AppendRow(0, LX.T_TEXT, "hello", false)
	b.AdvanceSize()
	b.AppendRow(0, LX.T_TEXT, "你好", false)
	b.AdvanceSize()
	b.Cols[0].Name = "z"
	b.SetColMap(map[string]int{"z": 0})

	expr := &PS.FunctionCall{Name: "octet_length", Args: []PS.Expr{&PS.Ident{Name: "z"}}}
	col := EvalBatchExpr(expr, b, nil)

	if col.Data.Ints[0] != 5 {
		t.Errorf("row 0 'hello': expected 5, got %d", col.Data.Ints[0])
	}
	if col.Data.Ints[1] != 6 {
		t.Errorf("row 1 '你好': expected 6, got %d", col.Data.Ints[1])
	}
}

func TestEvalBatchExpr_SqliteVersion(t *testing.T) {
	b := UT.GetBatch(0)
	b.Size = 0

	expr := &PS.FunctionCall{Name: "sqlite_version", Args: nil}
	col := EvalBatchExpr(expr, b, nil)

	if col.Type != LX.T_TEXT {
		t.Fatalf("expected text, got %v", col.Type)
	}
}

func TestEvalBatch_8Wide(t *testing.T) {
	b := makeIntBatch(nil)
	defer b.Put()
	for i := int64(0); i < 100; i++ {
		b.AppendRow(0, LX.T_INT_KW, i, false)
		b.AdvanceSize()
	}
	b.SetColMap(map[string]int{"x": 0})

	expr := &PS.BinaryExpr{
		Op:    LX.T_LT,
		Left:  &PS.Ident{Name: "x"},
		Right: &PS.NumberLiteral{Val: 50},
	}
	sel := EvalBatch(expr, b, nil)
	if len(sel) != 50 {
		t.Errorf("expected 50 rows, got %d", len(sel))
	}
	for i, idx := range sel {
		if int(idx) != i {
			t.Errorf("sel[%d]=%d, want %d", i, idx, i)
		}
	}
}
