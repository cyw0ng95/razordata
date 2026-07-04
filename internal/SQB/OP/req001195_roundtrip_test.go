package OP

import (
	"testing"

	LX "github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

func TestREQ001195_Roundtrip_SingleComparison(t *testing.T) {
	pred := &PS.BinaryExpr{
		Op:    LX.T_EQ,
		Left:  &PS.Ident{Name: "a"},
		Right: &PS.NumberLiteral{Val: 42},
	}
	_, params := pl.NormalizeForMemo(&PS.Select{
		From:  "t",
		Cols:  []PS.Expr{&PS.StarExpr{}},
		Where: pred,
	})
	if len(params) != 1 {
		t.Fatalf("expected 1 param, got %d", len(params))
	}
	f := NewFilter(nil, pred)
	litCount := f.CountComparisonLiterals()
	if litCount != 1 {
		t.Fatalf("CountComparisonLiterals = %d, want 1", litCount)
	}
	f.ReplaceLiterals(params)
	replaced, ok := f.Predicate().(*PS.BinaryExpr)
	if !ok {
		t.Fatalf("predicate is not BinaryExpr after replace: %T", f.Predicate())
	}
	lit, ok := replaced.Right.(*PS.NumberLiteral)
	if !ok {
		t.Fatalf("Right is not NumberLiteral: %T", replaced.Right)
	}
	if lit.Val != 42 {
		t.Errorf("replaced literal = %d, want 42", lit.Val)
	}
}

func TestREQ001195_Roundtrip_MultipleComparisons(t *testing.T) {
	pred := &PS.BinaryExpr{
		Op: LX.T_AND,
		Left: &PS.BinaryExpr{
			Op:    LX.T_EQ,
			Left:  &PS.Ident{Name: "a"},
			Right: &PS.NumberLiteral{Val: 1},
		},
		Right: &PS.BinaryExpr{
			Op:    LX.T_GT,
			Left:  &PS.Ident{Name: "b"},
			Right: &PS.NumberLiteral{Val: 100},
		},
	}
	_, params := pl.NormalizeForMemo(&PS.Select{
		From:  "t",
		Cols:  []PS.Expr{&PS.StarExpr{}},
		Where: pred,
	})
	if len(params) != 2 {
		t.Fatalf("expected 2 params, got %d", len(params))
	}
	f := NewFilter(nil, pred)
	litCount := f.CountComparisonLiterals()
	if litCount != 2 {
		t.Fatalf("CountComparisonLiterals = %d, want 2", litCount)
	}
	f.ReplaceLiterals(params)
	replaced := f.Predicate().(*PS.BinaryExpr)
	left := replaced.Left.(*PS.BinaryExpr)
	right := replaced.Right.(*PS.BinaryExpr)
	if left.Right.(*PS.NumberLiteral).Val != 1 {
		t.Errorf("left literal = %v, want 1", left.Right)
	}
	if right.Right.(*PS.NumberLiteral).Val != 100 {
		t.Errorf("right literal = %v, want 100", right.Right)
	}
}

func TestREQ001195_ParameterizedKey_SameStructureDifferentLiterals(t *testing.T) {
	stmt1 := &PS.Select{
		From: "t",
		Cols: []PS.Expr{&PS.StarExpr{}},
		Where: &PS.BinaryExpr{
			Op:    LX.T_EQ,
			Left:  &PS.Ident{Name: "a"},
			Right: &PS.NumberLiteral{Val: 1},
		},
	}
	stmt2 := &PS.Select{
		From: "t",
		Cols: []PS.Expr{&PS.StarExpr{}},
		Where: &PS.BinaryExpr{
			Op:    LX.T_EQ,
			Left:  &PS.Ident{Name: "a"},
			Right: &PS.NumberLiteral{Val: 999},
		},
	}
	norm1, params1 := pl.NormalizeForMemo(stmt1)
	norm2, params2 := pl.NormalizeForMemo(stmt2)
	key1 := pl.SerializeKey(norm1)
	key2 := pl.SerializeKey(norm2)
	if key1 != key2 {
		t.Errorf("parameterized keys differ: %q vs %q", key1, key2)
	}
	if len(params1) != 1 || len(params2) != 1 {
		t.Fatalf("unexpected param counts: %d, %d", len(params1), len(params2))
	}
}

func TestREQ001195_ParameterizedKey_DifferentStructure(t *testing.T) {
	stmt1 := &PS.Select{
		From: "t",
		Cols: []PS.Expr{&PS.StarExpr{}},
		Where: &PS.BinaryExpr{
			Op:    LX.T_EQ,
			Left:  &PS.Ident{Name: "a"},
			Right: &PS.NumberLiteral{Val: 1},
		},
	}
	stmt2 := &PS.Select{
		From: "t",
		Cols: []PS.Expr{&PS.StarExpr{}},
		Where: &PS.BinaryExpr{
			Op:    LX.T_EQ,
			Left:  &PS.Ident{Name: "b"},
			Right: &PS.NumberLiteral{Val: 1},
		},
	}
	norm1, _ := pl.NormalizeForMemo(stmt1)
	norm2, _ := pl.NormalizeForMemo(stmt2)
	key1 := pl.SerializeKey(norm1)
	key2 := pl.SerializeKey(norm2)
	if key1 == key2 {
		t.Errorf("different cols should produce different keys, both = %q", key1)
	}
}

func TestREQ001195_Roundtrip_StringLiteral(t *testing.T) {
	pred := &PS.BinaryExpr{
		Op:    LX.T_EQ,
		Left:  &PS.Ident{Name: "name"},
		Right: &PS.StringLiteral{Val: "hello"},
	}
	_, params := pl.NormalizeForMemo(&PS.Select{
		From:  "t",
		Cols:  []PS.Expr{&PS.StarExpr{}},
		Where: pred,
	})
	if len(params) != 1 {
		t.Fatalf("expected 1 param, got %d", len(params))
	}
	f := NewFilter(nil, pred)
	f.ReplaceLiterals(params)
	replaced := f.Predicate().(*PS.BinaryExpr)
	lit := replaced.Right.(*PS.StringLiteral)
	if lit.Val != "hello" {
		t.Errorf("replaced string = %q, want \"hello\"", lit.Val)
	}
}

func TestREQ001195_Roundtrip_BoolLiteral(t *testing.T) {
	pred := &PS.BinaryExpr{
		Op:    LX.T_EQ,
		Left:  &PS.Ident{Name: "flag"},
		Right: &PS.BoolLiteral{Val: true},
	}
	_, params := pl.NormalizeForMemo(&PS.Select{
		From:  "t",
		Cols:  []PS.Expr{&PS.StarExpr{}},
		Where: pred,
	})
	if len(params) != 1 {
		t.Fatalf("expected 1 param, got %d", len(params))
	}
	f := NewFilter(nil, pred)
	f.ReplaceLiterals(params)
	replaced := f.Predicate().(*PS.BinaryExpr)
	lit := replaced.Right.(*PS.BoolLiteral)
	if lit.Val != true {
		t.Errorf("replaced bool = %v, want true", lit.Val)
	}
}

func TestREQ001195_Roundtrip_NullLiteral(t *testing.T) {
	pred := &PS.BinaryExpr{
		Op:    LX.T_EQ,
		Left:  &PS.Ident{Name: "x"},
		Right: &PS.NullLiteral{},
	}
	_, params := pl.NormalizeForMemo(&PS.Select{
		From:  "t",
		Cols:  []PS.Expr{&PS.StarExpr{}},
		Where: pred,
	})
	// NullLiteral is NOT treated as a parameterizable literal by
	// isLiteral (only Number/Float/String/Bool). Both NormalizeForMemo
	// and ReplaceLiterals consistently skip it — 0 params is correct.
	if len(params) != 0 {
		t.Fatalf("expected 0 params (NullLiteral not parameterized), got %d", len(params))
	}
	f := NewFilter(nil, pred)
	f.ReplaceLiterals(nil)
	replaced := f.Predicate().(*PS.BinaryExpr)
	if _, ok := replaced.Right.(*PS.NullLiteral); !ok {
		t.Errorf("expected NullLiteral preserved after replace, got %T", replaced.Right)
	}
}

func TestREQ001195_Roundtrip_FloatLiteral(t *testing.T) {
	pred := &PS.BinaryExpr{
		Op:    LX.T_GT,
		Left:  &PS.Ident{Name: "score"},
		Right: &PS.FloatLiteral{Val: 3.14},
	}
	_, params := pl.NormalizeForMemo(&PS.Select{
		From:  "t",
		Cols:  []PS.Expr{&PS.StarExpr{}},
		Where: pred,
	})
	if len(params) != 1 {
		t.Fatalf("expected 1 param, got %d", len(params))
	}
	f := NewFilter(nil, pred)
	f.ReplaceLiterals(params)
	replaced := f.Predicate().(*PS.BinaryExpr)
	lit := replaced.Right.(*PS.FloatLiteral)
	if lit.Val != 3.14 {
		t.Errorf("replaced float = %v, want 3.14", lit.Val)
	}
}

func TestREQ001195_Roundtrip_LiteralOnLeft(t *testing.T) {
	pred := &PS.BinaryExpr{
		Op:    LX.T_EQ,
		Left:  &PS.NumberLiteral{Val: 5},
		Right: &PS.Ident{Name: "a"},
	}
	_, params := pl.NormalizeForMemo(&PS.Select{
		From:  "t",
		Cols:  []PS.Expr{&PS.StarExpr{}},
		Where: pred,
	})
	if len(params) != 1 {
		t.Fatalf("expected 1 param, got %d", len(params))
	}
	f := NewFilter(nil, pred)
	f.ReplaceLiterals(params)
	replaced := f.Predicate().(*PS.BinaryExpr)
	lit := replaced.Left.(*PS.NumberLiteral)
	if lit.Val != 5 {
		t.Errorf("left literal = %v, want 5", lit.Val)
	}
}

func TestREQ001195_Roundtrip_NonComparisonPreserved(t *testing.T) {
	pred := &PS.BinaryExpr{
		Op:    LX.T_PLUS,
		Left:  &PS.Ident{Name: "a"},
		Right: &PS.NumberLiteral{Val: 1},
	}
	_, params := pl.NormalizeForMemo(&PS.Select{
		From:  "t",
		Cols:  []PS.Expr{&PS.StarExpr{}},
		Where: pred,
	})
	if len(params) != 0 {
		t.Fatalf("expected 0 params for non-comparison op, got %d", len(params))
	}
	f := NewFilter(nil, pred)
	f.ReplaceLiterals(nil)
	replaced := f.Predicate().(*PS.BinaryExpr)
	if replaced.Right.(*PS.NumberLiteral).Val != 1 {
		t.Errorf("non-comparison literal should be preserved, got %v", replaced.Right)
	}
}

func TestREQ001195_Roundtrip_InExpr(t *testing.T) {
	pred := &PS.InExpr{
		Expr: &PS.Ident{Name: "a"},
		List: []PS.Expr{
			&PS.NumberLiteral{Val: 1},
			&PS.NumberLiteral{Val: 2},
			&PS.NumberLiteral{Val: 3},
		},
	}
	_, params := pl.NormalizeForMemo(&PS.Select{
		From:  "t",
		Cols:  []PS.Expr{&PS.StarExpr{}},
		Where: pred,
	})
	if len(params) != 0 {
		t.Fatalf("InExpr list items are not parameterized (no col=literal pattern), got %d params", len(params))
	}
	f := NewFilter(nil, pred)
	f.ReplaceLiterals(nil)
	replaced := f.Predicate().(*PS.InExpr)
	if len(replaced.List) != 3 {
		t.Fatalf("InExpr list len = %d, want 3", len(replaced.List))
	}
	for i, want := range []int64{1, 2, 3} {
		lit := replaced.List[i].(*PS.NumberLiteral)
		if lit.Val != want {
			t.Errorf("InExpr[%d] = %d, want %d", i, lit.Val, want)
		}
	}
}

func TestREQ001195_Roundtrip_BetweenExpr(t *testing.T) {
	pred := &PS.BetweenExpr{
		Expr: &PS.Ident{Name: "a"},
		Low:  &PS.NumberLiteral{Val: 10},
		High: &PS.NumberLiteral{Val: 20},
	}
	_, params := pl.NormalizeForMemo(&PS.Select{
		From:  "t",
		Cols:  []PS.Expr{&PS.StarExpr{}},
		Where: pred,
	})
	if len(params) != 0 {
		t.Fatalf("BETWEEN bounds are not parameterized (no col=literal pattern), got %d params", len(params))
	}
	f := NewFilter(nil, pred)
	f.ReplaceLiterals(nil)
	replaced := f.Predicate().(*PS.BetweenExpr)
	low := replaced.Low.(*PS.NumberLiteral)
	high := replaced.High.(*PS.NumberLiteral)
	if low.Val != 10 || high.Val != 20 {
		t.Errorf("BetweenExpr Low=%d High=%d, want 10 and 20", low.Val, high.Val)
	}
}

func TestREQ001195_Roundtrip_NestedInComparison(t *testing.T) {
	pred := &PS.BinaryExpr{
		Op: LX.T_AND,
		Left: &PS.BinaryExpr{
			Op:    LX.T_EQ,
			Left:  &PS.Ident{Name: "x"},
			Right: &PS.NumberLiteral{Val: 100},
		},
		Right: &PS.InExpr{
			Expr: &PS.Ident{Name: "y"},
			List: []PS.Expr{
				&PS.StringLiteral{Val: "a"},
				&PS.StringLiteral{Val: "b"},
			},
		},
	}
	_, params := pl.NormalizeForMemo(&PS.Select{
		From:  "t",
		Cols:  []PS.Expr{&PS.StarExpr{}},
		Where: pred,
	})
	if len(params) != 1 {
		t.Fatalf("expected 1 param (only x=100 is a BinaryExpr comparison), got %d", len(params))
	}
	f := NewFilter(nil, pred)
	f.ReplaceLiterals(params)
	replaced := f.Predicate().(*PS.BinaryExpr)
	left := replaced.Left.(*PS.BinaryExpr)
	if left.Right.(*PS.NumberLiteral).Val != 100 {
		t.Errorf("comparison literal lost, got %v", left.Right)
	}
	right := replaced.Right.(*PS.InExpr)
	if right.List[0].(*PS.StringLiteral).Val != "a" {
		t.Errorf("InExpr[0] lost, got %v", right.List[0])
	}
	if right.List[1].(*PS.StringLiteral).Val != "b" {
		t.Errorf("InExpr[1] lost, got %v", right.List[1])
	}
}

func TestREQ001195_Roundtrip_CountMatchParams(t *testing.T) {
	tests := []struct {
		name      string
		predicate PS.Expr
		wantCount int
	}{
		{"single_eq", &PS.BinaryExpr{Op: LX.T_EQ, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 1}}, 1},
		{"single_ne", &PS.BinaryExpr{Op: LX.T_NE, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 1}}, 1},
		{"single_lt", &PS.BinaryExpr{Op: LX.T_LT, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 1}}, 1},
		{"single_le", &PS.BinaryExpr{Op: LX.T_LE, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 1}}, 1},
		{"single_gt", &PS.BinaryExpr{Op: LX.T_GT, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 1}}, 1},
		{"single_ge", &PS.BinaryExpr{Op: LX.T_GE, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 1}}, 1},
		{"two_ands", &PS.BinaryExpr{
			Op: LX.T_AND,
			Left:  &PS.BinaryExpr{Op: LX.T_EQ, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 1}},
			Right: &PS.BinaryExpr{Op: LX.T_GT, Left: &PS.Ident{Name: "b"}, Right: &PS.NumberLiteral{Val: 10}},
		}, 2},
		{"three_ands", &PS.BinaryExpr{
			Op: LX.T_AND,
			Left: &PS.BinaryExpr{
				Op: LX.T_AND,
				Left:  &PS.BinaryExpr{Op: LX.T_EQ, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 1}},
				Right: &PS.BinaryExpr{Op: LX.T_LT, Left: &PS.Ident{Name: "b"}, Right: &PS.NumberLiteral{Val: 100}},
			},
			Right: &PS.BinaryExpr{Op: LX.T_GE, Left: &PS.Ident{Name: "c"}, Right: &PS.NumberLiteral{Val: 0}},
		}, 3},
		{"or_nested", &PS.BinaryExpr{
			Op: LX.T_OR,
			Left:  &PS.BinaryExpr{Op: LX.T_EQ, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 1}},
			Right: &PS.BinaryExpr{Op: LX.T_EQ, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 2}},
		}, 2},
		{"in_list", &PS.InExpr{Expr: &PS.Ident{Name: "a"}, List: []PS.Expr{
			&PS.NumberLiteral{Val: 1}, &PS.NumberLiteral{Val: 2}, &PS.NumberLiteral{Val: 3},
		}}, 0},
		{"between", &PS.BetweenExpr{Expr: &PS.Ident{Name: "a"}, Low: &PS.NumberLiteral{Val: 1}, High: &PS.NumberLiteral{Val: 10}}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, params := pl.NormalizeForMemo(&PS.Select{
				From:  "t",
				Cols:  []PS.Expr{&PS.StarExpr{}},
				Where: tt.predicate,
			})
			if len(params) != tt.wantCount {
				t.Fatalf("NormalizeForMemo params = %d, want %d", len(params), tt.wantCount)
			}
			f := NewFilter(nil, tt.predicate)
			litCount := f.CountComparisonLiterals()
			if litCount != tt.wantCount {
				t.Errorf("CountComparisonLiterals = %d, want %d", litCount, tt.wantCount)
			}
			f.ReplaceLiterals(params)
		})
	}
}