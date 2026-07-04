package EX

import (
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQB/AG"
	"github.com/cyw0ng95/razordata/internal/SQB/OP"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func TestTryVectorizePlan_Ineligible(t *testing.T) {
	// Tree with HashJoin is not vectorizable — should return original root.
	left := OP.NewSeqScan("t1")
	right := OP.NewSeqScan("t2")
	hj := OP.NewHashJoin(left, right, "t1", "t2", []string{"a"}, []string{"a"}, 0)
	result := tryVectorizePlan(hj)
	if result != hj {
		t.Fatal("expected original HashJoin root returned unchanged")
	}
}

func TestTryVectorizePlan_IneligibleDistinct(t *testing.T) {
	child := OP.NewSeqScan("t1")
	dist := OP.NewDistinct(child)
	result := tryVectorizePlan(dist)
	if result != dist {
		t.Fatal("expected original Distinct root returned unchanged")
	}
}

func TestTryVectorizePlan_SeqScanOnly(t *testing.T) {
	ss := OP.NewSeqScan("t1")
	result := tryVectorizePlan(ss)
	if result == ss {
		t.Fatal("expected wrapped operator, not original SeqScan")
	}
	// Result should be a BatchToRowAdapter wrapping VectorizedSeqScan.
	// Verify it implements pl.Operator (Next method exists).
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestTryVectorizePlan_SeqScanFilter(t *testing.T) {
	ss := OP.NewSeqScan("t1")
	pred := &PS.BoolLiteral{Val: true}
	filt := OP.NewFilter(ss, pred)
	result := tryVectorizePlan(filt)
	if result == filt {
		t.Fatal("expected wrapped operator, not original Filter")
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestTryVectorizePlan_SeqScanProject(t *testing.T) {
	ss := OP.NewSeqScan("t1")
	exprs := []PS.Expr{&PS.StarExpr{}}
	proj := OP.NewProject(ss, exprs)
	result := tryVectorizePlan(proj)
	if result == proj {
		t.Fatal("expected wrapped operator, not original Project")
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestTryVectorizePlan_Aggregate(t *testing.T) {
	ss := OP.NewSeqScan("t1")
	groupCols := []PS.Expr{&PS.Ident{Name: "a"}}
	aggs := []PS.Expr{
		&PS.AggregateFunc{Name: "count", Arg: &PS.StarExpr{}},
	}
	agg := AG.NewAggregate(ss, groupCols, aggs)
	result := tryVectorizePlan(agg)
	if result == agg {
		t.Fatal("expected wrapped operator, not original Aggregate")
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestTryVectorizePlan_FilterChainIneligible(t *testing.T) {
	// Filter(Filter(SeqScan)) — nested filters can't be vectorized
	// because VectorizedFilter requires *VectorizedSeqScan as child.
	ss := OP.NewSeqScan("t1")
	pred := &PS.BoolLiteral{Val: true}
	inner := OP.NewFilter(ss, pred)
	outer := OP.NewFilter(inner, pred)
	result := tryVectorizePlan(outer)
	if result != outer {
		t.Fatal("expected original root returned for nested Filter chain")
	}
}

func TestIsEligible_Nil(t *testing.T) {
	if !isEligible(nil) {
		t.Fatal("nil operator should be eligible")
	}
}

func TestIsEligible_ProjectOverFilter(t *testing.T) {
	// Project(Filter(SeqScan)) should be eligible.
	ss := OP.NewSeqScan("t1")
	pred := &PS.BoolLiteral{Val: true}
	filt := OP.NewFilter(ss, pred)
	exprs := []PS.Expr{&PS.StarExpr{}}
	proj := OP.NewProject(filt, exprs)
	if !isEligible(proj) {
		t.Fatal("Project(Filter(SeqScan)) should be eligible")
	}
}

func TestIsEligible_AggregateOverFilter(t *testing.T) {
	// Aggregate(Filter(SeqScan)) should be eligible.
	ss := OP.NewSeqScan("t1")
	pred := &PS.BoolLiteral{Val: true}
	filt := OP.NewFilter(ss, pred)
	groupCols := []PS.Expr{&PS.Ident{Name: "a"}}
	aggs := []PS.Expr{
		&PS.AggregateFunc{Name: "count", Arg: &PS.StarExpr{}},
	}
	agg := AG.NewAggregate(filt, groupCols, aggs)
	if !isEligible(agg) {
		t.Fatal("Aggregate(Filter(SeqScan)) should be eligible")
	}
}

func TestTransformOp_Nil(t *testing.T) {
	if transformOp(nil) != nil {
		t.Fatal("transformOp(nil) should return nil")
	}
}

func TestTransformOp_UnknownType(t *testing.T) {
	// A HashJoin is not transformable.
	left := OP.NewSeqScan("t1")
	right := OP.NewSeqScan("t2")
	hj := OP.NewHashJoin(left, right, "t1", "t2", []string{"a"}, []string{"a"}, 0)
	if transformOp(hj) != nil {
		t.Fatal("transformOp(HashJoin) should return nil")
	}
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
