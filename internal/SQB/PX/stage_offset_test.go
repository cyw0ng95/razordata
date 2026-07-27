package PX

import (
	"context"
	"testing"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

func TestOffsetStageSpec_Category(t *testing.T) {
	spec := &OffsetStageSpec{Offset: 5}
	if spec.Category() != CatTransform {
		t.Errorf("expected CatTransform, got %v", spec.Category())
	}
}

func TestOffsetStageSpec_NegativeOffset_Clamped(t *testing.T) {
	spec := &OffsetStageSpec{Offset: -3}
	stage := spec.NewRuntime().(*OffsetStage)
	if stage.offset != 0 {
		t.Fatalf("expected offset clamped to 0, got %d", stage.offset)
	}
	if stage.remaining != 0 {
		t.Fatalf("expected remaining=0, got %d", stage.remaining)
	}
}

func TestOffsetStage_NextBatch_SkipEntireBatches(t *testing.T) {
	child := &mockStage{batches: []*UT.Batch{
		makeIntBatch([]int64{1, 2}),
		makeIntBatch([]int64{3, 4}),
		makeIntBatch([]int64{5, 6}),
	}}
	offset := &OffsetStage{child: child, offset: 4, remaining: 4}
	defer offset.Close()

	batch, err := offset.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected non-nil batch")
	}
	// Skipped first 4 rows (batches 0 and 1 entirely, first 2 of batch 2 are
	// actually rows 5 and 6). Wait — batch 2 has rows [5,6] which are rows
	// index 4,5. After skipping 4, we should get rows starting from index 4.
	// sliceBatch(batch, start=0) for the first batch after skip phase.
	// Actually: first 2 batches (4 rows) are fully skipped, remaining=0,
	// then we just pass through the next batch.
	if batch.Size != 2 {
		t.Fatalf("expected 2 rows, got %d", batch.Size)
	}
}

func TestOffsetStage_NextBatch_PartialSkip(t *testing.T) {
	child := &mockStage{batches: []*UT.Batch{
		makeIntBatch([]int64{1, 2, 3, 4, 5}),
	}}
	offset := &OffsetStage{child: child, offset: 2, remaining: 2}
	defer offset.Close()

	batch, err := offset.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected non-nil batch")
	}
	if batch.Size != 3 {
		t.Fatalf("expected 3 rows (5-2 offset), got %d", batch.Size)
	}
}

func TestOffsetStage_NextBatch_ZeroOffset(t *testing.T) {
	child := &mockStage{batches: []*UT.Batch{
		makeIntBatch([]int64{1, 2, 3}),
	}}
	offset := &OffsetStage{child: child, offset: 0, remaining: 0}
	defer offset.Close()

	batch, err := offset.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil || batch.Size != 3 {
		t.Fatalf("expected 3 rows (no offset), got %d", batch.Size)
	}
}

func TestOffsetStage_NextBatch_OffsetBeyondAllRows(t *testing.T) {
	child := &mockStage{batches: []*UT.Batch{
		makeIntBatch([]int64{1, 2}),
	}}
	offset := &OffsetStage{child: child, offset: 10, remaining: 10}
	defer offset.Close()

	batch, err := offset.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch != nil {
		t.Fatal("expected nil batch when offset exceeds all rows")
	}
}

func TestOffsetStage_NextBatch_EOF(t *testing.T) {
	child := &mockStage{batches: nil}
	offset := &OffsetStage{child: child, offset: 0, remaining: 0}
	defer offset.Close()

	batch, err := offset.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch != nil {
		t.Fatal("expected nil batch at EOF")
	}
}

func TestOffsetStage_Reset(t *testing.T) {
	offset := &OffsetStage{offset: 5, remaining: 0}
	err := offset.Reset(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if offset.remaining != 5 {
		t.Fatalf("expected remaining=5 after reset, got %d", offset.remaining)
	}
}

func TestOffsetStage_SetChild(t *testing.T) {
	offset := &OffsetStage{}
	child := &mockStage{}
	offset.SetChild(SingleChild, child)
	if offset.child != child {
		t.Fatal("expected child to be set")
	}
}

func TestOffsetStage_Close(t *testing.T) {
	child := &mockStage{}
	offset := &OffsetStage{child: child}
	if err := offset.Close(); err != nil {
		t.Fatal(err)
	}
	if !child.closed {
		t.Fatal("expected child to be closed")
	}
}

func TestSliceBatch_Basic(t *testing.T) {
	batch := makeIntBatch([]int64{10, 20, 30, 40, 50})
	batch.Pooled = false

	out, err := sliceBatch(batch, 2)
	if err != nil {
		t.Fatal(err)
	}
	if out == nil {
		t.Fatal("expected non-nil batch")
	}
	if out.Size != 3 {
		t.Fatalf("expected 3 rows, got %d", out.Size)
	}
	if out.Cols[0].Data.Ints[0] != 30 || out.Cols[0].Data.Ints[2] != 50 {
		t.Fatalf("expected [30,40,50], got %v", out.Cols[0].Data.Ints[:3])
	}
}

func TestSliceBatch_StartBeyondSize(t *testing.T) {
	batch := makeIntBatch([]int64{1, 2, 3})
	batch.Pooled = false

	out, err := sliceBatch(batch, 5)
	if err != nil {
		t.Fatal(err)
	}
	if out != nil {
		t.Fatal("expected nil batch when start >= logical size")
	}
}

func TestSliceBatch_WithSel(t *testing.T) {
	batch := makeIntBatch([]int64{10, 20, 30, 40, 50})
	batch.Pooled = false
	batch.Sel = []uint16{4, 2, 0, 3, 1} // reorder: 50,30,10,40,20
	batch.Size = 5

	out, err := sliceBatch(batch, 2)
	if err != nil {
		t.Fatal(err)
	}
	if out == nil {
		t.Fatal("expected non-nil batch")
	}
	if out.Size != 3 {
		t.Fatalf("expected 3 rows, got %d", out.Size)
	}
	// After Sel reorder: index 2→Sel[2]=0→10, index 3→Sel[3]=3→40, index 4→Sel[4]=1→20
	if len(out.Cols[0].Data.Ints) < 3 {
		t.Fatalf("expected at least 3 ints, got %d", len(out.Cols[0].Data.Ints))
	}
}
