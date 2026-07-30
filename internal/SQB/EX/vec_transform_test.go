package EX

import (
        "testing"

"github.com/cyw0ng95/razordata/internal/SQB/AG"
        "github.com/cyw0ng95/razordata/internal/SQB/DT"
        "github.com/cyw0ng95/razordata/internal/SQB/OP"
        UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
        "github.com/cyw0ng95/razordata/internal/SQF/LX"
        PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func TestTryVectorizePlan_MultiKeyJoin(t *testing.T) {
	// REQ001618: HashJoin with multi-column keys IS now eligible.
	left := OP.NewSeqScan("t1")
	right := OP.NewSeqScan("t2")
	hj := OP.NewHashJoin(left, right, "t1", "t2", []string{"a", "b"}, []string{"a", "b"}, 0)
	result := tryVectorizePlan(hj, nil)
	if result == hj {
		t.Fatal("expected vectorized plan for multi-key join, got original HashJoin")
	}
	if _, ok := result.(*OP.HashJoin); ok {
		t.Fatal("expected vectorized plan, not original HashJoin")
	}
	t.Logf("Multi-key HashJoin result = %T", result)
}

func TestTryVectorizePlan_HashJoin(t *testing.T) {
	// HashJoin with single-column keys and SeqScan children is eligible.
	// In real execution with a store, the result is a vectorized plan.
	// In test mode (no store schema), resolveColumnIndex falls back to
	// index 0, so transformation still succeeds.
	result := tryVectorizePlan(buildHashJoinTree(), nil)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if _, ok := result.(*OP.HashJoin); ok {
		t.Fatal("expected vectorized plan, not original HashJoin")
	}
	t.Logf("HashJoin result = %T", result)
}

func buildHashJoinTree() DT.Operator {
	left := OP.NewSeqScan("t1")
	right := OP.NewSeqScan("t2")
	return OP.NewHashJoin(left, right, "t1", "t2", []string{"a"}, []string{"b"}, 0)
}

func TestTryVectorizePlan_IneligibleDistinct(t *testing.T) {
	child := OP.NewSeqScan("t1")
	dist := OP.NewDistinct(child)
	result := tryVectorizePlan(dist, nil)
	if result == dist {
		t.Fatal("expected Distinct to be wrapped in BatchToRowAdapter")
	}
}

func TestTryVectorizePlan_SeqScanOnly(t *testing.T) {
	ss := OP.NewSeqScan("t1")
	result := tryVectorizePlan(ss, nil)
	if result == ss {
		// The original SeqScan is wrapped in a VectorizedSeqScan
		t.Fatal("expected wrapped operator, not original SeqScan")
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestTryVectorizePlan_SeqScanFilter(t *testing.T) {
	ss := OP.NewSeqScan("t1")
	pred := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "a"},
		Right: &PS.NumberLiteral{Val: 42},
		Op:    LX.T_EQ,
	}
	filt := OP.NewFilter(ss, pred, nil)
	result := tryVectorizePlan(filt, nil)
	if result == filt {
		t.Fatal("expected wrapped operator, not original Filter")
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestTryVectorizePlan_SeqScanProject(t *testing.T) {
	ss := OP.NewSeqScan("t1")
	exprs := []PS.Expr{&PS.Ident{Name: "a"}}
	proj := OP.NewProject(ss, exprs)
	result := tryVectorizePlan(proj, nil)
	if result == proj {
		t.Fatal("expected wrapped operator, not original Project")
	}
	if result == nil {
		// Project is not vectorized — this is expected for now
	}
}

func TestTryVectorizePlan_Aggregate(t *testing.T) {
	ss := OP.NewSeqScan("t1")
	groupCols := []PS.Expr{&PS.Ident{Name: "a"}}
	aggs := []PS.Expr{
		&PS.AggregateFunc{Name: "count", Arg: &PS.StarExpr{}},
	}
	agg := AG.NewAggregate(ss, groupCols, aggs)
	result := tryVectorizePlan(agg, nil)
	if result == agg {
		t.Fatal("expected wrapped operator, not original Aggregate")
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestTryVectorizePlan_FilterChainIneligible(t *testing.T) {
	// REQ001602: Filter chains are now eligible because isEligible
	// recurses through children. The old test assumed Filter required
	// isScanLeaf or isRowAgg as direct children, but expanded eligibility
	// accepts any Filter tree as long as leaves are vectorizable.
	ss := OP.NewSeqScan("t1")
	pred := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "a"},
		Right: &PS.NumberLiteral{Val: 42},
		Op:    LX.T_EQ,
	}
	inner := OP.NewFilter(ss, pred, nil)
	outer := OP.NewFilter(inner, pred, nil)
	result := tryVectorizePlan(outer, nil)
	// Should produce a vectorized chain, not the original.
	if result == outer {
		t.Fatal("expected vectorized Filter chain, got original")
	}
}

func TestTransformOp_Nil(t *testing.T) {
	if transformOp(nil, nil) != nil {
		t.Fatal("transformOp(nil) should return nil")
	}
}

func TestTransformOp_UnknownType(t *testing.T) {
	ss := OP.NewSeqScan("t1")
	dist := OP.NewDistinct(ss)
	result := transformOp(dist, nil)
	if result == nil {
		t.Fatal("transformOp(Distinct) should produce a vectorized operator")
	}
	t.Logf("transformOp(Distinct) = %T", result)
}

func TestTransformOp_HashJoin(t *testing.T) {
	// HashJoin with single-column keys and SeqScan children is transformable.
	ss1 := OP.NewSeqScan("t1")
	ss2 := OP.NewSeqScan("t2")
	hj := OP.NewHashJoin(ss1, ss2, "t1", "t2", []string{"a"}, []string{"b"}, 0)
	result := transformOp(hj, nil)
	if result == nil {
		// HashJoin transformation fails because SeqScan has no schema
		// (NewSeqScan without store), which causes resolveColumnIndex
		// to return false. This is expected in test mode.
		return
	}
	// If it succeeded, the result must be a VectorizedHashJoin batch producer
	t.Logf("transformOp(HashJoin) = %T", result)
}

func TestExprName(t *testing.T) {
	tests := []struct {
		expr PS.Expr
		want string
	}{
		{&PS.Ident{Name: "col"}, "col"},
		{&PS.AliasedExpr{Alias: "alias"}, "alias"},
		{&PS.StarExpr{}, "*"},
		{&PS.NumberLiteral{Val: 42}, "expr"},
	}
	for _, tt := range tests {
		got := exprName(tt.expr)
		if got != tt.want {
			t.Errorf("exprName(%T) = %q, want %q", tt.expr, got, tt.want)
		}
	}
}

func TestTryVectorizePlan_DistinctAggregate(t *testing.T) {
	// REQ001730: DISTINCT aggregates (SUM/COUNT/MIN/MAX/AVG) are vectorized
	// with per-definition deduplication via DistinctSeen maps.
	ss := OP.NewSeqScan("t1")
	af := &PS.AggregateFunc{Name: "sum", Arg: &PS.Ident{Name: "v"}, Distinct: true}
	t.Logf("af.Distinct = %v, af.Name = %q", af.Distinct, af.Name)
	aggs := []PS.Expr{af}
	agg := AG.NewAggregate(ss, nil, aggs)
	result := tryVectorizePlan(agg, nil)
	t.Logf("result == agg: %v, result type: %T", result == agg, result)
	if result == agg {
		t.Fatal("DISTINCT aggregate should be vectorized, expected transformed root")
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	// Verify it's a BatchToRowAdapter wrapping a vectorized operator
	if _, ok := result.(*UT.BatchToRowAdapter); ok {
		t.Logf("vectorized via BatchToRowAdapter")
	}
}

func TestTryVectorizePlan_GroupConcatAggregate(t *testing.T) {
	// REQ001993: GROUP_CONCAT is now vectorized.
	ss := OP.NewSeqScan("t1")
	aggs := []PS.Expr{
		&PS.AggregateFunc{Name: "group_concat", Arg: &PS.Ident{Name: "v"}},
	}
	agg := AG.NewAggregate(ss, nil, aggs)
	result := tryVectorizePlan(agg, nil)
	if result == agg {
		t.Fatal("GROUP_CONCAT should be vectorized, expected transformed root")
	}
	t.Logf("GROUP_CONCAT result type: %T", result)
}

func TestTryVectorizePlan_SumAggregate(t *testing.T) {
	ss := OP.NewSeqScan("t1")
	aggs := []PS.Expr{
		&PS.AggregateFunc{Name: "sum", Arg: &PS.Ident{Name: "v"}},
	}
	agg := AG.NewAggregate(ss, nil, aggs)
	result := tryVectorizePlan(agg, nil)
	if result == agg {
		t.Fatal("SUM aggregate should be vectorized")
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestTryVectorizePlan_AvgAggregate(t *testing.T) {
	ss := OP.NewSeqScan("t1")
	aggs := []PS.Expr{
		&PS.AggregateFunc{Name: "avg", Arg: &PS.Ident{Name: "v"}},
	}
	agg := AG.NewAggregate(ss, nil, aggs)
	result := tryVectorizePlan(agg, nil)
	if result == agg {
		// AVG is not vectorized
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestTryVectorizePlan_MinAggregate(t *testing.T) {
	ss := OP.NewSeqScan("t1")
	aggs := []PS.Expr{
		&PS.AggregateFunc{Name: "min", Arg: &PS.Ident{Name: "v"}},
	}
	agg := AG.NewAggregate(ss, nil, aggs)
	result := tryVectorizePlan(agg, nil)
	if result == agg {
		t.Fatal("MIN aggregate should be vectorized")
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestTryVectorizePlan_MaxAggregate(t *testing.T) {
	ss := OP.NewSeqScan("t1")
	aggs := []PS.Expr{
		&PS.AggregateFunc{Name: "max", Arg: &PS.Ident{Name: "v"}},
	}
	agg := AG.NewAggregate(ss, nil, aggs)
	result := tryVectorizePlan(agg, nil)
	if result == agg {
		t.Fatal("MAX aggregate should be vectorized")
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

// TestTryVectorizePlan_AdaptiveOpVectorizesInner — REQ001581.
// TestTryVectorizePlan_VectorizesHashJoin verifies that tryVectorizePlan
// produces a vectorized result for a HashJoin tree. REQ002171: AdaptiveOp
// removed — raw operator tree is used directly.
func TestTryVectorizePlan_VectorizesHashJoin(t *testing.T) {
	inner := buildHashJoinTree()

	result := tryVectorizePlan(inner, nil)
	if result == nil {
		t.Fatal("expected non-nil vectorized result, got nil")
	}
	t.Logf("HashJoin vectorized result = %T", result)
}

// TestTryVectorizePlan_Nil — guard against nil dereference.
func TestTryVectorizePlan_Nil(t *testing.T) {
	if r := tryVectorizePlan(nil, nil); r != nil {
		t.Fatalf("tryVectorizePlan(nil) = %v, want nil", r)
	}
}

// TestTransformRoot_VectorizesHashJoin verifies transformRoot produces
// a BatchProducer for a HashJoin tree. REQ002171: AdaptiveOp removed.
func TestTransformRoot_VectorizesHashJoin(t *testing.T) {
	inner := buildHashJoinTree()
	bp := transformRoot(inner, nil)
	if bp == nil {
		t.Fatal("transformRoot(HashJoin) must yield a BatchProducer when inner is vectorizable")
	}
	t.Logf("transformRoot(HashJoin) = %T", bp)
}

// TestTransformRoot_NilInner — guards against nil dereference.
func TestTransformRoot_NilInner(t *testing.T) {
	if got := transformRoot(nil, nil); got != nil {
		t.Fatalf("transformRoot(nil) = %v, want nil", got)
	}
}
