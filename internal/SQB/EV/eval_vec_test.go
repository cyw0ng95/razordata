package EV

import (
	"fmt"
	"sort"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	"github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// makeIntBatch creates a batch with a single int64 column.
func makeIntBatch(values []int64) *UT.Batch {
	b := UT.GetBatch(1)
	for _, v := range values {
		b.AppendRow(0, LX.T_INT_KW, v, false)
		b.AdvanceSize()
	}
	return b
}

// TestEvalBatch_Int64ColLit_EQ tests x = 5.
func TestEvalBatch_Int64ColLit_EQ(t *testing.T) {
	b := makeIntBatch([]int64{1, 5, 3, 5, 7, 5, 9})
	defer b.Put()

	// Direct test of compareInt64ColLit (the vectorized fast path)
	col := b.Cols[0]
	sel := CompareInt64ColLit(col, 5, LX.T_EQ, b.Size)
	expected := []uint16{1, 3, 5}
	if !equalSelection(sel, expected) {
		t.Errorf("col=5: got %v, want %v", sel, expected)
	}
}

// TestEvalBatch_Int64ColLit_LT tests x < 5.
func TestEvalBatch_Int64ColLit_LT(t *testing.T) {
	b := makeIntBatch([]int64{1, 2, 3, 5, 7, 8, 9})
	defer b.Put()

	col := b.Cols[0]
	sel := CompareInt64ColLit(col, 5, LX.T_LT, b.Size)
	expected := []uint16{0, 1, 2}
	if !equalSelection(sel, expected) {
		t.Errorf("col<5: got %v, want %v", sel, expected)
	}
}

// TestEvalBatch_Int64ColLit_GT tests x > 5.
func TestEvalBatch_Int64ColLit_GT(t *testing.T) {
	b := makeIntBatch([]int64{1, 5, 3, 5, 7, 5, 9})
	defer b.Put()

	col := b.Cols[0]
	sel := CompareInt64ColLit(col, 5, LX.T_GT, b.Size)
	expected := []uint16{4, 6}
	if !equalSelection(sel, expected) {
		t.Errorf("col>5: got %v, want %v", sel, expected)
	}
}

// TestEvalBatch_Int64ColCol tests col1 = col2.
func TestEvalBatch_Int64ColCol(t *testing.T) {
	b := UT.GetBatch(2)
	defer b.Put()
	leftVals := []int64{1, 2, 3, 4, 5}
	rightVals := []int64{1, 0, 3, 0, 5}
	for i := range leftVals {
		b.AppendRow(0, LX.T_INT_KW, leftVals[i], false)
		b.AppendRow(1, LX.T_INT_KW, rightVals[i], false)
		b.AdvanceSize()
	}

	sel := CompareInt64Cols(b.Cols[0], b.Cols[1], LX.T_EQ, b.Size)
	expected := []uint16{0, 2, 4}
	if !equalSelection(sel, expected) {
		t.Errorf("col0=col1: got %v, want %v", sel, expected)
	}
}

// TestEvalBatch_Float64ColLit tests float comparisons.
func TestEvalBatch_Float64ColLit(t *testing.T) {
	b := UT.GetBatch(1)
	defer b.Put()
	vals := []float64{1.0, 2.5, 3.7, 4.0, 5.5}
	for _, v := range vals {
		b.AppendRow(0, LX.T_FLOAT_KW, v, false)
		b.AdvanceSize()
	}

	col := b.Cols[0]
	sel := CompareFloat64ColLit(col, 3.0, LX.T_GT, b.Size)
	expected := []uint16{2, 3, 4}
	if !equalSelection(sel, expected) {
		t.Errorf("col>3.0: got %v, want %v", sel, expected)
	}
}

// TestEvalBatch_StringColLit tests string equality.
func TestEvalBatch_StringColLit(t *testing.T) {
	b := UT.GetBatch(1)
	defer b.Put()
	vals := []string{"apple", "banana", "cherry", "banana"}
	for _, v := range vals {
		b.AppendRow(0, LX.T_TEXT, v, false)
		b.AdvanceSize()
	}

	col := b.Cols[0]
	sel := CompareStringColLit(col, "banana", LX.T_EQ, b.Size)
	expected := []uint16{1, 3}
	if !equalSelection(sel, expected) {
		t.Errorf("col=banana: got %v, want %v", sel, expected)
	}
}

// TestEvalBatch_AllMatch returns nil selection when all rows match.
func TestEvalBatch_AllMatch(t *testing.T) {
	b := makeIntBatch([]int64{10, 20, 30, 40, 50})
	defer b.Put()

	// col > 0 matches all
	col := b.Cols[0]
	sel := CompareInt64ColLit(col, 0, LX.T_GT, b.Size)
	if sel == nil {
		t.Error("expected non-nil selection for all-match")
	}
	if len(sel) != 5 {
		t.Errorf("expected len(sel)=5, got %d", len(sel))
	}
}

// TestEvalBatch_NoMatch returns empty selection when no rows match.
func TestEvalBatch_NoMatch(t *testing.T) {
	b := makeIntBatch([]int64{100, 200, 300, 400, 500})
	defer b.Put()

	col := b.Cols[0]
	sel := CompareInt64ColLit(col, 100, LX.T_LT, b.Size)
	if len(sel) != 0 {
		t.Errorf("expected empty selection, got %v", sel)
	}
}

// TestEvalBatch_EmptyBatch returns nil for empty batch.
func TestEvalBatch_EmptyBatch(t *testing.T) {
	b := UT.GetBatch(1)
	defer b.Put()
	sel := EvalBatch(&PS.NumberLiteral{Val: 1}, b, nil)
	if sel != nil {
		t.Errorf("expected nil for empty batch, got %v", sel)
	}
}

// TestEvalBatch_NilExpr returns nil for nil expression.
func TestEvalBatch_NilExpr(t *testing.T) {
	b := makeIntBatch([]int64{1, 2, 3})
	defer b.Put()
	sel := EvalBatch(nil, b, nil)
	if sel != nil {
		t.Errorf("expected nil for nil expr, got %v", sel)
	}
}

// TestEvalBatch_LargeBatch verifies 4-wide unrolling correctness.
func TestEvalBatch_LargeBatch(t *testing.T) {
	const n = 1000
	b := UT.GetBatch(1)
	defer b.Put()
	for i := 0; i < n; i++ {
		b.AppendRow(0, LX.T_INT_KW, int64(i), false)
		b.AdvanceSize()
	}

	col := b.Cols[0]
	// col % 7 == 0
	sel := make([]uint16, 0)
	for i := 0; i < n; i++ {
		if i%7 == 0 {
			sel = append(sel, uint16(i))
		}
	}
	// Apply filter via vectorized path
	actual := CompareInt64ColLit(col, 0, LX.T_GE, n) // all match
	if len(actual) != n {
		t.Errorf("all-match: expected %d, got %d", n, len(actual))
	}

	// Test with 5 == 0
	sel5 := make([]uint16, 0)
	for i := 0; i < n; i++ {
		if i == 5 {
			sel5 = append(sel5, uint16(i))
		}
	}
	actual5 := CompareInt64ColLit(col, 5, LX.T_EQ, n)
	if !equalSelection(actual5, sel5) {
		t.Errorf("col==5: got %v, want %v", actual5, sel5)
	}
}

// TestSwapOp verifies operator swapping for reversed column-literal.
func TestSwapOp(t *testing.T) {
	cases := []struct {
		in, want LX.TokenType
	}{
		{LX.T_LT, LX.T_GT},
		{LX.T_LE, LX.T_GE},
		{LX.T_GT, LX.T_LT},
		{LX.T_GE, LX.T_LE},
		{LX.T_EQ, LX.T_EQ},
	}
	for _, c := range cases {
		if got := SwapOp(c.in); got != c.want {
			t.Errorf("SwapOp(%d) = %d; want %d", c.in, got, c.want)
		}
	}
}

// TestInvertSelection verifies complement of selection.
func TestInvertSelection(t *testing.T) {
	sel := []uint16{0, 2, 4}
	inv := InvertSelection(sel, 6)
	expected := []uint16{1, 3, 5}
	if !equalSelection(inv, expected) {
		t.Errorf("invert: got %v, want %v", inv, expected)
	}
}

// equalSelection checks if two selection vectors are equal.
func equalSelection(a, b []uint16) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestEvalBatch_InList verifies the vectorized IN-list path produces
// identical results to the row-at-a-time fallback (REQ000991).
func TestEvalBatch_InList(t *testing.T) {
	b := makeIntBatch([]int64{10, 20, 30, 40, 50, 60, 70, 80})
	defer b.Put()
	// Must set column name for extractColumnRef to match the Ident in InExpr.
	b.Cols[0].Name = "c0"

	// IN (10, 30, 50) — should match rows 0, 2, 4
	inExpr := &PS.InExpr{
		Expr: &PS.Ident{Name: "c0"},
		List: []PS.Expr{
			&PS.NumberLiteral{Val: 10},
			&PS.NumberLiteral{Val: 30},
			&PS.NumberLiteral{Val: 50},
		},
	}

	got := EvalBatch(inExpr, b, nil)
	want := []uint16{0, 2, 4}
	if !equalSelection(got, want) {
		t.Errorf("IN(10,30,50): got %v, want %v", got, want)
	}

	// Empty list — no rows match
	emptyExpr := &PS.InExpr{
		Expr: &PS.Ident{Name: "c0"},
		List: []PS.Expr{},
	}
	gotEmpty := EvalBatch(emptyExpr, b, nil)
	if len(gotEmpty) != 0 {
		t.Errorf("empty IN list: got %v, want empty", gotEmpty)
	}

	// Column not found — should fall back to row-at-a-time
	unkExpr := &PS.InExpr{
		Expr: &PS.Ident{Name: "unknown"},
		List: []PS.Expr{&PS.NumberLiteral{Val: 10}},
	}
	gotUnk := EvalBatch(unkExpr, b, nil)
	// Row-at-a-time fallback: row "unknown" won't be found, so EvalValue returns error,
	// and evalRowFallback skips the row (continue). No rows match.
	if len(gotUnk) != 0 {
		t.Errorf("unknown column IN list: got %v, want empty", gotUnk)
	}
}

// TestEvalBatch_NotInList verifies NOT IN via UnaryExpr wrapping (REQ000991).
func TestEvalBatch_NotInList(t *testing.T) {
	b := makeIntBatch([]int64{10, 20, 30, 40, 50})
	defer b.Put()
	b.Cols[0].Name = "c0"

	// NOT (c0 IN (20, 40)) — should match rows 0, 2, 4
	notIn := &PS.UnaryExpr{
		Op: LX.T_NOT,
		Operand: &PS.InExpr{
			Expr: &PS.Ident{Name: "c0"},
			List: []PS.Expr{
				&PS.NumberLiteral{Val: 20},
				&PS.NumberLiteral{Val: 40},
			},
		},
	}

	got := EvalBatch(notIn, b, nil)
	want := []uint16{0, 2, 4}
	if !equalSelection(got, want) {
		t.Errorf("NOT IN(20,40): got %v, want %v", got, want)
	}
}

// TestEvalBatch_InList_String verifies string IN-list vectorized path (REQ000991).
func TestEvalBatch_InList_String(t *testing.T) {
	b := UT.GetBatch(1)
	defer b.Put()
	vals := []string{"a", "b", "c", "b", "d", "e"}
	for _, v := range vals {
		b.AppendRow(0, LX.T_TEXT, v, false)
		b.AdvanceSize()
	}
	b.Cols[0].Name = "s0"

	inExpr := &PS.InExpr{
		Expr: &PS.Ident{Name: "s0"},
		List: []PS.Expr{
			&PS.StringLiteral{Val: "b"},
			&PS.StringLiteral{Val: "d"},
			&PS.StringLiteral{Val: "f"},
		},
	}

	got := EvalBatch(inExpr, b, nil)
	want := []uint16{1, 3, 4} // "b" at idx 1,3; "d" at idx 4
	if !equalSelection(got, want) {
		t.Errorf("IN('b','d','f'): got %v, want %v", got, want)
	}
}

// TestEvalBatch_InList_Nulls verifies NULL handling (REQ000991).
func TestEvalBatch_InList_Nulls(t *testing.T) {
	b := UT.GetBatch(1)
	defer b.Put()
	vals := []int64{10, 20, 0, 40, 0, 60} // use 0 for NULL placeholders
	for _, v := range vals {
		isNull := v == 0
		b.AppendRow(0, LX.T_INT_KW, v, isNull)
		b.AdvanceSize()
	}
	b.Cols[0].Name = "c0"

	inExpr := &PS.InExpr{
		Expr: &PS.Ident{Name: "c0"},
		List: []PS.Expr{
			&PS.NumberLiteral{Val: 10},
			&PS.NumberLiteral{Val: 40},
			&PS.NumberLiteral{Val: 60},
		},
	}

	got := EvalBatch(inExpr, b, nil)
	want := []uint16{0, 3, 5} // idx 2,4 are null → skipped
	if !equalSelection(got, want) {
		t.Errorf("IN(10,40,60) with nulls: got %v, want %v", got, want)
	}
}

// TestEvalBatch_InList_NonLiteralExpr falls back to row-at-a-time (REQ000991).
func TestEvalBatch_InList_NonLiteralExpr(t *testing.T) {
	b := makeIntBatch([]int64{10, 20, 30, 40})
	defer b.Put()
	b.Cols[0].Name = "c0"

	// List contains a non-literal expression (param) — forces fallback
	inExpr := &PS.InExpr{
		Expr: &PS.Ident{Name: "c0"},
		List: []PS.Expr{
			&PS.Param{Index: 0},
		},
	}
	params := []any{int64(10)}
	got := EvalBatch(inExpr, b, params)
	want := []uint16{0}
	if !equalSelection(got, want) {
		t.Errorf("IN(param): got %v, want %v", got, want)
	}
}

// BenchmarkEvalBatch_InList benchmarks vectorized IN-list (REQ000991).
func BenchmarkEvalBatch_InList(b *testing.B) {
	const n = 1024
	batch := UT.GetBatch(1)
	defer batch.Put()
	for i := 0; i < n; i++ {
		batch.AppendRow(0, LX.T_INT_KW, int64(i%100), false)
		batch.AdvanceSize()
	}
	batch.Cols[0].Name = "c0"

	expr := &PS.InExpr{
		Expr: &PS.Ident{Name: "c0"},
		List: []PS.Expr{
			&PS.NumberLiteral{Val: 10},
			&PS.NumberLiteral{Val: 30},
			&PS.NumberLiteral{Val: 50},
			&PS.NumberLiteral{Val: 70},
			&PS.NumberLiteral{Val: 90},
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = EvalBatch(expr, batch, nil)
	}
}

func TestEqualSelection(t *testing.T) {
	if !equalSelection([]uint16{1, 2, 3}, []uint16{1, 2, 3}) {
		t.Error("equal")
	}
	if equalSelection([]uint16{1, 2}, []uint16{1, 2, 3}) {
		t.Error("different lengths")
	}
	if equalSelection([]uint16{1, 2, 3}, []uint16{1, 2, 4}) {
		t.Error("different values")
	}
}

// BenchmarkEvalBatch_Int64EQ benchmarks int64 column-literal EQ.
func BenchmarkEvalBatch_Int64EQ(b *testing.B) {
	const n = 1024
	batch := UT.GetBatch(1)
	defer batch.Put()
	for i := 0; i < n; i++ {
		batch.AppendRow(0, LX.T_INT_KW, int64(i), false)
		batch.AdvanceSize()
	}
	col := batch.Cols[0]

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = CompareInt64ColLit(col, 512, LX.T_EQ, n)
	}
}

// BenchmarkEvalBatch_Int64GT benchmarks int64 column-literal GT.
func BenchmarkEvalBatch_Int64GT(b *testing.B) {
	const n = 1024
	batch := UT.GetBatch(1)
	defer batch.Put()
	for i := 0; i < n; i++ {
		batch.AppendRow(0, LX.T_INT_KW, int64(i), false)
		batch.AdvanceSize()
	}
	col := batch.Cols[0]

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = CompareInt64ColLit(col, 512, LX.T_GT, n)
	}
}

