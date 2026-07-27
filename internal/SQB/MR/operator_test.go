package MR

import (
	"context"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// testBatchSource is a simple BatchProducer for testing.
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

func TestMapReduceOperator_ScalarCountStar(t *testing.T) {
	specs := []AccumulatorSpec{
		{Kind: AggCount, Col: -1},
	}
	reducer := NewAggregateReducer(specs, nil)
	shuffler := NewScalarShuffler(reducer, specs)
	mapper := NewAggregateMapper(nil)

	src := &testBatchSource{
		batches: []*UT.Batch{
			makeIntBatch([]int64{1, 2, 3}),
			makeIntBatch([]int64{4, 5}),
		},
	}

	op := NewMapReduceOperator(src, mapper, shuffler)
	defer op.Close()

	ctx := context.Background()
	batch, err := op.NextBatch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected result batch")
	}
	defer batch.Put()

	if batch.Size != 1 {
		t.Fatalf("expected 1 row, got %d", batch.Size)
	}
	if batch.Cols[0].Data.Ints[0] != 5 {
		t.Fatalf("expected count=5, got %d", batch.Cols[0].Data.Ints[0])
	}

	// Second call should return nil (EOF)
	batch2, err := op.NextBatch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if batch2 != nil {
		t.Fatal("expected EOF on second call")
	}
}

func TestMapReduceOperator_GroupByCount(t *testing.T) {
	specs := []AccumulatorSpec{
		{Kind: AggCount, Col: -1},
	}
	reducer := NewAggregateReducer(specs, []int{0})
	extractor := NewSimpleIntKeyExtractor(0)
	shuffler := NewHashShuffler(reducer, specs, extractor)
	mapper := NewAggregateMapper([]int{0})

	src := &testBatchSource{
		batches: []*UT.Batch{
			makeGroupBatch([]int64{1, 1, 2}, []int64{10, 20, 30}),
			makeGroupBatch([]int64{1, 3}, []int64{40, 50}),
		},
	}

	op := NewMapReduceOperator(src, mapper, shuffler)
	defer op.Close()

	ctx := context.Background()
	batch, err := op.NextBatch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected result batch")
	}
	defer batch.Put()

	if batch.Size != 3 {
		t.Fatalf("expected 3 groups, got %d", batch.Size)
	}

	// group 1: 3 rows (2 from batch1 + 1 from batch2)
	checkGroupCount(t, batch, 1, 3)
	// group 2: 1 row
	checkGroupCount(t, batch, 2, 1)
	// group 3: 1 row
	checkGroupCount(t, batch, 3, 1)
}

func TestMapReduceOperator_ResetAndReuse(t *testing.T) {
	specs := []AccumulatorSpec{
		{Kind: AggCount, Col: -1},
	}
	reducer := NewAggregateReducer(specs, []int{0})
	extractor := NewSimpleIntKeyExtractor(0)
	shuffler := NewHashShuffler(reducer, specs, extractor)
	mapper := NewAggregateMapper([]int{0})

	src := &testBatchSource{
		batches: []*UT.Batch{
			makeGroupBatch([]int64{1, 1, 2}, []int64{10, 20, 30}),
		},
	}

	op := NewMapReduceOperator(src, mapper, shuffler)
	defer op.Close()

	ctx := context.Background()

	// First run
	batch1, err := op.NextBatch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	size1 := batch1.Size
	batch1.Put()

	if size1 != 2 {
		t.Fatalf("first run: expected 2 groups, got %d", size1)
	}

	// Reset and re-run with same source (reset the source too)
	src.idx = 0
	op.Reset()

	batch2, err := op.NextBatch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if batch2 == nil {
		t.Fatal("expected result batch after reset")
	}
	defer batch2.Put()

	if batch2.Size != 2 {
		t.Fatalf("second run: expected 2 groups, got %d", batch2.Size)
	}
	checkGroupCount(t, batch2, 1, 2)
	checkGroupCount(t, batch2, 2, 1)
}

func TestMapReduceOperator_EmptyInput(t *testing.T) {
	specs := []AccumulatorSpec{
		{Kind: AggCount, Col: -1},
	}
	reducer := NewAggregateReducer(specs, nil)
	shuffler := NewScalarShuffler(reducer, specs)
	mapper := NewAggregateMapper(nil)

	src := &testBatchSource{batches: nil}
	op := NewMapReduceOperator(src, mapper, shuffler)
	defer op.Close()

	ctx := context.Background()
	batch, err := op.NextBatch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected result batch for empty input (COUNT(*) = 0)")
	}
	defer batch.Put()

	if batch.Cols[0].Data.Ints[0] != 0 {
		t.Fatalf("expected count=0 for empty input, got %d", batch.Cols[0].Data.Ints[0])
	}
}

func TestMapReduceOperator_SumDistinct(t *testing.T) {
	specs := []AccumulatorSpec{
		{Kind: AggSum, Col: 1, Distinct: true},
	}
	reducer := NewAggregateReducer(specs, []int{0})
	extractor := NewSimpleIntKeyExtractor(0)
	shuffler := NewHashShuffler(reducer, specs, extractor)
	mapper := NewAggregateMapper([]int{0})

	src := &testBatchSource{
		batches: []*UT.Batch{
			makeGroupBatch([]int64{1, 1, 1}, []int64{10, 10, 20}),
			makeGroupBatch([]int64{2, 2}, []int64{5, 5}),
		},
	}

	op := NewMapReduceOperator(src, mapper, shuffler)
	defer op.Close()

	ctx := context.Background()
	batch, err := op.NextBatch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected result batch")
	}
	defer batch.Put()

	// group 1: sum(distinct) = 10 + 20 = 30
	checkGroupSum(t, batch, 1, 30)
	// group 2: sum(distinct) = 5
	checkGroupSum(t, batch, 2, 5)
}
