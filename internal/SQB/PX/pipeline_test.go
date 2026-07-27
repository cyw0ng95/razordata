package PX

import (
	"context"
	"testing"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// Reuse makeIntBatch from stage_test.go (same package).

func TestPipeline_Execute_SingleStage(t *testing.T) {
	spec := &PipelineSpec{
		Stages: []StageSpec{
			&mockStageSpec{
				batches: []*UT.Batch{
					makeIntBatch([]int64{1, 2, 3}),
					makeIntBatch([]int64{4, 5}),
				},
				cat: CatSource,
			},
		},
		RootIdx: 0,
	}

	pipeline, err := spec.NewRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer pipeline.Close()

	rows, err := pipeline.Execute(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 5 {
		t.Fatalf("expected 5 rows, got %d", len(rows))
	}
}

func TestPipeline_Execute_EmptyResult(t *testing.T) {
	spec := &PipelineSpec{
		Stages: []StageSpec{
			&mockStageSpec{batches: nil, cat: CatSource},
		},
		RootIdx: 0,
	}

	pipeline, err := spec.NewRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer pipeline.Close()

	rows, err := pipeline.Execute(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected 0 rows for empty input, got %d", len(rows))
	}
}

func TestPipeline_Reset_Supported(t *testing.T) {
	stageSpec := &mockStageSpec{
		batches: []*UT.Batch{makeIntBatch([]int64{1, 2})},
		cat:     CatSource,
	}
	spec := &PipelineSpec{
		Stages:  []StageSpec{stageSpec},
		RootIdx: 0,
	}

	pipeline, err := spec.NewRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer pipeline.Close()

	// First execution
	rows, err := pipeline.Execute(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("first run: expected 2 rows, got %d", len(rows))
	}

	// Reset and re-execute
	if err := pipeline.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}

	rows2, err := pipeline.Execute(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows2) != 2 {
		t.Fatalf("second run after reset: expected 2 rows, got %d", len(rows2))
	}
}

func TestPipeline_Reset_NotSupported(t *testing.T) {
	spec := &PipelineSpec{
		Stages: []StageSpec{
			&nonResettableSpec{batches: []*UT.Batch{makeIntBatch([]int64{1})}},
		},
		RootIdx: 0,
	}

	pipeline, err := spec.NewRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer pipeline.Close()

	// First execution
	rows, err := pipeline.Execute(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("first run: expected 1 row, got %d", len(rows))
	}

	// Reset should recreate the stage from spec
	if err := pipeline.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}

	rows2, err := pipeline.Execute(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows2) != 1 {
		t.Fatalf("second run after reset: expected 1 row, got %d", len(rows2))
	}
}

func TestPipeline_Close_Idempotent(t *testing.T) {
	spec := &PipelineSpec{
		Stages:  []StageSpec{&mockStageSpec{cat: CatSource}},
		RootIdx: 0,
	}

	pipeline, err := spec.NewRuntime()
	if err != nil {
		t.Fatal(err)
	}

	// Close twice should not error
	if err := pipeline.Close(); err != nil {
		t.Fatal(err)
	}
	if err := pipeline.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestPipeline_Execute_ClosedPipeline(t *testing.T) {
	spec := &PipelineSpec{
		Stages:  []StageSpec{&mockStageSpec{cat: CatSource}},
		RootIdx: 0,
	}

	pipeline, err := spec.NewRuntime()
	if err != nil {
		t.Fatal(err)
	}
	pipeline.Close()

	_, err = pipeline.Execute(context.Background())
	if err == nil {
		t.Fatal("expected error executing closed pipeline")
	}
}

// --- nonResettableSpec creates stages that return ErrResetNotSupported ---

type nonResettableSpec struct {
	batches []*UT.Batch
}

func (s *nonResettableSpec) NewRuntime() Stage {
	return &mockNonResettableStage{batches: s.batches}
}

func (s *nonResettableSpec) Category() StageCategory { return CatSource }
