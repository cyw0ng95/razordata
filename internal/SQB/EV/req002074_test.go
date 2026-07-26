package EV

import (
	"math"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// ── COALESCE / IFNULL ──

func TestCoalesceBatch_HappyPath(t *testing.T) {
	// Batch with 3 columns: a (int), b (int), c (int)
	// Row 0: a=1, b=NULL, c=3  → COALESCE=1
	// Row 1: a=NULL, b=2, c=3  → COALESCE=2
	// Row 2: a=NULL, b=NULL, c=3 → COALESCE=3
	b := UT.GetBatch(3)
	// Row 0
	b.AppendRow(0, LX.T_INT_KW, int64(1), false)
	b.AppendRow(1, LX.T_INT_KW, int64(0), true)
	b.AppendRow(2, LX.T_INT_KW, int64(3), false)
	b.AdvanceSize()
	// Row 1
	b.AppendRow(0, LX.T_INT_KW, int64(0), true)
	b.AppendRow(1, LX.T_INT_KW, int64(2), false)
	b.AppendRow(2, LX.T_INT_KW, int64(3), false)
	b.AdvanceSize()
	// Row 2
	b.AppendRow(0, LX.T_INT_KW, int64(0), true)
	b.AppendRow(1, LX.T_INT_KW, int64(0), true)
	b.AppendRow(2, LX.T_INT_KW, int64(3), false)
	b.AdvanceSize()
	b.Cols[0].Name = "a"
	b.Cols[1].Name = "b"
	b.Cols[2].Name = "c"
	b.SetColMap(map[string]int{"a": 0, "b": 1, "c": 2})

	expr := &PS.FunctionCall{
		Name: "coalesce",
		Args: []PS.Expr{&PS.Ident{Name: "a"}, &PS.Ident{Name: "b"}, &PS.Ident{Name: "c"}},
	}
	col := EvalBatchExpr(expr, b, nil)

	if col.Type != LX.T_INT_KW {
		t.Fatalf("expected int type, got %v", col.Type)
	}
	expected := []int64{1, 2, 3}
	for i, want := range expected {
		if col.Data.Ints[i] != want {
			t.Errorf("row %d: expected %d, got %d", i, want, col.Data.Ints[i])
		}
	}
}

func TestCoalesceBatch_AllNull(t *testing.T) {
	b := UT.GetBatch(2)
	// Row 0: both NULL
	b.AppendRow(0, LX.T_INT_KW, int64(0), true)
	b.AppendRow(1, LX.T_INT_KW, int64(0), true)
	b.AdvanceSize()
	// Row 1: both NULL
	b.AppendRow(0, LX.T_INT_KW, int64(0), true)
	b.AppendRow(1, LX.T_INT_KW, int64(0), true)
	b.AdvanceSize()
	b.Cols[0].Name = "a"
	b.Cols[1].Name = "b"
	b.SetColMap(map[string]int{"a": 0, "b": 1})

	expr := &PS.FunctionCall{
		Name: "coalesce",
		Args: []PS.Expr{&PS.Ident{Name: "a"}, &PS.Ident{Name: "b"}},
	}
	col := EvalBatchExpr(expr, b, nil)

	for i := 0; i < 2; i++ {
		if !isNull(col, i) {
			t.Errorf("row %d: expected NULL, got non-null", i)
		}
	}
}

func TestIfNullBatch_HappyPath(t *testing.T) {
	b := UT.GetBatch(2)
	b.AppendRow(0, LX.T_TEXT, "hello", false)
	b.AppendRow(1, LX.T_TEXT, "world", true)
	b.AdvanceSize()
	b.AppendRow(0, LX.T_TEXT, "", true)
	b.AppendRow(1, LX.T_TEXT, "fallback", false)
	b.AdvanceSize()
	b.Cols[0].Name = "a"
	b.Cols[1].Name = "b"
	b.SetColMap(map[string]int{"a": 0, "b": 1})

	expr := &PS.FunctionCall{
		Name: "ifnull",
		Args: []PS.Expr{&PS.Ident{Name: "a"}, &PS.Ident{Name: "b"}},
	}
	col := EvalBatchExpr(expr, b, nil)

	if col.Type != LX.T_TEXT {
		t.Fatalf("expected text type, got %v", col.Type)
	}
	expected := []string{"hello", "fallback"}
	for i, want := range expected {
		if isNull(col, i) {
			t.Errorf("row %d: unexpected NULL", i)
			continue
		}
		if col.Data.Strs[i] != want {
			t.Errorf("row %d: expected %q, got %q", i, want, col.Data.Strs[i])
		}
	}
}

func TestCoalesceBatch_EmptyBatch(t *testing.T) {
	b := UT.GetBatch(1)
	b.Cols[0].Name = "a"
	b.SetColMap(map[string]int{"a": 0})

	expr := &PS.FunctionCall{
		Name: "coalesce",
		Args: []PS.Expr{&PS.Ident{Name: "a"}},
	}
	col := EvalBatchExpr(expr, b, nil)
	if col.Type != LX.T_NULL && col.Type != LX.T_INT_KW {
		t.Fatalf("expected a type for empty batch, got %v", col.Type)
	}
}

// ── NULLIF ──

func TestNullIfBatch_HappyPath(t *testing.T) {
	b := UT.GetBatch(2)
	// Row 0: a=1, b=2 → not equal → 1
	b.AppendRow(0, LX.T_INT_KW, int64(1), false)
	b.AppendRow(1, LX.T_INT_KW, int64(2), false)
	b.AdvanceSize()
	// Row 1: a=3, b=3 → equal → NULL
	b.AppendRow(0, LX.T_INT_KW, int64(3), false)
	b.AppendRow(1, LX.T_INT_KW, int64(3), false)
	b.AdvanceSize()
	// Row 2: a=NULL, b=1 → NULL (left is null)
	b.AppendRow(0, LX.T_INT_KW, int64(0), true)
	b.AppendRow(1, LX.T_INT_KW, int64(1), false)
	b.AdvanceSize()
	// Row 3: a=5, b=NULL → 5 (right is null, not equal)
	b.AppendRow(0, LX.T_INT_KW, int64(5), false)
	b.AppendRow(1, LX.T_INT_KW, int64(0), true)
	b.AdvanceSize()
	b.Cols[0].Name = "a"
	b.Cols[1].Name = "b"
	b.SetColMap(map[string]int{"a": 0, "b": 1})

	expr := &PS.FunctionCall{
		Name: "nullif",
		Args: []PS.Expr{&PS.Ident{Name: "a"}, &PS.Ident{Name: "b"}},
	}
	col := EvalBatchExpr(expr, b, nil)

	// Row 0: 1
	if isNull(col, 0) || col.Data.Ints[0] != 1 {
		t.Errorf("row 0: expected 1, got null=%v val=%d", isNull(col, 0), col.Data.Ints[0])
	}
	// Row 1: NULL
	if !isNull(col, 1) {
		t.Errorf("row 1: expected NULL, got %d", col.Data.Ints[1])
	}
	// Row 2: NULL (left was null)
	if !isNull(col, 2) {
		t.Errorf("row 2: expected NULL (left was null), got %d", col.Data.Ints[2])
	}
	// Row 3: 5
	if isNull(col, 3) || col.Data.Ints[3] != 5 {
		t.Errorf("row 3: expected 5, got null=%v val=%d", isNull(col, 3), col.Data.Ints[3])
	}
}

func TestNullIfBatch_TextEqual(t *testing.T) {
	b := UT.GetBatch(2)
	b.AppendRow(0, LX.T_TEXT, "abc", false)
	b.AppendRow(1, LX.T_TEXT, "abc", false)
	b.AdvanceSize()
	b.AppendRow(0, LX.T_TEXT, "def", false)
	b.AppendRow(1, LX.T_TEXT, "xyz", false)
	b.AdvanceSize()
	b.Cols[0].Name = "a"
	b.Cols[1].Name = "b"
	b.SetColMap(map[string]int{"a": 0, "b": 1})

	expr := &PS.FunctionCall{
		Name: "nullif",
		Args: []PS.Expr{&PS.Ident{Name: "a"}, &PS.Ident{Name: "b"}},
	}
	col := EvalBatchExpr(expr, b, nil)

	if !isNull(col, 0) {
		t.Errorf("row 0: expected NULL (equal strings), got %q", col.Data.Strs[0])
	}
	if isNull(col, 1) || col.Data.Strs[1] != "def" {
		t.Errorf("row 1: expected 'def', got null=%v val=%q", isNull(col, 1), col.Data.Strs[1])
	}
}

// ── SUBSTR / SUBSTRING ──

func TestSubstrBatch_HappyPath(t *testing.T) {
	b := UT.GetBatch(2)
	// Row 0: s="hello", start=1, len=3 → "hel"
	b.AppendRow(0, LX.T_TEXT, "hello", false)
	b.AppendRow(1, LX.T_INT_KW, int64(1), false)
	b.AdvanceSize()
	// Row 1: s="world", start=2, len=3 → "orl"
	b.AppendRow(0, LX.T_TEXT, "world", false)
	b.AppendRow(1, LX.T_INT_KW, int64(2), false)
	b.AdvanceSize()
	// Row 2: s="test", start=3 → "st" (no length)
	b.AppendRow(0, LX.T_TEXT, "test", false)
	b.AppendRow(1, LX.T_INT_KW, int64(3), false)
	b.AdvanceSize()
	b.Cols[0].Name = "s"
	b.Cols[1].Name = "start"
	b.SetColMap(map[string]int{"s": 0, "start": 1})

	// With length arg
	expr3 := &PS.FunctionCall{
		Name: "substr",
		Args: []PS.Expr{&PS.Ident{Name: "s"}, &PS.Ident{Name: "start"}, &PS.NumberLiteral{Val: 3}},
	}
	col3 := EvalBatchExpr(expr3, b, nil)
	if col3.Data.Strs[0] != "hel" {
		t.Errorf("row 0 with len: expected 'hel', got %q", col3.Data.Strs[0])
	}
	if col3.Data.Strs[1] != "orl" {
		t.Errorf("row 1 with len: expected 'orl', got %q", col3.Data.Strs[1])
	}

	// Without length arg
	expr2 := &PS.FunctionCall{
		Name: "substr",
		Args: []PS.Expr{&PS.Ident{Name: "s"}, &PS.Ident{Name: "start"}},
	}
	col2 := EvalBatchExpr(expr2, b, nil)
	if col2.Data.Strs[2] != "st" {
		t.Errorf("row 2 no len: expected 'st', got %q", col2.Data.Strs[2])
	}
}

func TestSubstrBatch_NullHandling(t *testing.T) {
	b := UT.GetBatch(2)
	b.AppendRow(0, LX.T_TEXT, "", true) // NULL string
	b.AppendRow(1, LX.T_INT_KW, int64(1), false)
	b.AdvanceSize()
	b.AppendRow(0, LX.T_TEXT, "hello", false)
	b.AppendRow(1, LX.T_INT_KW, int64(0), true) // NULL start
	b.AdvanceSize()
	b.Cols[0].Name = "s"
	b.Cols[1].Name = "start"
	b.SetColMap(map[string]int{"s": 0, "start": 1})

	expr := &PS.FunctionCall{
		Name: "substr",
		Args: []PS.Expr{&PS.Ident{Name: "s"}, &PS.Ident{Name: "start"}},
	}
	col := EvalBatchExpr(expr, b, nil)

	if !isNull(col, 0) {
		t.Errorf("row 0: expected NULL for null string input")
	}
	if !isNull(col, 1) {
		t.Errorf("row 1: expected NULL for null start input")
	}
}

func TestSubstrBatch_EdgeCases(t *testing.T) {
	b := UT.GetBatch(2)
	// start=0 → from beginning
	b.AppendRow(0, LX.T_TEXT, "abc", false)
	b.AppendRow(1, LX.T_INT_KW, int64(0), false)
	b.AdvanceSize()
	// start past end → empty string
	b.AppendRow(0, LX.T_TEXT, "hi", false)
	b.AppendRow(1, LX.T_INT_KW, int64(10), false)
	b.AdvanceSize()
	// empty string input
	b.AppendRow(0, LX.T_TEXT, "", false)
	b.AppendRow(1, LX.T_INT_KW, int64(1), false)
	b.AdvanceSize()
	b.Cols[0].Name = "s"
	b.Cols[1].Name = "start"
	b.SetColMap(map[string]int{"s": 0, "start": 1})

	expr := &PS.FunctionCall{
		Name: "substr",
		Args: []PS.Expr{&PS.Ident{Name: "s"}, &PS.Ident{Name: "start"}},
	}
	col := EvalBatchExpr(expr, b, nil)

	if col.Data.Strs[0] != "abc" {
		t.Errorf("start=0: expected 'abc', got %q", col.Data.Strs[0])
	}
	if col.Data.Strs[1] != "" {
		t.Errorf("start past end: expected '', got %q", col.Data.Strs[1])
	}
	if col.Data.Strs[2] != "" {
		t.Errorf("empty string: expected '', got %q", col.Data.Strs[2])
	}
}

// ── REPLACE ──

func TestReplaceBatch_HappyPath(t *testing.T) {
	b := UT.GetBatch(3)
	// Row 0: "hello world", replace "world"→"there"
	b.AppendRow(0, LX.T_TEXT, "hello world", false)
	b.AppendRow(1, LX.T_TEXT, "world", false)
	b.AppendRow(2, LX.T_TEXT, "there", false)
	b.AdvanceSize()
	// Row 1: "aaa", replace "a"→"b"
	b.AppendRow(0, LX.T_TEXT, "aaa", false)
	b.AppendRow(1, LX.T_TEXT, "a", false)
	b.AppendRow(2, LX.T_TEXT, "b", false)
	b.AdvanceSize()
	// Row 2: "xyz", replace "notfound"→"nope"
	b.AppendRow(0, LX.T_TEXT, "xyz", false)
	b.AppendRow(1, LX.T_TEXT, "notfound", false)
	b.AppendRow(2, LX.T_TEXT, "nope", false)
	b.AdvanceSize()
	b.Cols[0].Name = "s"
	b.Cols[1].Name = "from"
	b.Cols[2].Name = "to"
	b.SetColMap(map[string]int{"s": 0, "from": 1, "to": 2})

	expr := &PS.FunctionCall{
		Name: "replace",
		Args: []PS.Expr{&PS.Ident{Name: "s"}, &PS.Ident{Name: "from"}, &PS.Ident{Name: "to"}},
	}
	col := EvalBatchExpr(expr, b, nil)

	if col.Data.Strs[0] != "hello there" {
		t.Errorf("row 0: expected 'hello there', got %q", col.Data.Strs[0])
	}
	if col.Data.Strs[1] != "bbb" {
		t.Errorf("row 1: expected 'bbb', got %q", col.Data.Strs[1])
	}
	if col.Data.Strs[2] != "xyz" {
		t.Errorf("row 2: expected 'xyz', got %q", col.Data.Strs[2])
	}
}

func TestReplaceBatch_NullHandling(t *testing.T) {
	b := UT.GetBatch(3)
	// Row 0: null string
	b.AppendRow(0, LX.T_TEXT, "", true)
	b.AppendRow(1, LX.T_TEXT, "a", false)
	b.AppendRow(2, LX.T_TEXT, "b", false)
	b.AdvanceSize()
	// Row 1: null "from"
	b.AppendRow(0, LX.T_TEXT, "hello", false)
	b.AppendRow(1, LX.T_TEXT, "", true)
	b.AppendRow(2, LX.T_TEXT, "x", false)
	b.AdvanceSize()
	b.Cols[0].Name = "s"
	b.Cols[1].Name = "from"
	b.Cols[2].Name = "to"
	b.SetColMap(map[string]int{"s": 0, "from": 1, "to": 2})

	expr := &PS.FunctionCall{
		Name: "replace",
		Args: []PS.Expr{&PS.Ident{Name: "s"}, &PS.Ident{Name: "from"}, &PS.Ident{Name: "to"}},
	}
	col := EvalBatchExpr(expr, b, nil)

	if !isNull(col, 0) {
		t.Errorf("row 0: expected NULL for null string")
	}
	if !isNull(col, 1) {
		t.Errorf("row 1: expected NULL for null 'from'")
	}
}

// ── TRIM ──

func TestTrimBatch_HappyPath(t *testing.T) {
	b := UT.GetBatch(1)
	b.AppendRow(0, LX.T_TEXT, "  hello  ", false)
	b.AdvanceSize()
	b.AppendRow(0, LX.T_TEXT, "world", false)
	b.AdvanceSize()
	b.AppendRow(0, LX.T_TEXT, "\t\nhi\r\n", false)
	b.AdvanceSize()
	b.Cols[0].Name = "s"
	b.SetColMap(map[string]int{"s": 0})

	expr := &PS.FunctionCall{
		Name: "trim",
		Args: []PS.Expr{&PS.Ident{Name: "s"}},
	}
	col := EvalBatchExpr(expr, b, nil)

	if col.Data.Strs[0] != "hello" {
		t.Errorf("row 0: expected 'hello', got %q", col.Data.Strs[0])
	}
	if col.Data.Strs[1] != "world" {
		t.Errorf("row 1: expected 'world', got %q", col.Data.Strs[1])
	}
	if col.Data.Strs[2] != "hi" {
		t.Errorf("row 2: expected 'hi', got %q", col.Data.Strs[2])
	}
}

func TestTrimBatch_Null(t *testing.T) {
	b := UT.GetBatch(1)
	b.AppendRow(0, LX.T_TEXT, "", true)
	b.AdvanceSize()
	b.Cols[0].Name = "s"
	b.SetColMap(map[string]int{"s": 0})

	expr := &PS.FunctionCall{
		Name: "trim",
		Args: []PS.Expr{&PS.Ident{Name: "s"}},
	}
	col := EvalBatchExpr(expr, b, nil)

	if !isNull(col, 0) {
		t.Errorf("expected NULL for null input")
	}
}

// ── ROUND ──

func TestRoundBatch_HappyPath(t *testing.T) {
	b := UT.GetBatch(2)
	// Row 0: 3.14159, prec=2 → 3.14
	b.AppendRow(0, LX.T_FLOAT_KW, 3.14159, false)
	b.AppendRow(1, LX.T_INT_KW, int64(2), false)
	b.AdvanceSize()
	// Row 1: 2.71828, prec=0 → 3.0
	b.AppendRow(0, LX.T_FLOAT_KW, 2.71828, false)
	b.AppendRow(1, LX.T_INT_KW, int64(0), false)
	b.AdvanceSize()
	// Row 2: -1.5, prec=0 → -2.0
	b.AppendRow(0, LX.T_FLOAT_KW, -1.5, false)
	b.AppendRow(1, LX.T_INT_KW, int64(0), false)
	b.AdvanceSize()
	// Row 3: 123.456, prec=1 → 123.5
	b.AppendRow(0, LX.T_FLOAT_KW, 123.456, false)
	b.AppendRow(1, LX.T_INT_KW, int64(1), false)
	b.AdvanceSize()
	b.Cols[0].Name = "x"
	b.Cols[1].Name = "n"
	b.SetColMap(map[string]int{"x": 0, "n": 1})

	expr := &PS.FunctionCall{
		Name: "round",
		Args: []PS.Expr{&PS.Ident{Name: "x"}, &PS.Ident{Name: "n"}},
	}
	col := EvalBatchExpr(expr, b, nil)

	if math.Abs(col.Data.Floats[0]-3.14) > 1e-9 {
		t.Errorf("row 0: expected 3.14, got %f", col.Data.Floats[0])
	}
	if math.Abs(col.Data.Floats[1]-3.0) > 1e-9 {
		t.Errorf("row 1: expected 3.0, got %f", col.Data.Floats[1])
	}
	if math.Abs(col.Data.Floats[2]-(-2.0)) > 1e-9 {
		t.Errorf("row 2: expected -2.0, got %f", col.Data.Floats[2])
	}
	if math.Abs(col.Data.Floats[3]-123.5) > 1e-9 {
		t.Errorf("row 3: expected 123.5, got %f", col.Data.Floats[3])
	}
}

func TestRoundBatch_NoPrec(t *testing.T) {
	b := UT.GetBatch(1)
	b.AppendRow(0, LX.T_FLOAT_KW, 2.5, false)
	b.AdvanceSize()
	b.AppendRow(0, LX.T_FLOAT_KW, -2.5, false)
	b.AdvanceSize()
	b.Cols[0].Name = "x"
	b.SetColMap(map[string]int{"x": 0})

	expr := &PS.FunctionCall{
		Name: "round",
		Args: []PS.Expr{&PS.Ident{Name: "x"}},
	}
	col := EvalBatchExpr(expr, b, nil)

	if math.Abs(col.Data.Floats[0]-3.0) > 1e-9 {
		t.Errorf("row 0: expected 3.0, got %f", col.Data.Floats[0])
	}
	if math.Abs(col.Data.Floats[1]-(-3.0)) > 1e-9 {
		t.Errorf("row 1: expected -3.0, got %f", col.Data.Floats[1])
	}
}

func TestRoundBatch_Null(t *testing.T) {
	b := UT.GetBatch(2)
	b.AppendRow(0, LX.T_FLOAT_KW, 0.0, true)
	b.AppendRow(1, LX.T_INT_KW, int64(2), false)
	b.AdvanceSize()
	b.AppendRow(0, LX.T_FLOAT_KW, 1.5, false)
	b.AppendRow(1, LX.T_INT_KW, int64(0), true)
	b.AdvanceSize()
	b.Cols[0].Name = "x"
	b.Cols[1].Name = "n"
	b.SetColMap(map[string]int{"x": 0, "n": 1})

	expr := &PS.FunctionCall{
		Name: "round",
		Args: []PS.Expr{&PS.Ident{Name: "x"}, &PS.Ident{Name: "n"}},
	}
	col := EvalBatchExpr(expr, b, nil)

	if !isNull(col, 0) {
		t.Errorf("row 0: expected NULL for null input")
	}
	if !isNull(col, 1) {
		t.Errorf("row 1: expected NULL for null precision")
	}
}

// ── TYPEOF ──

func TestTypeofBatch_HappyPath(t *testing.T) {
	// Test with int column
	b := UT.GetBatch(1)
	b.AppendRow(0, LX.T_INT_KW, int64(42), false)
	b.AdvanceSize()
	b.AppendRow(0, LX.T_INT_KW, int64(0), true) // NULL → "null"
	b.AdvanceSize()
	b.AppendRow(0, LX.T_INT_KW, int64(7), false)
	b.AdvanceSize()
	b.Cols[0].Name = "x"
	b.SetColMap(map[string]int{"x": 0})

	expr := &PS.FunctionCall{
		Name: "typeof",
		Args: []PS.Expr{&PS.Ident{Name: "x"}},
	}
	col := EvalBatchExpr(expr, b, nil)

	if col.Data.Strs[0] != "integer" {
		t.Errorf("row 0: expected 'integer', got %q", col.Data.Strs[0])
	}
	if col.Data.Strs[1] != "null" {
		t.Errorf("row 1: expected 'null', got %q", col.Data.Strs[1])
	}
	if col.Data.Strs[2] != "integer" {
		t.Errorf("row 2: expected 'integer', got %q", col.Data.Strs[2])
	}
}

func TestTypeofBatch_FloatAndText(t *testing.T) {
	// Float batch
	b := UT.GetBatch(1)
	b.AppendRow(0, LX.T_FLOAT_KW, 3.14, false)
	b.AdvanceSize()
	b.Cols[0].Name = "x"
	b.SetColMap(map[string]int{"x": 0})

	expr := &PS.FunctionCall{
		Name: "typeof",
		Args: []PS.Expr{&PS.Ident{Name: "x"}},
	}
	col := EvalBatchExpr(expr, b, nil)
	if col.Data.Strs[0] != "real" {
		t.Errorf("float: expected 'real', got %q", col.Data.Strs[0])
	}

	// Text batch
	b2 := UT.GetBatch(1)
	b2.AppendRow(0, LX.T_TEXT, "hello", false)
	b2.AdvanceSize()
	b2.Cols[0].Name = "x"
	b2.SetColMap(map[string]int{"x": 0})

	col2 := EvalBatchExpr(expr, b2, nil)
	if col2.Data.Strs[0] != "text" {
		t.Errorf("text: expected 'text', got %q", col2.Data.Strs[0])
	}
}

// ── INSTR ──

func TestInstrBatch_HappyPath(t *testing.T) {
	b := UT.GetBatch(2)
	// Row 0: "hello", "ll" → 3
	b.AppendRow(0, LX.T_TEXT, "hello", false)
	b.AppendRow(1, LX.T_TEXT, "ll", false)
	b.AdvanceSize()
	// Row 1: "hello", "x" → 0
	b.AppendRow(0, LX.T_TEXT, "hello", false)
	b.AppendRow(1, LX.T_TEXT, "x", false)
	b.AdvanceSize()
	// Row 2: "abcabc", "abc" → 1 (first occurrence)
	b.AppendRow(0, LX.T_TEXT, "abcabc", false)
	b.AppendRow(1, LX.T_TEXT, "abc", false)
	b.AdvanceSize()
	// Row 3: "test", "" → 1 (empty substring always matches at 1)
	b.AppendRow(0, LX.T_TEXT, "test", false)
	b.AppendRow(1, LX.T_TEXT, "", false)
	b.AdvanceSize()
	b.Cols[0].Name = "s"
	b.Cols[1].Name = "sub"
	b.SetColMap(map[string]int{"s": 0, "sub": 1})

	expr := &PS.FunctionCall{
		Name: "instr",
		Args: []PS.Expr{&PS.Ident{Name: "s"}, &PS.Ident{Name: "sub"}},
	}
	col := EvalBatchExpr(expr, b, nil)

	if col.Data.Ints[0] != 3 {
		t.Errorf("row 0: expected 3, got %d", col.Data.Ints[0])
	}
	if col.Data.Ints[1] != 0 {
		t.Errorf("row 1: expected 0, got %d", col.Data.Ints[1])
	}
	if col.Data.Ints[2] != 1 {
		t.Errorf("row 2: expected 1, got %d", col.Data.Ints[2])
	}
	if col.Data.Ints[3] != 1 {
		t.Errorf("row 3: expected 1, got %d", col.Data.Ints[3])
	}
}

func TestInstrBatch_Null(t *testing.T) {
	b := UT.GetBatch(2)
	b.AppendRow(0, LX.T_TEXT, "", true)
	b.AppendRow(1, LX.T_TEXT, "a", false)
	b.AdvanceSize()
	b.AppendRow(0, LX.T_TEXT, "hello", false)
	b.AppendRow(1, LX.T_TEXT, "", true)
	b.AdvanceSize()
	b.Cols[0].Name = "s"
	b.Cols[1].Name = "sub"
	b.SetColMap(map[string]int{"s": 0, "sub": 1})

	expr := &PS.FunctionCall{
		Name: "instr",
		Args: []PS.Expr{&PS.Ident{Name: "s"}, &PS.Ident{Name: "sub"}},
	}
	col := EvalBatchExpr(expr, b, nil)

	if !isNull(col, 0) {
		t.Errorf("row 0: expected NULL for null string")
	}
	if !isNull(col, 1) {
		t.Errorf("row 1: expected NULL for null substring")
	}
}

// ── HEX ──

func TestHexBatch_IntInput(t *testing.T) {
	b := UT.GetBatch(1)
	b.AppendRow(0, LX.T_INT_KW, int64(255), false)
	b.AdvanceSize()
	b.AppendRow(0, LX.T_INT_KW, int64(16), false)
	b.AdvanceSize()
	b.AppendRow(0, LX.T_INT_KW, int64(0), false)
	b.AdvanceSize()
	b.Cols[0].Name = "x"
	b.SetColMap(map[string]int{"x": 0})

	expr := &PS.FunctionCall{
		Name: "hex",
		Args: []PS.Expr{&PS.Ident{Name: "x"}},
	}
	col := EvalBatchExpr(expr, b, nil)

	if col.Data.Strs[0] != "FF" {
		t.Errorf("row 0: expected 'FF', got %q", col.Data.Strs[0])
	}
	if col.Data.Strs[1] != "10" {
		t.Errorf("row 1: expected '10', got %q", col.Data.Strs[1])
	}
	if col.Data.Strs[2] != "0" {
		t.Errorf("row 2: expected '0', got %q", col.Data.Strs[2])
	}
}

func TestHexBatch_StringInput(t *testing.T) {
	b := UT.GetBatch(1)
	b.AppendRow(0, LX.T_TEXT, "ab", false)
	b.AdvanceSize()
	b.AppendRow(0, LX.T_TEXT, "", false)
	b.AdvanceSize()
	b.Cols[0].Name = "x"
	b.SetColMap(map[string]int{"x": 0})

	expr := &PS.FunctionCall{
		Name: "hex",
		Args: []PS.Expr{&PS.Ident{Name: "x"}},
	}
	col := EvalBatchExpr(expr, b, nil)

	if col.Data.Strs[0] != "6162" {
		t.Errorf("row 0: expected '6162', got %q", col.Data.Strs[0])
	}
	if col.Data.Strs[1] != "" {
		t.Errorf("row 1: expected '', got %q", col.Data.Strs[1])
	}
}

func TestHexBatch_Null(t *testing.T) {
	b := UT.GetBatch(1)
	b.AppendRow(0, LX.T_INT_KW, int64(0), true)
	b.AdvanceSize()
	b.Cols[0].Name = "x"
	b.SetColMap(map[string]int{"x": 0})

	expr := &PS.FunctionCall{
		Name: "hex",
		Args: []PS.Expr{&PS.Ident{Name: "x"}},
	}
	col := EvalBatchExpr(expr, b, nil)

	if !isNull(col, 0) {
		t.Errorf("expected NULL for null input")
	}
}
