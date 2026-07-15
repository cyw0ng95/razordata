package PF

import (
	"testing"

	OC "github.com/cyw0ng95/razordata/internal/SQO/OC"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

func TestSortElimination_SameOrder_RemovesOuter(t *testing.T) {
	innerSort := &noopOp{
		name:    "sort",
		orderBy: []pl.OrderSpec{{Col: "a"}, {Col: "b", Desc: true}},
	}
	scan := &noopOp{name: "seqscan"}
	innerSort.SetChild(scan)

	outerSort := &noopOp{
		name:    "sort",
		orderBy: []pl.OrderSpec{{Col: "a"}, {Col: "b", Desc: true}},
	}
	outerSort.SetChild(innerSort)

	pass := &SortEliminationPass{}
	plan, err := pass.Apply(&OC.Plan{Root: outerSort}, &OC.Context{})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	if plan == nil || plan.Root == nil {
		t.Fatal("Apply returned nil plan")
	}
	result, ok := plan.Root.(*noopOp)
	if !ok {
		t.Fatalf("root = %T, want *noopOp", plan.Root)
	}
	if result.name != "sort" {
		t.Errorf("root name = %q, want sort (inner)", result.name)
	}
	if result.child == nil {
		t.Fatal("inner sort child is nil")
	}
	if result.child.(*noopOp).name != "seqscan" {
		t.Errorf("inner sort child = %q, want seqscan", result.child.(*noopOp).name)
	}
}

func TestSortElimination_DifferentOrder_KeepsBoth(t *testing.T) {
	innerSort := &noopOp{
		name:    "sort",
		orderBy: []pl.OrderSpec{{Col: "a"}},
	}
	scan := &noopOp{name: "seqscan"}
	innerSort.SetChild(scan)

	outerSort := &noopOp{
		name:    "sort",
		orderBy: []pl.OrderSpec{{Col: "b"}},
	}
	outerSort.SetChild(innerSort)

	pass := &SortEliminationPass{}
	plan, err := pass.Apply(&OC.Plan{Root: outerSort}, &OC.Context{})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	if plan == nil || plan.Root == nil {
		t.Fatal("Apply returned nil plan")
	}
	result, ok := plan.Root.(*noopOp)
	if !ok {
		t.Fatalf("root = %T, want *noopOp", plan.Root)
	}
	if result.name != "sort" {
		t.Errorf("root name = %q, want sort (outer)", result.name)
	}
	if result.child == nil {
		t.Fatal("outer sort child is nil")
	}
	if result.child.(*noopOp).name != "sort" {
		t.Errorf("outer sort child = %q, want sort (inner)", result.child.(*noopOp).name)
	}
}

func TestSortElimination_NilPlan(t *testing.T) {
	pass := &SortEliminationPass{}
	result, err := pass.Apply(nil, &OC.Context{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Error("expected nil result for nil plan")
	}
}

func TestSortElimination_NoSortChild(t *testing.T) {
	scan := &noopOp{name: "seqscan"}
	sort := &noopOp{
		name:    "sort",
		orderBy: []pl.OrderSpec{{Col: "a"}},
	}
	sort.SetChild(scan)

	pass := &SortEliminationPass{}
	plan, err := pass.Apply(&OC.Plan{Root: sort}, &OC.Context{})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	if plan == nil || plan.Root == nil {
		t.Fatal("Apply returned nil plan")
	}
	result, ok := plan.Root.(*noopOp)
	if !ok {
		t.Fatalf("root = %T, want *noopOp", plan.Root)
	}
	if result.name != "sort" {
		t.Errorf("sort should be unchanged, got name=%q", result.name)
	}
	if result.child != scan {
		t.Error("sort child should be unchanged")
	}
}

func TestSortElimination_DescMismatch(t *testing.T) {
	innerSort := &noopOp{
		name:    "sort",
		orderBy: []pl.OrderSpec{{Col: "a", Desc: false}},
	}
	scan := &noopOp{name: "seqscan"}
	innerSort.SetChild(scan)

	outerSort := &noopOp{
		name:    "sort",
		orderBy: []pl.OrderSpec{{Col: "a", Desc: true}},
	}
	outerSort.SetChild(innerSort)

	pass := &SortEliminationPass{}
	plan, err := pass.Apply(&OC.Plan{Root: outerSort}, &OC.Context{})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	result, ok := plan.Root.(*noopOp)
	if !ok {
		t.Fatalf("root = %T, want *noopOp", plan.Root)
	}
	if result.name != "sort" {
		t.Errorf("root name = %q, want sort (outer)", result.name)
	}
	if result.child == nil || result.child.(*noopOp).name != "sort" {
		t.Error("inner sort should be preserved when desc differs")
	}
}

func TestSortElimination_LengthMismatch(t *testing.T) {
	innerSort := &noopOp{
		name:    "sort",
		orderBy: []pl.OrderSpec{{Col: "a"}},
	}
	scan := &noopOp{name: "seqscan"}
	innerSort.SetChild(scan)

	outerSort := &noopOp{
		name:    "sort",
		orderBy: []pl.OrderSpec{{Col: "a"}, {Col: "b"}},
	}
	outerSort.SetChild(innerSort)

	pass := &SortEliminationPass{}
	plan, err := pass.Apply(&OC.Plan{Root: outerSort}, &OC.Context{})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	result, ok := plan.Root.(*noopOp)
	if !ok {
		t.Fatalf("root = %T, want *noopOp", plan.Root)
	}
	if result.name != "sort" {
		t.Errorf("root name = %q, want sort (outer)", result.name)
	}
}

func TestSortElimination_NilRoot(t *testing.T) {
	pass := &SortEliminationPass{}
	plan, err := pass.Apply(&OC.Plan{Root: nil}, &OC.Context{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan == nil || plan.Root != nil {
		t.Error("expected nil root for nil input")
	}
}

func TestSortElimination_ThreeLevelSameOrder(t *testing.T) {
	innerScan := &noopOp{name: "seqscan"}
	midSort := &noopOp{
		name:    "sort",
		orderBy: []pl.OrderSpec{{Col: "x"}},
	}
	midSort.SetChild(innerScan)

	outerSort := &noopOp{
		name:    "sort",
		orderBy: []pl.OrderSpec{{Col: "x"}},
	}
	outerSort.SetChild(midSort)

	pass := &SortEliminationPass{}
	plan, err := pass.Apply(&OC.Plan{Root: outerSort}, &OC.Context{})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	result, ok := plan.Root.(*noopOp)
	if !ok {
		t.Fatalf("root = %T, want *noopOp", plan.Root)
	}
	if result.name != "sort" {
		t.Errorf("root name = %q, want sort (mid)", result.name)
	}
	if result.child == nil || result.child.(*noopOp).name != "seqscan" {
		t.Error("outer sort removed, mid sort now points to scan")
	}
}

func TestSortOrdersMatch_True(t *testing.T) {
	a := []pl.OrderSpec{{Col: "a"}, {Col: "b", Desc: true}}
	b := []pl.OrderSpec{{Col: "a"}, {Col: "b", Desc: true}}
	if !sortOrdersMatch(a, b) {
		t.Error("expected true for identical specs")
	}
}

func TestSortOrdersMatch_DiffLen(t *testing.T) {
	a := []pl.OrderSpec{{Col: "a"}}
	b := []pl.OrderSpec{{Col: "a"}, {Col: "b"}}
	if sortOrdersMatch(a, b) {
		t.Error("expected false for different lengths")
	}
}

func TestSortOrdersMatch_EmptyVsNil(t *testing.T) {
	a := []pl.OrderSpec{}
	var b []pl.OrderSpec
	if !sortOrdersMatch(a, b) {
		t.Error("expected true for empty vs nil")
	}
}
