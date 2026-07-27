package PX

import (
	"context"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// --- Edge case tests ---

func TestLimitStage_NegativeLimit(t *testing.T) {
	// Negative limit = unlimited.
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

func TestOffsetStage_OffsetLargerThanData(t *testing.T) {
	child := &mockStage{batches: []*UT.Batch{
		makeIntBatch([]int64{1}),
		makeIntBatch([]int64{2}),
	}}
	offset := &OffsetStage{child: child, offset: 100, remaining: 100}
	defer offset.Close()

	batch, err := offset.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch != nil {
		t.Fatal("expected nil when offset > total rows")
	}
}

func TestFilterStage_ClosedChild(t *testing.T) {
	child := &mockStage{}
	filter := &FilterStage{
		child: child,
		pred:  &PS.BinaryExpr{Op: LX.T_GT, Left: &PS.Ident{Name: "v", SlotIdx: 0}, Right: &PS.NumberLiteral{Val: 0}},
	}
	filter.Close()

	// After close, child is closed. NextBatch should just return EOF.
	batch, err := filter.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch != nil {
		t.Fatal("expected nil after close")
	}
}

// --- Pipeline integration tests ---

func TestPipeline_ScanLimit(t *testing.T) {
	// Scan → Limit pipeline
	spec := &PipelineSpec{
		Stages: []StageSpec{
			&ScanStageSpec{NewProducer: func() UT.BatchProducer {
				return &mockBatchProducer{batches: []*UT.Batch{
					makeIntBatch([]int64{1, 2, 3, 4, 5}),
				}}
			}},
			&LimitStageSpec{Limit: 3},
		},
		Edges:   []EdgeSpec{{From: 1, To: 0, Side: SingleChild}},
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
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
}

func TestPipeline_ScanOffset(t *testing.T) {
	// Scan → Offset pipeline
	spec := &PipelineSpec{
		Stages: []StageSpec{
			&ScanStageSpec{NewProducer: func() UT.BatchProducer {
				return &mockBatchProducer{batches: []*UT.Batch{
					makeIntBatch([]int64{1, 2, 3, 4, 5}),
				}}
			}},
			&OffsetStageSpec{Offset: 2},
		},
		Edges:   []EdgeSpec{{From: 1, To: 0, Side: SingleChild}},
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
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
}

func TestPipeline_ScanFilterLimit(t *testing.T) {
	// Scan → Filter → Limit pipeline
	spec := &PipelineSpec{
		Stages: []StageSpec{
			&ScanStageSpec{NewProducer: func() UT.BatchProducer {
				return &mockBatchProducer{batches: []*UT.Batch{
					makeIntBatch([]int64{1, 5, 2, 8, 3, 10}),
				}}
			}},
			&FilterStageSpec{Pred: &PS.BinaryExpr{
				Op: LX.T_GT, Left: &PS.Ident{Name: "v", SlotIdx: 0}, Right: &PS.NumberLiteral{Val: 3},
			}},
			&LimitStageSpec{Limit: 2},
		},
		Edges: []EdgeSpec{
			{From: 1, To: 0, Side: SingleChild},
			{From: 2, To: 1, Side: SingleChild},
		},
		RootIdx: 2,
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

func TestPipeline_FusedScanLimit(t *testing.T) {
	// FusedScan → Limit pipeline
	spec := &PipelineSpec{
		Stages: []StageSpec{
			&FusedScanStageSpec{
				SourceFactory: func() UT.BatchProducer {
					return &mockBatchProducer{batches: []*UT.Batch{
						makeIntBatch([]int64{1, 2, 3, 4, 5}),
					}}
				},
				Limit: 3,
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
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
}

func TestPipeline_ResetLimit(t *testing.T) {
	// Scan → Limit, then reset and re-execute
	spec := &PipelineSpec{
		Stages: []StageSpec{
			&ScanStageSpec{NewProducer: func() UT.BatchProducer {
				return &mockBatchProducer{batches: []*UT.Batch{
					makeIntBatch([]int64{1, 2, 3, 4, 5}),
				}}
			}},
			&LimitStageSpec{Limit: 2},
		},
		Edges:   []EdgeSpec{{From: 1, To: 0, Side: SingleChild}},
		RootIdx: 1,
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

	// Reset — the ScanStage returns ErrResetNotSupported, so
	// Pipeline recreates it from spec.
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

func TestPipeline_EmptyResult(t *testing.T) {
	// Scan with no data → Limit
	spec := &PipelineSpec{
		Stages: []StageSpec{
			&ScanStageSpec{NewProducer: func() UT.BatchProducer {
				return &mockBatchProducer{}
			}},
			&LimitStageSpec{Limit: 5},
		},
		Edges:   []EdgeSpec{{From: 1, To: 0, Side: SingleChild}},
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
	if len(rows) != 0 {
		t.Fatalf("expected 0 rows for empty scan, got %d", len(rows))
	}
}
