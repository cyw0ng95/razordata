package PX

import (
	"context"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	PL "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

func TestLegacyBatchStageSpec_NewRuntime(t *testing.T) {
	batches := []*UT.Batch{makeIntBatch([]int64{1, 2, 3})}
	spec := &LegacyBatchStageSpec{
		Specialize: func(_ DT.Operator, _ PL.QueryPlanner) UT.BatchProducer {
			return &simpleProducer{batches: batches}
		},
	}

	stage := spec.NewRuntime()
	if stage == nil {
		t.Fatal("expected non-nil stage")
	}
	if stage.(*LegacyBatchStage) == nil {
		t.Fatal("expected *LegacyBatchStage")
	}
}

func TestLegacyBatchStage_NextBatch(t *testing.T) {
	batches := []*UT.Batch{makeIntBatch([]int64{1, 2})}
	stage := &LegacyBatchStage{producer: &simpleProducer{batches: batches}}

	ctx := context.Background()
	batch, err := stage.NextBatch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected batch")
	}
	if batch.Size != 2 {
		t.Fatalf("expected 2 rows, got %d", batch.Size)
	}

	// EOF
	batch2, err := stage.NextBatch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if batch2 != nil {
		t.Fatal("expected EOF")
	}
}

func TestLegacyBatchStage_Reset_NotSupported(t *testing.T) {
	stage := &LegacyBatchStage{producer: &simpleProducer{}}
	err := stage.Reset(context.Background())
	if err != ErrResetNotSupported {
		t.Fatalf("expected ErrResetNotSupported, got %v", err)
	}
}

func TestLegacyBatchStage_Close_Idempotent(t *testing.T) {
	stage := &LegacyBatchStage{producer: &simpleProducer{}}
	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyBatchStage_NextBatch_Closed(t *testing.T) {
	stage := &LegacyBatchStage{producer: &simpleProducer{}, closed: true}
	_, err := stage.NextBatch(context.Background())
	if err == nil {
		t.Fatal("expected error on closed stage")
	}
}

func TestLegacyBatchStageSpec_Category(t *testing.T) {
	spec := &LegacyBatchStageSpec{}
	if spec.Category() != CatSource {
		t.Fatalf("expected CatSource, got %v", spec.Category())
	}
}

// --- simpleProducer: minimal BatchProducer for testing ---

type simpleProducer struct {
	batches []*UT.Batch
	idx     int
}

func (p *simpleProducer) NextBatch(_ context.Context) (*UT.Batch, error) {
	if p.idx >= len(p.batches) {
		return nil, nil
	}
	b := p.batches[p.idx]
	p.idx++
	return b, nil
}

func (p *simpleProducer) Close() error { return nil }
