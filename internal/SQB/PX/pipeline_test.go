package PX

import (
	"context"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	PL "github.com/cyw0ng95/razordata/internal/SQF/PL"
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

// --- propagation mock ---

// propagatingStage implements all three propagation interfaces for testing.
type propagatingStage struct {
	batches    []*UT.Batch
	idx        int
	params     []any
	execCtx    *DT.ExecContext
	planner    PL.QueryPlanner
	paramBuf   []any
}

func (s *propagatingStage) NextBatch(_ context.Context) (*UT.Batch, error) {
	if s.idx >= len(s.batches) {
		return nil, nil
	}
	b := s.batches[s.idx]
	s.idx++
	return b, nil
}

func (s *propagatingStage) Reset(_ context.Context) error { s.idx = 0; return nil }
func (s *propagatingStage) Close() error                  { return nil }
func (s *propagatingStage) PropagateParams(args []any, buf *[]any) {
	s.params = args
	s.paramBuf = *buf
}
func (s *propagatingStage) PropagateExecContext(ec *DT.ExecContext) { s.execCtx = ec }
func (s *propagatingStage) PropagatePlanner(p PL.QueryPlanner)     { s.planner = p }

type propagatingSpec struct {
	stage *propagatingStage
}

func (s *propagatingSpec) NewRuntime() Stage {
	s.stage = &propagatingStage{
		batches: []*UT.Batch{makeIntBatch([]int64{1})},
	}
	return s.stage
}

func (s *propagatingSpec) Category() StageCategory { return CatSource }

func TestPipeline_PropagateParams(t *testing.T) {
	spec := &propagatingSpec{}
	ps := &PipelineSpec{
		Stages:  []StageSpec{spec},
		RootIdx: 0,
	}
	pipe, err := ps.NewRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer pipe.Close()

	args := []any{int64(42), "hello"}
	var buf []any
	pipe.PropagateParams(args, &buf)

	if spec.stage == nil {
		t.Fatal("stage not created")
	}
	if len(spec.stage.params) != 2 || spec.stage.params[0] != int64(42) || spec.stage.params[1] != "hello" {
		t.Fatalf("params not propagated: %v", spec.stage.params)
	}
}

func TestPipeline_PropagateExecContext(t *testing.T) {
	spec := &propagatingSpec{}
	ps := &PipelineSpec{
		Stages:  []StageSpec{spec},
		RootIdx: 0,
	}
	pipe, err := ps.NewRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer pipe.Close()

	ec := &DT.ExecContext{}
	pipe.PropagateExecContext(ec)

	if spec.stage.execCtx != ec {
		t.Fatal("execCtx not propagated")
	}
}

func TestPipeline_PropagatePlanner(t *testing.T) {
	spec := &propagatingSpec{}
	ps := &PipelineSpec{
		Stages:  []StageSpec{spec},
		RootIdx: 0,
	}
	pipe, err := ps.NewRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer pipe.Close()

	// Use a nil planner to test the propagation path (concrete type matters).
	// In real usage, this would be *EX.Planner.
	var p PL.QueryPlanner
	pipe.PropagatePlanner(p)

	if spec.stage.planner != p {
		t.Fatal("planner not propagated")
	}
}

func TestPipelineExecutor_PropagateAll(t *testing.T) {
	spec := &propagatingSpec{}
	ps := &PipelineSpec{
		Stages:  []StageSpec{spec},
		RootIdx: 0,
	}

	exec := NewPipelineExecutor(ps)
	exec.SetParams([]any{int64(7)})
	exec.SetExecContext(&DT.ExecContext{})

	// Execute triggers ensurePipeline which should propagate.
	rows, err := exec.Execute(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if len(spec.stage.params) != 1 || spec.stage.params[0] != int64(7) {
		t.Fatalf("params not propagated via executor: %v", spec.stage.params)
	}
	if spec.stage.execCtx == nil {
		t.Fatal("execCtx not propagated via executor")
	}
}

func TestPipelineExecutor_BatchProducer(t *testing.T) {
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

	exec := NewPipelineExecutor(spec)
	defer exec.Close()

	bp, err := exec.BatchProducer(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var got []int64
	for {
		batch, err := bp.NextBatch(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if batch == nil {
			break
		}
		for i := 0; i < batch.Size; i++ {
			got = append(got, batch.Value(0, i).(int64))
		}
	}
	if len(got) != 5 {
		t.Fatalf("expected 5 rows via BatchProducer, got %d", len(got))
	}
	for i, v := range got {
		if v != int64(i+1) {
			t.Fatalf("row %d = %d, want %d", i, v, i+1)
		}
	}
}
