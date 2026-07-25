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
	agg := NewVectorizedHashAggregate(src, nil, []AggDef{{Kind: AggCount, Col: -1}})

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
	agg := NewVectorizedHashAggregate(src, nil, []AggDef{{Kind: AggSum, Col: 0}})
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
	agg := NewVectorizedHashAggregate(src, []int{0}, []AggDef{{Kind: AggCount, Col: -1}})
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
	agg := NewVectorizedHashAggregate(src, nil, []AggDef{{Kind: AggCount, Col: -1}})

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

	agg := NewVectorizedHashAggregate(src, []int{0}, []AggDef{{Kind: AggCount, Col: -1}})
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
	agg := NewVectorizedHashAggregate(src, nil, []AggDef{
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
	agg := NewVectorizedHashAggregate(src, []int{0}, []AggDef{{Kind: AggSum, Col: 1}})
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
	agg := NewVectorizedHashAggregate(src, nil, []AggDef{{Kind: AggCount, Col: -1}})
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
	agg := NewVectorizedHashAggregate(src, []int{0}, []AggDef{{Kind: AggSum, Col: 1}})
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

// makeGroupBatch3 creates a 3-column batch: col 0 = group key 0 (INT),
// col 1 = group key 1 (INT), col 2 = value (INT).
func makeGroupBatch3(keys0, keys1, values []int64) *UT.Batch {
	if len(keys0) != len(keys1) || len(keys0) != len(values) {
		panic("length mismatch")
	}
	b := UT.GetBatch(3)
	b.SetColumnName(0, "g0")
	b.SetColumnName(1, "g1")
	b.SetColumnName(2, "v")
	b.Cols[0].Type = LX.T_INT_KW
	b.Cols[0].Data.Ints = make([]int64, len(keys0))
	copy(b.Cols[0].Data.Ints, keys0)
	b.Cols[1].Type = LX.T_INT_KW
	b.Cols[1].Data.Ints = make([]int64, len(keys1))
	copy(b.Cols[1].Data.Ints, keys1)
	b.Cols[2].Type = LX.T_INT_KW
	b.Cols[2].Data.Ints = make([]int64, len(values))
	copy(b.Cols[2].Data.Ints, values)
	b.Size = len(keys0)
	return b
}

func TestVectorizedHashAggregate_GroupByMultipleColumns(t *testing.T) {
	src := &testBatchSource{
		batches: []*UT.Batch{
			makeGroupBatch3(
				[]int64{1, 1, 2, 2, 1},
				[]int64{10, 20, 10, 20, 10},
				[]int64{100, 200, 300, 400, 500},
			),
		},
	}
	// GROUP BY g0, g1: (1,10)→2 rows, (1,20)→1 row, (2,10)→1 row, (2,20)→1 row
	// Expected COUNT per group:
	//   (1,10) → 2
	//   (1,20) → 1
	//   (2,10) → 1
	//   (2,20) → 1
	agg := NewVectorizedHashAggregate(src, []int{0, 1}, []AggDef{{Kind: AggCount, Col: -1}})
	defer agg.Close()

	batch, err := agg.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected result batch, got nil")
	}
	if batch.Size != 4 {
		t.Fatalf("expected 4 groups, got %d", batch.Size)
	}

	// Build result map: (g0,g1) -> count
	results := make(map[[2]int64]int64)
	for i := range batch.Size {

		g0 := batch.Value(0, i).(int64)
		g1 := batch.Value(1, i).(int64)
		count := batch.Value(2, i).(int64)
		results[[2]int64{g0, g1}] = count
	}

	tests := map[[2]int64]int64{
		{1, 10}: 2,
		{1, 20}: 1,
		{2, 10}: 1,
		{2, 20}: 1,
	}
	for key, expected := range tests {
		actual, ok := results[key]
		if !ok {
			t.Fatalf("expected group (%d, %d) not found", key[0], key[1])
		}
		if actual != expected {
			t.Fatalf("expected group (%d, %d) COUNT=%d, got %d", key[0], key[1], expected, actual)
		}
	}
}

func TestVectorizedHashAggregate_GroupByMultipleColumns_SUM(t *testing.T) {
	src := &testBatchSource{
		batches: []*UT.Batch{
			makeGroupBatch3(
				[]int64{1, 1, 2, 2},
				[]int64{10, 20, 10, 20},
				[]int64{100, 200, 300, 400},
			),
		},
	}
	// GROUP BY g0, g1, SUM(v):
	//   (1,10) → SUM=100
	//   (1,20) → SUM=200
	//   (2,10) → SUM=300
	//   (2,20) → SUM=400
	agg := NewVectorizedHashAggregate(src, []int{0, 1}, []AggDef{{Kind: AggSum, Col: 2}})
	defer agg.Close()

	batch, err := agg.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected result batch, got nil")
	}
	if batch.Size != 4 {
		t.Fatalf("expected 4 groups, got %d", batch.Size)
	}

	sumResults := make(map[[2]int64]int64)
	for i := range batch.Size {

		g0 := batch.Value(0, i).(int64)
		g1 := batch.Value(1, i).(int64)
		sum := batch.Value(2, i).(int64)
		sumResults[[2]int64{g0, g1}] = sum
	}

	expected := map[[2]int64]int64{
		{1, 10}: 100,
		{1, 20}: 200,
		{2, 10}: 300,
		{2, 20}: 400,
	}
	for key, exp := range expected {

		actual, ok := sumResults[key]
		if !ok {
			t.Fatalf("expected group (%d, %d) not found", key[0], key[1])
		}
		if actual != exp {
			t.Fatalf("expected group (%d, %d) SUM=%d, got %d", key[0], key[1], exp, actual)
		}
	}
}

