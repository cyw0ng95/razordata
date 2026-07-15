package PF

import (
	"testing"

	OC "github.com/cyw0ng95/razordata/internal/SQO/OC"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func TestImplicitCast_IntText(t *testing.T) {
	// WHERE a = '42' should become WHERE a = 42
	pred := &PS.BinaryExpr{
		Op:    0, // T_EQ (TokenType zero is fine for our structural test)
		Left:  &PS.Ident{Name: "a"},
		Right: &PS.StringLiteral{Val: "42"},
	}
	scan := &noopOp{name: "seqscan", pred: pred, hasPred: true}
	pass := &ImplicitCastEliminationPass{}
	plan, err := pass.Apply(&OC.Plan{Root: scan}, &OC.Context{})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	if plan == nil || plan.Root == nil {
		t.Fatal("expected non-nil plan")
	}
	filter, ok := plan.Root.(*noopOp)
	if !ok {
		t.Fatalf("root = %T, want *noopOp", plan.Root)
	}
	p, ok := filter.pred.(*PS.BinaryExpr)
	if !ok {
		t.Fatalf("predicate = %T, want *PS.BinaryExpr", filter.pred)
	}
	_, ok = p.Right.(*PS.NumberLiteral)
	if !ok {
		t.Errorf("Right = %T, want *PS.NumberLiteral", p.Right)
	}
	if nl, ok := p.Right.(*PS.NumberLiteral); ok && nl.Val != 42 {
		t.Errorf("NumberLiteral.Val = %d, want 42", nl.Val)
	}
}

func TestImplicitCast_INList(t *testing.T) {
	// WHERE a IN ('1', '2', '3') should become WHERE a IN (1, 2, 3)
	pred := &PS.InExpr{
		Expr: &PS.Ident{Name: "a"},
		List: []PS.Expr{
			&PS.StringLiteral{Val: "1"},
			&PS.StringLiteral{Val: "2"},
			&PS.StringLiteral{Val: "3"},
		},
	}
	scan := &noopOp{name: "seqscan", pred: pred, hasPred: true}
	pass := &ImplicitCastEliminationPass{}
	plan, err := pass.Apply(&OC.Plan{Root: scan}, &OC.Context{})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	if plan == nil || plan.Root == nil {
		t.Fatal("expected non-nil plan")
	}
	filter, ok := plan.Root.(*noopOp)
	if !ok {
		t.Fatalf("root = %T, want *noopOp", plan.Root)
	}
	ie, ok := filter.pred.(*PS.InExpr)
	if !ok {
		t.Fatalf("predicate = %T, want *PS.InExpr", filter.pred)
	}
	if len(ie.List) != 3 {
		t.Fatalf("IN list length = %d, want 3", len(ie.List))
	}
	for i, want := range []int64{1, 2, 3} {
		nl, ok := ie.List[i].(*PS.NumberLiteral)
		if !ok {
			t.Errorf("IN list[%d] = %T, want *PS.NumberLiteral", i, ie.List[i])
			continue
		}
		if nl.Val != want {
			t.Errorf("IN list[%d].Val = %d, want %d", i, nl.Val, want)
		}
	}
}

func TestImplicitCast_NoColumnType_NoOp(t *testing.T) {
	// WHERE a = 'hello' should remain unchanged (not numeric)
	pred := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "a"},
		Right: &PS.StringLiteral{Val: "hello"},
	}
	scan := &noopOp{name: "seqscan", pred: pred, hasPred: true}
	pass := &ImplicitCastEliminationPass{}
	plan, err := pass.Apply(&OC.Plan{Root: scan}, &OC.Context{})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	if plan == nil || plan.Root == nil {
		t.Fatal("expected non-nil plan")
	}
	filter, ok := plan.Root.(*noopOp)
	if !ok {
		t.Fatalf("root = %T, want *noopOp", plan.Root)
	}
	p, ok := filter.pred.(*PS.BinaryExpr)
	if !ok {
		t.Fatalf("predicate = %T, want *PS.BinaryExpr", filter.pred)
	}
	_, ok = p.Right.(*PS.StringLiteral)
	if !ok {
		t.Errorf("Right = %T, want *PS.StringLiteral (unchanged)", p.Right)
	}
}

