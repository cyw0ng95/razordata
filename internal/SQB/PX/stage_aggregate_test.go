package PX

import (
	"context"
	"testing"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

func TestAggregateStageSpec_Category(t *testing.T) {
	spec := &AggregateStageSpec{Specs: []AccumulatorSpec{}}
	if spec.Category() != CatMapReduce {
		t.Errorf("expected CatMapReduce, got %v", spec.Category())
	}
}

func TestAggregateStage_ScalarCount(t *testing.T) {
	// SELECT COUNT(*) FROM t
	child := &mockStage{batches: []*UT.Batch{
		makeIntBatch([]int64{1, 2, 3}),
		makeIntBatch([]int64{4, 5}),
	}}
	spec := &AggregateStageSpec{
		Specs: []AccumulatorSpec{
			{Kind: AggCount, Col: -1}, // COUNT(*)
		},
	}
	stage := spec.NewRuntime().(*AggregateStage)
	stage.SetChild(SingleChild, child)
	defer stage.Close()

	batch, err := stage.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected non-nil result batch")
	}
	if batch.Size != 1 {
		t.Fatalf("expected 1 row (scalar), got %d", batch.Size)
	}
	// The count column should be 5.
	if len(batch.Cols) == 0 || len(batch.Cols[0].Data.Ints) == 0 {
		t.Fatal("expected count column with data")
	}
	if batch.Cols[0].Data.Ints[0] != 5 {
		t.Fatalf("expected COUNT(*)=5, got %d", batch.Cols[0].Data.Ints[0])
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

func TestAggregateStage_ScalarSum(t *testing.T) {
	child := &mockStage{batches: []*UT.Batch{
		makeIntBatch([]int64{10, 20, 30}),
	}}
	spec := &AggregateStageSpec{
		Specs: []AccumulatorSpec{
			{Kind: AggSum, Col: 0}, // SUM(v)
		},
	}
	stage := spec.NewRuntime().(*AggregateStage)
	stage.SetChild(SingleChild, child)
	defer stage.Close()

	batch, err := stage.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected non-nil result batch")
	}
	if batch.Cols[0].Data.Ints[0] != 60 {
		t.Fatalf("expected SUM(v)=60, got %d", batch.Cols[0].Data.Ints[0])
	}
}

func TestAggregateStage_EmptyInput(t *testing.T) {
	child := &mockStage{batches: nil}
	spec := &AggregateStageSpec{
		Specs: []AccumulatorSpec{
			{Kind: AggCount, Col: -1},
		},
	}
	stage := spec.NewRuntime().(*AggregateStage)
	stage.SetChild(SingleChild, child)
	defer stage.Close()

	batch, err := stage.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected non-nil result batch (scalar aggregate on empty set)")
	}
	// COUNT(*) of empty set should be 0.
	if batch.Cols[0].Data.Ints[0] != 0 {
		t.Fatalf("expected COUNT(*)=0 on empty, got %d", batch.Cols[0].Data.Ints[0])
	}
}

func TestAggregateStage_Reset(t *testing.T) {
	child := &mockStage{batches: []*UT.Batch{
		makeIntBatch([]int64{1, 2, 3}),
	}}
	spec := &AggregateStageSpec{
		Specs: []AccumulatorSpec{
			{Kind: AggCount, Col: -1},
		},
	}
	stage := spec.NewRuntime().(*AggregateStage)
	stage.SetChild(SingleChild, child)
	defer stage.Close()

	// First execution
	batch, err := stage.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil || batch.Cols[0].Data.Ints[0] != 3 {
		t.Fatalf("expected COUNT(*)=3, got %d", batch.Cols[0].Data.Ints[0])
	}

	// Reset the aggregate stage (clears MR state).
	// Note: the child mockStage is also consumed; in production,
	// Pipeline.Reset() handles recreating non-resettable children.
	// For this unit test, we manually set a fresh child.
	if err := stage.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	freshChild := &mockStage{batches: []*UT.Batch{
		makeIntBatch([]int64{4, 5}),
	}}
	stage.SetChild(SingleChild, freshChild)

	batch, err = stage.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil || batch.Cols[0].Data.Ints[0] != 2 {
		t.Fatalf("expected COUNT(*)=2 after reset, got %d", batch.Cols[0].Data.Ints[0])
	}
}

func TestAggregateStage_SetChild(t *testing.T) {
	stage := &AggregateStage{}
	child := &mockStage{}
	stage.SetChild(SingleChild, child)
	if stage.child != child {
		t.Fatal("expected child to be set")
	}
}

func TestAggregateStage_Close(t *testing.T) {
	child := &mockStage{}
	stage := &AggregateStage{child: child}
	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}
	if !child.closed {
		t.Fatal("expected child to be closed")
	}
}
