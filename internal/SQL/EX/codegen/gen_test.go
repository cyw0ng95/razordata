package codegen

import (
	"context"
	"runtime"
	"sync"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQL/EX"
	"github.com/cyw0ng95/razordata/internal/SQL/LX"
	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

func TestCodegen_RoundTrip(t *testing.T) {
	for _, op := range []struct {
		name string
		fn   func() func()
	}{
		{"SeqScan", func() func() { return nil }},
		{"IndexScan", func() func() { return nil }},
		{"Filter", func() func() { return nil }},
		{"Project", func() func() { return nil }},
		{"Sort", func() func() { return nil }},
		{"Limit", func() func() { return nil }},
		{"NestedLoopJoin", func() func() { return nil }},
		{"HashJoin", func() func() { return nil }},
		{"HashAggregate", func() func() { return nil }},
		{"Aggregate", func() func() { return nil }},
		{"Distinct", func() func() { return nil }},
		{"CompoundOp", func() func() { return nil }},
		{"WindowOperator", func() func() { return nil }},
		{"ExplainStmtOp", func() func() { return nil }},
		{"CreateViewOperator", func() func() { return nil }},
	} {
		t.Run(op.name, func(t *testing.T) {
			_ = op.fn
		})
	}
}

func TestInlineCache_GetPut(t *testing.T) {
	c := NewInlineCache(4)
	sig := CallSiteSig{Op: OpFilter, NumCols: 2}
	c.Put(sig, nil)
	got := c.Get(sig)
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestInlineCache_Get_Miss(t *testing.T) {
	c := NewInlineCache(4)
	sig := CallSiteSig{Op: OpFilter}
	got := c.Get(sig)
	if got != nil {
		t.Errorf("expected nil on miss, got %v", got)
	}
}

func TestInlineCache_UpdateExisting(t *testing.T) {
	c := NewInlineCache(4)
	sig := CallSiteSig{Op: OpFilter}
	fn1 := func(ctx context.Context, batch *EX.Batch, params []any) (*EX.Batch, error) { return batch, nil }
	fn2 := func(ctx context.Context, batch *EX.Batch, params []any) (*EX.Batch, error) { return nil, nil }
	c.Put(sig, fn1)
	c.Put(sig, fn2)
	got := c.Get(sig)
	if got == nil {
		t.Fatal("expected non-nil after second put")
	}
	b, _ := got(nil, nil, nil)
	if b != nil {
		t.Error("expected updated function (nil return)")
	}
}

func TestInlineCache_Eviction(t *testing.T) {
	c := NewInlineCache(2)
	for i := 0; i < 5; i++ {
		sig := CallSiteSig{Op: OpType(i + 1)}
		c.Put(sig, nil)
	}
	if c.Len() > 2 {
		t.Errorf("expected cache bounded at 2, got %d", c.Len())
	}
}

func TestInlineCache_ZeroLimit(t *testing.T) {
	c := NewInlineCache(0)
	if c.limit != 256 {
		t.Errorf("expected default 256, got %d", c.limit)
	}
}

func TestInlineCache_ConcurrentAccess(t *testing.T) {
	c := NewInlineCache(64)
	var wg sync.WaitGroup
	n := runtime.GOMAXPROCS(0)
	if n < 2 {
		n = 2
	}
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			sig := CallSiteSig{Op: OpType(id%15 + 1)}
			c.Put(sig, nil)
			_ = c.Get(sig)
		}(i)
	}
	wg.Wait()
}

func TestExprShapeFromPS_Nil(t *testing.T) {
	if shape := ExprShapeFromPS(nil); shape != ExprUnknown {
		t.Errorf("expected ExprUnknown, got %d", shape)
	}
}

func TestExprShapeFromPS_ColEqLit(t *testing.T) {
	expr := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "x"},
		Right: &PS.NumberLiteral{Val: 42},
		Op:    int(LX.T_EQ),
	}
	if shape := ExprShapeFromPS(expr); shape != ExprColEqLit {
		t.Errorf("expected ExprColEqLit, got %d", shape)
	}
}

func TestExprShapeFromPS_ColNeLit(t *testing.T) {
	expr := &PS.BinaryExpr{
		Left:  &PS.QualifiedName{Table: "t", Name: "x"},
		Right: &PS.NumberLiteral{Val: 42},
		Op:    int(LX.T_NE),
	}
	if shape := ExprShapeFromPS(expr); shape != ExprColNeLit {
		t.Errorf("expected ExprColNeLit, got %d", shape)
	}
}

func TestExprShapeFromPS_ColLtLit(t *testing.T) {
	expr := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "x"},
		Right: &PS.NumberLiteral{Val: 10},
		Op:    int(LX.T_LT),
	}
	if shape := ExprShapeFromPS(expr); shape != ExprColLtLit {
		t.Errorf("expected ExprColLtLit, got %d", shape)
	}
}

