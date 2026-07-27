package PX

import (
	"context"
	"testing"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

func TestDistinctStageSpec_Category(t *testing.T) {
	spec := &DistinctStageSpec{KeyCols: []int{0}}
	if spec.Category() != CatMapReduce {
		t.Errorf("expected CatMapReduce, got %v", spec.Category())
	}
}

func TestDistinctStage_Basic(t *testing.T) {
	child := &mockStage{batches: []*UT.Batch{
		makeIntBatch([]int64{1, 2, 3, 2, 1}),
	}}
	stage := &DistinctStage{
		child:   child,
		keyCols: []int{0},
	}
	defer stage.Close()

	batch, err := stage.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected non-nil batch")
	}
	if batch.Size != 3 {
		t.Fatalf("expected 3 unique rows, got %d", batch.Size)
	}
}

func TestDistinctStage_AllUnique(t *testing.T) {
	child := &mockStage{batches: []*UT.Batch{
		makeIntBatch([]int64{1, 2, 3}),
	}}
	stage := &DistinctStage{
		child:   child,
		keyCols: []int{0},
	}
	defer stage.Close()

	batch, err := stage.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected non-nil batch")
	}
	if batch.Size != 3 {
		t.Fatalf("expected 3 rows (all unique), got %d", batch.Size)
	}
}

func TestDistinctStage_AllDuplicates(t *testing.T) {
	child := &mockStage{batches: []*UT.Batch{
		makeIntBatch([]int64{7, 7, 7}),
	}}
	stage := &DistinctStage{
		child:   child,
		keyCols: []int{0},
	}
	defer stage.Close()

	batch, err := stage.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected non-nil batch")
	}
	if batch.Size != 1 {
		t.Fatalf("expected 1 unique row, got %d", batch.Size)
	}
	if batch.Cols[0].Data.Ints[0] != 7 {
		t.Fatalf("expected value 7, got %d", batch.Cols[0].Data.Ints[0])
	}
}

func TestDistinctStage_EmptyInput(t *testing.T) {
	child := &mockStage{batches: nil}
	stage := &DistinctStage{
		child:   child,
		keyCols: []int{0},
	}
	defer stage.Close()

	batch, err := stage.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch != nil {
		t.Fatal("expected nil batch for empty input")
	}
}

func TestDistinctStage_MultipleBatches(t *testing.T) {
	child := &mockStage{batches: []*UT.Batch{
		makeIntBatch([]int64{1, 2}),
		makeIntBatch([]int64{2, 3}),
		makeIntBatch([]int64{3, 1}),
	}}
	stage := &DistinctStage{
		child:   child,
		keyCols: []int{0},
	}
	defer stage.Close()

	batch, err := stage.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected non-nil batch")
	}
	if batch.Size != 3 {
		t.Fatalf("expected 3 unique rows (1,2,3), got %d", batch.Size)
	}
}

func TestDistinctStage_Reset(t *testing.T) {
	child := &mockStage{batches: []*UT.Batch{
		makeIntBatch([]int64{1, 2, 1}),
	}}
	stage := &DistinctStage{
		child:   child,
		keyCols: []int{0},
	}
	defer stage.Close()

	batch, _ := stage.NextBatch(context.Background())
	if batch == nil || batch.Size != 2 {
		t.Fatal("first execution failed")
	}

	if err := stage.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Reset clears the seen map and drained flag.
	if stage.drained {
		t.Fatal("expected drained=false after reset")
	}
	if stage.seen != nil {
		t.Fatal("expected seen=nil after reset")
	}
	if stage.result != nil {
		t.Fatal("expected result=nil after reset")
	}
	if stage.pos != 0 {
		t.Fatalf("expected pos=0 after reset, got %d", stage.pos)
	}
}

func TestDistinctStage_SetChild(t *testing.T) {
	stage := &DistinctStage{}
	child := &mockStage{}
	stage.SetChild(SingleChild, child)
	if stage.child != child {
		t.Fatal("expected child to be set")
	}
}

func TestDistinctStage_Close(t *testing.T) {
	child := &mockStage{}
	stage := &DistinctStage{child: child}
	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}
	if !child.closed {
		t.Fatal("expected child to be closed")
	}
}

func TestDistinctStageSpec_NewRuntime(t *testing.T) {
	spec := &DistinctStageSpec{KeyCols: []int{0, 1}}
	stage := spec.NewRuntime().(*DistinctStage)
	if len(stage.keyCols) != 2 {
		t.Fatalf("expected 2 keyCols, got %d", len(stage.keyCols))
	}
}
