package OP

import (
	"context"
	"testing"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

func TestVectorizedHashJoin_BloomFilterPushdown(t *testing.T) {
	// Build side: keys 1..100 with values 10*i.
	// Probe side: mix of keys that exist (2, 4, 6) and keys that
	// definitely do not exist (1001..1100) to exercise the bloom
	// filter quick-exit path.
	buildKeys := make([]int64, 100)
	buildVals := make([]int64, 100)
	for i := 0; i < 100; i++ {
		buildKeys[i] = int64(i + 1)
		buildVals[i] = int64((i + 1) * 10)
	}
	build := &testBatchProducer{
		batches: []*UT.Batch{makeJoinBuildBatch(buildKeys, buildVals)},
	}

	probeKeys := make([]int64, 103)
	probeKeys[0] = 2
	probeKeys[1] = 4
	probeKeys[2] = 6
	for i := 0; i < 100; i++ {
		probeKeys[i+3] = 1001 + int64(i)
	}
	probe := &testBatchProducer{
		batches: []*UT.Batch{makeJoinProbeBatch(probeKeys)},
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

	// Only 3 probe keys (2, 4, 6) should match.
	if batch.Size != 3 {
		t.Fatalf("expected 3 rows, got %d", batch.Size)
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
	if results[6] != 60 {
		t.Errorf("expected k=6,v=60, got v=%d", results[6])
	}

	batch.Put()
}

func TestVectorizedHashJoin_BloomFilter_AllMiss(t *testing.T) {
	build := &testBatchProducer{
		batches: []*UT.Batch{makeJoinBuildBatch(
			[]int64{100, 200, 300},
			[]int64{1, 2, 3},
		)},
	}
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
		t.Fatalf("expected nil (no matches), got batch with %d rows", batch.Size)
	}
}

func TestVectorizedHashJoin_BloomFilter_NegativeKeys(t *testing.T) {
	// Verify bloom filter works with negative int64 keys.
	build := &testBatchProducer{
		batches: []*UT.Batch{makeJoinBuildBatch(
			[]int64{-5, -3, 0, 3, 5},
			[]int64{10, 20, 30, 40, 50},
		)},
	}
	probe := &testBatchProducer{
		batches: []*UT.Batch{makeJoinProbeBatch([]int64{-3, 0, 7})},
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
	if results[-3] != 20 {
		t.Errorf("expected k=-3,v=20, got v=%d", results[-3])
	}
	if results[0] != 30 {
		t.Errorf("expected k=0,v=30, got v=%d", results[0])
	}

	batch.Put()
}

func TestVectorizedHashJoin_BloomFilter_FloatKeys(t *testing.T) {
	// Build a batch with float key column.
	b := UT.GetBatch(2)
	b.SetColumnName(0, "k")
	b.SetColumnName(1, "v")
	b.Cols[0].Type = LX.T_FLOAT_KW
	b.Cols[0].Data.Floats = []float64{1.5, 2.5, 3.5}
	b.Cols[1].Type = LX.T_FLOAT_KW
	b.Cols[1].Data.Floats = []float64{10, 20, 30}
	b.Size = 3

	build := &testBatchProducer{batches: []*UT.Batch{b}}

	// Probe with one match (2.5) and two misses.
	probe := &testBatchProducer{
		batches: []*UT.Batch{makeFloatProbeBatch([]float64{2.5, 9.9, 1.5})},
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

	results := make(map[float64]float64)
	for i := 0; i < batch.Size; i++ {
		k := UT.BatchValueAt(batch.Cols[0], i).(float64)
		v := UT.BatchValueAt(batch.Cols[1], i).(float64)
		results[k] = v
	}
	if results[2.5] != 20 {
		t.Errorf("expected k=2.5,v=20, got v=%f", results[2.5])
	}
	if results[1.5] != 10 {
		t.Errorf("expected k=1.5,v=10, got v=%f", results[1.5])
	}

	batch.Put()
}

func makeFloatProbeBatch(keys []float64) *UT.Batch {
	n := len(keys)
	b := UT.GetBatch(1)
	b.SetColumnName(0, "pk")
	b.Cols[0].Type = LX.T_FLOAT_KW
	b.Cols[0].Data.Floats = make([]float64, n)
	for i := 0; i < n; i++ {
		b.Cols[0].Data.Floats[i] = keys[i]
	}
	b.Size = n
	return b
}
