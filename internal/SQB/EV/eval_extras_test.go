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
