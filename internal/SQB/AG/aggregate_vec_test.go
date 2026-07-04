package AG

import (
	"context"
	"testing"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// testBatchSource is a simple BatchProducer that returns batches
// from a pre-built slice, one at a time.
type testBatchSource struct {
	batches []*UT.Batch
	idx     int
}

func (s *testBatchSource) NextBatch(_ context.Context) (*UT.Batch, error) {
	if s.idx >= len(s.batches) {
		return nil, nil
	}
	b := s.batches[s.idx]
	s.idx++
	return b, nil
}

func (s *testBatchSource) Close() error { return nil }

// makeIntBatch creates a 1-column INT_KW batch with the given values.
func makeIntBatch(values []int64) *UT.Batch {
	b := UT.GetBatch(1)
	b.SetColumnName(0, "v")
	b.Cols[0].Type = LX.T_INT_KW
	b.Cols[0].Data.Ints = make([]int64, len(values))
	copy(b.Cols[0].Data.Ints, values)
	b.Size = len(values)
	return b
}

// makeGroupBatch creates a 2-column batch: col 0 = group key (INT),
// col 1 = value (INT).
func makeGroupBatch(keys, values []int64) *UT.Batch {
	b := UT.GetBatch(2)
	b.SetColumnName(0, "g")
	b.SetColumnName(1, "v")
	b.Cols[0].Type = LX.T_INT_KW
	b.Cols[0].Data.Ints = make([]int64, len(keys))
	copy(b.Cols[0].Data.Ints, keys)
	b.Cols[1].Type = LX.T_INT_KW
	b.Cols[1].Data.Ints = make([]int64, len(values))
	copy(b.Cols[1].Data.Ints, values)
	b.Size = len(keys)
	return b
}

func TestVectorizedHashAggregate_CountStar(t *testing.T) {
	src := &testBatchSource{
		batches: []*UT.Batch{
			makeIntBatch([]int64{1, 2, 3, 4, 5}),
		},
	}
	agg := NewVectorizedHashAggregate(src, -1, []AggDef{{Kind: AggCount, Col: -1}})
	defer agg.Close()

	batch, err := agg.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected result batch, got nil")
	}
	if batch.Size != 1 {
		t.Fatalf("expected 1 row, got %d", batch.Size)
	}
	val := batch.Value(0, 0)
	if val != int64(5) {
		t.Fatalf("expected COUNT(*)=5, got %v", val)
	}
	// Second call should return nil
	batch2, err := agg.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch2 != nil {
		t.Fatal("expected nil on second call")
	}
}

func TestVectorizedHashAggregate_SumInt64(t *testing.T) {
	src := &testBatchSource{
		batches: []*UT.Batch{
			makeIntBatch([]int64{10, 20, 30}),
		},
	}
	agg := NewVectorizedHashAggregate(src, -1, []AggDef{{Kind: AggSum, Col: 0}})
	defer agg.Close()

	batch, err := agg.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected result batch, got nil")
	}
	if batch.Size != 1 {
		t.Fatalf("expected 1 row, got %d", batch.Size)
	}
	val := batch.Value(0, 0)
	if val != int64(60) {
		t.Fatalf("expected SUM=60, got %v", val)
	}
}

func TestVectorizedHashAggregate_GroupByCount(t *testing.T) {
	src := &testBatchSource{
		batches: []*UT.Batch{
			makeGroupBatch([]int64{1, 1, 2, 2, 2, 3}, []int64{10, 20, 30, 40, 50, 60}),
		},
	}
	agg := NewVectorizedHashAggregate(src, 0, []AggDef{{Kind: AggCount, Col: -1}})
	defer agg.Close()

	batch, err := agg.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected result batch, got nil")
	}
	if batch.Size != 3 {
		t.Fatalf("expected 3 groups, got %d", batch.Size)
	}

	// Build a map of group key -> count for verification
	result := make(map[int64]int64)
	for i := range batch.Size {
		key := batch.Value(0, i).(int64)
		cnt := batch.Value(1, i).(int64)
		result[key] = cnt
	}
	if result[1] != 2 {
		t.Fatalf("expected group 1 count=2, got %d", result[1])
	}
	if result[2] != 3 {
		t.Fatalf("expected group 2 count=3, got %d", result[2])
	}
	if result[3] != 1 {
		t.Fatalf("expected group 3 count=1, got %d", result[3])
	}
}

func TestVectorizedHashAggregate_EmptyInput(t *testing.T) {
	src := &testBatchSource{
		batches: []*UT.Batch{},
	}
	agg := NewVectorizedHashAggregate(src, -1, []AggDef{{Kind: AggCount, Col: -1}})
	defer agg.Close()

	batch, err := agg.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected result batch, got nil")
	}
	if batch.Size != 1 {
		t.Fatalf("expected 1 row, got %d", batch.Size)
	}
	val := batch.Value(0, 0)
	if val != int64(0) {
		t.Fatalf("expected COUNT(*)=0 on empty input, got %v", val)
	}
}

