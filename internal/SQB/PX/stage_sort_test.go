package PX

import (
	"context"
	"testing"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

func TestSortStageSpec_Category(t *testing.T) {
	spec := &SortStageSpec{SortCols: []int{0}, Desc: []bool{false}}
	if spec.Category() != CatMapReduce {
		t.Errorf("expected CatMapReduce, got %v", spec.Category())
	}
}

func TestSortStage_Ascending(t *testing.T) {
	child := &mockStage{batches: []*UT.Batch{
		makeIntBatch([]int64{5, 1, 3, 2, 4}),
	}}
	stage := &SortStage{
		child:     child,
		sortCols:  []int{0},
		desc:      []bool{false},
		batchSize: 1024,
	}
	defer stage.Close()

	batch, err := stage.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected non-nil batch")
	}
	if batch.Size != 5 {
		t.Fatalf("expected 5 rows, got %d", batch.Size)
	}
	expected := []int64{1, 2, 3, 4, 5}
	for i, v := range expected {
		if batch.Cols[0].Data.Ints[i] != v {
			t.Fatalf("row %d: expected %d, got %d", i, v, batch.Cols[0].Data.Ints[i])
		}
	}
}

func TestSortStage_Descending(t *testing.T) {
	child := &mockStage{batches: []*UT.Batch{
		makeIntBatch([]int64{1, 3, 2}),
	}}
	stage := &SortStage{
		child:     child,
		sortCols:  []int{0},
		desc:      []bool{true},
		batchSize: 1024,
	}
	defer stage.Close()

	batch, err := stage.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected non-nil batch")
	}
	expected := []int64{3, 2, 1}
	for i, v := range expected {
		if batch.Cols[0].Data.Ints[i] != v {
			t.Fatalf("row %d: expected %d, got %d", i, v, batch.Cols[0].Data.Ints[i])
		}
	}
}

func TestSortStage_MultipleBatches(t *testing.T) {
	child := &mockStage{batches: []*UT.Batch{
		makeIntBatch([]int64{5, 1}),
		makeIntBatch([]int64{3, 2, 4}),
	}}
	stage := &SortStage{
		child:     child,
		sortCols:  []int{0},
		desc:      []bool{false},
		batchSize: 3, // small batch size to produce multiple output batches
	}
	defer stage.Close()

	// First batch: [1, 2, 3]
	batch, err := stage.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil || batch.Size != 3 {
		t.Fatalf("expected 3 rows, got %d", batch.Size)
	}
	if batch.Cols[0].Data.Ints[0] != 1 || batch.Cols[0].Data.Ints[2] != 3 {
		t.Fatalf("expected [1,2,3], got %v", batch.Cols[0].Data.Ints[:3])
	}

	// Second batch: [4, 5]
	batch, err = stage.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil || batch.Size != 2 {
		t.Fatalf("expected 2 rows, got %d", batch.Size)
	}
	if batch.Cols[0].Data.Ints[0] != 4 || batch.Cols[0].Data.Ints[1] != 5 {
		t.Fatalf("expected [4,5], got %v", batch.Cols[0].Data.Ints[:2])
	}

	// EOF
	batch, err = stage.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch != nil {
		t.Fatal("expected nil at EOF")
	}
}

func TestSortStage_EmptyInput(t *testing.T) {
	child := &mockStage{batches: nil}
	stage := &SortStage{
		child:     child,
		sortCols:  []int{0},
		desc:      []bool{false},
		batchSize: 1024,
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

func TestSortStage_Reset(t *testing.T) {
	child := &mockStage{batches: []*UT.Batch{
		makeIntBatch([]int64{3, 1, 2}),
	}}
	stage := &SortStage{
		child:     child,
		sortCols:  []int{0},
		desc:      []bool{false},
		batchSize: 1024,
	}
	defer stage.Close()

	batch, _ := stage.NextBatch(context.Background())
	if batch == nil || batch.Cols[0].Data.Ints[0] != 1 {
		t.Fatal("first execution failed")
	}

	if err := stage.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Reset clears the drained flag and result, but the child is consumed.
	// Verify that the state is cleared correctly.
	if stage.drained {
		t.Fatal("expected drained=false after reset")
	}
	if stage.result != nil {
		t.Fatal("expected result=nil after reset")
	}
	if stage.pos != 0 {
		t.Fatalf("expected pos=0 after reset, got %d", stage.pos)
	}
}

func TestSortStage_SetChild(t *testing.T) {
	stage := &SortStage{}
	child := &mockStage{}
	stage.SetChild(SingleChild, child)
	if stage.child != child {
		t.Fatal("expected child to be set")
	}
}

func TestSortStage_Close(t *testing.T) {
	child := &mockStage{}
	stage := &SortStage{child: child}
	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}
	if !child.closed {
		t.Fatal("expected child to be closed")
	}
}

func TestSortStageSpec_NewRuntime(t *testing.T) {
	spec := &SortStageSpec{SortCols: []int{0}, Desc: []bool{false}, BatchSize: 512}
	stage := spec.NewRuntime().(*SortStage)
	if stage.batchSize != 512 {
		t.Fatalf("expected batchSize=512, got %d", stage.batchSize)
	}
}

func TestSortStageSpec_NewRuntime_DefaultBatchSize(t *testing.T) {
	spec := &SortStageSpec{SortCols: []int{0}, Desc: []bool{false}}
	stage := spec.NewRuntime().(*SortStage)
	if stage.batchSize != UT.BatchSize {
		t.Fatalf("expected batchSize=%d, got %d", UT.BatchSize, stage.batchSize)
	}
}