// BenchmarkEvalBatch_StringEQ benchmarks string column-literal EQ.
func BenchmarkEvalBatch_StringEQ(b *testing.B) {
	const n = 1024
	batch := UT.GetBatch(1)
	defer batch.Put()
	for i := 0; i < n; i++ {
		batch.AppendRow(0, LX.T_TEXT, "value", false)
		batch.AdvanceSize()
	}
	col := batch.Cols[0]

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = CompareStringColLit(col, "value", LX.T_EQ, n)
	}
}

// TestEvalBatch_AndOr verifies REQ001010: AND/OR batch evaluation.
func TestEvalBatch_AndOr(t *testing.T) {
	const n = 10
	batch := UT.GetBatch(1)
	defer batch.Put()
	for i := 0; i < n; i++ {
		batch.AppendRow(0, LX.T_INT_KW, int64(i), false)
		batch.AdvanceSize()
	}
	batch.Cols[0].Name = "x"
	batch.SetColMap(map[string]int{"x": 0})

	// a > 3 AND a < 8 => rows 4,5,6,7
	andExpr := &PS.BinaryExpr{
		Op: LX.T_AND,
		Left: &PS.BinaryExpr{
			Op: LX.T_GT,
			Left: &PS.Ident{Name: "x"},
			Right: &PS.NumberLiteral{Val: 3},
		},
		Right: &PS.BinaryExpr{
			Op: LX.T_LT,
			Left: &PS.Ident{Name: "x"},
			Right: &PS.NumberLiteral{Val: 8},
		},
	}
	andSel := EvalBatch(andExpr, batch, nil)
	sort.Slice(andSel, func(i, j int) bool { return andSel[i] < andSel[j] })
	assertUint16Slice(t, andSel, []uint16{4, 5, 6, 7})

	// a < 2 OR a > 8 => rows 0,1,9
	orExpr := &PS.BinaryExpr{
		Op: LX.T_OR,
		Left: &PS.BinaryExpr{
			Op: LX.T_LT,
			Left: &PS.Ident{Name: "x"},
			Right: &PS.NumberLiteral{Val: 2},
		},
		Right: &PS.BinaryExpr{
			Op: LX.T_GT,
			Left: &PS.Ident{Name: "x"},
			Right: &PS.NumberLiteral{Val: 8},
		},
	}
	orSel := EvalBatch(orExpr, batch, nil)
	sort.Slice(orSel, func(i, j int) bool { return orSel[i] < orSel[j] })
	assertUint16Slice(t, orSel, []uint16{0, 1, 9})
}

