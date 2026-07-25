package OP

import (
	"context"
	"testing"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// makeSortedBatch builds a columnar batch with the given int64 column.
func makeSortedBatch(colName string, keys []int64) *UT.Batch {
	b := UT.GetBatch(1)
	b.SetColumnName(0, colName)
	b.Cols[0].Type = LX.T_INT_KW
	b.Cols[0].Data.Ints = make([]int64, len(keys))
	for i, k := range keys {
		b.Cols[0].Data.Ints[i] = k
	}
	b.Size = len(keys)
	return b
}

// TestBatchMergeJoin_InnerEquiJoin tests the pure batch merge join
// with a basic INNER join on sorted inputs.
func TestBatchMergeJoin_InnerEquiJoin(t *testing.T) {
	left := &testBatchProducer{
		batches: []*UT.Batch{makeSortedBatch("k", []int64{1, 2, 3})},
	}
	right := &testBatchProducer{
		batches: []*UT.Batch{makeSortedBatch("k", []int64{2, 3, 4})},
	}

	j := NewBatchMergeJoin(left, right, []int{0}, []int{0}, JoinKindInner)
	defer j.Close()

	ctx := context.Background()
	batch, err := j.NextBatch(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch == nil {
		t.Fatal("expected batch, got nil")
	}
	// Matches: (2,2), (3,3)
	if batch.Size != 2 {
		t.Fatalf("expected 2 rows, got %d", batch.Size)
	}

	// Verify row data.
	for i := 0; i < batch.Size; i++ {
		lk := UT.BatchValueAt(batch.Cols[0], i).(int64)
		rk := UT.BatchValueAt(batch.Cols[1], i).(int64)
		if lk != rk {
			t.Errorf("row %d: left key %d != right key %d", i, lk, rk)
		}
	}

	batch.Put()
	// EOF
	batch2, _ := j.NextBatch(ctx)
	if batch2 != nil {
		t.Fatal("expected nil (EOF), got batch")
	}
}

// TestStreamingBatchMergeJoin_InnerEquiJoin verifies the streaming variant
// produces identical results to the batch merge join. REQ002004.
func TestStreamingBatchMergeJoin_InnerEquiJoin(t *testing.T) {
	left := &testBatchProducer{
		batches: []*UT.Batch{makeSortedBatch("k", []int64{1, 2, 3})},
	}
	right := &testBatchProducer{
		batches: []*UT.Batch{makeSortedBatch("k", []int64{2, 3, 4})},
	}

	j := NewStreamingBatchMergeJoin(left, right, []int{0}, []int{0}, JoinKindInner)
	defer j.Close()

	ctx := context.Background()
	batch, err := j.NextBatch(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch == nil {
		t.Fatal("expected batch, got nil")
	}
	if batch.Size != 2 {
		t.Fatalf("expected 2 matched rows, got %d", batch.Size)
	}
	for i := 0; i < batch.Size; i++ {
		lk := UT.BatchValueAt(batch.Cols[0], i).(int64)
		rk := UT.BatchValueAt(batch.Cols[1], i).(int64)
		if lk != rk {
			t.Errorf("row %d: left key %d != right key %d", i, lk, rk)
		}
	}
	batch.Put()

	// EOF.
	batch2, _ := j.NextBatch(ctx)
	if batch2 != nil {
		t.Fatal("expected nil (EOF), got batch")
	}
}

// TestStreamingBatchMergeJoin_MultiBatchLeft verifies the streaming
// variant correctly handles multi-batch left inputs. REQ002004.
func TestStreamingBatchMergeJoin_MultiBatchLeft(t *testing.T) {
	left := &testBatchProducer{
		batches: []*UT.Batch{
			makeSortedBatch("k", []int64{1, 2}),
			makeSortedBatch("k", []int64{3, 4}),
		},
	}
	right := &testBatchProducer{
		batches: []*UT.Batch{makeSortedBatch("k", []int64{2, 3, 5})},
	}

	j := NewStreamingBatchMergeJoin(left, right, []int{0}, []int{0}, JoinKindInner)
	defer j.Close()

	var totalRows int
	ctx := context.Background()
	for {
		batch, err := j.NextBatch(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if batch == nil {
			break
		}
		totalRows += batch.Size
		batch.Put()
	}
	// Matches: (2,2), (3,3)
	if totalRows != 2 {
		t.Errorf("expected 2 total matched rows, got %d", totalRows)
	}
}

// TestStreamingBatchMergeJoin_DuplicateKeys verifies that duplicate
// keys on the right side correctly produce cartesian products. REQ002004.
func TestStreamingBatchMergeJoin_DuplicateKeys(t *testing.T) {
	left := &testBatchProducer{
		batches: []*UT.Batch{makeSortedBatch("k", []int64{1})},
	}
	right := &testBatchProducer{
		batches: []*UT.Batch{makeSortedBatch("k", []int64{1, 1, 1})},
	}

	j := NewStreamingBatchMergeJoin(left, right, []int{0}, []int{0}, JoinKindInner)
	defer j.Close()

	batch, err := j.NextBatch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch == nil {
		t.Fatal("expected batch, got nil")
	}
	if batch.Size != 3 {
		t.Fatalf("expected 3 cartesian rows (1 left × 3 right), got %d", batch.Size)
	}
	batch.Put()
}

// TestBatchMergeJoin_LeftOuter tests LEFT outer join.
func TestBatchMergeJoin_LeftOuter(t *testing.T) {
	left := &testBatchProducer{
		batches: []*UT.Batch{makeSortedBatch("k", []int64{1, 2, 3})},
	}
	right := &testBatchProducer{
		batches: []*UT.Batch{makeSortedBatch("k", []int64{2, 4})},
	}

	j := NewBatchMergeJoin(left, right, []int{0}, []int{0}, JoinKindLeft)
	defer j.Close()

	ctx := context.Background()
	// First batch: matched (2,2) + unmatched (1, NULL)
	batch, err := j.NextBatch(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch == nil {
		t.Fatal("expected batch, got nil")
	}
	if batch.Size < 2 {
		t.Fatalf("expected at least 2 rows, got %d", batch.Size)
	}
	batch.Put()

	// Drain remaining batches.
	for {
		b, _ := j.NextBatch(ctx)
		if b == nil {
			break
		}
		b.Put()
	}

	// Total expected: (1, NULL), (2, 2), (3, NULL) = 3 rows
	// (4, NULL) is NOT emitted because right outer is not requested.
}

// TestBatchMergeJoin_RightOuter tests RIGHT outer join.
func TestBatchMergeJoin_RightOuter(t *testing.T) {
	left := &testBatchProducer{
		batches: []*UT.Batch{makeSortedBatch("k", []int64{1, 2})},
	}
	right := &testBatchProducer{
		batches: []*UT.Batch{makeSortedBatch("k", []int64{2, 3})},
	}

	j := NewBatchMergeJoin(left, right, []int{0}, []int{0}, JoinKindRight)
	defer j.Close()

	ctx := context.Background()
	totalRows := 0
	for {
		b, err := j.NextBatch(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if b == nil {
			break
		}
		totalRows += b.Size
		b.Put()
	}
	// Expected: (NULL, 3) + matched (2,2) = 2 rows.
	if totalRows != 2 {
		t.Fatalf("expected 2 rows, got %d", totalRows)
	}
}

// TestBatchMergeJoin_MultipleBatches tests multi-batch inputs.
func TestBatchMergeJoin_MultipleBatches(t *testing.T) {
	left := &testBatchProducer{
		batches: []*UT.Batch{
			makeSortedBatch("k", []int64{1, 2}),
			makeSortedBatch("k", []int64{3, 4}),
		},
	}
	right := &testBatchProducer{
		batches: []*UT.Batch{
			makeSortedBatch("k", []int64{2, 3}),
			makeSortedBatch("k", []int64{5}),
		},
	}

	j := NewBatchMergeJoin(left, right, []int{0}, []int{0}, JoinKindInner)
	defer j.Close()

	ctx := context.Background()
	totalRows := 0
	for {
		b, err := j.NextBatch(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if b == nil {
			break
		}
		totalRows += b.Size
		b.Put()
	}
	// Matches: (2,2), (3,3) = 2 rows.
	if totalRows != 2 {
		t.Fatalf("expected 2 rows, got %d", totalRows)
	}
}

// TestBatchMergeJoin_NoMatches tests empty result.
func TestBatchMergeJoin_NoMatches(t *testing.T) {
	left := &testBatchProducer{
		batches: []*UT.Batch{makeSortedBatch("k", []int64{1, 2})},
	}
	right := &testBatchProducer{
		batches: []*UT.Batch{makeSortedBatch("k", []int64{3, 4})},
	}

	j := NewBatchMergeJoin(left, right, []int{0}, []int{0}, JoinKindInner)
	defer j.Close()

	ctx := context.Background()
	batch, err := j.NextBatch(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch != nil {
		t.Fatalf("expected nil (no matches), got batch with %d rows", batch.Size)
	}
}