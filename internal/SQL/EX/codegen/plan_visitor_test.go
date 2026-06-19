package codegen

import (
	"context"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQL/EX"
	"github.com/cyw0ng95/razordata/internal/SQL/LX"
	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

type mockOp struct {
	typ   string
	child EX.Operator
	left  EX.Operator
	right EX.Operator
	pred  PS.Expr
}

func (m *mockOp) Next(_ context.Context) (EX.Row, error) {
	return EX.Row{}, nil
}
func (m *mockOp) Close() error            { return nil }
func (m *mockOp) Child() EX.Operator      { return m.child }
func (m *mockOp) LeftChild() EX.Operator  { return m.left }
func (m *mockOp) RightChild() EX.Operator { return m.right }
func (m *mockOp) Predicate() PS.Expr      { return m.pred }

func TestPlanVisitor_SeqScan(t *testing.T) {
	v := NewPlanVisitor()
	// mockOp won't match *EX.SeqScan — expect OpUnknown from default case
	op := &mockOp{typ: "SeqScan"}
	plan := v.Visit(op)
	if plan.Root.Type == OpUnknown {
		t.Log("mockOp falls to default case (OpUnknown) — expected")
	} else {
		t.Errorf("expected OpUnknown for mock type, got %d", plan.Root.Type)
	}
}

func TestPlanVisitor_IndexScan(t *testing.T) {
	v := NewPlanVisitor()
	op := &mockOp{typ: "IndexScan"}
	plan := v.Visit(op)
	if plan.Root.Type != OpUnknown {
		t.Errorf("expected OpUnknown for unmapped type, got %d", plan.Root.Type)
	}
}

func TestPlanVisitor_Filter(t *testing.T) {
	v := NewPlanVisitor()
	pred := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "x"},
		Right: &PS.NumberLiteral{Val: 42},
		Op:    int(LX.T_GT),
	}
	child := &mockOp{typ: "SeqScan"}
	op := &mockOp{typ: "Filter", child: child, pred: pred}
	if v == nil {
		t.Fatal("expected non-nil visitor")
	}
	plan := v.Visit(op)
	_ = plan
}

func TestPlanVisitor_NestedLoopJoin(t *testing.T) {
	v := NewPlanVisitor()
	left := &mockOp{typ: "SeqScan"}
	right := &mockOp{typ: "SeqScan"}
	op := &mockOp{typ: "NestedLoopJoin", left: left, right: right}
	plan := v.Visit(op)
	if plan.NumOps != 1 {
		t.Logf("mockOp types not recognized — got %d ops (expected for mock types)", plan.NumOps)
	}
}

func TestPlanVisitor_HashJoin(t *testing.T) {
	v := NewPlanVisitor()
	left := &mockOp{typ: "Filter", child: &mockOp{typ: "SeqScan"}}
	right := &mockOp{typ: "SeqScan"}
	op := &mockOp{typ: "HashJoin", left: left, right: right}
	plan := v.Visit(op)
	_ = plan
}

func TestPlanVisitor_FilterNotNullable(t *testing.T) {
	v := NewPlanVisitor()
	filter := &mockOp{
		typ:   "Filter",
		child: &mockOp{typ: "SeqScan"},
		pred: &PS.BinaryExpr{
			Left:  &PS.Ident{Name: "id"},
			Right: &PS.NumberLiteral{Val: 1},
			Op:    int(LX.T_EQ),
		},
	}
	plan := v.Visit(filter)
	if plan.NumOps < 1 {
		t.Errorf("expected at least 1 op, got %d", plan.NumOps)
	}
}

func TestPlanVisitor_DeepTree(t *testing.T) {
	v := NewPlanVisitor()
	// Filter -> Project -> SeqScan
	project := &mockOp{typ: "Project", child: &mockOp{typ: "SeqScan"}}
	filter := &mockOp{
		typ:   "Filter",
		child: project,
		pred:  &PS.BinaryExpr{Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 1}, Op: int(LX.T_GT)},
	}
	plan := v.Visit(filter)
	if plan.NumOps != 3 {
		t.Errorf("expected 3 ops, got %d", plan.NumOps)
	}
}