func assertUint16Slice(t *testing.T, got, want []uint16) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("got %v, want %v", got, want)
		return
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("got %v, want %v", got, want)
			return
		}
	}
}

// TestEvalBatchExpr_ColumnRef verifies that a column reference returns
// a shallow copy of the source column data.
func TestEvalBatchExpr_ColumnRef(t *testing.T) {
	b := makeTestBatch(5)
	expr := &PS.Ident{Name: "x"}
	col := EvalBatchExpr(expr, b, nil)
	if col.Type != LX.T_INT_KW {
		t.Errorf("expected INT_KW type, got %v", col.Type)
	}
	if len(col.Data.Ints) < 5 {
		t.Fatalf("expected at least 5 ints, got %d", len(col.Data.Ints))
	}
	for i := 0; i < 5; i++ {
		if col.Data.Ints[i] != int64(i) {
			t.Errorf("col.Data.Ints[%d] = %d, want %d", i, col.Data.Ints[i], i)
		}
	}
}

// TestEvalBatchExpr_IntLiteral verifies that a number literal fills the column.
func TestEvalBatchExpr_IntLiteral(t *testing.T) {
	b := makeTestBatch(4)
	expr := &PS.NumberLiteral{Val: 42}
	col := EvalBatchExpr(expr, b, nil)
	if col.Type != LX.T_INT_KW {
		t.Errorf("expected INT_KW type, got %v", col.Type)
	}
	if len(col.Data.Ints) < 4 {
		t.Fatalf("expected at least 4 ints, got %d", len(col.Data.Ints))
	}
	for i := 0; i < 4; i++ {
		if col.Data.Ints[i] != 42 {
			t.Errorf("col.Data.Ints[%d] = %d, want 42", i, col.Data.Ints[i])
		}
	}
}

