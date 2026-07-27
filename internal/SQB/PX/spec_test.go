package PX

import (
	"context"
	"testing"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// mockStageSpec creates a mockStage on each NewRuntime call.
type mockStageSpec struct {
	batches []*UT.Batch
	cat     StageCategory
	created int // count of NewRuntime calls
}

func (s *mockStageSpec) NewRuntime() Stage {
	s.created++
	return &mockStage{batches: s.batches}
}

func (s *mockStageSpec) Category() StageCategory { return s.cat }

// mockChildSetterStage accepts a child stage via SetChild.
type mockChildSetterStage struct {
	mockStage
	child Stage
}

func (m *mockChildSetterStage) SetChild(side ChildSide, child Stage) {
	m.child = child
}

type mockChildSetterSpec struct {
	batches []*UT.Batch
	cat     StageCategory
}

func (s *mockChildSetterSpec) NewRuntime() Stage {
	return &mockChildSetterStage{mockStage: mockStage{batches: s.batches}}
}

func (s *mockChildSetterSpec) Category() StageCategory { return s.cat }

func TestPipelineSpec_NewRuntime_SingleStage(t *testing.T) {
	spec := &PipelineSpec{
		Stages: []StageSpec{
			&mockStageSpec{batches: []*UT.Batch{makeIntBatch([]int64{1, 2, 3})}, cat: CatSource},
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
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
}

func TestPipelineSpec_NewRuntime_TwoStages(t *testing.T) {
	sourceSpec := &mockStageSpec{
		batches: []*UT.Batch{makeIntBatch([]int64{1, 2})},
		cat:     CatSource,
	}

	// The transform stage delegates to its child.
	transformStageFn := func() Stage {
		return &delegatingStage{child: nil}
	}

	spec := &PipelineSpec{
		Stages: []StageSpec{
			sourceSpec,
			&delegatingStageSpec{newFn: transformStageFn, cat: CatTransform},
		},
		Edges: []EdgeSpec{
			{From: 1, To: 0, Side: SingleChild},
		},
		RootIdx: 1,
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
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
}

func TestPipelineSpec_NewRuntime_InvalidRootIdx(t *testing.T) {
	spec := &PipelineSpec{
		Stages:  []StageSpec{&mockStageSpec{cat: CatSource}},
		RootIdx: 5,
	}
	_, err := spec.NewRuntime()
	if err == nil {
		t.Fatal("expected error for invalid RootIdx")
	}
}

func TestPipelineSpec_NewRuntime_EmptyStages(t *testing.T) {
	spec := &PipelineSpec{Stages: nil, RootIdx: 0}
	_, err := spec.NewRuntime()
	if err == nil {
		t.Fatal("expected error for empty stages")
	}
}

func TestPipelineSpec_NewRuntime_InvalidEdge(t *testing.T) {
	spec := &PipelineSpec{
		Stages:  []StageSpec{&mockStageSpec{cat: CatSource}},
		RootIdx: 0,
		Edges:   []EdgeSpec{{From: 0, To: 5, Side: SingleChild}},
	}
	_, err := spec.NewRuntime()
	if err == nil {
		t.Fatal("expected error for invalid edge To")
	}
}

func TestPipelineSpec_NewRuntime_IndependentInstances(t *testing.T) {
	spec := &PipelineSpec{
		Stages: []StageSpec{
			&mockStageSpec{batches: []*UT.Batch{makeIntBatch([]int64{1})}, cat: CatSource},
		},
		RootIdx: 0,
	}

	p1, err := spec.NewRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer p1.Close()

	p2, err := spec.NewRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer p2.Close()

	// Two pipelines should be independent
	if p1 == p2 {
		t.Fatal("pipelines should be independent instances")
	}
}

// --- delegatingStage: a transform that delegates to its child ---

type delegatingStage struct {
	child  Stage
	closed bool
}

func (d *delegatingStage) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if d.child == nil {
		return nil, nil
	}
	return d.child.NextBatch(ctx)
}

func (d *delegatingStage) Reset(ctx context.Context) error {
	if d.child != nil {
		return d.child.Reset(ctx)
	}
	return nil
}

func (d *delegatingStage) Close() error {
	d.closed = true
	return nil
}

func (d *delegatingStage) SetChild(_ ChildSide, child Stage) {
	d.child = child
}

type delegatingStageSpec struct {
	newFn func() Stage
	cat   StageCategory
}

func (s *delegatingStageSpec) NewRuntime() Stage { return s.newFn() }
func (s *delegatingStageSpec) Category() StageCategory {
	if s.cat != 0 {
		return s.cat
	}
	return CatTransform
}

// Verify compile-time: delegatingStage implements ChildSetter
var _ ChildSetter = (*delegatingStage)(nil)

// Verify PipelineSpec has expected fields
func TestPipelineSpec_OutputSchema(t *testing.T) {
	spec := &PipelineSpec{
		Stages:      []StageSpec{&mockStageSpec{cat: CatSource}},
		RootIdx:     0,
		OutputCols:  []string{"a", "b"},
		OutputTypes: []LX.TokenType{LX.T_INT_KW, LX.T_TEXT},
		Cost:        42.0,
		MemoKey:     "sel:t1",
		SQLText:     "SELECT 1",
	}
	if len(spec.OutputCols) != 2 {
		t.Fatalf("expected 2 output cols, got %d", len(spec.OutputCols))
	}
	if spec.Cost != 42.0 {
		t.Fatalf("expected cost 42, got %f", spec.Cost)
	}
}