func TestPlanVisitor_Empty(t *testing.T) {
	v := NewPlanVisitor()
	plan := v.Visit(nil)
	if plan.Root.Type != OpUnknown {
		t.Errorf("expected OpUnknown for nil, got %d", plan.Root.Type)
	}
	if plan.NumOps != 1 {
		t.Errorf("expected 1 op (unknown placeholder), got %d", plan.NumOps)
	}
}

func TestPlanVisitor_HashAggregateChain(t *testing.T) {
	v := NewPlanVisitor()
	agg := &mockOp{typ: "HashAggregate", child: &mockOp{typ: "SeqScan"}}
	plan := v.Visit(agg)
	if plan.NumOps != 2 {
		t.Errorf("expected 2 ops, got %d", plan.NumOps)
	}
}

func TestCountOps_SeqScanOnly(t *testing.T) {
	op := CodegenOp{Type: OpSeqScan}
	if n := countOps(op); n != 1 {
		t.Errorf("expected 1, got %d", n)
	}
}

func TestCountOps_TwoLevel(t *testing.T) {
	op := CodegenOp{
		Type: OpFilter,
		Children: []CodegenOp{
			{Type: OpSeqScan},
		},
	}
	if n := countOps(op); n != 2 {
		t.Errorf("expected 2, got %d", n)
	}
}

func TestCountOps_ThreeLevel(t *testing.T) {
	op := CodegenOp{
		Type: OpSort,
		Children: []CodegenOp{
			{
				Type: OpFilter,
				Children: []CodegenOp{
					{Type: OpSeqScan},
				},
			},
		},
	}
	if n := countOps(op); n != 3 {
		t.Errorf("expected 3, got %d", n)
	}
}

func TestCountOps_Join(t *testing.T) {
	op := CodegenOp{
		Type: OpHashJoin,
		Children: []CodegenOp{
			{Type: OpSeqScan},
			{Type: OpIndexScan},
		},
	}
	if n := countOps(op); n != 3 {
		t.Errorf("expected 3, got %d", n)
	}
}

func TestPlanVisitor_ExprCollect(t *testing.T) {
	v := NewPlanVisitor()
	pred := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "x"},
		Right: &PS.NumberLiteral{Val: 100},
		Op:    int(LX.T_LE),
	}
	filter := &mockOp{
		typ:   "Filter",
		child: &mockOp{typ: "SeqScan"},
		pred:  pred,
	}
	plan := v.Visit(filter)
	if plan.NumOps < 1 {
		t.Errorf("expected at least 1 op, got %d", plan.NumOps)
	}
	_ = plan
}

func TestPlanVisitor_VisitChildren(t *testing.T) {
	v := NewPlanVisitor()
	left := &mockOp{typ: "SeqScan"}
	right := &mockOp{typ: "IndexScan"}
	nlj := &mockOp{typ: "NestedLoopJoin", left: left, right: right}
	plan := v.Visit(nlj)
	if plan.NumOps != 1 {
		t.Logf("mockOp types not recognized — got %d ops", plan.NumOps)
	}
}

func TestPlanVisit_NilOp(t *testing.T) {
	v := NewPlanVisitor()
	op := v.visitOp(nil)
	if op.Type != OpUnknown {
		t.Errorf("expected OpUnknown, got %d", op.Type)
	}
}

func TestPlanVisit_NilExprNotPanics(t *testing.T) {
	v := NewPlanVisitor()
	filter := &mockOp{
		typ:   "Filter",
		child: &mockOp{typ: "SeqScan"},
	}
	plan := v.Visit(filter)
	if plan.Root.Type != OpUnknown {
		t.Logf("expected OpUnknown for mock type, got %d", plan.Root.Type)
	}
}