// TestEvalBatchExpr_AddInt64 verifies int64 addition over a batch.
func TestEvalBatchExpr_AddInt64(t *testing.T) {
	b := makeTestBatch(5)
	// x + 10
	expr := &PS.BinaryExpr{
		Op:    LX.T_PLUS,
		Left:  &PS.Ident{Name: "x"},
		Right: &PS.NumberLiteral{Val: 10},
	}
	col := EvalBatchExpr(expr, b, nil)
	if col.Type != LX.T_INT_KW {
		t.Errorf("expected INT_KW type, got %v", col.Type)
	}
	for i := 0; i < 5; i++ {
		want := int64(i) + 10
		if col.Data.Ints[i] != want {
			t.Errorf("[%d]: got %d, want %d", i, col.Data.Ints[i], want)
		}
	}
}

// TestEvalBatchExpr_MulInt64WithSel verifies int64 multiplication
// with a selection vector applied to the batch.
func TestEvalBatchExpr_MulInt64WithSel(t *testing.T) {
	b := makeTestBatch(5)
	b.Sel = []uint16{0, 2, 4} // only rows 0, 2, 4 are valid

	// x * 3
	expr := &PS.BinaryExpr{
		Op:    LX.T_STAR,
		Left:  &PS.Ident{Name: "x"},
		Right: &PS.NumberLiteral{Val: 3},
	}
	col := EvalBatchExpr(expr, b, nil)
	if col.Type != LX.T_INT_KW {
		t.Errorf("expected INT_KW type, got %v", col.Type)
	}
	// Selected rows: x[0]=0*3=0, x[2]=2*3=6, x[4]=4*3=12
	expected := []int64{0, 0, 6, 0, 12}
	for i := 0; i < 5; i++ {
		if col.Data.Ints[i] != expected[i] {
			t.Errorf("[%d]: got %d, want %d", i, col.Data.Ints[i], expected[i])
		}
	}
}

