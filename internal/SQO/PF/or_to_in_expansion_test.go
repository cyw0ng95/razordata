package PF

import (
	"testing"

	OC "github.com/cyw0ng95/razordata/internal/SQO/OC"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	LX "github.com/cyw0ng95/razordata/internal/SQF/LX"
)

func buildFilterTree(pred interface{}, child pl.Operator) pl.Operator {
	return &noopOp{name: "filter", child: child, pred: pred, hasPred: true}
}

func buildScan(name string) *scanOp {
	return &scanOp{name: name, scanCols: []string{"a", "b", "c"}}
}

func TestOrToIn_Simple(t *testing.T) {
	pass := &OrToInExpansionPass{}
	scan := buildScan("t1")
	pred := &PS.BinaryExpr{
		Op:    LX.T_OR,
		Left:  &PS.BinaryExpr{Op: LX.T_EQ, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 1}},
		Right: &PS.BinaryExpr{Op: LX.T_EQ, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 2}},
	}
	filter := buildFilterTree(pred, scan)

	plan, err := pass.Apply(&OC.Plan{Root: filter}, &OC.Context{Factory: &mockFactory{}})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	if plan == nil || plan.Root == nil {
		t.Fatal("nil plan")
	}
	f, ok := plan.Root.(*noopOp)
	if !ok {
		t.Fatalf("root = %T, want *noopOp", plan.Root)
	}
	if !f.hasPred {
		t.Fatal("filter should still have predicate")
	}
	inExpr, ok := f.pred.(*PS.InExpr)
	if !ok {
		t.Fatalf("predicate = %T, want *PS.InExpr", f.pred)
	}
	if inExpr.Expr.(*PS.Ident).Name != "a" {
		t.Errorf("InExpr column = %q, want %q", inExpr.Expr.(*PS.Ident).Name, "a")
	}
	if len(inExpr.List) != 2 {
		t.Fatalf("InExpr.List length = %d, want 2", len(inExpr.List))
	}
	if nl, ok := inExpr.List[0].(*PS.NumberLiteral); !ok || nl.Val != 1 {
		t.Errorf("InExpr.List[0] = %v, want NumberLiteral{Val: 1}", inExpr.List[0])
	}
	if nl, ok := inExpr.List[1].(*PS.NumberLiteral); !ok || nl.Val != 2 {
		t.Errorf("InExpr.List[1] = %v, want NumberLiteral{Val: 2}", inExpr.List[1])
	}
}

func TestOrToIn_Nested(t *testing.T) {
	pass := &OrToInExpansionPass{}
	scan := buildScan("t1")
	// (a = 1 OR a = 2) OR a = 3
	inner := &PS.BinaryExpr{
		Op:    LX.T_OR,
		Left:  &PS.BinaryExpr{Op: LX.T_EQ, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 1}},
		Right: &PS.BinaryExpr{Op: LX.T_EQ, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 2}},
	}
	pred := &PS.BinaryExpr{
		Op:    LX.T_OR,
		Left:  inner,
		Right: &PS.BinaryExpr{Op: LX.T_EQ, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 3}},
	}
	filter := buildFilterTree(pred, scan)

	plan, err := pass.Apply(&OC.Plan{Root: filter}, &OC.Context{Factory: &mockFactory{}})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	if plan == nil || plan.Root == nil {
		t.Fatal("nil plan")
	}
	f, ok := plan.Root.(*noopOp)
	if !ok {
		t.Fatalf("root = %T, want *noopOp", plan.Root)
	}
	inExpr, ok := f.pred.(*PS.InExpr)
	if !ok {
		t.Fatalf("predicate = %T, want *PS.InExpr", f.pred)
	}
	if len(inExpr.List) != 3 {
		t.Fatalf("InExpr.List length = %d, want 3", len(inExpr.List))
	}
	for i, want := range []int64{1, 2, 3} {
		if nl, ok := inExpr.List[i].(*PS.NumberLiteral); !ok || nl.Val != want {
			t.Errorf("InExpr.List[%d] = %v, want NumberLiteral{Val: %d}", i, inExpr.List[i], want)
		}
	}
}

func TestOrToIn_DifferentColumns_NoOp(t *testing.T) {
	pass := &OrToInExpansionPass{}
	scan := buildScan("t1")
	// a = 1 OR b = 2
	pred := &PS.BinaryExpr{
		Op:    LX.T_OR,
		Left:  &PS.BinaryExpr{Op: LX.T_EQ, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 1}},
		Right: &PS.BinaryExpr{Op: LX.T_EQ, Left: &PS.Ident{Name: "b"}, Right: &PS.NumberLiteral{Val: 2}},
	}
	filter := buildFilterTree(pred, scan)

	plan, err := pass.Apply(&OC.Plan{Root: filter}, &OC.Context{Factory: &mockFactory{}})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	if plan == nil || plan.Root == nil {
		t.Fatal("nil plan")
	}
	f, ok := plan.Root.(*noopOp)
	if !ok {
		t.Fatalf("root = %T, want *noopOp", plan.Root)
	}
	// Should be unchanged — still a BinaryExpr (OR), not an InExpr
	if _, ok := f.pred.(*PS.InExpr); ok {
		t.Error("different columns should not be converted to IN")
	}
}