func TestVectorizedHashAggregate_NoGroupBy(t *testing.T) {
	src := &testBatchSource{
		batches: []*UT.Batch{
			makeIntBatch([]int64{1, 2, 3}),
		},
	}
	agg := NewVectorizedHashAggregate(src, nil, []AggDef{{Kind: AggCount, Col: -1}})
	defer agg.Close()

	batch, err := agg.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected result batch, got nil")
	}
	if batch.Size != 1 {
		t.Fatalf("expected 1 row (no GROUP BY), got %d", batch.Size)
	}
	if batch.Value(0, 0) != int64(3) {

		t.Fatalf("expected COUNT=3, got %v", batch.Value(0, 0))
	}
}

// REQ001993: GROUP_CONCAT vectorized tests.

// makeStrBatch creates a 1-column TEXT batch with the given values.
func makeStrBatch(values []string) *UT.Batch {
	b := UT.GetBatch(1)
	b.SetColumnName(0, "v")
	b.Cols[0].Type = LX.T_TEXT
	b.Cols[0].Data.Strs = make([]string, len(values))
	copy(b.Cols[0].Data.Strs, values)
	b.Size = len(values)
	return b
}

// makeGroupStrBatch creates a 2-column batch: col 0 = group key (INT),
// col 1 = value (TEXT).
func makeGroupStrBatch(keys []int64, values []string) *UT.Batch {
	b := UT.GetBatch(2)
	b.SetColumnName(0, "g")
	b.SetColumnName(1, "v")
	b.Cols[0].Type = LX.T_INT_KW
	b.Cols[0].Data.Ints = make([]int64, len(keys))
	copy(b.Cols[0].Data.Ints, keys)
	b.Cols[1].Type = LX.T_TEXT
	b.Cols[1].Data.Strs = make([]string, len(values))
	copy(b.Cols[1].Data.Strs, values)
	b.Size = len(keys)
	return b
}

func TestVectorizedHashAggregate_GroupConcatNoGroup(t *testing.T) {
	src := &testBatchSource{
		batches: []*UT.Batch{
			makeStrBatch([]string{"a", "b", "c"}),
		},
	}
	agg := NewVectorizedHashAggregate(src, nil, []AggDef{{Kind: AggGroupConcat, Col: 0, Separator: ","}})
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
	if val != "a,b,c" {
		t.Fatalf("expected GROUP_CONCAT='a,b,c', got %v", val)
	}
}

func TestVectorizedHashAggregate_GroupConcatCustomSeparator(t *testing.T) {
	src := &testBatchSource{
		batches: []*UT.Batch{
			makeStrBatch([]string{"x", "y", "z"}),
		},
	}
	agg := NewVectorizedHashAggregate(src, nil, []AggDef{{Kind: AggGroupConcat, Col: 0, Separator: "|"}})
	defer agg.Close()

	batch, err := agg.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	val := batch.Value(0, 0)
	if val != "x|y|z" {
		t.Fatalf("expected GROUP_CONCAT='x|y|z', got %v", val)
	}
}

func TestVectorizedHashAggregate_GroupConcatGroupBy(t *testing.T) {
	src := &testBatchSource{
		batches: []*UT.Batch{
			makeGroupStrBatch([]int64{1, 1, 2, 2}, []string{"a", "b", "c", "d"}),
		},
	}
	agg := NewVectorizedHashAggregate(src, []int{0}, []AggDef{{Kind: AggGroupConcat, Col: 1, Separator: ","}})
	defer agg.Close()

	batch, err := agg.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch.Size != 2 {
		t.Fatalf("expected 2 rows, got %d", batch.Size)
	}
	// Hash order is not guaranteed; collect results into a map
	results := make(map[int64]string)
	for i := 0; i < batch.Size; i++ {
		key := batch.Value(0, i)
		val := batch.Cols[1].Data.Strs[i]
		results[key.(int64)] = val
	}
	if results[1] != "a,b" {
		t.Fatalf("expected group 1 = 'a,b', got %q", results[1])
	}
	if results[2] != "c,d" {
		t.Fatalf("expected group 2 = 'c,d', got %q", results[2])
	}
}

func TestVectorizedHashAggregate_GroupConcatDistinct(t *testing.T) {
	src := &testBatchSource{
		batches: []*UT.Batch{
			makeStrBatch([]string{"a", "b", "a", "c", "b"}),
		},
	}
	agg := NewVectorizedHashAggregate(src, nil, []AggDef{{Kind: AggGroupConcat, Col: 0, Separator: ",", Distinct: true}})
	defer agg.Close()

	batch, err := agg.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	val := batch.Value(0, 0)
	// DISTINCT order: first-seen order = a, b, c
	if val != "a,b,c" {
		t.Fatalf("expected DISTINCT GROUP_CONCAT='a,b,c', got %v", val)
	}
}

func TestVectorizedHashAggregate_GroupConcatEmpty(t *testing.T) {
	src := &testBatchSource{
		batches: []*UT.Batch{},
	}
	agg := NewVectorizedHashAggregate(src, nil, []AggDef{{Kind: AggGroupConcat, Col: 0, Separator: ","}})
	defer agg.Close()

	batch, err := agg.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected result batch, got nil")
	}
	if batch.Size != 1 {
		t.Fatalf("expected 1 row (empty result placeholder), got %d", batch.Size)
	}
	// Empty GROUP_CONCAT returns NULL (represented as empty string in this test)
	val := batch.Value(0, 0)
	if val != "" {
		t.Fatalf("expected empty string for empty GROUP_CONCAT, got %v", val)
	}
}
