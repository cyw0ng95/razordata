package OP

import (
	"context"
	"testing"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// makeJoinBuildBatch creates a build-side batch with key and value columns.
func makeJoinBuildBatch(keys, vals []int64) *UT.Batch {
	n := len(keys)
	b := UT.GetBatch(2)
	b.SetColumnName(0, "k")
	b.SetColumnName(1, "v")
	b.Cols[0].Type = LX.T_INT_KW
	b.Cols[0].Data.Ints = make([]int64, n)
	b.Cols[1].Type = LX.T_INT_KW
	b.Cols[1].Data.Ints = make([]int64, n)
	for i := 0; i < n; i++ {
		b.Cols[0].Data.Ints[i] = keys[i]
		b.Cols[1].Data.Ints[i] = vals[i]
	}
	b.Size = n
	return b
}

// makeJoinProbeBatch creates a probe-side batch with a single key column.
func makeJoinProbeBatch(keys []int64) *UT.Batch {
	n := len(keys)
	b := UT.GetBatch(1)
	b.SetColumnName(0, "pk")
	b.Cols[0].Type = LX.T_INT_KW
	b.Cols[0].Data.Ints = make([]int64, n)
	for i := 0; i < n; i++ {
		b.Cols[0].Data.Ints[i] = keys[i]
	}
	b.Size = n
	return b
}

func TestVectorizedHashJoin_InnerEquiJoin(t *testing.T) {
	build := &testBatchProducer{
		batches: []*UT.Batch{makeJoinBuildBatch(
			[]int64{1, 2, 3},
			[]int64{10, 20, 30},
		)},
	}
	probe := &testBatchProducer{
		batches: []*UT.Batch{makeJoinProbeBatch([]int64{2, 3, 4})},
	}

	j := NewVectorizedHashJoin(build, probe, 0, 0)
	defer j.Close()

	ctx := context.Background()
	batch, err := j.NextBatch(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch == nil {
		t.Fatal("expected batch, got nil")
	}

	// Should have 2 matching rows: (2,20) and (3,30).
	if batch.Size != 2 {
		t.Fatalf("expected 2 rows, got %d", batch.Size)
	}

	// Check output columns: k, v, pk (first 3 populated columns).
	if batch.Cols[0].Name != "k" || batch.Cols[1].Name != "v" || batch.Cols[2].Name != "pk" {
		t.Fatalf("expected columns k,v,pk, got %q,%q,%q", batch.Cols[0].Name, batch.Cols[1].Name, batch.Cols[2].Name)
	}

	// Verify values: row 0 should be (2, 20, 2), row 1 should be (3, 30, 3).
	// The order depends on hash table slot order, so sort or check both possibilities.
	results := make(map[int64]int64) // k -> v
	for i := 0; i < batch.Size; i++ {
		k := UT.BatchValueAt(batch.Cols[0], i).(int64)
		v := UT.BatchValueAt(batch.Cols[1], i).(int64)
		results[k] = v
	}
	if results[2] != 20 {
		t.Errorf("expected k=2,v=20, got k=2,v=%d", results[2])
	}
	if results[3] != 30 {
		t.Errorf("expected k=3,v=30, got k=3,v=%d", results[3])
	}

	// Verify probe key column.
	for i := 0; i < batch.Size; i++ {
		pk := UT.BatchValueAt(batch.Cols[2], i).(int64)
		k := UT.BatchValueAt(batch.Cols[0], i).(int64)
		if pk != k {
			t.Errorf("row %d: probe key %d != build key %d", i, pk, k)
		}
	}

	batch.Put()

	// Next call should return nil (EOF).
	batch2, err := j.NextBatch(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch2 != nil {
		t.Fatal("expected nil (EOF), got batch")
	}
}

func TestVectorizedHashJoin_NoMatch(t *testing.T) {
	build := &testBatchProducer{
		batches: []*UT.Batch{makeJoinBuildBatch(
			[]int64{1, 2, 3},
			[]int64{10, 20, 30},
		)},
	}
	probe := &testBatchProducer{
		batches: []*UT.Batch{makeJoinProbeBatch([]int64{4, 5, 6})},
	}

	j := NewVectorizedHashJoin(build, probe, 0, 0)
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

func TestVectorizedHashJoin_MultipleBatches(t *testing.T) {
	// Build in 2 batches.
	build := &testBatchProducer{
		batches: []*UT.Batch{
			makeJoinBuildBatch([]int64{1, 2}, []int64{10, 20}),
			makeJoinBuildBatch([]int64{3, 4}, []int64{30, 40}),
		},
	}
	probe := &testBatchProducer{
		batches: []*UT.Batch{makeJoinProbeBatch([]int64{2, 4})},
	}

	j := NewVectorizedHashJoin(build, probe, 0, 0)
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
	if results[4] != 40 {
		t.Errorf("expected k=4,v=40, got v=%d", results[4])
	}

	batch.Put()
}

func TestVectorizedHashJoin_EmptyBuild(t *testing.T) {
	build := &testBatchProducer{batches: nil}
	probe := &testBatchProducer{
		batches: []*UT.Batch{makeJoinProbeBatch([]int64{1, 2, 3})},
	}

	j := NewVectorizedHashJoin(build, probe, 0, 0)
	defer j.Close()

	ctx := context.Background()
	batch, err := j.NextBatch(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch != nil {
		t.Fatalf("expected nil (empty build), got batch with %d rows", batch.Size)
	}
}

func TestVectorizedHashJoin_NULLKey(t *testing.T) {
	// Build side has NULL keys.
	buildBatch := UT.GetBatch(2)
	buildBatch.SetColumnName(0, "k")
	buildBatch.SetColumnName(1, "v")
	buildBatch.Cols[0].Type = LX.T_INT_KW
	buildBatch.Cols[0].Data.Ints = []int64{1, 2}
	buildBatch.Cols[0].Nulls = []bool{true, false} // first key is NULL
	buildBatch.Cols[1].Type = LX.T_INT_KW
	buildBatch.Cols[1].Data.Ints = []int64{10, 20}
	buildBatch.Size = 2

	build := &testBatchProducer{batches: []*UT.Batch{buildBatch}}
	probe := &testBatchProducer{
		batches: []*UT.Batch{makeJoinProbeBatch([]int64{1, 2})},
	}

	j := NewVectorizedHashJoin(build, probe, 0, 0)
	defer j.Close()

	ctx := context.Background()
	batch, err := j.NextBatch(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch == nil {
		t.Fatal("expected batch, got nil")
	}

	// Only key=2 should match (key=1 was NULL in build).
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