func TestOrToIn_SingleDisjunct_NoOp(t *testing.T) {
	pass := &OrToInExpansionPass{}
	scan := buildScan("t1")
	// a = 1 (single disjunct, no OR chain)
	pred := &PS.BinaryExpr{Op: LX.T_EQ, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 1}}
	filter := buildFilterTree(pred, scan)

	plan, err := pass.Apply(&OC.Plan{Root: filter}, &OC.Context{Factory: &mockFactory{}})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	if plan == nil || plan.Root == nil {
		t.Fatal("nil plan")
	}
	f, ok := plan.Root.(*noopOp)
	if !ok {
		t.Fatalf("root = %T, want *noopOp", plan.Root)
	}
	// Should be unchanged — still a BinaryExpr, not an InExpr
	if _, ok := f.pred.(*PS.InExpr); ok {
		t.Error("single disjunct should not be converted to IN")
	}
}

func TestOrToIn_NilPlan(t *testing.T) {
	pass := &OrToInExpansionPass{}
	result, err := pass.Apply(nil, &OC.Context{Factory: &mockFactory{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Error("expected nil result for nil plan")
	}
}

func TestOrToIn_NilFactory(t *testing.T) {
	pass := &OrToInExpansionPass{}
	scan := buildScan("t1")
	pred := &PS.BinaryExpr{
		Op:    LX.T_OR,
		Left:  &PS.BinaryExpr{Op: LX.T_EQ, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 1}},
		Right: &PS.BinaryExpr{Op: LX.T_EQ, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 2}},
	}
	filter := buildFilterTree(pred, scan)

	_, err := pass.Apply(&OC.Plan{Root: filter}, &OC.Context{Factory: nil})
	if err == nil {
		t.Error("expected error for nil factory")
	}
}

func TestOrToIn_ReverseEquality(t *testing.T) {
	pass := &OrToInExpansionPass{}
	scan := buildScan("t1")
	// 1 = a OR 2 = a (literal on left side)
	pred := &PS.BinaryExpr{
		Op:    LX.T_OR,
		Left:  &PS.BinaryExpr{Op: LX.T_EQ, Left: &PS.NumberLiteral{Val: 1}, Right: &PS.Ident{Name: "a"}},
		Right: &PS.BinaryExpr{Op: LX.T_EQ, Left: &PS.NumberLiteral{Val: 2}, Right: &PS.Ident{Name: "a"}},
	}
	filter := buildFilterTree(pred, scan)

	plan, err := pass.Apply(&OC.Plan{Root: filter}, &OC.Context{Factory: &mockFactory{}})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	if plan == nil || plan.Root == nil {
		t.Fatal("nil plan")
	}
	f, ok := plan.Root.(*noopOp)
	if !ok {
		t.Fatalf("root = %T, want *noopOp", plan.Root)
	}
	inExpr, ok := f.pred.(*PS.InExpr)
	if !ok {
		t.Fatalf("predicate = %T, want *PS.InExpr", f.pred)
	}
	if len(inExpr.List) != 2 {
		t.Fatalf("InExpr.List length = %d, want 2", len(inExpr.List))
	}
}

func TestOrToIn_NonEqualityDisjunct_NoOp(t *testing.T) {
	pass := &OrToInExpansionPass{}
	scan := buildScan("t1")
	// a = 1 OR b > 2 (second disjunct is not equality)
	pred := &PS.BinaryExpr{
		Op:    LX.T_OR,
		Left:  &PS.BinaryExpr{Op: LX.T_EQ, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 1}},
		Right: &PS.BinaryExpr{Op: LX.T_GT, Left: &PS.Ident{Name: "b"}, Right: &PS.NumberLiteral{Val: 2}},
	}
	filter := buildFilterTree(pred, scan)

	plan, err := pass.Apply(&OC.Plan{Root: filter}, &OC.Context{Factory: &mockFactory{}})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	if plan == nil || plan.Root == nil {
		t.Fatal("nil plan")
	}
	f, ok := plan.Root.(*noopOp)
	if !ok {
		t.Fatalf("root = %T, want *noopOp", plan.Root)
	}
	// Should be unchanged — not all disjuncts are equalities
	if _, ok := f.pred.(*PS.InExpr); ok {
		t.Error("non-equality disjunct should prevent IN conversion")
	}
}