// TestEvalBatchExpr_FloatMul verifies float64 multiplication over a batch.
func TestEvalBatchExpr_FloatMul(t *testing.T) {
	b := makeTestBatch(4)
	// y * 2.0
	expr := &PS.BinaryExpr{
		Op:    LX.T_STAR,
		Left:  &PS.Ident{Name: "y"},
		Right: &PS.FloatLiteral{Val: 2.0},
	}
	col := EvalBatchExpr(expr, b, nil)
	if col.Type != LX.T_FLOAT_KW {
		t.Errorf("expected FLOAT_KW type, got %v", col.Type)
	}
	for i := 0; i < 4; i++ {
		want := float64(i) * 1.5 * 2.0
		if col.Data.Floats[i] != want {
			t.Errorf("[%d]: got %f, want %f", i, col.Data.Floats[i], want)
		}
	}
}

// TestEvalBatchExpr_ConcatString verifies string concatenation over a batch.
func TestEvalBatchExpr_ConcatString(t *testing.T) {
	b := makeTestBatch(4)
	// z || '_suffix'
	expr := &PS.BinaryExpr{
		Op:    LX.T_CONCAT,
		Left:  &PS.Ident{Name: "z"},
		Right: &PS.StringLiteral{Val: "_suffix"},
	}
	col := EvalBatchExpr(expr, b, nil)
	if col.Type != LX.T_TEXT {
		t.Errorf("expected TEXT type, got %v", col.Type)
	}
	for i := 0; i < 4; i++ {
		want := fmt.Sprintf("s%d_suffix", i)
		if col.Data.Strs[i] != want {
			t.Errorf("[%d]: got %q, want %q", i, col.Data.Strs[i], want)
		}
	}
}