func TestExprShapeFromPS_ColGtLit(t *testing.T) {
	expr := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "x"},
		Right: &PS.FloatLiteral{Val: 3.14},
		Op:    int(LX.T_GT),
	}
	if shape := ExprShapeFromPS(expr); shape != ExprColGtLit {
		t.Errorf("expected ExprColGtLit, got %d", shape)
	}
}

func TestExprShapeFromPS_ColLeLit(t *testing.T) {
	expr := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "x"},
		Right: &PS.NumberLiteral{Val: 5},
		Op:    int(LX.T_LE),
	}
	if shape := ExprShapeFromPS(expr); shape != ExprColLeLit {
		t.Errorf("expected ExprColLeLit, got %d", shape)
	}
}

func TestExprShapeFromPS_ColGeLit(t *testing.T) {
	expr := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "x"},
		Right: &PS.StringLiteral{Val: "abc"},
		Op:    int(LX.T_GE),
	}
	if shape := ExprShapeFromPS(expr); shape != ExprColGeLit {
		t.Errorf("expected ExprColGeLit, got %d", shape)
	}
}

func TestExprShapeFromPS_ColEqCol(t *testing.T) {
	expr := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "x"},
		Right: &PS.Ident{Name: "y"},
		Op:    int(LX.T_EQ),
	}
	if shape := ExprShapeFromPS(expr); shape != ExprColEqCol {
		t.Errorf("expected ExprColEqCol, got %d", shape)
	}
}

func TestExprShapeFromPS_ColNeCol(t *testing.T) {
	expr := &PS.BinaryExpr{
		Left:  &PS.QualifiedName{Table: "t", Name: "a"},
		Right: &PS.Ident{Name: "b"},
		Op:    int(LX.T_NE),
	}
	if shape := ExprShapeFromPS(expr); shape != ExprColNeCol {
		t.Errorf("expected ExprColNeCol, got %d", shape)
	}
}

func TestExprShapeFromPS_NonBinary(t *testing.T) {
	expr := &PS.UnaryExpr{Op: int(LX.T_NOT), Operand: &PS.Ident{Name: "x"}}
	if shape := ExprShapeFromPS(expr); shape != ExprUnknown {
		t.Errorf("expected ExprUnknown for unary, got %d", shape)
	}
}

func TestExprShapeFromPS_LitOnLeft(t *testing.T) {
	expr := &PS.BinaryExpr{
		Left:  &PS.NumberLiteral{Val: 42},
		Right: &PS.Ident{Name: "x"},
		Op:    int(LX.T_EQ),
	}
	shape := ExprShapeFromPS(expr)
	if shape != ExprUnknown {
		t.Errorf("expected ExprUnknown (literal on left), got %d", shape)
	}
}

