package PX

import (
	"context"
	"testing"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

func TestLimitStageSpec_Category(t *testing.T) {
	spec := &LimitStageSpec{Limit: 10}
	if spec.Category() != CatTransform {
		t.Errorf("expected CatTransform, got %v", spec.Category())
	}
}

func TestLimitStage_NextBatch_WithinLimit(t *testing.T) {
	child := &mockStage{batches: []*UT.Batch{
		makeIntBatch([]int64{1, 2}),
		makeIntBatch([]int64{3, 4}),
	}}
	limit := &LimitStage{child: child, limit: 10, remaining: 10}
	defer limit.Close()

	batch, err := limit.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil || batch.Size != 2 {
		t.Fatalf("expected 2 rows, got %d", batch.Size)
	}

	batch, err = limit.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil || batch.Size != 2 {
		t.Fatalf("expected 2 rows, got %d", batch.Size)
	}
}

func TestLimitStage_NextBatch_ExactlyAtLimit(t *testing.T) {
	child := &mockStage{batches: []*UT.Batch{
		makeIntBatch([]int64{1, 2, 3}),
	}}
	limit := &LimitStage{child: child, limit: 3, remaining: 3}
	defer limit.Close()

	batch, err := limit.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil || batch.Size != 3 {
		t.Fatalf("expected 3 rows, got %d", batch.Size)
	}

	// Next call should return nil (limit exhausted).
	batch, err = limit.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch != nil {
		t.Fatal("expected nil batch after limit exhausted")
	}
}

func TestLimitStage_NextBatch_Truncation(t *testing.T) {
	child := &mockStage{batches: []*UT.Batch{
		makeIntBatch([]int64{1, 2, 3, 4, 5}),
	}}
	limit := &LimitStage{child: child, limit: 3, remaining: 3}
	defer limit.Close()

	batch, err := limit.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected non-nil batch")
	}
	if batch.Size != 3 {
		t.Fatalf("expected 3 rows after truncation, got %d", batch.Size)
	}
	if batch.Cols[0].Data.Ints[0] != 1 || batch.Cols[0].Data.Ints[2] != 3 {
		t.Fatalf("expected [1,2,3], got %v", batch.Cols[0].Data.Ints[:3])
	}
}

func TestLimitStage_NextBatch_Unlimited(t *testing.T) {
	child := &mockStage{batches: []*UT.Batch{
		makeIntBatch([]int64{1, 2, 3}),
	}}
	limit := &LimitStage{child: child, limit: -1, remaining: -1}
	defer limit.Close()

	batch, err := limit.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil || batch.Size != 3 {
		t.Fatalf("expected 3 rows (unlimited), got %d", batch.Size)
	}
}

func TestLimitStage_NextBatch_ZeroLimit(t *testing.T) {
	child := &mockStage{batches: []*UT.Batch{
		makeIntBatch([]int64{1, 2, 3}),
	}}
	limit := &LimitStage{child: child, limit: 0, remaining: 0}
	defer limit.Close()

	batch, err := limit.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch != nil {
		t.Fatal("expected nil batch with limit=0")
	}
}

func TestLimitStage_NextBatch_EOF(t *testing.T) {
	child := &mockStage{batches: nil}
	limit := &LimitStage{child: child, limit: 10, remaining: 10}
	defer limit.Close()

	batch, err := limit.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch != nil {
		t.Fatal("expected nil batch at EOF")
	}
}

func TestLimitStage_Reset(t *testing.T) {
	limit := &LimitStage{limit: 5, remaining: 0}
	err := limit.Reset(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if limit.remaining != 5 {
		t.Fatalf("expected remaining=5 after reset, got %d", limit.remaining)
	}
}

func TestLimitStage_SetChild(t *testing.T) {
	limit := &LimitStage{}
	child := &mockStage{}
	limit.SetChild(SingleChild, child)
	if limit.child != child {
		t.Fatal("expected child to be set")
	}
}

func TestLimitStage_Close(t *testing.T) {
	child := &mockStage{}
	limit := &LimitStage{child: child}
	if err := limit.Close(); err != nil {
		t.Fatal(err)
	}
	if !child.closed {
		t.Fatal("expected child to be closed")
	}
}

func TestLimitStageSpec_NewRuntime(t *testing.T) {
	spec := &LimitStageSpec{Limit: 42}
	stage := spec.NewRuntime().(*LimitStage)
	if stage.limit != 42 {
		t.Fatalf("expected limit=42, got %d", stage.limit)
	}
	if stage.remaining != 42 {
		t.Fatalf("expected remaining=42, got %d", stage.remaining)
	}
}
