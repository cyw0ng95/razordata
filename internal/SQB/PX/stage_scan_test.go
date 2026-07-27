package PX

import (
	"context"
	"testing"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// mockBatchProducer implements UT.BatchProducer for testing ScanStage.
type mockBatchProducer struct {
	batches []*UT.Batch
	idx     int
	closed  bool
}

func (m *mockBatchProducer) NextBatch(_ context.Context) (*UT.Batch, error) {
	if m.idx >= len(m.batches) {
		return nil, nil
	}
	b := m.batches[m.idx]
	m.idx++
	return b, nil
}

func (m *mockBatchProducer) Close() error {
	m.closed = true
	return nil
}

func TestScanStageSpec_Category(t *testing.T) {
	spec := &ScanStageSpec{NewProducer: func() UT.BatchProducer {
		return &mockBatchProducer{}
	}}
	if spec.Category() != CatSource {
		t.Errorf("expected CatSource, got %v", spec.Category())
	}
}

func TestScanStage_NextBatch(t *testing.T) {
	b1 := makeIntBatch([]int64{1, 2, 3})
	b2 := makeIntBatch([]int64{4, 5})
	spec := &ScanStageSpec{NewProducer: func() UT.BatchProducer {
		return &mockBatchProducer{batches: []*UT.Batch{b1, b2}}
	}}
	stage := spec.NewRuntime()
	defer stage.Close()

	ctx := context.Background()

	// First batch
	batch, err := stage.NextBatch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected non-nil batch")
	}
	if batch.Size != 3 {
		t.Fatalf("expected 3 rows, got %d", batch.Size)
	}

	// Second batch
	batch, err = stage.NextBatch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected non-nil batch")
	}
	if batch.Size != 2 {
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

func TestScanStage_NextBatch_Empty(t *testing.T) {
	spec := &ScanStageSpec{NewProducer: func() UT.BatchProducer {
		return &mockBatchProducer{}
	}}
	stage := spec.NewRuntime()
	defer stage.Close()

	batch, err := stage.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch != nil {
		t.Fatal("expected nil batch for empty producer")
	}
}

func TestScanStage_Reset_ReturnsErrResetNotSupported(t *testing.T) {
	spec := &ScanStageSpec{NewProducer: func() UT.BatchProducer {
		return &mockBatchProducer{}
	}}
	stage := spec.NewRuntime()
	defer stage.Close()

	err := stage.Reset(context.Background())
	if err != ErrResetNotSupported {
		t.Fatalf("expected ErrResetNotSupported, got %v", err)
	}
}

func TestScanStage_Close(t *testing.T) {
	producer := &mockBatchProducer{}
	spec := &ScanStageSpec{NewProducer: func() UT.BatchProducer {
		return producer
	}}
	stage := spec.NewRuntime()

	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}
	if !producer.closed {
		t.Fatal("expected producer to be closed")
	}
}

func TestScanStage_Close_Idempotent(t *testing.T) {
	spec := &ScanStageSpec{NewProducer: func() UT.BatchProducer {
		return &mockBatchProducer{}
	}}
	stage := spec.NewRuntime()

	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}
}