func TestImplicitCast_NilPlan(t *testing.T) {
	pass := &ImplicitCastEliminationPass{}
	result, err := pass.Apply(nil, &OC.Context{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Error("expected nil result for nil plan")
	}
}

func TestImplicitCast_NilRoot(t *testing.T) {
	pass := &ImplicitCastEliminationPass{}
	plan := &OC.Plan{Root: nil}
	result, err := pass.Apply(plan, &OC.Context{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Root != nil {
		t.Error("expected nil root")
	}
}

func TestImplicitCast_NegativeNumericString(t *testing.T) {
	// WHERE a = '-123' should become WHERE a = -123
	pred := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "a"},
		Right: &PS.StringLiteral{Val: "-123"},
	}
	scan := &noopOp{name: "seqscan", pred: pred, hasPred: true}
	pass := &ImplicitCastEliminationPass{}
	plan, err := pass.Apply(&OC.Plan{Root: scan}, &OC.Context{})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	filter, ok := plan.Root.(*noopOp)
	if !ok {
		t.Fatalf("root = %T, want *noopOp", plan.Root)
	}
	p, ok := filter.pred.(*PS.BinaryExpr)
	if !ok {
		t.Fatalf("predicate = %T, want *PS.BinaryExpr", filter.pred)
	}
	nl, ok := p.Right.(*PS.NumberLiteral)
	if !ok {
		t.Fatalf("Right = %T, want *PS.NumberLiteral", p.Right)
	}
	if nl.Val != -123 {
		t.Errorf("NumberLiteral.Val = %d, want -123", nl.Val)
	}
}

func TestImplicitCast_MixedINList(t *testing.T) {
	// WHERE a IN ('1', 'hello', '3') — only numeric strings converted
	pred := &PS.InExpr{
		Expr: &PS.Ident{Name: "a"},
		List: []PS.Expr{
			&PS.StringLiteral{Val: "1"},
			&PS.StringLiteral{Val: "hello"},
			&PS.StringLiteral{Val: "3"},
		},
	}
	scan := &noopOp{name: "seqscan", pred: pred, hasPred: true}
	pass := &ImplicitCastEliminationPass{}
	plan, err := pass.Apply(&OC.Plan{Root: scan}, &OC.Context{})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	filter, ok := plan.Root.(*noopOp)
	if !ok {
		t.Fatalf("root = %T, want *noopOp", plan.Root)
	}
	ie, ok := filter.pred.(*PS.InExpr)
	if !ok {
		t.Fatalf("predicate = %T, want *PS.InExpr", filter.pred)
	}
	// Item 0: "1" -> NumberLiteral(1)
	if _, ok := ie.List[0].(*PS.NumberLiteral); !ok {
		t.Errorf("IN list[0] = %T, want *PS.NumberLiteral", ie.List[0])
	}
	// Item 1: "hello" stays StringLiteral
	if _, ok := ie.List[1].(*PS.StringLiteral); !ok {
		t.Errorf("IN list[1] = %T, want *PS.StringLiteral", ie.List[1])
	}
	// Item 2: "3" -> NumberLiteral(3)
	if _, ok := ie.List[2].(*PS.NumberLiteral); !ok {
		t.Errorf("IN list[2] = %T, want *PS.NumberLiteral", ie.List[2])
	}
}

func TestImplicitCast_ZeroString(t *testing.T) {
	// WHERE a = '0' should become WHERE a = 0
	pred := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "a"},
		Right: &PS.StringLiteral{Val: "0"},
	}
	scan := &noopOp{name: "seqscan", pred: pred, hasPred: true}
	pass := &ImplicitCastEliminationPass{}
	plan, err := pass.Apply(&OC.Plan{Root: scan}, &OC.Context{})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	filter, ok := plan.Root.(*noopOp)
	if !ok {
		t.Fatalf("root = %T, want *noopOp", plan.Root)
	}
	p, ok := filter.pred.(*PS.BinaryExpr)
	if !ok {
		t.Fatalf("predicate = %T, want *PS.BinaryExpr", filter.pred)
	}
	nl, ok := p.Right.(*PS.NumberLiteral)
	if !ok {
		t.Fatalf("Right = %T, want *PS.NumberLiteral", p.Right)
	}
	if nl.Val != 0 {
		t.Errorf("NumberLiteral.Val = %d, want 0", nl.Val)
	}
}

