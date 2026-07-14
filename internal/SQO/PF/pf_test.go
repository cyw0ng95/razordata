package PF

import (
	"context"
	"testing"

	LX "github.com/cyw0ng95/razordata/internal/SQF/LX"
	OC "github.com/cyw0ng95/razordata/internal/SQO/OC"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// scanOp is a minimal scan operator for testing column pruning and
// predicate pushdown. It implements pl.Operator + ColPrunable +
// ColumnSchema + RelationSource + PredicateCarrier but NOT
// Parent/Children2 — it is unconditionally a leaf.
type scanOp struct {
	name     string
	usedCols []string
	scanCols []string
	pred     interface{}
	hasPred  bool
}

func (s *scanOp) Next(context.Context) (pl.Row, error) { return pl.Row{}, nil }
func (s *scanOp) Close() error                          { return nil }
func (s *scanOp) UsedCols() []string                    { return s.usedCols }
func (s *scanOp) SetUsedCols(cols []string)             { s.usedCols = cols }
func (s *scanOp) Columns() []string                     { return s.scanCols }
func (s *scanOp) ColumnIndex(name string) int {
	for i, c := range s.scanCols {
		if c == name {
			return i
		}
	}
	return -1
}
func (s *scanOp) Table() string        { return s.name }
func (s *scanOp) Predicate() interface{} { return s.pred }
func (s *scanOp) SetPredicate(p interface{}) {
	s.pred = p
	s.hasPred = p != nil
}
func (s *scanOp) HasPredicate() bool { return s.hasPred }

// noopOp is a minimal pl.Operator for testing tree walks.
type noopOp struct {
	name     string
	child    pl.Operator
	left     pl.Operator
	right    pl.Operator
	pred     interface{}
	hasPred  bool
	colsExpr []PS.Expr
	limitVal int64
	offset   int64
	topN     bool
	orderBy  []pl.OrderSpec
}

func (n *noopOp) Next(ctx context.Context) (pl.Row, error) { return pl.Row{}, nil }
func (n *noopOp) Close() error                              { return nil }
func (n *noopOp) Child() pl.Operator                        { return n.child }
func (n *noopOp) SetChild(c pl.Operator)                    { n.child = c }
func (n *noopOp) Left() pl.Operator                         { return n.left }
func (n *noopOp) SetLeft(c pl.Operator)                     { n.left = c }
func (n *noopOp) Right() pl.Operator                        { return n.right }
func (n *noopOp) SetRight(c pl.Operator)                    { n.right = c }
func (n *noopOp) Predicate() interface{}                    { return n.pred }
func (n *noopOp) SetPredicate(p interface{})                { n.pred = p; n.hasPred = p != nil }
func (n *noopOp) HasPredicate() bool                        { return n.hasPred }
func (n *noopOp) Cols() []PS.Expr                           { return n.colsExpr }
func (n *noopOp) Limit() int64                              { return n.limitVal }
func (n *noopOp) Offset() int64                             { return n.offset }
func (n *noopOp) IsTopN() bool                              { return n.topN }
func (n *noopOp) SetTopN(b bool)                            { n.topN = b }
func (n *noopOp) OrderBy() []pl.OrderSpec                   { return n.orderBy }

// mockFactory returns a noopOp for every factory call.
type mockFactory struct{}

func (m *mockFactory) NewSeqScan(table string, schema []string) pl.Operator     { return &noopOp{name: "seqscan:" + table} }
func (m *mockFactory) NewIndexScan(table, index string, schema []string) pl.Operator { return &noopOp{name: "indexscan:" + table} }
func (m *mockFactory) NewIndexOnlyScan(table, index string, schema []string) pl.Operator { return nil }
func (m *mockFactory) NewFilter(child pl.Operator, pred interface{}) pl.Operator { return &noopOp{name: "filter", child: child, pred: pred, hasPred: true} }
func (m *mockFactory) NewProject(child pl.Operator, cols []string, exprs []interface{}) pl.Operator { return &noopOp{name: "project", child: child} }
func (m *mockFactory) NewFilterProject(child pl.Operator, pred interface{}, cols []string, exprs []interface{}) pl.Operator {
	psExprs := make([]PS.Expr, len(exprs))
	for i, e := range exprs {
		psExprs[i] = e.(PS.Expr)
	}
	return &noopOp{name: "filterproject", child: child, pred: pred, hasPred: true, colsExpr: psExprs}
}
func (m *mockFactory) NewHashJoin(l, r pl.Operator, lk, rk string, jt pl.JoinType) pl.Operator { return &noopOp{name: "hashjoin"} }
func (m *mockFactory) NewNestedLoopJoin(l, r pl.Operator, p interface{}, jt pl.JoinType) pl.Operator { return nil }
func (m *mockFactory) NewAggregate(child pl.Operator, gc []string, aggs []pl.AggregateSpec) pl.Operator { return &noopOp{name: "agg"} }
func (m *mockFactory) NewHashAggregate(child pl.Operator, gc []string, aggs []pl.AggregateSpec) pl.Operator { return nil }
func (m *mockFactory) NewSort(child pl.Operator, ob []pl.OrderSpec) pl.Operator { return &noopOp{name: "sort"} }
func (m *mockFactory) NewLimit(child pl.Operator, limit, offset int64) pl.Operator { return &noopOp{name: "limit"} }
func (m *mockFactory) NewDistinct(child pl.Operator) pl.Operator { return &noopOp{name: "distinct"} }
func (m *mockFactory) NewSetOp(l, r pl.Operator, op pl.SetOpType) pl.Operator { return nil }
func (m *mockFactory) NewValues(rows [][]interface{}) pl.Operator { return &noopOp{name: "empty"} }

func TestFilterProjectFusion_FusesFilterProjectChain(t *testing.T) {
	factory := &mockFactory{}
	leaf := &noopOp{name: "seqscan"}
	proj := &noopOp{name: "project", child: leaf, colsExpr: []PS.Expr{
		&PS.Ident{Name: "a"},
		&PS.Ident{Name: "b"},
	}}
	filter := &noopOp{name: "filter", child: proj, pred: &PS.BinaryExpr{}, hasPred: true}

	pass := &FilterProjectFusionPass{}
	plan, err := pass.Apply(&OC.Plan{Root: filter}, &OC.Context{Factory: factory})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	if plan == nil || plan.Root == nil {
		t.Fatal("Apply returned nil plan")
	}
	fused, ok := plan.Root.(*noopOp)
	if !ok {
		t.Fatalf("root type = %T, want *noopOp", plan.Root)
	}
	if fused.name != "filterproject" {
		t.Errorf("name = %q, want %q", fused.name, "filterproject")
	}
	if fused.child == nil {
		t.Fatal("fused child is nil")
	}
	if fused.child.(*noopOp).name != "seqscan" {
		t.Errorf("fused child name = %q, want %q", fused.child.(*noopOp).name, "seqscan")
	}
}

func TestFilterProjectFusion_NoOp_NoFilter(t *testing.T) {
	factory := &mockFactory{}
	leaf := &noopOp{name: "seqscan"}
	proj := &noopOp{name: "project", child: leaf}

	pass := &FilterProjectFusionPass{}
	plan, err := pass.Apply(&OC.Plan{Root: proj}, &OC.Context{Factory: factory})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	if plan.Root.(*noopOp).name != "project" {
		t.Errorf("no-fusion case changed root: %q", plan.Root.(*noopOp).name)
	}
}

func TestFilterProjectFusion_NoOp_NilFactory(t *testing.T) {
	leaf := &noopOp{name: "seqscan"}
	pass := &FilterProjectFusionPass{}
	_, err := pass.Apply(&OC.Plan{Root: leaf}, &OC.Context{Factory: nil})
	if err == nil {
		t.Fatal("expected error for nil factory")
	}
}

func TestWalkOp(t *testing.T) {
	leaf := &noopOp{name: "leaf"}
	parent := &noopOp{name: "parent", left: leaf}

	var visited []string
	result := WalkOp(parent, func(op pl.Operator) pl.Operator {
		visited = append(visited, op.(*noopOp).name)
		return op
	})
	if len(visited) != 2 {
		t.Errorf("visited %d nodes, want 2", len(visited))
	}
	if result.(*noopOp).name != "parent" {
		t.Errorf("root name = %q, want parent", result.(*noopOp).name)
	}
}

func TestWalkOp_Nil(t *testing.T) {
	result := WalkOp(nil, func(op pl.Operator) pl.Operator { return op })
	if result != nil {
		t.Error("WalkOp(nil) should return nil")
	}
}

func TestColumnPruning_PrunesScanCols(t *testing.T) {
	scan := &scanOp{name: "t1", scanCols: []string{"a", "b", "c", "d", "e"}}
	proj := &noopOp{name: "project", child: scan, colsExpr: []PS.Expr{
		&PS.Ident{Name: "a"},
		&PS.Ident{Name: "c"},
	}}

	pass := &ColumnPruningPass{}
	plan, err := pass.Apply(&OC.Plan{Root: proj}, &OC.Context{})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	pruned := plan.Root.(*noopOp).child.(*scanOp)
	if len(pruned.usedCols) != 2 {
		t.Fatalf("UsedCols = %v, want [a c]", pruned.usedCols)
	}
	hasA, hasC := false, false
	for _, c := range pruned.usedCols {
		if c == "a" {
			hasA = true
		}
		if c == "c" {
			hasC = true
		}
	}
	if !hasA || !hasC {
		t.Errorf("UsedCols = %v, missing a or c", pruned.usedCols)
	}
}

func TestColumnPruning_FilterAddsPredCols(t *testing.T) {
	scan := &scanOp{name: "t1", scanCols: []string{"a", "b", "c", "d"}}
	filter := &noopOp{name: "filter", child: scan, pred: &PS.Ident{Name: "b"}, hasPred: true}
	proj := &noopOp{name: "project", child: filter, colsExpr: []PS.Expr{
		&PS.Ident{Name: "a"},
	}}

	pass := &ColumnPruningPass{}
	_, err := pass.Apply(&OC.Plan{Root: proj}, &OC.Context{})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	pruned := scan.usedCols
	if len(pruned) != 2 {
		t.Fatalf("UsedCols = %v, want [a b]", pruned)
	}
}

func TestColumnPruning_NilRoot(t *testing.T) {
	pass := &ColumnPruningPass{}
	plan, err := pass.Apply(&OC.Plan{Root: nil}, &OC.Context{})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	if plan != nil && plan.Root != nil {
		t.Error("expected nil root")
	}
}

func TestColumnPruning_JoinPropagatesToBothSides(t *testing.T) {
	leftScan := &scanOp{name: "l", scanCols: []string{"a", "b", "c"}}
	rightScan := &scanOp{name: "r", scanCols: []string{"d", "e", "f"}}
	join := &noopOp{name: "hashjoin", left: leftScan, right: rightScan}

	pass := &ColumnPruningPass{}
	_, err := pass.Apply(&OC.Plan{Root: join}, &OC.Context{})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	// Without Project or Filter above, no pruning info → scans stay nil (all cols).
	if leftScan.usedCols != nil {
		t.Errorf("leftScan usedCols = %v, want nil (no pruning)", leftScan.usedCols)
	}
	if rightScan.usedCols != nil {
		t.Errorf("rightScan usedCols = %v, want nil (no pruning)", rightScan.usedCols)
	}
}

func TestColumnPruning_JoinWithProjectPrunes(t *testing.T) {
	leftScan := &scanOp{name: "l", scanCols: []string{"a", "b", "c"}}
	rightScan := &scanOp{name: "r", scanCols: []string{"d", "e", "f"}}
	join := &noopOp{name: "hashjoin", left: leftScan, right: rightScan}
	proj := &noopOp{name: "project", child: join, colsExpr: []PS.Expr{
		&PS.Ident{Name: "a"},
		&PS.Ident{Name: "d"},
	}}

	pass := &ColumnPruningPass{}
	_, err := pass.Apply(&OC.Plan{Root: proj}, &OC.Context{})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	if len(leftScan.usedCols) != 1 {
		t.Errorf("leftScan usedCols = %v, want [a]", leftScan.usedCols)
	}
	if len(rightScan.usedCols) != 1 {
		t.Errorf("rightScan usedCols = %v, want [d]", rightScan.usedCols)
	}
}

func TestPredicatePushdown_FilterMovesToScan(t *testing.T) {
	scan := &scanOp{name: "t1", scanCols: []string{"a", "b"}}
	filter := &noopOp{name: "filter", child: scan, pred: &PS.Ident{Name: "a"}, hasPred: true}

	pass := &PredicatePushdownPass{}
	plan, err := pass.Apply(&OC.Plan{Root: filter}, &OC.Context{})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	// Filter should be eliminated, scan should have the predicate.
	result, ok := plan.Root.(*scanOp)
	if !ok {
		t.Fatalf("root = %T, want *scanOp (filter should be removed)", plan.Root)
	}
	if !result.HasPredicate() {
		t.Fatal("scan should have predicate after pushdown")
	}
}

func TestPredicatePushdown_FilterStaysWhenScanCantCarry(t *testing.T) {
	// A filter on a non-PredicateCarrier child should stay in place.
	child := &noopOp{name: "project"}
	filter := &noopOp{name: "filter", child: child, pred: &PS.Ident{Name: "a"}, hasPred: true}

	pass := &PredicatePushdownPass{}
	plan, err := pass.Apply(&OC.Plan{Root: filter}, &OC.Context{})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	result, ok := plan.Root.(*noopOp)
	if !ok || result.name != "filter" {
		t.Fatalf("root = %T, want *noopOp filter (should stay)", plan.Root)
	}
}

func TestLimitPushdown_FusesLimitSort(t *testing.T) {
	sort := &noopOp{name: "sort", orderBy: []pl.OrderSpec{{Col: "a"}}}
	limit := &noopOp{name: "limit", child: sort, limitVal: 10}

	pass := &LimitPushdownPass{}
	plan, err := pass.Apply(&OC.Plan{Root: limit}, &OC.Context{})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	// Limit should be removed, sort should be TopN.
	result, ok := plan.Root.(*noopOp)
	if !ok {
		t.Fatalf("root = %T, want *noopOp", plan.Root)
	}
	if result.name != "sort" {
		t.Errorf("root name = %q, want sort", result.name)
	}
	if !result.topN {
		t.Error("sort should be marked TopN")
	}
}

func TestLimitPushdown_NoSort_Stays(t *testing.T) {
	scan := &scanOp{name: "t1", scanCols: []string{"a"}}
	limit := &noopOp{name: "limit", child: scan, limitVal: 10}

	pass := &LimitPushdownPass{}
	plan, err := pass.Apply(&OC.Plan{Root: limit}, &OC.Context{})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	result, ok := plan.Root.(*noopOp)
	if !ok || result.name != "limit" {
		t.Errorf("root = %T, want limit", plan.Root)
	}
}

func TestColumnPruning_NoColPrunable_Safe(t *testing.T) {
	// A tree with no ColPrunable nodes should not panic.
	proj := &noopOp{name: "project", colsExpr: []PS.Expr{&PS.Ident{Name: "a"}}}
	filter := &noopOp{name: "filter", child: proj, pred: &PS.Ident{Name: "b"}, hasPred: true}

	pass := &ColumnPruningPass{}
	_, err := pass.Apply(&OC.Plan{Root: filter}, &OC.Context{})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
}

func TestWalkOp_Binary(t *testing.T) {
	left := &noopOp{name: "left"}
	right := &noopOp{name: "right"}
	parent := &noopOp{name: "join", left: left, right: right}

	var visited []string
	WalkOp(parent, func(op pl.Operator) pl.Operator {
		visited = append(visited, op.(*noopOp).name)
		return op
	})
	if len(visited) != 3 {
		t.Errorf("visited %d nodes, want 3 (left, right, join)", len(visited))
	}
}

// ---- ConstantFoldingPass tests ----

func TestConstantFolding_BypassesTruePredicate(t *testing.T) {
	// Filter WHERE 1=1 folds to TRUE → bypass filter, child becomes root
	truePred := &PS.BinaryExpr{
		Op:    LX.T_EQ,
		Left:  &PS.NumberLiteral{Val: 1},
		Right: &PS.NumberLiteral{Val: 1},
	}
	scan := &scanOp{name: "t1", scanCols: []string{"a"}}
	filter := &noopOp{name: "filter", child: scan, pred: truePred, hasPred: true}

	plan := &OC.Plan{Root: filter}
	ctx := &OC.Context{Factory: &mockFactory{}}

	pass := &ConstantFoldingPass{}
	result, err := pass.Apply(plan, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Root != scan {
		t.Errorf("expected root to be scan (filter bypassed), got %T: %v", result.Root, result.Root)
	}
}

func TestConstantFolding_ReplacesFalsePredicateWithEmpty(t *testing.T) {
	// Filter WHERE 1=0 folds to FALSE → replace with empty values
	falsePred := &PS.BinaryExpr{
		Op:    LX.T_EQ,
		Left:  &PS.NumberLiteral{Val: 1},
		Right: &PS.NumberLiteral{Val: 0},
	}
	scan := &scanOp{name: "t1", scanCols: []string{"a"}}
	filter := &noopOp{name: "filter", child: scan, pred: falsePred, hasPred: true}

	plan := &OC.Plan{Root: filter}
	ctx := &OC.Context{Factory: &mockFactory{}}

	pass := &ConstantFoldingPass{}
	result, err := pass.Apply(plan, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	empty, ok := result.Root.(*noopOp)
	if !ok {
		t.Fatalf("expected root to be noopOp (empty values), got %T", result.Root)
	}
	if empty.name != "empty" {
		t.Errorf("expected name empty, got %q", empty.name)
	}
}

func TestConstantFolding_LeavesNonConstantPredicate(t *testing.T) {
	// Filter WHERE col > 0 is not foldable; filter unchanged
	pred := &PS.BinaryExpr{
		Op:    LX.T_GT,
		Left:  &PS.Ident{Name: "a"},
		Right: &PS.NumberLiteral{Val: 0},
	}
	scan := &scanOp{name: "t1", scanCols: []string{"a"}}
	filter := &noopOp{name: "filter", child: scan, pred: pred, hasPred: true}

	plan := &OC.Plan{Root: filter}
	ctx := &OC.Context{Factory: &mockFactory{}}

	pass := &ConstantFoldingPass{}
	result, err := pass.Apply(plan, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	filterResult, ok := result.Root.(*noopOp)
	if !ok {
		t.Fatalf("expected root to be noopOp (filter unchanged), got %T", result.Root)
	}
	if filterResult.name != "filter" {
		t.Errorf("expected filter unchanged, got %q", filterResult.name)
	}
	if filterResult.child != scan {
		t.Error("expected filter child to remain scan")
	}
}

func TestConstantFolding_NilPlan(t *testing.T) {
	pass := &ConstantFoldingPass{}
	result, err := pass.Apply(nil, &OC.Context{Factory: &mockFactory{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Error("expected nil result for nil plan")
	}
}

func TestConstantFolding_NilFactory(t *testing.T) {
	pass := &ConstantFoldingPass{}
	result, err := pass.Apply(&OC.Plan{Root: &scanOp{}}, nil)
	if err == nil {
		t.Error("expected error for nil Context")
	}
	if result == nil {
		t.Error("expected plan returned even on error (same as other passes)")
	}
}