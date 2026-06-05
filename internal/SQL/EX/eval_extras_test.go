package EX

import (
	"testing"
	"time"

	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

func TestEvalNow_ReturnsRFC3339(t *testing.T) {
	before := time.Now()
	got, err := Eval(&PS.FunctionCall{Name: "NOW"}, nil, nil)
	if err != nil {
		t.Fatalf("NOW: %v", err)
	}
	after := time.Now()
	s, ok := got.(string)
	if !ok {
		t.Fatalf("NOW: expected string, got %T", got)
	}
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
			got, err := Eval(expr, nil, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if s, ok := got.(string); !ok || s != c.want {
				t.Errorf("got %v, want %q", got, c.want)
			}
		})
	}
}