func TestLooksLikeNumber(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"42", true},
		{"0", true},
		{"-123", true},
		{"hello", false},
		{"", false},
		{"-0", true},
		{"-", false},
		{"12.34", false},
		{"1e5", false},
		{"007", true},
	}
	for _, tc := range tests {
		got := looksLikeNumber(tc.input)
		if got != tc.want {
			t.Errorf("looksLikeNumber(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestImplicitCast_NoPredicate_Stays(t *testing.T) {
	// A filter without a predicate should pass through unchanged
	scan := &noopOp{name: "seqscan"}
	pass := &ImplicitCastEliminationPass{}
	plan, err := pass.Apply(&OC.Plan{Root: scan}, &OC.Context{})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	if plan.Root != scan {
		t.Error("expected root to be unchanged")
	}
}

func TestImplicitCast_NestedExpressions(t *testing.T) {
	// WHERE a = '42' AND b = '99' — both sides should be rewritten
	rightPred := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "b"},
		Right: &PS.StringLiteral{Val: "99"},
	}
	leftPred := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "a"},
		Right: &PS.StringLiteral{Val: "42"},
	}
	pred := &PS.BinaryExpr{
		Op:    0, // T_AND
		Left:  leftPred,
		Right: rightPred,
	}
	scan := &noopOp{name: "seqscan", pred: pred, hasPred: true}
	pass := &ImplicitCastEliminationPass{}
	plan, err := pass.Apply(&OC.Plan{Root: scan}, &OC.Context{})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	filter, ok := plan.Root.(*noopOp)
	if !ok {
		t.Fatalf("root = %T, want *noopOp", plan.Root)
	}
	andPred, ok := filter.pred.(*PS.BinaryExpr)
	if !ok {
		t.Fatalf("predicate = %T, want *PS.BinaryExpr", filter.pred)
	}
	if _, ok := andPred.Left.(*PS.BinaryExpr); !ok {
		t.Fatalf("Left = %T, want *PS.BinaryExpr", andPred.Left)
	}
	if _, ok := andPred.Right.(*PS.BinaryExpr); !ok {
		t.Fatalf("Right = %T, want *PS.BinaryExpr", andPred.Right)
	}
}

func TestImplicitCast_FloatStringUnchanged(t *testing.T) {
	// WHERE a = '3.14' stays StringLiteral (not purely integer)
	pred := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "a"},
		Right: &PS.StringLiteral{Val: "3.14"},
	}
	scan := &noopOp{name: "seqscan", pred: pred, hasPred: true}
	pass := &ImplicitCastEliminationPass{}
	plan, err := pass.Apply(&OC.Plan{Root: scan}, &OC.Context{})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	filter, ok := plan.Root.(*noopOp)
	if !ok {
		t.Fatalf("root = %T, want *noopOp", plan.Root)
	}
	p, ok := filter.pred.(*PS.BinaryExpr)
	if !ok {
		t.Fatalf("predicate = %T, want *PS.BinaryExpr", filter.pred)
	}
	if _, ok := p.Right.(*PS.StringLiteral); !ok {
		t.Errorf("Right = %T, want *PS.StringLiteral (unchanged)", p.Right)
	}
}

