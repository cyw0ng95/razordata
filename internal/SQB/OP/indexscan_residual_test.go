package OP

import (
	"testing"

	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func TestIndexScan_Residual_NilHotPath(t *testing.T) {
	var is IndexScan
	if is.Residual() != nil {
		t.Fatalf("expected nil residual, got %v", is.Residual())
	}
	is.WithResidual(nil)
	if is.Residual() != nil {
		t.Fatalf("WithResidual(nil) should clear")
	}
}

func TestIndexScan_Residual_Truthiness(t *testing.T) {
	cases := []struct {
		name string
		expr PS.Expr
		want bool
	}{
		{"int_zero", &PS.NumberLiteral{Val: 0}, false},
		{"int_nonzero", &PS.NumberLiteral{Val: 7}, true},
		{"bool_true", &PS.BoolLiteral{Val: true}, true},
		{"bool_false", &PS.BoolLiteral{Val: false}, false},
		{"null", &PS.NullLiteral{}, false},
		{"text_nonempty", &PS.StringLiteral{Val: "hi"}, true},
		{"text_empty", &PS.StringLiteral{Val: ""}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var is IndexScan
			is.WithResidual([]PS.Expr{tc.expr})
			row := &Row{Cols: []string{"x"}}
			if got := is.matchResidual(row); got != tc.want {
				t.Errorf("matchResidual(%s) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

func TestIndexScan_Residual_ShortCircuit(t *testing.T) {
	var is IndexScan
	exprTrue := &PS.BoolLiteral{Val: true}
	exprFalse := &PS.BoolLiteral{Val: false}
	row := &Row{Cols: []string{"x"}}

	// All truthy → match passes.
	is.WithResidual([]PS.Expr{exprTrue, exprTrue})
	if !is.matchResidual(row) {
		t.Fatalf("two truthy residuals should pass")
	}

	// First falsy → short-circuits, returns false.
	bad := &PS.BinaryExpr{Op: 0xFFFF, Left: &PS.NumberLiteral{Val: 1}, Right: &PS.NumberLiteral{Val: 1}}
	// The bad expr would error on eval. matchResidual must NOT
	// reach it because the first predicate is falsy.
	is.WithResidual([]PS.Expr{exprFalse, bad})
	if is.matchResidual(row) {
		t.Fatalf("first falsy should short-circuit, got true")
	}
}
