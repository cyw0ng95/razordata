package PX

import (
	"context"
	"errors"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// errorStageSpec emits a single error on NextBatch, used to verify error
// propagation through the pipelined channel.
type errorStageSpec struct{}

func (s *errorStageSpec) NewRuntime() Stage { return &errorStage{} }
func (s *errorStageSpec) Category() StageCategory {
	return CatSource
}

type errorStage struct{ closed bool }

func (m *errorStage) NextBatch(_ context.Context) (*UT.Batch, error) {
	return nil, errors.New("px: injected test error")
}
func (m *errorStage) Reset(_ context.Context) error        { return ErrResetNotSupported }
func (m *errorStage) Close() error                          { m.closed = true; return nil }

// intBatchSpec builds a single-source spec emitting the given int values.
func intBatchSpec(values []int64) *PipelineSpec {
	return &PipelineSpec{
		Stages:  []StageSpec{&mockStageSpec{batches: []*UT.Batch{makeIntBatch(values)}, cat: CatSource}},
		RootIdx: 0,
	}
}

// twoStageSpec builds a source -> transform (delegating) spec. The
// transform delegates to its child, exercising the multi-stage channel
// path through a composed pipeline root.
func twoStageSpec(values []int64) *PipelineSpec {
	return &PipelineSpec{
		Stages: []StageSpec{
			&mockStageSpec{batches: []*UT.Batch{makeIntBatch(values)}, cat: CatSource},
			&delegatingStageSpec{newFn: func() Stage { return &delegatingStage{} }, cat: CatTransform},
		},
		Edges:   []EdgeSpec{{From: 1, To: 0, Side: SingleChild}},
		RootIdx: 1,
	}
}

// drainRows streams all rows from a PipelineStream. Each row's Data is
// deep-copied because PipelineStream.Next aliases a reused buffer.
func drainRows(t *testing.T, ps *PipelineStream) []DT.Row {
	t.Helper()
	var rows []DT.Row
	for {
		r, err := ps.Next()
		if err != nil {
			if err == DT.ErrNoRows {
				return rows
			}
			t.Fatalf("unexpected stream error: %v", err)
		}
		cp := make([]DT.Value, len(r.Data))
		copy(cp, r.Data)
		r.Data = cp
		rows = append(rows, r)
	}
}

func TestPipelinedExecutor_Execute_SingleStage(t *testing.T) {
	spec := intBatchSpec([]int64{1, 2, 3})
	got, err := NewPipelinedExecutor(spec).Execute(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(got))
	}
	for i, r := range got {
		if r.Data[0].AsInt() != int64(i+1) {
			t.Fatalf("row %d: expected %d, got %d", i, i+1, r.Data[0].AsInt())
		}
	}
}

func TestPipelinedExecutor_Execute_TwoStage(t *testing.T) {
	spec := twoStageSpec([]int64{10, 20, 30, 40})
	got, err := NewPipelinedExecutor(spec).Execute(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("expected 4 rows, got %d", len(got))
	}
}

func TestPipelinedExecutor_ExecuteStream(t *testing.T) {
	spec := intBatchSpec([]int64{7, 8, 9})
	ps, err := NewPipelinedExecutor(spec).ExecuteStream(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer ps.Close()
	rows := drainRows(t, ps)
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
}

func TestPipelinedExecutor_EquivalentToMaterializing(t *testing.T) {
	values := []int64{5, 6, 7, 8, 9}
	spec := twoStageSpec(values)

	mat, err := NewPipelineExecutor(spec).Execute(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	pipe, err := NewPipelinedExecutor(spec).Execute(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(mat) != len(pipe) {
		t.Fatalf("row count mismatch: materializing=%d pipelined=%d", len(mat), len(pipe))
	}
	for i := range mat {
		if mat[i].Data[0].AsInt() != pipe[i].Data[0].AsInt() {
			t.Fatalf("row %d mismatch: materializing=%d pipelined=%d",
				i, mat[i].Data[0].AsInt(), pipe[i].Data[0].AsInt())
		}
	}
}

func TestPipelinedExecutor_ContextCancel(t *testing.T) {
	// Source that emits many batches so the producer blocks on the
	// bounded channel until cancellation.
	var batches []*UT.Batch
	for i := 0; i < 100; i++ {
		batches = append(batches, makeIntBatch([]int64{int64(i)}))
	}
	spec := &PipelineSpec{
		Stages:  []StageSpec{&mockStageSpec{batches: batches, cat: CatSource}},
		RootIdx: 0,
	}

	ctx, cancel := context.WithCancel(context.Background())
	ps, err := NewPipelinedExecutor(spec).ExecuteStream(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Consume a few rows, then cancel mid-stream.
	for i := 0; i < 3; i++ {
		if _, err := ps.Next(); err != nil {
			t.Fatalf("unexpected error on row %d: %v", i, err)
		}
	}
	cancel()
	if err := ps.Close(); err != nil {
		t.Fatalf("close after cancel: %v", err)
	}
}

func TestPipelinedExecutor_CloseIdempotent(t *testing.T) {
	spec := intBatchSpec([]int64{1, 2, 3})
	ps, err := NewPipelinedExecutor(spec).ExecuteStream(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := ps.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := ps.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

func TestPipelinedExecutor_Empty(t *testing.T) {
	spec := intBatchSpec(nil)
	got, err := NewPipelinedExecutor(spec).Execute(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 rows, got %d", len(got))
	}
}

func TestPipelinedExecutor_ErrorPropagates(t *testing.T) {
	spec := &PipelineSpec{
		Stages:  []StageSpec{&errorStageSpec{}},
		RootIdx: 0,
	}
	ps, err := NewPipelinedExecutor(spec).ExecuteStream(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer ps.Close()
	for {
		_, err := ps.Next()
		if err != nil {
			if err == DT.ErrNoRows {
				t.Fatal("expected error, got EOF")
			}
			return // error propagated correctly
		}
	}
}