// TestEvalBatchExpr_NullPropagation verifies that NULL on either side
// of an arithmetic expression produces NULL in the output.
func TestEvalBatchExpr_NullPropagation(t *testing.T) {
	b := makeTestBatch(4)
	// Mark row 1 of x as NULL, row 2 of y as NULL
	b.Cols[0].Nulls = make([]bool, 4)
	b.Cols[0].Nulls[1] = true
	b.Cols[1].Nulls = make([]bool, 4)
	b.Cols[1].Nulls[2] = true

	// x + y
	expr := &PS.BinaryExpr{
		Op:    LX.T_PLUS,
		Left:  &PS.Ident{Name: "x"},
		Right: &PS.Ident{Name: "y"},
	}
	col := EvalBatchExpr(expr, b, nil)
	// Row 0: 0 + 0.0 = 0.0 (float)
	// Row 1: NULL x → NULL
	// Row 2: x[2]=2 + NULL y → NULL
	// Row 3: 3 + 4.5 = 7.5 (float)

	if col.Nulls == nil {
		t.Fatal("expected non-nil Nulls slice")
	}
	if !col.Nulls[1] {
		t.Errorf("expected null at index 1 (left is null), but it's not null")
	}
	if !col.Nulls[2] {
		t.Errorf("expected null at index 2 (right is null), but it's not null")
	}
	if col.Nulls[0] {
		t.Errorf("index 0 should not be null")
	}
	if col.Nulls[3] {
		t.Errorf("index 3 should not be null")
	}
	// Row 0: 0 + 0.0 = 0.0
	if col.Data.Floats[0] != 0.0 {
		t.Errorf("[0]: got %f, want 0.0", col.Data.Floats[0])
	}
}

