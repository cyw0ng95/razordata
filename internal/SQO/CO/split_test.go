package CO

import (
	"testing"

	LX "github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func makeAndExpr(left, right PS.Expr) *PS.BinaryExpr {
	return &PS.BinaryExpr{Op: LX.T_AND, Left: left, Right: right}
}

func TestSplitAnd_Nil(t *testing.T) {
	cache := make(map[uintptr][]PS.Expr)
	if got := SplitAnd(nil, cache); got != nil {
		t.Errorf("SplitAnd(nil) = %v, want nil", got)
	}
}

func TestSplitAnd_SingleExpr(t *testing.T) {
	cache := make(map[uintptr][]PS.Expr)
	e := &PS.NumberLiteral{Val: 1}
	got := SplitAnd(e, cache)
	if len(got) != 1 || got[0] != e {
		t.Errorf("SplitAnd(single) = %v, want [%v]", got, e)
	}
}

func TestSplitAnd_FlatConjunction(t *testing.T) {
	cache := make(map[uintptr][]PS.Expr)
	a := &PS.Ident{Name: "a"}
	b := &PS.Ident{Name: "b"}
	c := &PS.Ident{Name: "c"}
	e := makeAndExpr(makeAndExpr(a, b), c)
	got := SplitAnd(e, cache)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	exp := []PS.Expr{a, b, c}
	for i, g := range got {
		if g != exp[i] {
			t.Errorf("got[%d] = %v, want %v", i, g, exp[i])
		}
	}
}

func TestSplitAnd_Cached(t *testing.T) {
	cache := make(map[uintptr][]PS.Expr)
	e := makeAndExpr(&PS.Ident{Name: "x"}, &PS.Ident{Name: "y"})
	first := SplitAnd(e, cache)
	second := SplitAnd(e, cache)
	if len(second) != len(first) {
		t.Errorf("cached call returned different length")
	}
}
