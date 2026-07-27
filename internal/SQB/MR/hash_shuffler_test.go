package MR

import (
	"context"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

func TestHashShuffler_SingleGroupCount(t *testing.T) {
	specs := []AccumulatorSpec{
		{Kind: AggCount, Col: -1}, // COUNT(*)
	}
	reducer := NewAggregateReducer(specs, []int{0})
	extractor := NewSimpleIntKeyExtractor(0)
	shuffler := NewHashShuffler(reducer, specs, extractor)

	// Create batch with 2 groups: group=1 has 3 rows, group=2 has 2 rows
	batch := makeGroupBatch(
		[]int64{1, 1, 2, 1, 2}, // group keys
		[]int64{10, 20, 30, 40, 50}, // values
	)
	defer batch.Put()

	ctx := context.Background()
	if err := shuffler.AcceptBatch(ctx, batch); err != nil {
		t.Fatal(err)
	}

	result, err := shuffler.Finalize(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 result batch, got %d", len(result))
	}

	b := result[0]
	defer b.Put()

	if b.Size != 2 {
		t.Fatalf("expected 2 groups, got %d rows", b.Size)
	}

	// Check group 1 count = 3
	if b.Cols[0].Data.Ints[0] != 1 && b.Cols[0].Data.Ints[1] != 1 {
		// group order may vary, find group 1
		found := false
		for i := 0; i < b.Size; i++ {
			if b.Cols[0].Data.Ints[i] == 1 {
				count := b.Cols[1].Data.Ints[i]
				if count != 3 {
					t.Fatalf("group 1: expected count=3, got %d", count)
				}
				found = true
				break
			}
		}
		if !found {
			t.Fatal("group 1 not found in result")
		}
	}

	// Check group 2 count = 2
	found2 := false
	for i := 0; i < b.Size; i++ {
		if b.Cols[0].Data.Ints[i] == 2 {
			count := b.Cols[1].Data.Ints[i]
			if count != 2 {
				t.Fatalf("group 2: expected count=2, got %d", count)
			}
			found2 = true
			break
		}
	}
	if !found2 {
		t.Fatal("group 2 not found in result")
	}
}

func TestHashShuffler_GroupBySum(t *testing.T) {
	specs := []AccumulatorSpec{
		{Kind: AggSum, Col: 1}, // SUM(value)
	}
	reducer := NewAggregateReducer(specs, []int{0})
	extractor := NewSimpleIntKeyExtractor(0)
	shuffler := NewHashShuffler(reducer, specs, extractor)

	batch := makeGroupBatch(
		[]int64{1, 1, 2, 1, 2}, // group keys
		[]int64{10, 20, 30, 40, 50}, // values
	)
	defer batch.Put()

	ctx := context.Background()
	if err := shuffler.AcceptBatch(ctx, batch); err != nil {
		t.Fatal(err)
	}

	result, err := shuffler.Finalize(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b := result[0]
	defer b.Put()

	// group 1: 10+20+40 = 70
	// group 2: 30+50 = 80
	checkGroupSum(t, b, 1, 70)
	checkGroupSum(t, b, 2, 80)
}

func TestHashShuffler_EmptyInput(t *testing.T) {
	specs := []AccumulatorSpec{
		{Kind: AggCount, Col: -1},
	}
	reducer := NewAggregateReducer(specs, []int{0})
	extractor := NewSimpleIntKeyExtractor(0)
	shuffler := NewHashShuffler(reducer, specs, extractor)

	ctx := context.Background()
	result, err := shuffler.Finalize(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b := result[0]
	defer b.Put()

	if b.Size != 0 {
		t.Fatalf("expected 0 rows for empty input, got %d", b.Size)
	}
}

func TestHashShuffler_NullKeysExcluded(t *testing.T) {
	specs := []AccumulatorSpec{
		{Kind: AggCount, Col: -1},
	}
	reducer := NewAggregateReducer(specs, []int{0})
	extractor := NewSimpleIntKeyExtractor(0)
	shuffler := NewHashShuffler(reducer, specs, extractor)

	batch := makeGroupBatch(
		[]int64{1, 0, 2}, // group keys (key at index 1 is "null" in our test)
		[]int64{10, 20, 30},
	)
	// Mark row 1 as NULL in key column
	batch.Cols[0].Nulls[1] = true
	defer batch.Put()

	ctx := context.Background()
	if err := shuffler.AcceptBatch(ctx, batch); err != nil {
		t.Fatal(err)
	}

	result, err := shuffler.Finalize(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b := result[0]
	defer b.Put()

	if b.Size != 2 {
		t.Fatalf("expected 2 groups (NULL key excluded), got %d", b.Size)
	}
}

func TestHashShuffler_ResetAndReuse(t *testing.T) {
	specs := []AccumulatorSpec{
		{Kind: AggCount, Col: -1},
	}
	reducer := NewAggregateReducer(specs, []int{0})
	extractor := NewSimpleIntKeyExtractor(0)
	shuffler := NewHashShuffler(reducer, specs, extractor)

	ctx := context.Background()

	// First run
	batch1 := makeGroupBatch([]int64{1, 1, 2}, []int64{10, 20, 30})
	if err := shuffler.AcceptBatch(ctx, batch1); err != nil {
		t.Fatal(err)
	}
	batch1.Put()

	result1, err := shuffler.Finalize(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b1 := result1[0]
	size1 := b1.Size
	b1.Put()

	if size1 != 2 {
		t.Fatalf("first run: expected 2 groups, got %d", size1)
	}

	// Reset
	shuffler.Reset()

	// Second run with different data
	batch2 := makeGroupBatch([]int64{3, 3, 3, 4}, []int64{1, 2, 3, 4})
	if err := shuffler.AcceptBatch(ctx, batch2); err != nil {
		t.Fatal(err)
	}
	batch2.Put()

	result2, err := shuffler.Finalize(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b2 := result2[0]
	defer b2.Put()

	if b2.Size != 2 {
		t.Fatalf("second run: expected 2 groups, got %d", b2.Size)
	}

	// Verify group 3 has count 3, group 4 has count 1
	checkGroupCount(t, b2, 3, 3)
	checkGroupCount(t, b2, 4, 1)
}

func TestScalarShuffler_CountStar(t *testing.T) {
	specs := []AccumulatorSpec{
		{Kind: AggCount, Col: -1},
	}
	reducer := NewAggregateReducer(specs, nil)
	shuffler := NewScalarShuffler(reducer, specs)

	batch := makeIntBatch([]int64{1, 2, 3, 4, 5})
	defer batch.Put()

	ctx := context.Background()
	if err := shuffler.AcceptBatch(ctx, batch); err != nil {
		t.Fatal(err)
	}

	result, err := shuffler.Finalize(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b := result[0]
	defer b.Put()

	if b.Size != 1 {
		t.Fatalf("expected 1 row, got %d", b.Size)
	}
	if b.Cols[0].Data.Ints[0] != 5 {
		t.Fatalf("expected count=5, got %d", b.Cols[0].Data.Ints[0])
	}
}

func TestScalarShuffler_Sum(t *testing.T) {
	specs := []AccumulatorSpec{
		{Kind: AggSum, Col: 0},
	}
	reducer := NewAggregateReducer(specs, nil)
	shuffler := NewScalarShuffler(reducer, specs)

	batch := makeIntBatch([]int64{10, 20, 30})
	defer batch.Put()

	ctx := context.Background()
	if err := shuffler.AcceptBatch(ctx, batch); err != nil {
		t.Fatal(err)
	}

	result, err := shuffler.Finalize(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b := result[0]
	defer b.Put()

	if b.Cols[0].Data.Ints[0] != 60 {
		t.Fatalf("expected sum=60, got %d", b.Cols[0].Data.Ints[0])
	}
}

func TestScalarShuffler_EmptyInput(t *testing.T) {
	specs := []AccumulatorSpec{
		{Kind: AggCount, Col: -1},
	}
	reducer := NewAggregateReducer(specs, nil)
	shuffler := NewScalarShuffler(reducer, specs)

	ctx := context.Background()
	result, err := shuffler.Finalize(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b := result[0]
	defer b.Put()

	// COUNT(*) of empty set = 0
	if b.Size != 1 {
		t.Fatalf("expected 1 row, got %d", b.Size)
	}
	if b.Cols[0].Data.Ints[0] != 0 {
		t.Fatalf("expected count=0 for empty input, got %d", b.Cols[0].Data.Ints[0])
	}
}

// Helper: create a 2-column batch: col0=group key, col1=value
func makeGroupBatch(keys, values []int64) *UT.Batch {
	b := UT.GetBatch(2)
	b.Pooled = false // don't return to pool; caller owns lifecycle
	b.SetColumnName(0, "g")
	b.SetColumnName(1, "v")
	b.Cols[0].Type = LX.T_INT_KW
	b.Cols[1].Type = LX.T_INT_KW
	b.Cols[0].Data.Ints = make([]int64, len(keys))
	b.Cols[1].Data.Ints = make([]int64, len(values))
	copy(b.Cols[0].Data.Ints, keys)
	copy(b.Cols[1].Data.Ints, values)
	b.Cols[0].Nulls = make([]bool, len(keys))
	b.Cols[1].Nulls = make([]bool, len(values))
	b.Size = len(keys)
	return b
}

func checkGroupSum(t *testing.T, b *UT.Batch, groupKey int64, expected int64) {
	t.Helper()
	for i := 0; i < b.Size; i++ {
		if b.Cols[0].Data.Ints[i] == groupKey {
			if b.Cols[1].Data.Ints[i] != expected {
				t.Fatalf("group %d: expected sum=%d, got %d", groupKey, expected, b.Cols[1].Data.Ints[i])
			}
			return
		}
	}
	t.Fatalf("group %d not found in result", groupKey)
}

func checkGroupCount(t *testing.T, b *UT.Batch, groupKey int64, expected int64) {
	t.Helper()
	for i := 0; i < b.Size; i++ {
		if b.Cols[0].Data.Ints[i] == groupKey {
			if b.Cols[1].Data.Ints[i] != expected {
				t.Fatalf("group %d: expected count=%d, got %d", groupKey, expected, b.Cols[1].Data.Ints[i])
			}
			return
		}
	}
	t.Fatalf("group %d not found in result", groupKey)
}
