package PF

import (
	"testing"

	OC "github.com/cyw0ng95/razordata/internal/SQO/OC"
	LX "github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func TestRedundantPredicate_SameColumn(t *testing.T) {
	// WHERE a > 10 AND a > 5  ->  WHERE a > 10
	pred := &PS.BinaryExpr{
		Op:    LX.T_AND,
		Left:  &PS.BinaryExpr{Op: LX.T_GT, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 10}},
		Right: &PS.BinaryExpr{Op: LX.T_GT, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 5}},
	}
	scan := &scanOp{name: "t1", scanCols: []string{"a"}}
	filter := &noopOp{name: "filter", child: scan, pred: pred, hasPred: true}

	pass := &RedundantPredicateEliminationPass{}
	plan := &OC.Plan{Root: filter}
	result, err := pass.Apply(plan, &OC.Context{Factory: &mockFactory{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	f, ok := result.Root.(*noopOp)
	if !ok {
		t.Fatalf("expected root noopOp, got %T", result.Root)
	}
	predExpr, ok := f.pred.(PS.Expr)
	if !ok {
		t.Fatalf("expected predicate as PS.Expr, got %T", f.pred)
	}
	be, ok := predExpr.(*PS.BinaryExpr)
	if !ok {
		t.Fatalf("expected BinaryExpr, got %T", predExpr)
	}
	// The surviving conjunct should be a > 10
	if be.Op != LX.T_GT {
		t.Fatalf("expected op GT, got %v", be.Op)
	}
	ident, ok := be.Left.(*PS.Ident)
	if !ok || ident.Name != "a" {
		t.Fatalf("expected left Ident a, got %v", be.Left)
	}
	nl, ok := be.Right.(*PS.NumberLiteral)
	if !ok || nl.Val != 10 {
		t.Fatalf("expected right NumberLiteral 10, got %v", be.Right)
	}
}

func TestRedundantPredicate_Between(t *testing.T) {
	// WHERE a BETWEEN 1 AND 10 AND a > 3
	// a BETWEEN 1 AND 10 does NOT imply a > 3 (a could be 1 or 2)
	// So both conjuncts should remain.
	pred := &PS.BinaryExpr{
		Op:    LX.T_AND,
		Left:  &PS.BetweenExpr{Expr: &PS.Ident{Name: "a"}, Low: &PS.NumberLiteral{Val: 1}, High: &PS.NumberLiteral{Val: 10}},
		Right: &PS.BinaryExpr{Op: LX.T_GT, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 3}},
	}
	scan := &scanOp{name: "t1", scanCols: []string{"a"}}
	filter := &noopOp{name: "filter", child: scan, pred: pred, hasPred: true}

	pass := &RedundantPredicateEliminationPass{}
	plan := &OC.Plan{Root: filter}
	result, err := pass.Apply(plan, &OC.Context{Factory: &mockFactory{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	f, ok := result.Root.(*noopOp)
	if !ok {
		t.Fatalf("expected root noopOp, got %T", result.Root)
	}
	predExpr, ok := f.pred.(PS.Expr)
	if !ok {
		t.Fatalf("expected predicate as PS.Expr, got %T", f.pred)
	}
	// Should still be AND with both conjuncts
	be, ok := predExpr.(*PS.BinaryExpr)
	if !ok || be.Op != LX.T_AND {
		t.Fatalf("expected AND expression, got %T", predExpr)
	}
}

func TestRedundantPredicate_NoOp(t *testing.T) {
	// WHERE a > 5 AND b > 3  ->  unchanged (different columns)
	pred := &PS.BinaryExpr{
		Op:    LX.T_AND,
		Left:  &PS.BinaryExpr{Op: LX.T_GT, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 5}},
		Right: &PS.BinaryExpr{Op: LX.T_GT, Left: &PS.Ident{Name: "b"}, Right: &PS.NumberLiteral{Val: 3}},
	}
	scan := &scanOp{name: "t1", scanCols: []string{"a", "b"}}
	filter := &noopOp{name: "filter", child: scan, pred: pred, hasPred: true}

	pass := &RedundantPredicateEliminationPass{}
	plan := &OC.Plan{Root: filter}
	result, err := pass.Apply(plan, &OC.Context{Factory: &mockFactory{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	f, ok := result.Root.(*noopOp)
	if !ok {
		t.Fatalf("expected root noopOp, got %T", result.Root)
	}
	predExpr, ok := f.pred.(PS.Expr)
	if !ok {
		t.Fatalf("expected predicate as PS.Expr, got %T", f.pred)
	}
	// Should be unchanged AND
	be, ok := predExpr.(*PS.BinaryExpr)
	if !ok || be.Op != LX.T_AND {
		t.Fatalf("expected AND expression unchanged, got %T", predExpr)
	}
}

func TestRedundantPredicate_EqualityImplies(t *testing.T) {
	// WHERE a = 5 AND a > 3  ->  WHERE a = 5
	pred := &PS.BinaryExpr{
		Op:    LX.T_AND,
		Left:  &PS.BinaryExpr{Op: LX.T_EQ, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 5}},
		Right: &PS.BinaryExpr{Op: LX.T_GT, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 3}},
	}
	scan := &scanOp{name: "t1", scanCols: []string{"a"}}
	filter := &noopOp{name: "filter", child: scan, pred: pred, hasPred: true}

	pass := &RedundantPredicateEliminationPass{}
	plan := &OC.Plan{Root: filter}
	result, err := pass.Apply(plan, &OC.Context{Factory: &mockFactory{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	f, ok := result.Root.(*noopOp)
	if !ok {
		t.Fatalf("expected root noopOp, got %T", result.Root)
	}
	predExpr, ok := f.pred.(PS.Expr)
	if !ok {
		t.Fatalf("expected predicate as PS.Expr, got %T", f.pred)
	}
	be, ok := predExpr.(*PS.BinaryExpr)
	if !ok {
		t.Fatalf("expected BinaryExpr, got %T", predExpr)
	}
	if be.Op != LX.T_EQ {
		t.Fatalf("expected op EQ, got %v", be.Op)
	}
	nl, ok := be.Right.(*PS.NumberLiteral)
	if !ok || nl.Val != 5 {
		t.Fatalf("expected right NumberLiteral 5, got %v", be.Right)
	}
}

func TestRedundantPredicate_NilPlan(t *testing.T) {
	pass := &RedundantPredicateEliminationPass{}
	result, err := pass.Apply(nil, &OC.Context{Factory: &mockFactory{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Error("expected nil result for nil plan")
	}
}

func TestRedundantPredicate_GtImpliesGe(t *testing.T) {
	// WHERE a > 5 AND a >= 3  ->  WHERE a > 5 (gt implies ge)
	pred := &PS.BinaryExpr{
		Op:    LX.T_AND,
		Left:  &PS.BinaryExpr{Op: LX.T_GT, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 5}},
		Right: &PS.BinaryExpr{Op: LX.T_GE, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 3}},
	}
	scan := &scanOp{name: "t1", scanCols: []string{"a"}}
	filter := &noopOp{name: "filter", child: scan, pred: pred, hasPred: true}

	pass := &RedundantPredicateEliminationPass{}
	plan := &OC.Plan{Root: filter}
	result, err := pass.Apply(plan, &OC.Context{Factory: &mockFactory{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	f, ok := result.Root.(*noopOp)
	if !ok {
		t.Fatalf("expected root noopOp, got %T", result.Root)
	}
	predExpr, ok := f.pred.(PS.Expr)
	if !ok {
		t.Fatalf("expected predicate as PS.Expr, got %T", f.pred)
	}
	be, ok := predExpr.(*PS.BinaryExpr)
	if !ok || be.Op != LX.T_GT {
		t.Fatalf("expected surviving conjunct a > 5, got %v", predExpr)
	}
}

func TestRedundantPredicate_LtImpliesLe(t *testing.T) {
	// WHERE a < 5 AND a <= 10  ->  WHERE a < 5 (lt implies le)
	pred := &PS.BinaryExpr{
		Op:    LX.T_AND,
		Left:  &PS.BinaryExpr{Op: LX.T_LT, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 5}},
		Right: &PS.BinaryExpr{Op: LX.T_LE, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 10}},
	}
	scan := &scanOp{name: "t1", scanCols: []string{"a"}}
	filter := &noopOp{name: "filter", child: scan, pred: pred, hasPred: true}

	pass := &RedundantPredicateEliminationPass{}
	plan := &OC.Plan{Root: filter}
	result, err := pass.Apply(plan, &OC.Context{Factory: &mockFactory{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	f, ok := result.Root.(*noopOp)
	if !ok {
		t.Fatalf("expected root noopOp, got %T", result.Root)
	}
	predExpr, ok := f.pred.(PS.Expr)
	if !ok {
		t.Fatalf("expected predicate as PS.Expr, got %T", f.pred)
	}
	be, ok := predExpr.(*PS.BinaryExpr)
	if !ok || be.Op != LX.T_LT {
		t.Fatalf("expected surviving conjunct a < 5, got %v", predExpr)
	}
}

func TestRedundantPredicate_GeDoesNotImpliesGt(t *testing.T) {
	// WHERE a >= 5 AND a > 3  ->  BOTH stay (ge does NOT imply gt)
	// a >= 5 does imply a > 3 though, so a > 3 is removed
	// Result: WHERE a >= 5
	pred := &PS.BinaryExpr{
		Op:    LX.T_AND,
		Left:  &PS.BinaryExpr{Op: LX.T_GE, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 5}},
		Right: &PS.BinaryExpr{Op: LX.T_GT, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 3}},
	}
	scan := &scanOp{name: "t1", scanCols: []string{"a"}}
	filter := &noopOp{name: "filter", child: scan, pred: pred, hasPred: true}

	pass := &RedundantPredicateEliminationPass{}
	plan := &OC.Plan{Root: filter}
	result, err := pass.Apply(plan, &OC.Context{Factory: &mockFactory{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	f, ok := result.Root.(*noopOp)
	if !ok {
		t.Fatalf("expected root noopOp, got %T", result.Root)
	}
	predExpr, ok := f.pred.(PS.Expr)
	if !ok {
		t.Fatalf("expected predicate as PS.Expr, got %T", f.pred)
	}
	be, ok := predExpr.(*PS.BinaryExpr)
	if !ok || be.Op != LX.T_GE {
		t.Fatalf("expected surviving conjunct a >= 5, got %v", predExpr)
	}
}

func TestRedundantPredicate_LeDoesNotImpliesLt(t *testing.T) {
	// WHERE a <= 5 AND a < 10  ->  WHERE a <= 5 (le does NOT imply lt, but a <= 5 implies a < 10)
	pred := &PS.BinaryExpr{
		Op:    LX.T_AND,
		Left:  &PS.BinaryExpr{Op: LX.T_LE, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 5}},
		Right: &PS.BinaryExpr{Op: LX.T_LT, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 10}},
	}
	scan := &scanOp{name: "t1", scanCols: []string{"a"}}
	filter := &noopOp{name: "filter", child: scan, pred: pred, hasPred: true}

	pass := &RedundantPredicateEliminationPass{}
	plan := &OC.Plan{Root: filter}
	result, err := pass.Apply(plan, &OC.Context{Factory: &mockFactory{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	f, ok := result.Root.(*noopOp)
	if !ok {
		t.Fatalf("expected root noopOp, got %T", result.Root)
	}
	predExpr, ok := f.pred.(PS.Expr)
	if !ok {
		t.Fatalf("expected predicate as PS.Expr, got %T", f.pred)
	}
	be, ok := predExpr.(*PS.BinaryExpr)
	if !ok || be.Op != LX.T_LE {
		t.Fatalf("expected surviving conjunct a <= 5, got %v", predExpr)
	}
}

func TestRedundantPredicate_ChainMultiple(t *testing.T) {
	// WHERE a > 10 AND a > 5 AND a > 3  ->  WHERE a > 10
	pred := &PS.BinaryExpr{
		Op:    LX.T_AND,
		Left:  &PS.BinaryExpr{Op: LX.T_AND, Left: &PS.BinaryExpr{Op: LX.T_GT, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 10}}, Right: &PS.BinaryExpr{Op: LX.T_GT, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 5}}},
		Right: &PS.BinaryExpr{Op: LX.T_GT, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 3}},
	}
	scan := &scanOp{name: "t1", scanCols: []string{"a"}}
	filter := &noopOp{name: "filter", child: scan, pred: pred, hasPred: true}

	pass := &RedundantPredicateEliminationPass{}
	plan := &OC.Plan{Root: filter}
	result, err := pass.Apply(plan, &OC.Context{Factory: &mockFactory{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	f, ok := result.Root.(*noopOp)
	if !ok {
		t.Fatalf("expected root noopOp, got %T", result.Root)
	}
	predExpr, ok := f.pred.(PS.Expr)
	if !ok {
		t.Fatalf("expected predicate as PS.Expr, got %T", f.pred)
	}
	be, ok := predExpr.(*PS.BinaryExpr)
	if !ok || be.Op != LX.T_GT {
		t.Fatalf("expected surviving conjunct a > 10, got %v", predExpr)
	}
	nl, ok := be.Right.(*PS.NumberLiteral)
	if !ok || nl.Val != 10 {
		t.Fatalf("expected right NumberLiteral 10, got %v", be.Right)
	}
}

func TestRedundantPredicate_EqualityImpliesEq(t *testing.T) {
	// WHERE a = 5 AND a = 5  ->  WHERE a = 5
	pred := &PS.BinaryExpr{
		Op:    LX.T_AND,
		Left:  &PS.BinaryExpr{Op: LX.T_EQ, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 5}},
		Right: &PS.BinaryExpr{Op: LX.T_EQ, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 5}},
	}
	scan := &scanOp{name: "t1", scanCols: []string{"a"}}
	filter := &noopOp{name: "filter", child: scan, pred: pred, hasPred: true}

	pass := &RedundantPredicateEliminationPass{}
	plan := &OC.Plan{Root: filter}
	result, err := pass.Apply(plan, &OC.Context{Factory: &mockFactory{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	f, ok := result.Root.(*noopOp)
	if !ok {
		t.Fatalf("expected root noopOp, got %T", result.Root)
	}
	predExpr, ok := f.pred.(PS.Expr)
	if !ok {
		t.Fatalf("expected predicate as PS.Expr, got %T", f.pred)
	}
	be, ok := predExpr.(*PS.BinaryExpr)
	if !ok || be.Op != LX.T_EQ {
		t.Fatalf("expected surviving conjunct a = 5, got %v", predExpr)
	}
}

func TestRedundantPredicate_EqualityImpliesLt(t *testing.T) {
	// WHERE a = 5 AND a < 10  ->  WHERE a = 5
	pred := &PS.BinaryExpr{
		Op:    LX.T_AND,
		Left:  &PS.BinaryExpr{Op: LX.T_EQ, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 5}},
		Right: &PS.BinaryExpr{Op: LX.T_LT, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 10}},
	}
	scan := &scanOp{name: "t1", scanCols: []string{"a"}}
	filter := &noopOp{name: "filter", child: scan, pred: pred, hasPred: true}

	pass := &RedundantPredicateEliminationPass{}
	plan := &OC.Plan{Root: filter}
	result, err := pass.Apply(plan, &OC.Context{Factory: &mockFactory{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	f, ok := result.Root.(*noopOp)
	if !ok {
		t.Fatalf("expected root noopOp, got %T", result.Root)
	}
	predExpr, ok := f.pred.(PS.Expr)
	if !ok {
		t.Fatalf("expected predicate as PS.Expr, got %T", f.pred)
	}
	be, ok := predExpr.(*PS.BinaryExpr)
	if !ok || be.Op != LX.T_EQ {
		t.Fatalf("expected surviving conjunct a = 5, got %v", predExpr)
	}
}

func TestRedundantPredicate_EqualityImpliesLe(t *testing.T) {
	// WHERE a = 5 AND a <= 5  ->  WHERE a = 5
	pred := &PS.BinaryExpr{
		Op:    LX.T_AND,
		Left:  &PS.BinaryExpr{Op: LX.T_EQ, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 5}},
		Right: &PS.BinaryExpr{Op: LX.T_LE, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 5}},
	}
	scan := &scanOp{name: "t1", scanCols: []string{"a"}}
	filter := &noopOp{name: "filter", child: scan, pred: pred, hasPred: true}

	pass := &RedundantPredicateEliminationPass{}
	plan := &OC.Plan{Root: filter}
	result, err := pass.Apply(plan, &OC.Context{Factory: &mockFactory{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	f, ok := result.Root.(*noopOp)
	if !ok {
		t.Fatalf("expected root noopOp, got %T", result.Root)
	}
	predExpr, ok := f.pred.(PS.Expr)
	if !ok {
		t.Fatalf("expected predicate as PS.Expr, got %T", f.pred)
	}
	be, ok := predExpr.(*PS.BinaryExpr)
	if !ok || be.Op != LX.T_EQ {
		t.Fatalf("expected surviving conjunct a = 5, got %v", predExpr)
	}
}

func TestRedundantPredicate_BetweenImpliesGe(t *testing.T) {
	// WHERE a BETWEEN 5 AND 10 AND a >= 5  ->  WHERE a BETWEEN 5 AND 10
	pred := &PS.BinaryExpr{
		Op:    LX.T_AND,
		Left:  &PS.BetweenExpr{Expr: &PS.Ident{Name: "a"}, Low: &PS.NumberLiteral{Val: 5}, High: &PS.NumberLiteral{Val: 10}},
		Right: &PS.BinaryExpr{Op: LX.T_GE, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 5}},
	}
	scan := &scanOp{name: "t1", scanCols: []string{"a"}}
	filter := &noopOp{name: "filter", child: scan, pred: pred, hasPred: true}

	pass := &RedundantPredicateEliminationPass{}
	plan := &OC.Plan{Root: filter}
	result, err := pass.Apply(plan, &OC.Context{Factory: &mockFactory{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	f, ok := result.Root.(*noopOp)
	if !ok {
		t.Fatalf("expected root noopOp, got %T", result.Root)
	}
	predExpr, ok := f.pred.(PS.Expr)
	if !ok {
		t.Fatalf("expected predicate as PS.Expr, got %T", f.pred)
	}
	_, ok = predExpr.(*PS.BetweenExpr)
	if !ok {
		t.Fatalf("expected surviving conjunct BetweenExpr, got %T", predExpr)
	}
}

func TestRedundantPredicate_BetweenImpliesLe(t *testing.T) {
	// WHERE a BETWEEN 1 AND 10 AND a <= 10  ->  WHERE a BETWEEN 1 AND 10
	pred := &PS.BinaryExpr{
		Op:    LX.T_AND,
		Left:  &PS.BetweenExpr{Expr: &PS.Ident{Name: "a"}, Low: &PS.NumberLiteral{Val: 1}, High: &PS.NumberLiteral{Val: 10}},
		Right: &PS.BinaryExpr{Op: LX.T_LE, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 10}},
	}
	scan := &scanOp{name: "t1", scanCols: []string{"a"}}
	filter := &noopOp{name: "filter", child: scan, pred: pred, hasPred: true}

	pass := &RedundantPredicateEliminationPass{}
	plan := &OC.Plan{Root: filter}
	result, err := pass.Apply(plan, &OC.Context{Factory: &mockFactory{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	f, ok := result.Root.(*noopOp)
	if !ok {
		t.Fatalf("expected root noopOp, got %T", result.Root)
	}
	predExpr, ok := f.pred.(PS.Expr)
	if !ok {
		t.Fatalf("expected predicate as PS.Expr, got %T", f.pred)
	}
	_, ok = predExpr.(*PS.BetweenExpr)
	if !ok {
		t.Fatalf("expected surviving conjunct BetweenExpr, got %T", predExpr)
	}
}

func TestRedundantPredicate_CaseInsensitive(t *testing.T) {
	// WHERE A > 10 AND a > 5  ->  WHERE A > 10 (case insensitive)
	pred := &PS.BinaryExpr{
		Op:    LX.T_AND,
		Left:  &PS.BinaryExpr{Op: LX.T_GT, Left: &PS.Ident{Name: "A"}, Right: &PS.NumberLiteral{Val: 10}},
		Right: &PS.BinaryExpr{Op: LX.T_GT, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 5}},
	}
	scan := &scanOp{name: "t1", scanCols: []string{"a"}}
	filter := &noopOp{name: "filter", child: scan, pred: pred, hasPred: true}

	pass := &RedundantPredicateEliminationPass{}
	plan := &OC.Plan{Root: filter}
	result, err := pass.Apply(plan, &OC.Context{Factory: &mockFactory{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	f, ok := result.Root.(*noopOp)
	if !ok {
		t.Fatalf("expected root noopOp, got %T", result.Root)
	}
	predExpr, ok := f.pred.(PS.Expr)
	if !ok {
		t.Fatalf("expected predicate as PS.Expr, got %T", f.pred)
	}
	be, ok := predExpr.(*PS.BinaryExpr)
	if !ok || be.Op != LX.T_GT {
		t.Fatalf("expected surviving conjunct A > 10, got %v", predExpr)
	}
}

func TestRedundantPredicate_NonConjunction(t *testing.T) {
	// WHERE a > 5 (single predicate, no AND)  ->  unchanged
	pred := &PS.BinaryExpr{Op: LX.T_GT, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 5}}
	scan := &scanOp{name: "t1", scanCols: []string{"a"}}
	filter := &noopOp{name: "filter", child: scan, pred: pred, hasPred: true}

	pass := &RedundantPredicateEliminationPass{}
	plan := &OC.Plan{Root: filter}
	result, err := pass.Apply(plan, &OC.Context{Factory: &mockFactory{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	f, ok := result.Root.(*noopOp)
	if !ok {
		t.Fatalf("expected root noopOp, got %T", result.Root)
	}
	// Predicate should be unchanged
	if f.pred != pred {
		t.Error("expected predicate to be unchanged for non-conjunction")
	}
}

func TestRedundantPredicate_NoFactory(t *testing.T) {
	pass := &RedundantPredicateEliminationPass{}
	_, err := pass.Apply(&OC.Plan{Root: &noopOp{}}, nil)
	if err == nil {
		t.Error("expected error for nil factory")
	}
}