func TestImplicitCast_StringWithLeadingZeros(t *testing.T) {
	// WHERE a = '007' should become WHERE a = 7 (strconv.ParseInt handles it)
	pred := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "a"},
		Right: &PS.StringLiteral{Val: "007"},
	}
	scan := &noopOp{name: "seqscan", pred: pred, hasPred: true}
	pass := &ImplicitCastEliminationPass{}
	plan, err := pass.Apply(&OC.Plan{Root: scan}, &OC.Context{})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	filter, ok := plan.Root.(*noopOp)
	if !ok {
		t.Fatalf("root = %T, want *noopOp", plan.Root)
	}
	p, ok := filter.pred.(*PS.BinaryExpr)
	if !ok {
		t.Fatalf("predicate = %T, want *PS.BinaryExpr", filter.pred)
	}
	nl, ok := p.Right.(*PS.NumberLiteral)
	if !ok {
		t.Fatalf("Right = %T, want *PS.NumberLiteral", p.Right)
	}
	if nl.Val != 7 {
		t.Errorf("NumberLiteral.Val = %d, want 7", nl.Val)
	}
}

func TestImplicitCast_EmptyINList(t *testing.T) {
	// WHERE a IN () — empty list, no-op
	pred := &PS.InExpr{
		Expr: &PS.Ident{Name: "a"},
		List: []PS.Expr{},
	}
	scan := &noopOp{name: "seqscan", pred: pred, hasPred: true}
	pass := &ImplicitCastEliminationPass{}
	plan, err := pass.Apply(&OC.Plan{Root: scan}, &OC.Context{})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	filter, ok := plan.Root.(*noopOp)
	if !ok {
		t.Fatalf("root = %T, want *noopOp", plan.Root)
	}
	ie, ok := filter.pred.(*PS.InExpr)
	if !ok {
		t.Fatalf("predicate = %T, want *PS.InExpr", filter.pred)
	}
	if len(ie.List) != 0 {
		t.Errorf("IN list length = %d, want 0", len(ie.List))
	}
}

func TestImplicitCast_StringsThatLookLikeNumbers(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"42", true},
		{"0", true},
		{"-1", true},
		{"-0", true},
		{"hello", false},
		{"", false},
		{"-", false},
		{"12.34", false},
		{"1e5", false},
		{"007", true},
	}
	for _, tc := range tests {
		got := looksLikeNumber(tc.input)
		if got != tc.want {
			t.Errorf("looksLikeNumber(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestImplicitCast_NotEqUnchanged(t *testing.T) {
	// WHERE a <> '42' — string literal not equal, should still be rewritten
	// (the pass operates on all StringLiterals regardless of operator)
	pred := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "a"},
		Right: &PS.StringLiteral{Val: "42"},
	}
	scan := &noopOp{name: "seqscan", pred: pred, hasPred: true}
	pass := &ImplicitCastEliminationPass{}
	plan, err := pass.Apply(&OC.Plan{Root: scan}, &OC.Context{})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	filter, ok := plan.Root.(*noopOp)
	if !ok {
		t.Fatalf("root = %T, want *noopOp", plan.Root)
	}
	p, ok := filter.pred.(*PS.BinaryExpr)
	if !ok {
		t.Fatalf("predicate = %T, want *PS.BinaryExpr", filter.pred)
	}
	if _, ok := p.Right.(*PS.NumberLiteral); !ok {
		t.Errorf("Right = %T, want *PS.NumberLiteral", p.Right)
	}
}

func TestImplicitCast_QualifiedColumnName(t *testing.T) {
	// WHERE t.a = '42' — qualified column name, still rewrites value
	pred := &PS.BinaryExpr{
		Left:  &PS.QualifiedName{Table: "t", Name: "a"},
		Right: &PS.StringLiteral{Val: "42"},
	}
	scan := &noopOp{name: "seqscan", pred: pred, hasPred: true}
	pass := &ImplicitCastEliminationPass{}
	plan, err := pass.Apply(&OC.Plan{Root: scan}, &OC.Context{})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	filter, ok := plan.Root.(*noopOp)
	if !ok {
		t.Fatalf("root = %T, want *noopOp", plan.Root)
	}
	p, ok := filter.pred.(*PS.BinaryExpr)
	if !ok {
		t.Fatalf("predicate = %T, want *PS.BinaryExpr", filter.pred)
	}
	nl, ok := p.Right.(*PS.NumberLiteral)
	if !ok {
		t.Fatalf("Right = %T, want *PS.NumberLiteral", p.Right)
	}
	if nl.Val != 42 {
		t.Errorf("NumberLiteral.Val = %d, want 42", nl.Val)
	}
}