// TestEvalValueViaEvalBatchExpr verifies that the new EvalValue (which
// wraps EvalBatchExpr) produces identical results to the original
// evalFallbackEvalValue for a variety of expression types.
func TestEvalValueViaEvalBatchExpr(t *testing.T) {
	tests := []struct {
		name   string
		expr   PS.Expr
		row    *Row
		params []any
	}{
		{
			name: "int literal",
			expr: &PS.NumberLiteral{Val: 42},
			row:  &Row{Cols: []string{"a"}, Data: []Value{DT.NewIntValue(10)}},
		},
		{
			name: "float literal",
			expr: &PS.FloatLiteral{Val: 3.14},
			row:  &Row{Cols: []string{"a"}, Data: []Value{DT.NewIntValue(10)}},
		},
		{
			name: "string literal",
			expr: &PS.StringLiteral{Val: "hello"},
			row:  &Row{Cols: []string{"a"}, Data: []Value{DT.NewIntValue(10)}},
		},
		{
			name: "bool literal",
			expr: &PS.BoolLiteral{Val: true},
			row:  &Row{Cols: []string{"a"}, Data: []Value{DT.NewIntValue(10)}},
		},
		{
			name: "null literal",
			expr: &PS.NullLiteral{},
			row:  &Row{Cols: []string{"a"}, Data: []Value{DT.NewIntValue(10)}},
		},
		{
			name: "column ref",
			expr: &PS.Ident{Name: "a"},
			row:  &Row{Cols: []string{"a"}, Data: []Value{DT.NewIntValue(99)}},
		},
		{
			name: "add",
			expr: &PS.BinaryExpr{
				Op:    LX.T_PLUS,
				Left:  &PS.NumberLiteral{Val: 5},
				Right: &PS.NumberLiteral{Val: 3},
			},
			row: &Row{Cols: []string{"a"}, Data: []Value{DT.NewIntValue(10)}},
		},
		{
			name: "concat",
			expr: &PS.BinaryExpr{
				Op:    LX.T_CONCAT,
				Left:  &PS.StringLiteral{Val: "hello "},
				Right: &PS.StringLiteral{Val: "world"},
			},
			row: &Row{Cols: []string{"a"}, Data: []Value{DT.NewIntValue(10)}},
		},
		{
			name: "equals comparison",
			expr: &PS.BinaryExpr{
				Op:    LX.T_EQ,
				Left:  &PS.NumberLiteral{Val: 5},
				Right: &PS.NumberLiteral{Val: 5},
			},
			row: &Row{Cols: []string{"a"}, Data: []Value{DT.NewIntValue(10)}},
		},
		{
			name: "param",
			expr: &PS.Param{Index: 0},
			row:  &Row{Cols: []string{"a"}, Data: []Value{DT.NewIntValue(10)}},
			params: []any{int64(100)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotErr := EvalValue(tt.expr, tt.row, tt.params)
			want, wantErr := evalFallbackEvalValue(tt.expr, tt.row, tt.params)

			if gotErr != nil && wantErr == nil {
				t.Errorf("EvalValue returned error %v, want nil", gotErr)
			}
			if gotErr == nil && wantErr != nil {
				t.Errorf("EvalValue returned nil, want error %v", wantErr)
			}
			if got.Kind != want.Kind {
				t.Errorf("kind mismatch: got %v, want %v", got.Kind, want.Kind)
			}
			if got.Kind == KindInt && got.I64 != want.I64 {
				t.Errorf("int64 mismatch: got %d, want %d", got.I64, want.I64)
			}
			if got.Kind == KindFloat && got.F64 != want.F64 {
				t.Errorf("float mismatch: got %f, want %f", got.F64, want.F64)
			}
			if got.Kind == KindText && got.S != want.S {
				t.Errorf("text mismatch: got %q, want %q", got.S, want.S)
			}
			if got.Kind == KindBool && got.Bo != want.Bo {
				t.Errorf("bool mismatch: got %v, want %v", got.Bo, want.Bo)
			}
		})
	}
}

