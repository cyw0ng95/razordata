package EV

import (
	"testing"
	"time"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PL "github.com/cyw0ng95/razordata/internal/SQF/PL"
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func TestEvalNow_ReturnsRFC3339(t *testing.T) {
	before := time.Now()
	got, err := EvalValue(&PS.FunctionCall{Name: "NOW"}, nil, nil)
	if err != nil {
		t.Fatalf("NOW: %v", err)
	}
	after := time.Now()
	if got.Kind != KindText {
		t.Fatalf("NOW: expected string, got Kind=%d", got.Kind)
	}
	s := got.S
	parsed, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Errorf("NOW: %q is not RFC3339: %v", s, err)
	}
	// Sanity: the parsed time is between before and after.
	if parsed.Before(before.Add(-time.Second)) || parsed.After(after.Add(time.Second)) {
		t.Errorf("NOW: parsed time %v out of expected range [%v, %v]", parsed, before, after)
	}
}

func TestEvalSubstr(t *testing.T) {
	cases := []struct {
		name string
		args []PS.Expr
		want string
	}{
		{
			name: "basic_2_3",
			args: []PS.Expr{&PS.StringLiteral{Val: "hello"}, &PS.NumberLiteral{Val: 2}, &PS.NumberLiteral{Val: 3}},
			want: "ell",
		},
		{
			name: "from_start",
			args: []PS.Expr{&PS.StringLiteral{Val: "hello"}, &PS.NumberLiteral{Val: 1}},
			want: "hello",
		},
		{
			name: "to_end",
			args: []PS.Expr{&PS.StringLiteral{Val: "hello"}, &PS.NumberLiteral{Val: 3}},
			want: "llo",
		},
		{
			name: "out_of_range",
			args: []PS.Expr{&PS.StringLiteral{Val: "hi"}, &PS.NumberLiteral{Val: 10}},
			want: "",
		},
		{
			name: "length_clamped",
			args: []PS.Expr{&PS.StringLiteral{Val: "hello"}, &PS.NumberLiteral{Val: 2}, &PS.NumberLiteral{Val: 100}},
			want: "ello",
		},
		{
			name: "start_zero_clamped",
			args: []PS.Expr{&PS.StringLiteral{Val: "hello"}, &PS.NumberLiteral{Val: 0}, &PS.NumberLiteral{Val: 2}},
			want: "he",
		},
		{
			name: "negative_length",
			args: []PS.Expr{&PS.StringLiteral{Val: "hello"}, &PS.NumberLiteral{Val: 2}, &PS.NumberLiteral{Val: -1}},
			want: "",
		},
		{
			name: "single_char",
			args: []PS.Expr{&PS.StringLiteral{Val: "hello"}, &PS.NumberLiteral{Val: 5}, &PS.NumberLiteral{Val: 1}},
			want: "o",
		},
		{
			name: "empty_input",
			args: []PS.Expr{&PS.StringLiteral{Val: ""}, &PS.NumberLiteral{Val: 1}},
			want: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			expr := &PS.FunctionCall{Name: "SUBSTR", Args: c.args}
			got, err := EvalValue(expr, nil, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Kind != KindText || got.S != c.want {
				t.Errorf("got %v, want %q", got, c.want)
			}
		})
	}
}

// TestQualifiedName_OuterChain verifies that EvalValue resolves
// QualifiedName column references correctly when the outer row
// chain is set up (REQ001098). x.b must resolve to the inner row,
// t1.b must resolve to the outer row.
func TestQualifiedName_OuterChain(t *testing.T) {
	outer := PL.Row{
		Cols:      []string{"a", "b", "c"},
		Data:      []Value{DT.NewIntValue(1), DT.NewIntValue(2), DT.NewIntValue(3)},
		TableName: "t1",
	}
	inner := PL.Row{
		Cols:      []string{"x.a", "x.b", "x.c"},
		Data:      []Value{DT.NewIntValue(10), DT.NewIntValue(20), DT.NewIntValue(30)},
		TableName: "x",
	}
	inner.Outer = &outer

	// x.b should resolve to inner row's b (20)
	v, err := EvalValue(&PS.QualifiedName{Table: "x", Name: "b"}, &inner, nil)
	if err != nil {
		t.Fatalf("x.b: %v", err)
	}
	if v.Kind != KindInt || v.I64 != 20 {
		t.Errorf("x.b: got %v, want 20", v)
	}

	// t1.b should resolve to outer row's b (2)
	v, err = EvalValue(&PS.QualifiedName{Table: "t1", Name: "b"}, &inner, nil)
	if err != nil {
		t.Fatalf("t1.b: %v", err)
	}
	if v.Kind != KindInt || v.I64 != 2 {
		t.Errorf("t1.b: got %v, want 2", v)
	}

	// x.c > t1.c should correctly compare inner to outer
	gt := &PS.BinaryExpr{
		Left:  &PS.QualifiedName{Table: "x", Name: "c"},
		Op:    LX.T_GT,
		Right: &PS.QualifiedName{Table: "t1", Name: "c"},
	}
	v, err = EvalValue(gt, &inner, nil)
	if err != nil {
		t.Fatalf("x.c > t1.c: %v", err)
	}
	// inner.c=30 > outer.c=3 → TRUE
	if v.Kind != KindBool || !v.Bo {
		t.Errorf("x.c > t1.c: got %v, want TRUE", v)
	}

	// x.b < t1.b should correctly compare inner to outer
	lt := &PS.BinaryExpr{
		Left:  &PS.QualifiedName{Table: "x", Name: "b"},
		Op:    LX.T_LT,
		Right: &PS.QualifiedName{Table: "t1", Name: "b"},
	}
	v, err = EvalValue(lt, &inner, nil)
	if err != nil {
		t.Fatalf("x.b < t1.b: %v", err)
	}
	// inner.b=20 < outer.b=2 → FALSE
	if v.Kind != KindBool || v.Bo {
		t.Errorf("x.b < t1.b: got %v, want FALSE", v)
	}
}

