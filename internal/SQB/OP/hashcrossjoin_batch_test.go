package OP

import (
	"context"
	"testing"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

func makeIntBatch(cols []string, ints ...[]int64) *UT.Batch {
	n := len(ints[0])
	b := UT.GetBatch(len(cols))
	for i, name := range cols {
		b.SetColumnName(i, name)
		b.Cols[i].Type = LX.T_INT_KW
		b.Cols[i].Data.Ints = make([]int64, n)
		for j := 0; j < n; j++ {
			b.Cols[i].Data.Ints[j] = ints[i][j]
		}
	}
	b.Size = n
	return b
}

func TestBatchHashCrossJoin_InnerEquiJoin(t *testing.T) {
	left := &testBatchProducer{
		batches: []*UT.Batch{makeIntBatch([]string{"k", "v"}, []int64{1, 2, 3}, []int64{10, 20, 30})},
	}
	right := &testBatchProducer{
		batches: []*UT.Batch{makeIntBatch([]string{"k"}, []int64{2, 3, 4})},
	}

	j := NewBatchHashCrossJoin(left, right, 0, 0)
	defer j.Close()

	ctx := context.Background()
	batch, err := j.NextBatch(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch == nil {
		t.Fatal("expected batch, got nil")
	}

	// Should match: (2,20)-(2), (3,30)-(3) = 2 rows.
	if batch.Size != 2 {
		t.Fatalf("expected 2 rows, got %d", batch.Size)
	}

	results := make(map[int64]int64)
	for i := 0; i < batch.Size; i++ {
		k := UT.BatchValueAt(batch.Cols[0], i).(int64)
		v := UT.BatchValueAt(batch.Cols[1], i).(int64)
		results[k] = v
	}
	if results[2] != 20 {
		t.Errorf("expected k=2,v=20, got v=%d", results[2])
	}
	if results[3] != 30 {
		t.Errorf("expected k=3,v=30, got v=%d", results[3])
	}

	batch.Put()
}

func TestBatchHashCrossJoin_NoMatch(t *testing.T) {
	left := &testBatchProducer{
		batches: []*UT.Batch{makeIntBatch([]string{"k"}, []int64{1, 2})},
	}
	right := &testBatchProducer{
		batches: []*UT.Batch{makeIntBatch([]string{"k"}, []int64{3, 4})},
	}

	j := NewBatchHashCrossJoin(left, right, 0, 0)
	defer j.Close()

	ctx := context.Background()
	batch, err := j.NextBatch(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch != nil {
		t.Fatal("expected nil (no matches), got batch")
	}
}

func TestBatchHashCrossJoin_EmptyLeft(t *testing.T) {
	left := &testBatchProducer{batches: nil}
	right := &testBatchProducer{
		batches: []*UT.Batch{makeIntBatch([]string{"k"}, []int64{1, 2})},
	}

	j := NewBatchHashCrossJoin(left, right, 0, 0)
	defer j.Close()

	ctx := context.Background()
	batch, err := j.NextBatch(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch != nil {
		t.Fatal("expected nil (empty left), got batch")
	}
}

func TestBatchHashCrossJoin_MultiBatches(t *testing.T) {
	left := &testBatchProducer{
		batches: []*UT.Batch{
			makeIntBatch([]string{"k", "v"}, []int64{1, 2}, []int64{10, 20}),
			makeIntBatch([]string{"k", "v"}, []int64{3, 4}, []int64{30, 40}),
		},
	}
	right := &testBatchProducer{
		batches: []*UT.Batch{
			makeIntBatch([]string{"k"}, []int64{2, 3}),
		},
	}

	j := NewBatchHashCrossJoin(left, right, 0, 0)
	defer j.Close()

	ctx := context.Background()
	totalRows := 0
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
	// Matches: (2,20)-(2), (3,30)-(3) = 2 rows.
	if totalRows != 2 {
		t.Fatalf("expected 2 rows, got %d", totalRows)
	}
}

func TestBatchHashCrossJoin_NullKey(t *testing.T) {
	// Left has NULL key on first row.
	leftBatch := UT.GetBatch(2)
	leftBatch.SetColumnName(0, "k")
	leftBatch.SetColumnName(1, "v")
	leftBatch.Cols[0].Type = LX.T_INT_KW
	leftBatch.Cols[0].Data.Ints = []int64{1, 2}
	leftBatch.Cols[0].Nulls = []bool{true, false}
	leftBatch.Cols[1].Type = LX.T_INT_KW
	leftBatch.Cols[1].Data.Ints = []int64{10, 20}
	leftBatch.Size = 2

	left := &testBatchProducer{batches: []*UT.Batch{leftBatch}}
	right := &testBatchProducer{
		batches: []*UT.Batch{makeIntBatch([]string{"k"}, []int64{1, 2})},
	}

	j := NewBatchHashCrossJoin(left, right, 0, 0)
	defer j.Close()

	ctx := context.Background()
	batch, err := j.NextBatch(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch == nil {
		t.Fatal("expected batch, got nil")
	}
	// Only k=2 should match (k=1 was NULL).
	if batch.Size != 1 {
		t.Fatalf("expected 1 row, got %d", batch.Size)
	}
	k := UT.BatchValueAt(batch.Cols[0], 0).(int64)
	v := UT.BatchValueAt(batch.Cols[1], 0).(int64)
	if k != 2 || v != 20 {
		t.Errorf("expected (2,20), got (%d,%d)", k, v)
	}
	batch.Put()
}