func TestImplicitCast_NilContext(t *testing.T) {
	// Nil context should not panic
	pred := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "a"},
		Right: &PS.StringLiteral{Val: "42"},
	}
	scan := &noopOp{name: "seqscan", pred: pred, hasPred: true}
	pass := &ImplicitCastEliminationPass{}
	plan, err := pass.Apply(&OC.Plan{Root: scan}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan == nil || plan.Root == nil {
		t.Fatal("expected non-nil plan")
	}
}

func TestImplicitCast_DigitsInLongString(t *testing.T) {
	// WHERE a = 'abc123' should stay StringLiteral (not purely numeric)
	pred := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "a"},
		Right: &PS.StringLiteral{Val: "abc123"},
	}
	scan := &noopOp{name: "seqscan", pred: pred, hasPred: true}
	pass := &ImplicitCastEliminationPass{}
	plan, err := pass.Apply(&OC.Plan{Root: scan}, &OC.Context{})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	filter, ok := plan.Root.(*noopOp)
	if !ok {
		t.Fatalf("root = %T, want *noopOp", plan.Root)
	}
	p, ok := filter.pred.(*PS.BinaryExpr)
	if !ok {
		t.Fatalf("predicate = %T, want *PS.BinaryExpr", filter.pred)
	}
	if _, ok := p.Right.(*PS.StringLiteral); !ok {
		t.Errorf("Right = %T, want *PS.StringLiteral (unchanged)", p.Right)
	}
}

func TestImplicitCast_ConcatExpression(t *testing.T) {
	// WHERE a = CONCAT('42', 'x') — the StringLiterals inside
	// CONCAT should also be rewritten to NumberLiterals where applicable
	pred := &PS.BinaryExpr{
		Left: &PS.Ident{Name: "a"},
		Right: &PS.FunctionCall{
			Name: "CONCAT",
			Args: []PS.Expr{
				&PS.StringLiteral{Val: "42"},
				&PS.StringLiteral{Val: "x"},
			},
		},
	}
	scan := &noopOp{name: "seqscan", pred: pred, hasPred: true}
	pass := &ImplicitCastEliminationPass{}
	plan, err := pass.Apply(&OC.Plan{Root: scan}, &OC.Context{})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	filter, ok := plan.Root.(*noopOp)
	if !ok {
		t.Fatalf("root = %T, want *noopOp", plan.Root)
	}
	p, ok := filter.pred.(*PS.BinaryExpr)
	if !ok {
		t.Fatalf("predicate = %T, want *PS.BinaryExpr", filter.pred)
	}
	call, ok := p.Right.(*PS.FunctionCall)
	if !ok {
		t.Fatalf("Right = %T, want *PS.FunctionCall", p.Right)
	}
	// First arg "42" should become NumberLiteral
	if _, ok := call.Args[0].(*PS.NumberLiteral); !ok {
		t.Errorf("Args[0] = %T, want *PS.NumberLiteral", call.Args[0])
	}
	// Second arg "x" should stay StringLiteral
	if _, ok := call.Args[1].(*PS.StringLiteral); !ok {
		t.Errorf("Args[1] = %T, want *PS.StringLiteral", call.Args[1])
	}
}

func BenchmarkImplicitCastElimination(b *testing.B) {
	pred := &PS.BinaryExpr{
		Left: &PS.Ident{Name: "a"},
		Right: &PS.FunctionCall{
			Name: "CONCAT",
			Args: []PS.Expr{
				&PS.StringLiteral{Val: "42"},
				&PS.StringLiteral{Val: "x"},
			},
		},
	}
	scan := &noopOp{name: "seqscan", pred: pred, hasPred: true}
	pass := &ImplicitCastEliminationPass{}
	plan := &OC.Plan{Root: scan}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = pass.Apply(plan, &OC.Context{})
	}
}
