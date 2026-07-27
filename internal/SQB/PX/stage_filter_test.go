package PX

import (
	"context"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

func TestFilterStageSpec_Category(t *testing.T) {
	spec := &FilterStageSpec{Pred: &PS.BinaryExpr{
		Op: LX.T_GT, Left: &PS.Ident{Name: "v", SlotIdx: 0}, Right: &PS.NumberLiteral{Val: 2},
	}}
	if spec.Category() != CatTransform {
		t.Errorf("expected CatTransform, got %v", spec.Category())
	}
}

func TestFilterStage_NextBatch_AllMatch(t *testing.T) {
	child := &mockStage{batches: []*UT.Batch{
		makeIntBatch([]int64{5, 6, 7}),
	}}
	filter := &FilterStage{
		child: child,
		pred: &PS.BinaryExpr{
			Op:    LX.T_GT,
			Left:  &PS.Ident{Name: "v", SlotIdx: 0},
			Right: &PS.NumberLiteral{Val: 2},
		},
	}
	defer filter.Close()

	batch, err := filter.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected non-nil batch")
	}
	// All values > 2, so all rows match (nil Sel means all match).
	if batch.Sel != nil {
		t.Fatalf("expected nil Sel (all match), got Sel with %d entries", len(batch.Sel))
	}
	if batch.Size != 3 {
		t.Fatalf("expected 3 rows, got %d", batch.Size)
	}
}

func TestFilterStage_NextBatch_PartialMatch(t *testing.T) {
	child := &mockStage{batches: []*UT.Batch{
		makeIntBatch([]int64{1, 5, 2, 8}),
	}}
	filter := &FilterStage{
		child: child,
		pred: &PS.BinaryExpr{
			Op:    LX.T_GT,
			Left:  &PS.Ident{Name: "v", SlotIdx: 0},
			Right: &PS.NumberLiteral{Val: 3},
		},
	}
	defer filter.Close()

	batch, err := filter.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected non-nil batch")
	}
	// Only 5 and 8 > 3, so 2 rows should match.
	if batch.Sel == nil {
		t.Fatal("expected non-nil Sel for partial match")
	}
	if batch.Size != 2 {
		t.Fatalf("expected 2 rows, got %d", batch.Size)
	}
}

func TestFilterStage_NextBatch_NoMatch(t *testing.T) {
	child := &mockStage{batches: []*UT.Batch{
		makeIntBatch([]int64{1, 2, 3}),
		makeIntBatch([]int64{10, 20}),
	}}
	filter := &FilterStage{
		child: child,
		pred: &PS.BinaryExpr{
			Op:    LX.T_GT,
			Left:  &PS.Ident{Name: "v", SlotIdx: 0},
			Right: &PS.NumberLiteral{Val: 5},
		},
	}
	defer filter.Close()

	// First batch has no matches, should be skipped.
	batch, err := filter.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected non-nil batch (from second input batch)")
	}
	// Second batch: 10 and 20 > 5 → both match.
	if batch.Size != 2 {
		t.Fatalf("expected 2 rows, got %d", batch.Size)
	}
}

func TestFilterStage_NextBatch_EOF(t *testing.T) {
	child := &mockStage{batches: nil}
	filter := &FilterStage{
		child: child,
		pred: &PS.BinaryExpr{
			Op:    LX.T_GT,
			Left:  &PS.Ident{Name: "v", SlotIdx: 0},
			Right: &PS.NumberLiteral{Val: 0},
		},
	}
	defer filter.Close()

	batch, err := filter.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch != nil {
		t.Fatal("expected nil batch at EOF")
	}
}

func TestFilterStage_Reset(t *testing.T) {
	filter := &FilterStage{
		pred:      &PS.BinaryExpr{Op: LX.T_GT, Left: &PS.Ident{Name: "v", SlotIdx: 0}, Right: &PS.NumberLiteral{Val: 0}},
		params:    []any{1, 2},
		inColIdx:  3,
	}
	err := filter.Reset(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if filter.params != nil {
		t.Fatal("expected params to be nil after reset")
	}
	if filter.inColIdx != -1 {
		t.Fatalf("expected inColIdx=-1 after reset, got %d", filter.inColIdx)
	}
}

func TestFilterStage_SetChild(t *testing.T) {
	filter := &FilterStage{}
	child := &mockStage{}
	filter.SetChild(SingleChild, child)
	if filter.child != child {
		t.Fatal("expected child to be set")
	}
}

func TestFilterStage_PropagateParams(t *testing.T) {
	filter := &FilterStage{}
	args := []any{42, "hello"}
	var buf []any
	filter.PropagateParams(args, &buf)
	if len(filter.params) != 2 {
		t.Fatalf("expected 2 params, got %d", len(filter.params))
	}
	if filter.params[0] != 42 || filter.params[1] != "hello" {
		t.Fatal("params not propagated correctly")
	}
}

func TestFilterStage_Close(t *testing.T) {
	child := &mockStage{}
	filter := &FilterStage{child: child}
	if err := filter.Close(); err != nil {
		t.Fatal(err)
	}
	if !child.closed {
		t.Fatal("expected child to be closed")
	}
}

func TestFilterStage_BloomFilter(t *testing.T) {
	// Test that FilterStageSpec creates a bloom filter when configured.
	spec := &FilterStageSpec{
		Pred:      &PS.BinaryExpr{Op: LX.T_GT, Left: &PS.Ident{Name: "v", SlotIdx: 0}, Right: &PS.NumberLiteral{Val: 0}},
		HasBloom:  true,
		BloomVals: []int64{1, 2, 3, 4, 5, 6, 7, 8, 9}, // 9 values > 8 threshold
	}
	stage := spec.NewRuntime().(*FilterStage)
	if stage.inBloom == nil {
		t.Fatal("expected bloom filter to be created")
	}
}

func TestFilterStage_NoBloomFilter_WhenValsTooFew(t *testing.T) {
	spec := &FilterStageSpec{
		Pred:      &PS.BinaryExpr{Op: LX.T_GT, Left: &PS.Ident{Name: "v", SlotIdx: 0}, Right: &PS.NumberLiteral{Val: 0}},
		HasBloom:  true,
		BloomVals: []int64{1, 2, 3}, // only 3 values < 8 threshold
	}
	stage := spec.NewRuntime().(*FilterStage)
	if stage.inBloom != nil {
		t.Fatal("expected no bloom filter with few values")
	}
}
