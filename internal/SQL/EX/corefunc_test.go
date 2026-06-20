package EX

import (
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

// REQ000382: ABS, HEX, ROUND unit tests.

func TestEvalAbs(t *testing.T) {
	cases := []struct {
		name string
		args []PS.Expr
		want any
	}{
		{"neg_int", []PS.Expr{&PS.NumberLiteral{Val: int64(-5)}}, int64(5)},
		{"pos_int", []PS.Expr{&PS.NumberLiteral{Val: int64(5)}}, int64(5)},
		{"zero", []PS.Expr{&PS.NumberLiteral{Val: int64(0)}}, int64(0)},
		{"neg_float", []PS.Expr{&PS.FloatLiteral{Val: -3.14}}, 3.14},
		{"pos_float", []PS.Expr{&PS.FloatLiteral{Val: 3.14}}, 3.14},
		{"null_in", []PS.Expr{&PS.NullLiteral{}}, nil},
		{"non_numeric", []PS.Expr{&PS.StringLiteral{Val: "abc"}}, 0.0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Eval(&PS.FunctionCall{Name: "ABS", Args: c.args}, nil, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Errorf("got %v (%T), want %v", got, got, c.want)
			}
		})
	}
}

func TestEvalAbs_MinInt64Overflow(t *testing.T) {
	_, err := Eval(&PS.FunctionCall{Name: "ABS", Args: []PS.Expr{&PS.NumberLiteral{Val: -9223372036854775808}}}, nil, nil)
	if err == nil {
		t.Errorf("expected error for ABS(MIN_INT64), got nil")
	}
}

func TestEvalHex(t *testing.T) {
	cases := []struct {
		name string
		args []PS.Expr
		want string
	}{
		{"string", []PS.Expr{&PS.StringLiteral{Val: "abc"}}, "616263"},
		{"empty_string", []PS.Expr{&PS.StringLiteral{Val: ""}}, ""},
		// Integer: SQLite first formats as decimal, then hex-encodes.
		{"int_255", []PS.Expr{&PS.NumberLiteral{Val: int64(255)}}, "323535"},
		{"int_neg", []PS.Expr{&PS.NumberLiteral{Val: int64(-1)}}, "2D31"},
		{"int_zero", []PS.Expr{&PS.NumberLiteral{Val: int64(0)}}, "30"},
		{"null", []PS.Expr{&PS.NullLiteral{}}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Eval(&PS.FunctionCall{Name: "HEX", Args: c.args}, nil, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if c.name == "null" {
				if got != nil {
					t.Errorf("got %v, want nil", got)
				}
				return
			}
			if s, ok := got.(string); !ok || s != c.want {
				t.Errorf("got %v, want %q", got, c.want)
			}
		})
	}
}

func TestEvalRound(t *testing.T) {
	cases := []struct {
		name string
		args []PS.Expr
		want float64
	}{
		{"integer_3.5", []PS.Expr{&PS.FloatLiteral{Val: 3.5}}, 4},
		{"integer_2.4", []PS.Expr{&PS.FloatLiteral{Val: 2.4}}, 2},
		{"two_places", []PS.Expr{&PS.FloatLiteral{Val: 3.14159}, &PS.NumberLiteral{Val: int64(2)}}, 3.14},
		{"zero_places", []PS.Expr{&PS.FloatLiteral{Val: 3.5}, &PS.NumberLiteral{Val: int64(0)}}, 4},
		{"neg_places", []PS.Expr{&PS.FloatLiteral{Val: 3.14}, &PS.NumberLiteral{Val: int64(-1)}}, 3},
		{"int_input", []PS.Expr{&PS.NumberLiteral{Val: int64(7)}}, 7},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Eval(&PS.FunctionCall{Name: "ROUND", Args: c.args}, nil, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if f, ok := got.(float64); !ok || f != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestEvalRound_Null(t *testing.T) {
	got, err := Eval(&PS.FunctionCall{Name: "ROUND", Args: []PS.Expr{&PS.NullLiteral{}}}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}