// TestEvalBatchExpr_Param verifies param resolution.
func TestEvalBatchExpr_Param(t *testing.T) {
	b := makeTestBatch(3)
	expr := &PS.Param{Index: 0}
	params := []any{int64(77)}
	col := EvalBatchExpr(expr, b, params)
	if col.Type != LX.T_INT_KW {
		t.Errorf("expected INT_KW, got %v", col.Type)
	}
	for i := 0; i < 3; i++ {
		if col.Data.Ints[i] != 77 {
			t.Errorf("[%d]: got %d, want 77", i, col.Data.Ints[i])
		}
	}
}

// TestEvalBatchExpr_Comparison verifies that comparison operators produce
// boolean columns with correct results.
func TestEvalBatchExpr_Comparison(t *testing.T) {
	b := makeTestBatch(5)
	// x > 2
	expr := &PS.BinaryExpr{
		Op:    LX.T_GT,
		Left:  &PS.Ident{Name: "x"},
		Right: &PS.NumberLiteral{Val: 2},
	}
	col := EvalBatchExpr(expr, b, nil)
	if col.Type != LX.T_BOOL {
		t.Errorf("expected BOOL type, got %v", col.Type)
	}
	expected := []bool{false, false, false, true, true}
	for i := 0; i < 5; i++ {
		if col.Data.Bools[i] != expected[i] {
			t.Errorf("[%d]: got %v, want %v", i, col.Data.Bools[i], expected[i])
		}
	}
}