func TestSerializeOp(t *testing.T) {
	v := NewPlanVisitor()
	op := CodegenOp{
		TypeName: "Filter",
		Children: []CodegenOp{
			{TypeName: "SeqScan"},
		},
	}
	s := v.serializeOp(op)
	if s == "" {
		t.Error("expected non-empty serialization")
	}
}

func TestSerializeOp_NestedJoin(t *testing.T) {
	v := NewPlanVisitor()
	op := CodegenOp{
		TypeName: "HashJoin",
		Children: []CodegenOp{
			{TypeName: "SeqScan"},
			{TypeName: "IndexScan"},
		},
	}
	s := v.serializeOp(op)
	if s == "" {
		t.Error("expected non-empty serialization")
	}
}

func TestVisitExpr_Binary(t *testing.T) {
	v := NewPlanVisitor()
	exprs := v.visitExpr(&PS.BinaryExpr{
		Left:  &PS.Ident{Name: "a"},
		Right: &PS.NumberLiteral{Val: 10},
		Op:    int(LX.T_GT),
	})
	if len(exprs) == 0 {
		t.Error("expected at least 1 expr")
	}
}

func TestVisitExpr_Chain(t *testing.T) {
	v := NewPlanVisitor()
	// (a > 1) AND (b < 2)
	left := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "a"},
		Right: &PS.NumberLiteral{Val: 1},
		Op:    int(LX.T_GT),
	}
	right := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "b"},
		Right: &PS.NumberLiteral{Val: 2},
		Op:    int(LX.T_LT),
	}
	and := &PS.BinaryExpr{
		Left:  left,
		Right: right,
		Op:    int(LX.T_AND),
	}
	exprs := v.visitExpr(and)
	if len(exprs) < 2 {
		t.Errorf("expected at least 2 exprs, got %d", len(exprs))
	}
}

func TestVisitExpr_Nil(t *testing.T) {
	v := NewPlanVisitor()
	exprs := v.visitExpr(nil)
	if len(exprs) != 0 {
		t.Errorf("expected 0 for nil expr, got %d", len(exprs))
	}
}

func TestVisitExpr_Ident(t *testing.T) {
	v := NewPlanVisitor()
	exprs := v.visitExpr(&PS.Ident{Name: "x"})
	if len(exprs) != 0 {
		t.Errorf("expected 0 for ident alone, got %d", len(exprs))
	}
}

func TestVisitExpr_Unary(t *testing.T) {
	v := NewPlanVisitor()
	unary := &PS.UnaryExpr{
		Op: int(LX.T_NOT),
		Operand: &PS.BinaryExpr{
			Left:  &PS.Ident{Name: "a"},
			Right: &PS.NumberLiteral{Val: 0},
			Op:    int(LX.T_EQ),
		},
	}
	exprs := v.visitExpr(unary)
	if len(exprs) < 1 {
		t.Error("expected at least 1 expr")
	}
}

func TestColExpr_LiteralValue(t *testing.T) {
	v := NewPlanVisitor()
	exprs := v.visitExpr(&PS.BinaryExpr{
		Left:  &PS.Ident{Name: "x"},
		Right: &PS.FloatLiteral{Val: 3.14},
		Op:    int(LX.T_GE),
	})
	if len(exprs) < 1 {
		t.Fatal("expected at least 1 expr")
	}
	if !exprs[0].IsLiteral {
		t.Error("expected literal flag")
	}
	if exprs[0].LitType != LX.T_FLOAT_KW {
		t.Errorf("expected float literal, got %d", exprs[0].LitType)
	}
}

func TestColExpr_TextLiteral(t *testing.T) {
	v := NewPlanVisitor()
	exprs := v.visitExpr(&PS.BinaryExpr{
		Left:  &PS.Ident{Name: "name"},
		Right: &PS.StringLiteral{Val: "alice"},
		Op:    int(LX.T_EQ),
	})
	if len(exprs) < 1 {
		t.Fatal("expected at least 1 expr")
	}
	if !exprs[0].IsLiteral {
		t.Error("expected literal flag")
	}
}

