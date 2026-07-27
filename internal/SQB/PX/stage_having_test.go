package PX

import (
	"context"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

func TestHavingStageSpec_Category(t *testing.T) {
	spec := &HavingStageSpec{Pred: &PS.BinaryExpr{
		Op: LX.T_GT, Left: &PS.Ident{Name: "v", SlotIdx: 0}, Right: &PS.NumberLiteral{Val: 1},
	}}
	if spec.Category() != CatTransform {
		t.Errorf("expected CatTransform, got %v", spec.Category())
	}
}

func TestHavingStage_NextBatch_AllMatch(t *testing.T) {
	child := &mockStage{batches: []*UT.Batch{
		makeIntBatch([]int64{5, 10, 15}),
	}}
	having := &HavingStage{
		child: child,
		pred: &PS.BinaryExpr{
			Op:    LX.T_GT,
			Left:  &PS.Ident{Name: "v", SlotIdx: 0},
			Right: &PS.NumberLiteral{Val: 2},
		},
	}
	defer having.Close()

	batch, err := having.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected non-nil batch")
	}
	if batch.Sel != nil {
		t.Fatal("expected nil Sel (all match)")
	}
	if batch.Size != 3 {
		t.Fatalf("expected 3 rows, got %d", batch.Size)
	}
}

func TestHavingStage_NextBatch_PartialMatch(t *testing.T) {
	child := &mockStage{batches: []*UT.Batch{
		makeIntBatch([]int64{1, 5, 2, 8}),
	}}
	having := &HavingStage{
		child: child,
		pred: &PS.BinaryExpr{
			Op:    LX.T_GT,
			Left:  &PS.Ident{Name: "v", SlotIdx: 0},
			Right: &PS.NumberLiteral{Val: 3},
		},
	}
	defer having.Close()

	batch, err := having.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected non-nil batch")
	}
	if batch.Sel == nil {
		t.Fatal("expected non-nil Sel for partial match")
	}
	if batch.Size != 2 {
		t.Fatalf("expected 2 rows (5,8), got %d", batch.Size)
	}
}

func TestHavingStage_NextBatch_NoMatch(t *testing.T) {
	child := &mockStage{batches: []*UT.Batch{
		makeIntBatch([]int64{1, 2}),
		makeIntBatch([]int64{10, 20}),
	}}
	having := &HavingStage{
		child: child,
		pred: &PS.BinaryExpr{
			Op:    LX.T_GT,
			Left:  &PS.Ident{Name: "v", SlotIdx: 0},
			Right: &PS.NumberLiteral{Val: 5},
		},
	}
	defer having.Close()

	// First batch has no matches, should be skipped.
	batch, err := having.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected non-nil batch (from second input)")
	}
	if batch.Size != 2 {
		t.Fatalf("expected 2 rows, got %d", batch.Size)
	}
}

func TestHavingStage_NextBatch_EOF(t *testing.T) {
	child := &mockStage{batches: nil}
	having := &HavingStage{
		child: child,
		pred:  &PS.BinaryExpr{Op: LX.T_GT, Left: &PS.Ident{Name: "v", SlotIdx: 0}, Right: &PS.NumberLiteral{Val: 0}},
	}
	defer having.Close()

	batch, err := having.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch != nil {
		t.Fatal("expected nil batch at EOF")
	}
}

func TestHavingStage_Reset(t *testing.T) {
	having := &HavingStage{params: []any{1, 2, 3}}
	err := having.Reset(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if having.params != nil {
		t.Fatal("expected params to be nil after reset")
	}
}

func TestHavingStage_SetChild(t *testing.T) {
	having := &HavingStage{}
	child := &mockStage{}
	having.SetChild(SingleChild, child)
	if having.child != child {
		t.Fatal("expected child to be set")
	}
}

func TestHavingStage_PropagateParams(t *testing.T) {
	having := &HavingStage{}
	args := []any{42}
	var buf []any
	having.PropagateParams(args, &buf)
	if len(having.params) != 1 || having.params[0] != 42 {
		t.Fatal("params not propagated correctly")
	}
}

func TestHavingStage_Close(t *testing.T) {
	child := &mockStage{}
	having := &HavingStage{child: child}
	if err := having.Close(); err != nil {
		t.Fatal(err)
	}
	if !child.closed {
		t.Fatal("expected child to be closed")
	}
}

func TestHavingStageSpec_NewRuntime(t *testing.T) {
	pred := &PS.BinaryExpr{Op: LX.T_GT, Left: &PS.Ident{Name: "v", SlotIdx: 0}, Right: &PS.NumberLiteral{Val: 0}}
	spec := &HavingStageSpec{Pred: pred}
	stage := spec.NewRuntime().(*HavingStage)
	if stage.pred != pred {
		t.Fatal("expected pred to be set")
	}
}