func TestVectorizedHashAggregate_NullGroupKey(t *testing.T) {
	// NULL keys should be skipped in GROUP BY
	src := &testBatchSource{
		batches: []*UT.Batch{
			makeGroupBatch([]int64{1, 0, 2}, []int64{10, 20, 30}),
		},
	}
	// Set NULL on the second row of col 0 (group key)
	batch := src.batches[0]
	batch.Cols[0].Nulls = make([]bool, 3)
	batch.Cols[0].Nulls[1] = true // row 1 has NULL group key

	agg := NewVectorizedHashAggregate(src, 0, []AggDef{{Kind: AggCount, Col: -1}})
	defer agg.Close()

	result, err := agg.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected result batch, got nil")
	}
	// Should have 2 groups (key 1 and key 2), NULL skipped
	if result.Size != 2 {
		t.Fatalf("expected 2 groups (NULLs skipped), got %d", result.Size)
	}
}

func TestVectorizedHashAggregate_MultipleAggregates(t *testing.T) {
	src := &testBatchSource{
		batches: []*UT.Batch{
			makeIntBatch([]int64{5, 10, 15}),
		},
	}
	agg := NewVectorizedHashAggregate(src, -1, []AggDef{
		{Kind: AggCount, Col: -1},
		{Kind: AggSum, Col: 0},
		{Kind: AggMin, Col: 0},
		{Kind: AggMax, Col: 0},
	})
	defer agg.Close()

	batch, err := agg.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected result batch, got nil")
	}
	if batch.Size != 1 {
		t.Fatalf("expected 1 row, got %d", batch.Size)
	}
	if batch.Value(0, 0) != int64(3) {
		t.Fatalf("expected COUNT=3, got %v", batch.Value(0, 0))
	}
	if batch.Value(1, 0) != int64(30) {
		t.Fatalf("expected SUM=30, got %v", batch.Value(1, 0))
	}
	if batch.Value(2, 0) != int64(5) {
		t.Fatalf("expected MIN=5, got %v", batch.Value(2, 0))
	}
	if batch.Value(3, 0) != int64(15) {
		t.Fatalf("expected MAX=15, got %v", batch.Value(3, 0))
	}
}

func TestVectorizedHashAggregate_GroupBySum(t *testing.T) {
	src := &testBatchSource{
		batches: []*UT.Batch{
			makeGroupBatch([]int64{1, 1, 2, 2, 2}, []int64{10, 20, 30, 40, 50}),
		},
	}
	agg := NewVectorizedHashAggregate(src, 0, []AggDef{{Kind: AggSum, Col: 1}})
	defer agg.Close()

	batch, err := agg.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected result batch, got nil")
	}
	if batch.Size != 2 {
		t.Fatalf("expected 2 groups, got %d", batch.Size)
	}

	result := make(map[int64]int64)
	for i := range batch.Size {
		key := batch.Value(0, i).(int64)
		sum := batch.Value(1, i).(int64)
		result[key] = sum
	}
	if result[1] != 30 {
		t.Fatalf("expected group 1 SUM=30, got %d", result[1])
	}
	if result[2] != 120 {
		t.Fatalf("expected group 2 SUM=120, got %d", result[2])
	}
}

func TestVectorizedHashAggregate_Close(t *testing.T) {
	src := &testBatchSource{
		batches: []*UT.Batch{
			makeIntBatch([]int64{1}),
		},
	}
	agg := NewVectorizedHashAggregate(src, -1, []AggDef{{Kind: AggCount, Col: -1}})
	if err := agg.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestVectorizedHashAggregate_MultipleBatches(t *testing.T) {
	src := &testBatchSource{
		batches: []*UT.Batch{
			makeGroupBatch([]int64{1, 2}, []int64{10, 20}),
			makeGroupBatch([]int64{1, 2}, []int64{30, 40}),
		},
	}
	agg := NewVectorizedHashAggregate(src, 0, []AggDef{{Kind: AggSum, Col: 1}})
	defer agg.Close()

	batch, err := agg.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected result batch, got nil")
	}
	if batch.Size != 2 {
		t.Fatalf("expected 2 groups, got %d", batch.Size)
	}

	result := make(map[int64]int64)
	for i := range batch.Size {
		key := batch.Value(0, i).(int64)
		sum := batch.Value(1, i).(int64)
		result[key] = sum
	}
	if result[1] != 40 {
		t.Fatalf("expected group 1 SUM=40, got %d", result[1])
	}
	if result[2] != 60 {
		t.Fatalf("expected group 2 SUM=60, got %d", result[2])
	}
}