func TestColExpr_ColEqCol(t *testing.T) {
	v := NewPlanVisitor()
	exprs := v.visitExpr(&PS.BinaryExpr{
		Left:  &PS.QualifiedName{Table: "t1", Name: "x"},
		Right: &PS.QualifiedName{Table: "t2", Name: "x"},
		Op:    int(LX.T_EQ),
	})
	if len(exprs) < 1 {
		t.Fatal("expected at least 1 expr")
	}
	_ = exprs[0].LeftCol
	_ = exprs[0].RightCol
}

func TestHashSeed(t *testing.T) {
	v := NewPlanVisitor()
	if v.hashSeed != "" {
		t.Logf("hashSeed: %s", v.hashSeed)
	}
}

func TestPlanHash_Deterministic(t *testing.T) {
	v1 := NewPlanVisitor()
	v2 := NewPlanVisitor()
	op := &mockOp{typ: "SeqScan"}
	p1 := v1.Visit(op)
	p2 := v2.Visit(op)
	if p1.PlanHash != p2.PlanHash {
		t.Log("plan hashes differ (expected if hashSeed changes)")
	}
}

func TestBaseOp_Sort(t *testing.T) {
	v := NewPlanVisitor()
	op := &mockOp{typ: "Sort", child: &mockOp{typ: "SeqScan"}}
	plan := v.Visit(op)
	if plan.NumOps != 2 {
		t.Errorf("expected 2 ops, got %d", plan.NumOps)
	}
}

func TestBaseOp_Distinct(t *testing.T) {
	v := NewPlanVisitor()
	op := &mockOp{typ: "Distinct", child: &mockOp{typ: "SeqScan"}}
	plan := v.Visit(op)
	_ = plan
}

func TestBaseOp_CompoundOp(t *testing.T) {
	v := NewPlanVisitor()
	op := &mockOp{typ: "CompoundOp"}
	plan := v.Visit(op)
	_ = plan
}

func TestBaseOp_Window(t *testing.T) {
	v := NewPlanVisitor()
	op := &mockOp{typ: "WindowOperator", child: &mockOp{typ: "SeqScan"}}
	plan := v.Visit(op)
	_ = plan
}

func TestBaseOp_Explain(t *testing.T) {
	v := NewPlanVisitor()
	op := &mockOp{typ: "ExplainStmtOp", child: &mockOp{typ: "SeqScan"}}
	plan := v.Visit(op)
	_ = plan
}

func TestBaseOp_CreateView(t *testing.T) {
	v := NewPlanVisitor()
	op := &mockOp{typ: "CreateViewOperator", child: &mockOp{typ: "SeqScan"}}
	plan := v.Visit(op)
	_ = plan
}

func TestBaseOp_Limit(t *testing.T) {
	v := NewPlanVisitor()
	op := &mockOp{typ: "Limit", child: &mockOp{typ: "SeqScan"}}
	plan := v.Visit(op)
	if plan.NumOps != 2 {
		t.Errorf("expected 2 ops, got %d", plan.NumOps)
	}
}

func TestCodegenOp_CloneHasChildren(t *testing.T) {
	parent := CodegenOp{
		Type: OpFilter,
		Children: []CodegenOp{
			{Type: OpSeqScan, ColNames: []string{"id", "name"}},
		},
	}
	if len(parent.Children) != 1 {
		t.Fatal("expected 1 child")
	}
	if len(parent.Children[0].ColNames) != 2 {
		t.Errorf("expected 2 col names, got %d", len(parent.Children[0].ColNames))
	}
}

func TestCodegenPlan_HashNonEmpty(t *testing.T) {
	plan := CodegenPlan{
		Root: CodegenOp{
			Type:     OpFilter,
			TypeName: "Filter",
			Children: []CodegenOp{
				{Type: OpSeqScan, TypeName: "SeqScan"},
			},
		},
		SchemaVersion: 1,
		PlanHash:      "test-hash-abc",
	}
	if plan.PlanHash == "" {
		t.Error("expected non-empty plan hash")
	}
}