// REQ001182: NOT BETWEEN NULL AND expr — three-valued NULL AND FALSE = FALSE.
func TestNotBetween_NullPropagation(t *testing.T) {
	row := &Row{Data: []Value{DT.NewIntValue(1)}, Cols: []string{"col1"}}
	// col1 NOT BETWEEN NULL AND -col1
	// = NOT(col1 BETWEEN NULL AND -col1)
	// col1 BETWEEN NULL AND -col1: col1 >= NULL is NULL, col1 <= -col1 is FALSE
	// NULL AND FALSE = FALSE, so BETWEEN = FALSE, NOT FALSE = TRUE
	// Therefore the row should NOT be filtered out.
	nb := &PS.UnaryExpr{
		Op: LX.T_NOT,
		Operand: &PS.BetweenExpr{
			Expr: &PS.Ident{Name: "col1"},
			Low:  &PS.NullLiteral{},
			High: &PS.UnaryExpr{Op: LX.T_MINUS, Operand: &PS.Ident{Name: "col1"}},
		},
	}
	v, err := EvalValue(nb, row, nil)
	if err != nil {
		t.Fatalf("NOT BETWEEN: %v", err)
	}
	if v.Kind != KindBool || !v.Bo {
		t.Errorf("NOT BETWEEN NULL AND -col1 for col1=1: got %v, want TRUE", v)
	}

	// col1 BETWEEN 0 AND NULL: col1 >= 0 is TRUE, col1 <= NULL is NULL
	// TRUE AND NULL = NULL — verify BETWEEN with only ONE NULL produces NULL.
	b := &PS.BetweenExpr{
		Expr: &PS.Ident{Name: "col1"},
		Low:  &PS.NumberLiteral{Val: 0},
		High: &PS.NullLiteral{},
	}
	v, err = EvalValue(b, row, nil)
	if err != nil {
		t.Fatalf("BETWEEN 0 AND NULL: %v", err)
	}
	if v.Kind != KindNull {
		t.Errorf("BETWEEN 0 AND NULL for col1=1: got %v, want NULL", v)
	}

	// col1 BETWEEN NULL AND 10: col1 >= NULL is NULL, col1 <= 10 is TRUE
	// NULL AND TRUE = NULL — verify both-non-FALSE with one NULL yields NULL.
	b2 := &PS.BetweenExpr{
		Expr: &PS.Ident{Name: "col1"},
		Low:  &PS.NullLiteral{},
		High: &PS.NumberLiteral{Val: 10},
	}
	v, err = EvalValue(b2, row, nil)
	if err != nil {
		t.Fatalf("BETWEEN NULL AND 10: %v", err)
	}
	if v.Kind != KindNull {
		t.Errorf("BETWEEN NULL AND 10 for col1=1: got %v, want NULL", v)
	}
}

// REQ001183: unary minus on TEXT returns NULL.
func TestUnaryMinus_TextReturnsNull(t *testing.T) {
	row := &Row{Data: []Value{DT.NewTextValue("a")}, Cols: []string{"col2"}}
	um := &PS.UnaryExpr{
		Op:      LX.T_MINUS,
		Operand: &PS.Ident{Name: "col2"},
	}
	v, err := EvalValue(um, row, nil)
	if err != nil {
		t.Fatalf("unary minus on TEXT: %v", err)
	}
	if v.Kind != KindNull {
		t.Errorf("unary minus on TEXT: got %v, want NULL", v)
	}
}

