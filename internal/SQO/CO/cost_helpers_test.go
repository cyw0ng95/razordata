package CO

import (
	"testing"

	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func TestCost_Nil(t *testing.T) {
	if got := Cost(nil); got != 0 {
		t.Errorf("Cost(nil) = %d, want 0", got)
	}
}

func TestCost_Literals(t *testing.T) {
	cases := []struct {
		name string
		expr PS.Expr
		want int
	}{
		{"NumberLiteral", &PS.NumberLiteral{Val: 1}, 1},
		{"FloatLiteral", &PS.FloatLiteral{Val: 1.0}, 1},
		{"StringLiteral", &PS.StringLiteral{Val: "x"}, 1},
		{"BoolLiteral", &PS.BoolLiteral{Val: true}, 1},
		{"NullLiteral", &PS.NullLiteral{}, 1},
		{"StarExpr", &PS.StarExpr{}, 1},
		{"Param", &PS.Param{Index: 0}, 1},
		{"Ident", &PS.Ident{Name: "x"}, 1},
		{"QualifiedName", &PS.QualifiedName{Table: "t", Name: "c"}, 1},
		{"RaiseFunc", &PS.RaiseFunc{Action: "ABORT"}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Cost(tc.expr); got != tc.want {
				t.Errorf("Cost = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestFuncArgCost(t *testing.T) {
	args := []PS.Expr{
		&PS.NumberLiteral{Val: 1},
		&PS.NumberLiteral{Val: 2},
		&PS.NumberLiteral{Val: 3},
	}
	if got := FuncArgCost(args); got != 3 {
		t.Errorf("FuncArgCost = %d, want 3", got)
	}
	if got := FuncArgCost(nil); got != 0 {
		t.Errorf("FuncArgCost(nil) = %d, want 0", got)
	}
}

func TestCaseExprCost(t *testing.T) {
	c := &PS.CaseExpr{
		WhenList: []PS.WhenClause{
			{Cond: &PS.NumberLiteral{Val: 1}, Then: &PS.NumberLiteral{Val: 2}},
		},
		Else: &PS.NumberLiteral{Val: 3},
	}
	// cond=1 + then=1 + else=1 = 3 (Cost adds 3 base, total 6)
	if got := CaseExprCost(c); got != 3 {
		t.Errorf("CaseExprCost = %d, want 3", got)
	}
	if got := CaseExprCost(&PS.CaseExpr{}); got != 0 {
		t.Errorf("CaseExprCost(empty) = %d, want 0", got)
	}
}

func TestCost_AggregateAndWindow(t *testing.T) {
	agg := &PS.AggregateFunc{Name: "count"}
	if got := Cost(agg); got != 5 {
		t.Errorf("Cost(AggregateFunc) = %d, want 5", got)
	}
	win := &PS.WindowFunc{Name: "row_number"}
	if got := Cost(win); got != 10 {
		t.Errorf("Cost(WindowFunc) = %d, want 10", got)
	}
}

func TestCost_IntervalLiteral(t *testing.T) {
	i := &PS.IntervalLiteral{Value: "7", Unit: "DAY"}
	if got := Cost(i); got != 2 {
		t.Errorf("Cost(IntervalLiteral) = %d, want 2", got)
	}
}

func TestCost_AliasedExpr(t *testing.T) {
	a := &PS.AliasedExpr{Expr: &PS.NumberLiteral{Val: 1}, Alias: "x"}
	if got := Cost(a); got != 1 {
		t.Errorf("Cost(AliasedExpr with literal) = %d, want 1", got)
	}
}
