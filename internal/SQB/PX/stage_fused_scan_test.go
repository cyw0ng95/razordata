package PX

import (
	"context"
	"testing"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

func TestFusedScanStageSpec_Category(t *testing.T) {
	spec := &FusedScanStageSpec{SourceFactory: func() UT.BatchProducer {
		return &mockBatchProducer{}
	}}
	if spec.Category() != CatTransform {
		t.Errorf("expected CatTransform, got %v", spec.Category())
	}
}

func TestFusedScanStage_NextBatch_NoFilterNoProject(t *testing.T) {
	// Without filter or projection, it's just a passthrough.
	spec := &FusedScanStageSpec{
		SourceFactory: func() UT.BatchProducer {
			return &mockBatchProducer{batches: []*UT.Batch{
				makeIntBatch([]int64{1, 2, 3}),
				makeIntBatch([]int64{4, 5}),
			}}
		},
		Limit: -1,
	}
	stage := spec.NewRuntime()
	defer stage.Close()

	ctx := context.Background()

	// First batch
	batch, err := stage.NextBatch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil || batch.Size != 3 {
		t.Fatalf("expected 3 rows, got %d", batch.Size)
	}

	// Second batch
	batch, err = stage.NextBatch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil || batch.Size != 2 {
		t.Fatalf("expected 2 rows, got %d", batch.Size)
	}

	// EOF
	batch, err = stage.NextBatch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if batch != nil {
		t.Fatal("expected nil batch at EOF")
	}
}

func TestFusedScanStage_NextBatch_WithLimit(t *testing.T) {
	spec := &FusedScanStageSpec{
		SourceFactory: func() UT.BatchProducer {
			return &mockBatchProducer{batches: []*UT.Batch{
				makeIntBatch([]int64{1, 2, 3, 4, 5}),
			}}
		},
		Limit: 3,
	}
	stage := spec.NewRuntime().(*FusedScanStage)
	defer stage.Close()

	batch, err := stage.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected non-nil batch")
	}
	if batch.Size != 3 {
		t.Fatalf("expected 3 rows (limit), got %d", batch.Size)
	}

	// Next call should return nil (limit exhausted).
	batch, err = stage.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch != nil {
		t.Fatal("expected nil batch after limit exhausted")
	}
}

func TestFusedScanStage_NextBatch_LimitAcrossBatches(t *testing.T) {
	spec := &FusedScanStageSpec{
		SourceFactory: func() UT.BatchProducer {
			return &mockBatchProducer{batches: []*UT.Batch{
				makeIntBatch([]int64{1, 2}),
				makeIntBatch([]int64{3, 4, 5}),
			}}
		},
		Limit: 4,
	}
	stage := spec.NewRuntime().(*FusedScanStage)
	defer stage.Close()

	ctx := context.Background()

	// First batch passes through fully.
	batch, err := stage.NextBatch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil || batch.Size != 2 {
		t.Fatalf("expected 2 rows, got %d", batch.Size)
	}

	// Second batch should be truncated to 2 more rows.
	batch, err = stage.NextBatch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected non-nil batch")
	}
	if batch.Size != 2 {
		t.Fatalf("expected 2 rows (remaining limit), got %d", batch.Size)
	}
}

func TestFusedScanStage_NextBatch_EmptySource(t *testing.T) {
	spec := &FusedScanStageSpec{
		SourceFactory: func() UT.BatchProducer {
			return &mockBatchProducer{}
		},
		Limit: -1,
	}
	stage := spec.NewRuntime()
	defer stage.Close()

	batch, err := stage.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch != nil {
		t.Fatal("expected nil batch for empty source")
	}
}

func TestFusedScanStage_Reset_ReturnsErrResetNotSupported(t *testing.T) {
	spec := &FusedScanStageSpec{
		SourceFactory: func() UT.BatchProducer {
			return &mockBatchProducer{}
		},
		Limit: -1,
	}
	stage := spec.NewRuntime()
	defer stage.Close()

	err := stage.Reset(context.Background())
	if err != ErrResetNotSupported {
		t.Fatalf("expected ErrResetNotSupported, got %v", err)
	}
}

func TestFusedScanStage_Close(t *testing.T) {
	producer := &mockBatchProducer{}
	spec := &FusedScanStageSpec{
		SourceFactory: func() UT.BatchProducer {
			return producer
		},
		Limit: -1,
	}
	stage := spec.NewRuntime()

	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}
	if !producer.closed {
		t.Fatal("expected producer to be closed")
	}
}

func TestFusedScanStage_PropagateParams(t *testing.T) {
	fused := &FusedScanStage{}
	args := []any{1, 2}
	var buf []any
	fused.PropagateParams(args, &buf)
	if len(fused.params) != 2 {
		t.Fatalf("expected 2 params, got %d", len(fused.params))
	}
}

func TestTruncateBatchInPlace(t *testing.T) {
	batch := makeIntBatch([]int64{10, 20, 30, 40, 50})
	batch.Pooled = false

	truncateBatchInPlace(batch, 3)
	if batch.Size != 3 {
		t.Fatalf("expected 3 rows, got %d", batch.Size)
	}

	truncateBatchInPlace(batch, 0)
	if batch.Size != 0 {
		t.Fatalf("expected 0 rows, got %d", batch.Size)
	}
}

func TestTruncateBatchInPlace_WithSel(t *testing.T) {
	batch := makeIntBatch([]int64{10, 20, 30, 40, 50})
	batch.Pooled = false
	batch.Sel = []uint16{0, 2, 4, 1, 3}
	batch.Size = 5

	truncateBatchInPlace(batch, 3)
	if batch.Size != 3 {
		t.Fatalf("expected 3 rows, got %d", batch.Size)
	}
	if len(batch.Sel) != 3 {
		t.Fatalf("expected Sel len 3, got %d", len(batch.Sel))
	}
}

func TestTruncateBatchInPlace_Negative(t *testing.T) {
	batch := makeIntBatch([]int64{10, 20, 30})
	batch.Pooled = false
	original := batch.Size

	truncateBatchInPlace(batch, -1)
	if batch.Size != original {
		t.Fatalf("negative n should be no-op, got size %d", batch.Size)
	}
}