func TestSpecialForms(t *testing.T) {
	tests := []struct {
		sql  string
		want any
	}{
		{"SELECT COALESCE(NULL, NULL, 3, 'x')", int64(3)},
		{"SELECT COALESCE(NULL, 42)", int64(42)},
		{"SELECT COALESCE('first', NULL, 'third')", "first"},
		{"SELECT NULLIF(5, 5)", nil},
		{"SELECT NULLIF(5, 6)", int64(5)},
		{"SELECT NULLIF('abc', 'abc')", nil},
		{"SELECT NULLIF('abc', 'def')", "abc"},
	}

	_ = tests
	for _, tt := range tests {
		_ = tt
		t.Run(tt.sql, func(t *testing.T) {
			parser := PS.NewParser(tt.sql)
			stmt, err := parser.Parse()
			if err != nil {
				t.Fatalf("Parse error: %v", err)
			}
			sel := stmt.(*PS.Select)
			got, err := EvalValue(sel.Cols[0], nil, nil)
			if err != nil {
				t.Fatalf("Eval error: %v", err)
			}
			if got.ToAny() != tt.want {
				t.Fatalf("Got %v (%T), want %v (%T)", got.ToAny(), got.ToAny(), tt.want, tt.want)
			}
		})
	}
}

func TestSpecialForms_NoParens(t *testing.T) {
	tests := []struct {
		sql  string
		want any
	}{
		{"SELECT COALESCE NULL, 42", int64(42)},
		{"SELECT NULLIF 5, 5", nil},
	}

	for _, tt := range tests {
		_ = tt
		t.Run(tt.sql, func(t *testing.T) {
			parser := PS.NewParser(tt.sql)
			stmt, err := parser.Parse()
			_ = stmt
			if err != nil {
				t.Fatalf("Parse error: %v", err)
			}
			sel := stmt.(*PS.Select)
			got, err := EvalValue(sel.Cols[0], nil, nil)
			_ = got
			if err != nil {
				t.Fatalf("Eval error: %v", err)
			}
			if got.ToAny() != tt.want {
				t.Fatalf("Got %v (%T), want %v (%T)", got.ToAny(), got.ToAny(), tt.want, tt.want)
			}
		})
	}
}

func TestOperators_Bitwise(t *testing.T) {
	tests := []struct {
		sql  string
		want any
	}{
		{"SELECT 5 & 3", int64(1)},
		{"SELECT 5 | 3", int64(7)},
		{"SELECT 5 ^ 3", int64(6)},
		{"SELECT ~5", int64(-6)},
		{"SELECT 5 % 3", int64(2)},
		{"SELECT 10 % 3", int64(1)},
		{"SELECT 'Hello' || ' World'", "Hello World"},
		{"SELECT 'a' || 'b' || 'c'", "abc"},
		{"SELECT 12 & 10 | 3", int64(12&10 | 3)},
		{"SELECT 255 ^ 255", int64(0)},
		{"SELECT ~0", int64(-1)},
	}

	for _, tt := range tests {
		_ = tt
		t.Run(tt.sql, func(t *testing.T) {
			parser := PS.NewParser(tt.sql)
			stmt, err := parser.Parse()
			if err != nil {
				t.Fatalf("Parse error: %v", err)
			}
			sel := stmt.(*PS.Select)
			got, err := EvalValue(sel.Cols[0], nil, nil)
			if err != nil {
				t.Fatalf("Eval error: %v", err)
			}
			if got.ToAny() != tt.want {
				t.Fatalf("Got %v (%T), want %v (%T)", got.ToAny(), got.ToAny(), tt.want, tt.want)
			}
		})
	}
}

func TestNumericOverflow(t *testing.T) {
	tests := []struct {
		sql  string
		want any
	}{
		{"SELECT 5 * 3", int64(15)},
		{"SELECT -5 * 3", int64(-15)},
		{"SELECT 0 * 100", int64(0)},
		{"SELECT 9223372036854775807 * 2", nil},
		{"SELECT 1000000000 * 10000000000", nil},
		{"SELECT (-9223372036854775807) * -1", int64(9223372036854775807)},
		{"SELECT 9999999999 * 9999999999", nil},
		{"SELECT 1000000 * 1000000", int64(1000000000000)},
		{"SELECT 46341 * 46341", int64(2147488281)},
	}

	for _, tt := range tests {
		_ = tt
		t.Run(tt.sql, func(t *testing.T) {
			parser := PS.NewParser(tt.sql)
			stmt, err := parser.Parse()
			_ = stmt
			if err != nil {
				_ = stmt
				t.Fatalf("Parse error: %v", err)
			}
			sel := stmt.(*PS.Select)
			got, err := EvalValue(sel.Cols[0], nil, nil)
			if err != nil {
				t.Fatalf("Eval error: %v", err)
			}
			if got.ToAny() != tt.want {
               		_ = got
				t.Fatalf("Got %v (%T), want %v (%T)", got.ToAny(), got.ToAny(), tt.want, tt.want)
			}
		})
	}
}
