package EV

import (
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// ── IS NULL batch tests ──

func TestIsNullBatch_Basic(t *testing.T) {
	// Batch: column "x" with values [1, NULL, 3, NULL, 5]
	n := 5
	b := UT.GetBatch(1)
	vals := []int64{1, 0, 3, 0, 5}
	nulls := []bool{false, true, false, true, false}
	for i := 0; i < n; i++ {
		b.AppendRow(0, LX.T_INT_KW, vals[i], nulls[i])
		b.AdvanceSize()
	}
	b.Cols[0].Name = "x"
	b.SetColMap(map[string]int{"x": 0})

	// x IS NULL
	expr := &PS.BinaryExpr{
		Op:    LX.T_IS,
		Left:  &PS.Ident{Name: "x"},
		Right: &PS.NullLiteral{},
	}
	sel := EvalBatch(expr, b, nil)
	want := []uint16{1, 3}
	if !slicesEqual(sel, want) {
		t.Fatalf("IS NULL: got %v, want %v", sel, want)
	}
}

func TestIsNullBatch_AllNull(t *testing.T) {
	n := 3
	b := UT.GetBatch(1)
	for i := 0; i < n; i++ {
		b.AppendRow(0, LX.T_INT_KW, int64(0), true)
		b.AdvanceSize()
	}
	b.Cols[0].Name = "x"
	b.SetColMap(map[string]int{"x": 0})

	expr := &PS.BinaryExpr{
		Op:    LX.T_IS,
		Left:  &PS.Ident{Name: "x"},
		Right: &PS.NullLiteral{},
	}
	sel := EvalBatch(expr, b, nil)
	// All rows are NULL → all match
	if sel == nil {
		t.Fatalf("IS NULL all-null: expected non-nil (all rows match), got nil")
	}
	if len(sel) != n {
		t.Fatalf("IS NULL all-null: expected %d rows, got %d", n, len(sel))
	}
}

func TestIsNullBatch_AllNonNull(t *testing.T) {
	n := 4
	b := UT.GetBatch(1)
	for i := 0; i < n; i++ {
		b.AppendRow(0, LX.T_INT_KW, int64(i), false)
		b.AdvanceSize()
	}
	b.Cols[0].Name = "x"
	b.SetColMap(map[string]int{"x": 0})

	expr := &PS.BinaryExpr{
		Op:    LX.T_IS,
		Left:  &PS.Ident{Name: "x"},
		Right: &PS.NullLiteral{},
	}
	sel := EvalBatch(expr, b, nil)
	// No rows are NULL → empty selection
	if sel == nil {
		t.Fatalf("IS NULL all-nonnull: expected empty slice, got nil")
	}
	if len(sel) != 0 {
		t.Fatalf("IS NULL all-nonnull: expected 0 rows, got %d", len(sel))
	}
}

// ── IS NOT NULL batch tests ──

func TestIsNotNullBatch_Basic(t *testing.T) {
	n := 5
	b := UT.GetBatch(1)
	vals := []int64{1, 0, 3, 0, 5}
	nulls := []bool{false, true, false, true, false}
	for i := 0; i < n; i++ {
		b.AppendRow(0, LX.T_INT_KW, vals[i], nulls[i])
		b.AdvanceSize()
	}
	b.Cols[0].Name = "x"
	b.SetColMap(map[string]int{"x": 0})

	// x IS NOT NULL
	notNull := &PS.UnaryExpr{Op: LX.T_NOT, Operand: &PS.NullLiteral{}}
	expr := &PS.BinaryExpr{
		Op:    LX.T_IS,
		Left:  &PS.Ident{Name: "x"},
		Right: notNull,
	}
	sel := EvalBatch(expr, b, nil)
	want := []uint16{0, 2, 4}
	if !slicesEqual(sel, want) {
		t.Fatalf("IS NOT NULL: got %v, want %v", sel, want)
	}
}

func TestIsNotNullBatch_AllNonNull(t *testing.T) {
	n := 3
	b := UT.GetBatch(1)
	for i := 0; i < n; i++ {
		b.AppendRow(0, LX.T_INT_KW, int64(i), false)
		b.AdvanceSize()
	}
	b.Cols[0].Name = "x"
	b.SetColMap(map[string]int{"x": 0})

	notNull := &PS.UnaryExpr{Op: LX.T_NOT, Operand: &PS.NullLiteral{}}
	expr := &PS.BinaryExpr{
		Op:    LX.T_IS,
		Left:  &PS.Ident{Name: "x"},
		Right: notNull,
	}
	sel := EvalBatch(expr, b, nil)
	// All rows are non-null → nil (all match)
	if sel != nil {
		t.Fatalf("IS NOT NULL all-nonnull: expected nil (all match), got %v", sel)
	}
}

func TestIsNotNullBatch_AllNull(t *testing.T) {
	n := 3
	b := UT.GetBatch(1)
	for i := 0; i < n; i++ {
		b.AppendRow(0, LX.T_INT_KW, int64(0), true)
		b.AdvanceSize()
	}
	b.Cols[0].Name = "x"
	b.SetColMap(map[string]int{"x": 0})

	notNull := &PS.UnaryExpr{Op: LX.T_NOT, Operand: &PS.NullLiteral{}}
	expr := &PS.BinaryExpr{
		Op:    LX.T_IS,
		Left:  &PS.Ident{Name: "x"},
		Right: notNull,
	}
	sel := EvalBatch(expr, b, nil)
	// All rows are NULL → empty selection
	if sel == nil {
		t.Fatalf("IS NOT NULL all-null: expected empty slice, got nil")
	}
	if len(sel) != 0 {
		t.Fatalf("IS NOT NULL all-null: expected 0 rows, got %d", len(sel))
	}
}

// ── LIKE batch tests ──

func TestLikeBatch_Exact(t *testing.T) {
	// Batch: text column "z" with ["hello", "world", "Hello", "HELLO", NULL]
	n := 5
	b := UT.GetBatch(1)
	strs := []string{"hello", "world", "Hello", "HELLO", ""}
	nulls := []bool{false, false, false, false, true}
	for i := 0; i < n; i++ {
		b.AppendRow(0, LX.T_TEXT, strs[i], nulls[i])
		b.AdvanceSize()
	}
	b.Cols[0].Name = "z"
	b.SetColMap(map[string]int{"z": 0})

	// z LIKE 'hello' (case-insensitive)
	expr := &PS.BinaryExpr{
		Op:    LX.T_LIKE,
		Left:  &PS.Ident{Name: "z"},
		Right: &PS.StringLiteral{Val: "hello"},
	}
	sel := EvalBatch(expr, b, nil)
	// All case variants of "hello" should match (case-insensitive)
	want := []uint16{0, 2, 3}
	if !slicesEqual(sel, want) {
		t.Fatalf("LIKE exact: got %v, want %v", sel, want)
	}
}

func TestLikeBatch_Prefix(t *testing.T) {
	// Batch: text column "z" with ["apple", "apricot", "banana", "APP", NULL]
	n := 5
	b := UT.GetBatch(1)
	strs := []string{"apple", "apricot", "banana", "APP", ""}
	nulls := []bool{false, false, false, false, true}
	for i := 0; i < n; i++ {
		b.AppendRow(0, LX.T_TEXT, strs[i], nulls[i])
		b.AdvanceSize()
	}
	b.Cols[0].Name = "z"
	b.SetColMap(map[string]int{"z": 0})

	// z LIKE 'ap%' (case-insensitive prefix)
	expr := &PS.BinaryExpr{
		Op:    LX.T_LIKE,
		Left:  &PS.Ident{Name: "z"},
		Right: &PS.StringLiteral{Val: "ap%"},
	}
	sel := EvalBatch(expr, b, nil)
	want := []uint16{0, 1, 3} // "apple", "apricot", "APP" all start with "ap" case-insensitively
	if !slicesEqual(sel, want) {
		t.Fatalf("LIKE prefix: got %v, want %v", sel, want)
	}
}

func TestLikeBatch_Complex(t *testing.T) {
	// Batch: text column "z" with ["abc", "aXc", "ac", "abbc", NULL]
	n := 5
	b := UT.GetBatch(1)
	strs := []string{"abc", "aXc", "ac", "abbc", ""}
	nulls := []bool{false, false, false, false, true}
	for i := 0; i < n; i++ {
		b.AppendRow(0, LX.T_TEXT, strs[i], nulls[i])
		b.AdvanceSize()
	}
	b.Cols[0].Name = "z"
	b.SetColMap(map[string]int{"z": 0})

	// z LIKE 'a_c' (underscore = single char)
	expr := &PS.BinaryExpr{
		Op:    LX.T_LIKE,
		Left:  &PS.Ident{Name: "z"},
		Right: &PS.StringLiteral{Val: "a_c"},
	}
	sel := EvalBatch(expr, b, nil)
	// "abc" and "aXc" match; "ac" does not (need exactly one char between a and c)
	want := []uint16{0, 1}
	if !slicesEqual(sel, want) {
		t.Fatalf("LIKE underscore: got %v, want %v", sel, want)
	}
}

func TestLikeBatch_WildcardBoth(t *testing.T) {
	// Batch: text column "z" with ["xby", "abyz", "ab", NULL]
	n := 4
	b := UT.GetBatch(1)
	strs := []string{"xby", "abyz", "ab", ""}
	nulls := []bool{false, false, false, true}
	for i := 0; i < n; i++ {
		b.AppendRow(0, LX.T_TEXT, strs[i], nulls[i])
		b.AdvanceSize()
	}
	b.Cols[0].Name = "z"
	b.SetColMap(map[string]int{"z": 0})

	// z LIKE '%b%' (contains 'b')
	expr := &PS.BinaryExpr{
		Op:    LX.T_LIKE,
		Left:  &PS.Ident{Name: "z"},
		Right: &PS.StringLiteral{Val: "%b%"},
	}
	sel := EvalBatch(expr, b, nil)
	// "xby", "abyz", "ab" all contain 'b'
	want := []uint16{0, 1, 2}
	if !slicesEqual(sel, want) {
		t.Fatalf("LIKE contains: got %v, want %v", sel, want)
	}
}

// ── GLOB batch tests ──

func TestGlobBatch_Basic(t *testing.T) {
	// GLOB is case-sensitive, * and ? wildcards
	n := 5
	b := UT.GetBatch(1)
	strs := []string{"hello.txt", "world.txt", "hello.csv", "HELLO.TXT", ""}
	nulls := []bool{false, false, false, false, true}
	for i := 0; i < n; i++ {
		b.AppendRow(0, LX.T_TEXT, strs[i], nulls[i])
		b.AdvanceSize()
	}
	b.Cols[0].Name = "z"
	b.SetColMap(map[string]int{"z": 0})

	// z GLOB '*.txt' (case-sensitive)
	expr := &PS.BinaryExpr{
		Op:    LX.T_GLOB,
		Left:  &PS.Ident{Name: "z"},
		Right: &PS.StringLiteral{Val: "*.txt"},
	}
	sel := EvalBatch(expr, b, nil)
	// Only "hello.txt" and "world.txt" match (case-sensitive, "HELLO.TXT" does not)
	want := []uint16{0, 1}
	if !slicesEqual(sel, want) {
		t.Fatalf("GLOB *.txt: got %v, want %v", sel, want)
	}
}

func TestGlobBatch_QuestionMark(t *testing.T) {
	n := 4
	b := UT.GetBatch(1)
	strs := []string{"abc", "aXc", "ac", "abbc"}
	nulls := []bool{false, false, false, false}
	for i := 0; i < n; i++ {
		b.AppendRow(0, LX.T_TEXT, strs[i], nulls[i])
		b.AdvanceSize()
	}
	b.Cols[0].Name = "z"
	b.SetColMap(map[string]int{"z": 0})

	// z GLOB 'a?c' (case-sensitive single char)
	expr := &PS.BinaryExpr{
		Op:    LX.T_GLOB,
		Left:  &PS.Ident{Name: "z"},
		Right: &PS.StringLiteral{Val: "a?c"},
	}
	sel := EvalBatch(expr, b, nil)
	// "abc" and "aXc" match; "ac" and "abbc" do not
	want := []uint16{0, 1}
	if !slicesEqual(sel, want) {
		t.Fatalf("GLOB a?c: got %v, want %v", sel, want)
	}
}

func TestGlobBatch_Exact(t *testing.T) {
	n := 3
	b := UT.GetBatch(1)
	strs := []string{"hello", "Hello", "HELLO"}
	for i := 0; i < n; i++ {
		b.AppendRow(0, LX.T_TEXT, strs[i], false)
		b.AdvanceSize()
	}
	b.Cols[0].Name = "z"
	b.SetColMap(map[string]int{"z": 0})

	// z GLOB 'hello' (exact match, case-sensitive)
	expr := &PS.BinaryExpr{
		Op:    LX.T_GLOB,
		Left:  &PS.Ident{Name: "z"},
		Right: &PS.StringLiteral{Val: "hello"},
	}
	sel := EvalBatch(expr, b, nil)
	// Only "hello" matches (case-sensitive)
	want := []uint16{0}
	if !slicesEqual(sel, want) {
		t.Fatalf("GLOB exact: got %v, want %v", sel, want)
	}
}

// ── BETWEEN batch tests ──

func TestBetweenBatch_Basic(t *testing.T) {
	n := 5
	b := UT.GetBatch(1)
	vals := []int64{1, 5, 10, 15, 20}
	for i := 0; i < n; i++ {
		b.AppendRow(0, LX.T_INT_KW, vals[i], false)
		b.AdvanceSize()
	}
	b.Cols[0].Name = "x"
	b.SetColMap(map[string]int{"x": 0})

	// x BETWEEN 5 AND 15
	expr := &PS.BetweenExpr{
		Expr: &PS.Ident{Name: "x"},
		Low:  &PS.NumberLiteral{Val: 5},
		High: &PS.NumberLiteral{Val: 15},
	}
	sel := EvalBatch(expr, b, nil)
	// Rows 1,2,3 have values 5,10,15 which are in [5,15]
	want := []uint16{1, 2, 3}
	if !slicesEqual(sel, want) {
		t.Fatalf("BETWEEN basic: got %v, want %v", sel, want)
	}
}

func TestBetweenBatch_NullValues(t *testing.T) {
	n := 5
	b := UT.GetBatch(1)
	vals := []int64{1, 0, 10, 0, 20}
	nulls := []bool{false, true, false, true, false}
	for i := 0; i < n; i++ {
		b.AppendRow(0, LX.T_INT_KW, vals[i], nulls[i])
		b.AdvanceSize()
	}
	b.Cols[0].Name = "x"
	b.SetColMap(map[string]int{"x": 0})

	// x BETWEEN 1 AND 15 — NULL rows should be excluded
	expr := &PS.BetweenExpr{
		Expr: &PS.Ident{Name: "x"},
		Low:  &PS.NumberLiteral{Val: 1},
		High: &PS.NumberLiteral{Val: 15},
	}
	sel := EvalBatch(expr, b, nil)
	// Row 0 (val=1) and Row 2 (val=10) match; rows 1,3 are NULL, row 4 (val=20) out of range
	want := []uint16{0, 2}
	if !slicesEqual(sel, want) {
		t.Fatalf("BETWEEN null: got %v, want %v", sel, want)
	}
}

func TestBetweenBatch_AllOutOfRange(t *testing.T) {
	n := 3
	b := UT.GetBatch(1)
	vals := []int64{1, 2, 3}
	for i := 0; i < n; i++ {
		b.AppendRow(0, LX.T_INT_KW, vals[i], false)
		b.AdvanceSize()
	}
	b.Cols[0].Name = "x"
	b.SetColMap(map[string]int{"x": 0})

	// x BETWEEN 10 AND 20
	expr := &PS.BetweenExpr{
		Expr: &PS.Ident{Name: "x"},
		Low:  &PS.NumberLiteral{Val: 10},
		High: &PS.NumberLiteral{Val: 20},
	}
	sel := EvalBatch(expr, b, nil)
	if sel == nil {
		t.Fatalf("BETWEEN all-out-of-range: expected empty slice, got nil")
	}
	if len(sel) != 0 {
		t.Fatalf("BETWEEN all-out-of-range: expected 0 rows, got %d", len(sel))
	}
}

func TestBetweenBatch_AllInRange(t *testing.T) {
	n := 3
	b := UT.GetBatch(1)
	vals := []int64{5, 10, 15}
	for i := 0; i < n; i++ {
		b.AppendRow(0, LX.T_INT_KW, vals[i], false)
		b.AdvanceSize()
	}
	b.Cols[0].Name = "x"
	b.SetColMap(map[string]int{"x": 0})

	// x BETWEEN 1 AND 20
	expr := &PS.BetweenExpr{
		Expr: &PS.Ident{Name: "x"},
		Low:  &PS.NumberLiteral{Val: 1},
		High: &PS.NumberLiteral{Val: 20},
	}
	sel := EvalBatch(expr, b, nil)
	// All rows match
	if sel == nil {
		t.Fatalf("BETWEEN all-in-range: expected nil (all match), got non-nil")
	}
	if len(sel) != n {
		t.Fatalf("BETWEEN all-in-range: expected %d rows, got %d", n, len(sel))
	}
}

func TestBetweenBatch_String(t *testing.T) {
	n := 4
	b := UT.GetBatch(1)
	strs := []string{"apple", "banana", "cherry", "date"}
	for i := 0; i < n; i++ {
		b.AppendRow(0, LX.T_TEXT, strs[i], false)
		b.AdvanceSize()
	}
	b.Cols[0].Name = "z"
	b.SetColMap(map[string]int{"z": 0})

	// z BETWEEN 'banana' AND 'cherry'
	expr := &PS.BetweenExpr{
		Expr: &PS.Ident{Name: "z"},
		Low:  &PS.StringLiteral{Val: "banana"},
		High: &PS.StringLiteral{Val: "cherry"},
	}
	sel := EvalBatch(expr, b, nil)
	want := []uint16{1, 2} // "banana" and "cherry"
	if !slicesEqual(sel, want) {
		t.Fatalf("BETWEEN string: got %v, want %v", sel, want)
	}
}

// ── EvalBatchExpr tests for IS/LIKE/GLOB returning columns ──

func TestEvalBatchExpr_IsNull(t *testing.T) {
	n := 3
	b := UT.GetBatch(1)
	vals := []int64{1, 0, 3}
	nulls := []bool{false, true, false}
	for i := 0; i < n; i++ {
		b.AppendRow(0, LX.T_INT_KW, vals[i], nulls[i])
		b.AdvanceSize()
	}
	b.Cols[0].Name = "x"
	b.SetColMap(map[string]int{"x": 0})

	expr := &PS.BinaryExpr{
		Op:    LX.T_IS,
		Left:  &PS.Ident{Name: "x"},
		Right: &PS.NullLiteral{},
	}
	col := EvalBatchExpr(expr, b, nil)

	if col.Type != LX.T_BOOL {
		t.Fatalf("expected bool type, got %v", col.Type)
	}
	expectedBools := []bool{false, true, false}
	for i := 0; i < n; i++ {
		if col.Data.Bools[i] != expectedBools[i] {
			t.Errorf("row %d: expected %v, got %v", i, expectedBools[i], col.Data.Bools[i])
		}
	}
}

func TestEvalBatchExpr_IsNotNull(t *testing.T) {
	n := 3
	b := UT.GetBatch(1)
	vals := []int64{1, 0, 3}
	nulls := []bool{false, true, false}
	for i := 0; i < n; i++ {
		b.AppendRow(0, LX.T_INT_KW, vals[i], nulls[i])
		b.AdvanceSize()
	}
	b.Cols[0].Name = "x"
	b.SetColMap(map[string]int{"x": 0})

	notNull := &PS.UnaryExpr{Op: LX.T_NOT, Operand: &PS.NullLiteral{}}
	expr := &PS.BinaryExpr{
		Op:    LX.T_IS,
		Left:  &PS.Ident{Name: "x"},
		Right: notNull,
	}
	col := EvalBatchExpr(expr, b, nil)

	if col.Type != LX.T_BOOL {
		t.Fatalf("expected bool type, got %v", col.Type)
	}
	expectedBools := []bool{true, false, true}
	for i := 0; i < n; i++ {
		if col.Data.Bools[i] != expectedBools[i] {
			t.Errorf("row %d: expected %v, got %v", i, expectedBools[i], col.Data.Bools[i])
		}
	}
}

func TestEvalBatchExpr_Like(t *testing.T) {
	n := 3
	b := UT.GetBatch(1)
	strs := []string{"hello", "world", ""}
	nulls := []bool{false, false, true}
	for i := 0; i < n; i++ {
		b.AppendRow(0, LX.T_TEXT, strs[i], nulls[i])
		b.AdvanceSize()
	}
	b.Cols[0].Name = "z"
	b.SetColMap(map[string]int{"z": 0})

	expr := &PS.BinaryExpr{
		Op:    LX.T_LIKE,
		Left:  &PS.Ident{Name: "z"},
		Right: &PS.StringLiteral{Val: "h%"},
	}
	col := EvalBatchExpr(expr, b, nil)

	if col.Type != LX.T_BOOL {
		t.Fatalf("expected bool type, got %v", col.Type)
	}
	// Row 0 ("hello") matches h%, row 1 ("world") does not, row 2 (NULL) → NULL
	if !col.Data.Bools[0] {
		t.Errorf("row 0: expected true, got false")
	}
	if col.Data.Bools[1] {
		t.Errorf("row 1: expected false, got true")
	}
	if col.Nulls == nil || !col.Nulls[2] {
		t.Errorf("row 2: expected NULL, got non-null")
	}
}

func TestEvalBatchExpr_Glob(t *testing.T) {
	n := 3
	b := UT.GetBatch(1)
	strs := []string{"hello.txt", "world.csv", ""}
	nulls := []bool{false, false, true}
	for i := 0; i < n; i++ {
		b.AppendRow(0, LX.T_TEXT, strs[i], nulls[i])
		b.AdvanceSize()
	}
	b.Cols[0].Name = "z"
	b.SetColMap(map[string]int{"z": 0})

	expr := &PS.BinaryExpr{
		Op:    LX.T_GLOB,
		Left:  &PS.Ident{Name: "z"},
		Right: &PS.StringLiteral{Val: "*.txt"},
	}
	col := EvalBatchExpr(expr, b, nil)

	if col.Type != LX.T_BOOL {
		t.Fatalf("expected bool type, got %v", col.Type)
	}
	if !col.Data.Bools[0] {
		t.Errorf("row 0: expected true (hello.txt matches *.txt)")
	}
	if col.Data.Bools[1] {
		t.Errorf("row 1: expected false (world.csv does not match *.txt)")
	}
	if col.Nulls == nil || !col.Nulls[2] {
		t.Errorf("row 2: expected NULL")
	}
}

// ── Empty batch edge cases ──

func TestIsNullBatch_EmptyBatch(t *testing.T) {
	b := UT.GetBatch(1)
	b.Cols[0].Name = "x"
	b.SetColMap(map[string]int{"x": 0})

	expr := &PS.BinaryExpr{
		Op:    LX.T_IS,
		Left:  &PS.Ident{Name: "x"},
		Right: &PS.NullLiteral{},
	}
	sel := EvalBatch(expr, b, nil)
	if sel != nil {
		t.Fatalf("empty batch: expected nil, got %v", sel)
	}
}

func TestBetweenBatch_EmptyBatch(t *testing.T) {
	b := UT.GetBatch(1)
	b.Cols[0].Name = "x"
	b.SetColMap(map[string]int{"x": 0})

	expr := &PS.BetweenExpr{
		Expr: &PS.Ident{Name: "x"},
		Low:  &PS.NumberLiteral{Val: 1},
		High: &PS.NumberLiteral{Val: 10},
	}
	sel := EvalBatch(expr, b, nil)
	if sel != nil {
		t.Fatalf("empty batch BETWEEN: expected nil, got %v", sel)
	}
}
