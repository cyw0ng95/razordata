package EX

import (
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// makeIntBatch creates a batch with a single int64 column.
func makeIntBatch(values []int64) *Batch {
	b := GetBatch(1)
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
	sel := compareInt64ColLit(col, 5, int(LX.T_EQ), b.Size)
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
	sel := compareInt64ColLit(col, 5, int(LX.T_LT), b.Size)
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
	sel := compareInt64ColLit(col, 5, int(LX.T_GT), b.Size)
	expected := []uint16{4, 6}
	if !equalSelection(sel, expected) {
		t.Errorf("col>5: got %v, want %v", sel, expected)
	}
}

// TestEvalBatch_Int64ColCol tests col1 = col2.
func TestEvalBatch_Int64ColCol(t *testing.T) {
	b := GetBatch(2)
	defer b.Put()
	leftVals := []int64{1, 2, 3, 4, 5}
	rightVals := []int64{1, 0, 3, 0, 5}
	for i := range leftVals {
		b.AppendRow(0, LX.T_INT_KW, leftVals[i], false)
		b.AppendRow(1, LX.T_INT_KW, rightVals[i], false)
		b.AdvanceSize()
	}

	sel := compareInt64Cols(b.Cols[0], b.Cols[1], int(LX.T_EQ), b.Size)
	expected := []uint16{0, 2, 4}
	if !equalSelection(sel, expected) {
		t.Errorf("col0=col1: got %v, want %v", sel, expected)
	}
}

// TestEvalBatch_Float64ColLit tests float comparisons.
func TestEvalBatch_Float64ColLit(t *testing.T) {
	b := GetBatch(1)
	defer b.Put()
	vals := []float64{1.0, 2.5, 3.7, 4.0, 5.5}
	for _, v := range vals {
		b.AppendRow(0, LX.T_FLOAT_KW, v, false)
		b.AdvanceSize()
	}

	col := b.Cols[0]
	sel := compareFloat64ColLit(col, 3.0, int(LX.T_GT), b.Size)
	expected := []uint16{2, 3, 4}
	if !equalSelection(sel, expected) {
		t.Errorf("col>3.0: got %v, want %v", sel, expected)
	}
}

// TestEvalBatch_StringColLit tests string equality.
func TestEvalBatch_StringColLit(t *testing.T) {
	b := GetBatch(1)
	defer b.Put()
	vals := []string{"apple", "banana", "cherry", "banana"}
	for _, v := range vals {
		b.AppendRow(0, LX.T_TEXT, v, false)
		b.AdvanceSize()
	}

	col := b.Cols[0]
	sel := compareStringColLit(col, "banana", int(LX.T_EQ), b.Size)
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
	sel := compareInt64ColLit(col, 0, int(LX.T_GT), b.Size)
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
	sel := compareInt64ColLit(col, 100, int(LX.T_LT), b.Size)
	if len(sel) != 0 {
		t.Errorf("expected empty selection, got %v", sel)
	}
}

// TestEvalBatch_EmptyBatch returns nil for empty batch.
func TestEvalBatch_EmptyBatch(t *testing.T) {
	b := GetBatch(1)
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
	b := GetBatch(1)
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
	actual := compareInt64ColLit(col, 0, int(LX.T_GE), n) // all match
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
	actual5 := compareInt64ColLit(col, 5, int(LX.T_EQ), n)
	if !equalSelection(actual5, sel5) {
		t.Errorf("col==5: got %v, want %v", actual5, sel5)
	}
}

// TestSwapOp verifies operator swapping for reversed column-literal.
func TestSwapOp(t *testing.T) {
	cases := []struct {
		in, want int
	}{
		{int(LX.T_LT), int(LX.T_GT)},
		{int(LX.T_LE), int(LX.T_GE)},
		{int(LX.T_GT), int(LX.T_LT)},
		{int(LX.T_GE), int(LX.T_LE)},
		{int(LX.T_EQ), int(LX.T_EQ)},
	}
	for _, c := range cases {
		if got := swapOp(c.in); got != c.want {
			t.Errorf("swapOp(%d) = %d; want %d", c.in, got, c.want)
		}
	}
}

// TestInvertSelection verifies complement of selection.
func TestInvertSelection(t *testing.T) {
	sel := []uint16{0, 2, 4}
	inv := invertSelection(sel, 6)
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
		Op: int(LX.T_NOT),
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
	b := GetBatch(1)
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
	b := GetBatch(1)
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
	batch := GetBatch(1)
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
	batch := GetBatch(1)
	defer batch.Put()
	for i := 0; i < n; i++ {
		batch.AppendRow(0, LX.T_INT_KW, int64(i), false)
		batch.AdvanceSize()
	}
	col := batch.Cols[0]

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = compareInt64ColLit(col, 512, int(LX.T_EQ), n)
	}
}

// BenchmarkEvalBatch_Int64GT benchmarks int64 column-literal GT.
func BenchmarkEvalBatch_Int64GT(b *testing.B) {
	const n = 1024
	batch := GetBatch(1)
	defer batch.Put()
	for i := 0; i < n; i++ {
		batch.AppendRow(0, LX.T_INT_KW, int64(i), false)
		batch.AdvanceSize()
	}
	col := batch.Cols[0]

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = compareInt64ColLit(col, 512, int(LX.T_GT), n)
	}
}

// BenchmarkEvalBatch_StringEQ benchmarks string column-literal EQ.
func BenchmarkEvalBatch_StringEQ(b *testing.B) {
	const n = 1024
	batch := GetBatch(1)
	defer batch.Put()
	for i := 0; i < n; i++ {
		batch.AppendRow(0, LX.T_TEXT, "value", false)
		batch.AdvanceSize()
	}
	col := batch.Cols[0]

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = compareStringColLit(col, "value", int(LX.T_EQ), n)
	}
}