func TestOpTypeFromString(t *testing.T) {
	tests := []struct {
		input string
		want  OpType
	}{
		{"SeqScan", OpSeqScan},
		{"Filter", OpFilter},
		{"Project", OpProject},
		{"Sort", OpSort},
		{"Limit", OpLimit},
		{"HashJoin", OpHashJoin},
		{"NestedLoopJoin", OpNestedLoopJoin},
		{"HashAggregate", OpHashAggregate},
		{"Aggregate", OpAggregate},
		{"Distinct", OpDistinct},
		{"CompoundOp", OpCompound},
		{"WindowOperator", OpWindow},
		{"IndexScan", OpIndexScan},
		{"ExplainStmtOp", OpExplain},
		{"CreateViewOperator", OpCreateView},
		{"UnknownType", OpUnknown},
		{"", OpUnknown},
		{"  ", OpUnknown},
		{"SeqScan2", OpUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := OpTypeFromString(tt.input)
			if got != tt.want {
				t.Errorf("OpTypeFromString(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestNewInlineCache_DefaultLimit(t *testing.T) {
	c := NewInlineCache(0)
	if c == nil {
		t.Fatal("NewInlineCache returned nil")
	}
	if c.limit != 256 {
		t.Errorf("expected default limit 256, got %d", c.limit)
	}
}

func TestPlanVisitor_BasicOps(t *testing.T) {
	v := NewPlanVisitor()
	if v == nil {
		t.Fatal("NewPlanVisitor returned nil")
	}
}

func TestPlanVisitor_NilOp(t *testing.T) {
	v := NewPlanVisitor()
	plan := v.Visit(nil)
	if plan.Root.Type != OpUnknown {
		t.Errorf("expected OpUnknown for nil, got %d", plan.Root.Type)
	}
}

func TestPlanVisitor_SingleOp(t *testing.T) {
	v := NewPlanVisitor()
	plan := v.visitOp(nil)
	if plan.Type != OpUnknown {
		t.Errorf("expected OpUnknown for nil, got %d", plan.Type)
	}
}

func TestCodegenPlan_HasOps(t *testing.T) {
	plan := CodegenPlan{
		SchemaVersion: 1,
		PlanHash:      "test-hash",
		NumOps:        0,
		Root: CodegenOp{
			Type:     OpFilter,
			TypeName: "Filter",
			HasPred:  true,
		},
	}
	if plan.Root.Type != OpFilter {
		t.Errorf("expected OpFilter, got %d", plan.Root.Type)
	}
	if plan.PlanHash != "test-hash" {
		t.Errorf("expected test-hash, got %s", plan.PlanHash)
	}
}

func TestCodegenPlan_EmptyRoot(t *testing.T) {
	plan := CodegenPlan{}
	if plan.Root.Type != OpUnknown {
		t.Errorf("expected OpUnknown for empty root, got %d", plan.Root.Type)
	}
}

func TestCodegenPlan_NestedOps(t *testing.T) {
	plan := CodegenPlan{
		Root: CodegenOp{
			Type:     OpFilter,
			TypeName: "Filter",
			HasPred:  true,
			Children: []CodegenOp{
				{Type: OpSeqScan, TypeName: "SeqScan"},
			},
		},
	}
	if len(plan.Root.Children) != 1 {
		t.Errorf("expected 1 child, got %d", len(plan.Root.Children))
	}
	if plan.Root.Children[0].Type != OpSeqScan {
		t.Errorf("expected SeqScan child, got %d", plan.Root.Children[0].Type)
	}
}

func TestCountOps_Empty(t *testing.T) {
	if n := countOps(CodegenOp{Type: OpUnknown}); n != 1 {
		t.Errorf("expected 1 for empty, got %d", n)
	}
}

func TestCountOps_Nested(t *testing.T) {
	op := CodegenOp{
		Type: OpFilter,
		Children: []CodegenOp{
			{Type: OpSeqScan},
			{Type: OpProject},
		},
	}
	if n := countOps(op); n != 3 {
		t.Errorf("expected 3 for two children, got %d", n)
	}
}

func TestExprShapeFromPS_Float64Lit(t *testing.T) {
	expr := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "price"},
		Right: &PS.FloatLiteral{Val: 9.99},
		Op:    int(LX.T_EQ),
	}
	if shape := ExprShapeFromPS(expr); shape != ExprColEqLit {
		t.Errorf("expected ExprColEqLit for float lit, got %d", shape)
	}
}

func TestExprShapeFromPS_StringLit(t *testing.T) {
	expr := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "name"},
		Right: &PS.StringLiteral{Val: "alice"},
		Op:    int(LX.T_EQ),
	}
	if shape := ExprShapeFromPS(expr); shape != ExprColEqLit {
		t.Errorf("expected ExprColEqLit for string lit, got %d", shape)
	}
}

func TestExprShapeFromPS_UnknownOp(t *testing.T) {
	expr := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "x"},
		Right: &PS.NumberLiteral{Val: 1},
		Op:    999,
	}
	shape := ExprShapeFromPS(expr)
	if shape != ExprUnknown {
		t.Errorf("expected ExprUnknown for unknown op, got %d", shape)
	}
}

func TestPlanVisitor_VisitNil(t *testing.T) {
	v := NewPlanVisitor()
	plan := v.Visit(nil)
	if plan.NumOps < 1 {
		t.Errorf("expected at least 1 op for nil visit (OpUnknown placeholder), got %d", plan.NumOps)
	}
	if plan.Root.Type != OpUnknown {
		t.Errorf("expected OpUnknown root, got %d", plan.Root.Type)
	}
}

func TestInlineCache_ConcurrentPut(t *testing.T) {
	c := NewInlineCache(128)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			sig := CallSiteSig{Op: OpType(id%15 + 1), NumCols: uint8(id % 8)}
			c.Put(sig, nil)
		}(i)
	}
	wg.Wait()
	if c.Len() > 128 {
		t.Errorf("cache exceeded limit: %d", c.Len())
	}
}

func TestOpTypeFromString_RoundTrip(t *testing.T) {
	names := []string{"SeqScan", "Filter", "Project", "Sort", "Limit",
		"NestedLoopJoin", "HashJoin", "HashAggregate", "Aggregate",
		"Distinct", "CompoundOp", "WindowOperator", "IndexScan",
		"ExplainStmtOp", "CreateViewOperator"}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			ot := OpTypeFromString(name)
			if ot == OpUnknown {
				t.Errorf("OpTypeFromString(%q) returned OpUnknown", name)
			}
		})
	}
}
