package MR

import (
	"math"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	"github.com/cyw0ng95/razordata/internal/SYS/AP"
)

func TestUnifiedAccum_CountStar(t *testing.T) {
	acc := &UnifiedAccum{}
	spec := &AccumulatorSpec{Kind: AggCount, Col: -1}

	batch := makeIntBatch([]int64{1, 2, 3, 4, 5})
	defer batch.Put()
	for i := 0; i < 5; i++ {
		acc.Update(spec, batch, i)
	}
	val, ok := acc.Result(spec)
	if !ok {
		t.Fatal("expected non-null result")
	}
	if val.(int64) != 5 {
		t.Fatalf("expected 5, got %v", val)
	}
}

func TestUnifiedAccum_CountCol(t *testing.T) {
	acc := &UnifiedAccum{}
	spec := &AccumulatorSpec{Kind: AggCount, Col: 0}

	batch := makeIntBatch([]int64{1, 2, 3, 0, 5})
	batch.Cols[0].Nulls = []bool{false, false, false, true, false} // row 3 is NULL
	defer batch.Put()

	for i := 0; i < 5; i++ {
		acc.Update(spec, batch, i)
	}
	val, ok := acc.Result(spec)
	if !ok {
		t.Fatal("expected non-null result")
	}
	if val.(int64) != 4 {
		t.Fatalf("expected 4 (skipping NULL), got %v", val)
	}
}

func TestUnifiedAccum_CountDistinct(t *testing.T) {
	acc := &UnifiedAccum{}
	spec := &AccumulatorSpec{Kind: AggCount, Col: 0, Distinct: true}

	batch := makeIntBatch([]int64{1, 2, 1, 3, 2, 4})
	defer batch.Put()

	for i := 0; i < 6; i++ {
		acc.Update(spec, batch, i)
	}
	val, ok := acc.Result(spec)
	if !ok {
		t.Fatal("expected non-null result")
	}
	if val.(int64) != 4 {
		t.Fatalf("expected 4 distinct values, got %v", val)
	}
}

func TestUnifiedAccum_SumInt(t *testing.T) {
	acc := &UnifiedAccum{}
	spec := &AccumulatorSpec{Kind: AggSum, Col: 0}

	batch := makeIntBatch([]int64{10, 20, 30})
	defer batch.Put()

	for i := 0; i < 3; i++ {
		acc.Update(spec, batch, i)
	}
	val, ok := acc.Result(spec)
	if !ok {
		t.Fatal("expected non-null result")
	}
	if val.(int64) != 60 {
		t.Fatalf("expected 60, got %v", val)
	}
}

func TestUnifiedAccum_SumNegate(t *testing.T) {
	acc := &UnifiedAccum{}
	spec := &AccumulatorSpec{Kind: AggSum, Col: 0, Negate: true}

	batch := makeIntBatch([]int64{10, 20, 30})
	defer batch.Put()

	for i := 0; i < 3; i++ {
		acc.Update(spec, batch, i)
	}
	val, ok := acc.Result(spec)
	if !ok {
		t.Fatal("expected non-null result")
	}
	if val.(int64) != -60 {
		t.Fatalf("expected -60, got %v", val)
	}
}

func TestUnifiedAccum_SumEmpty(t *testing.T) {
	acc := &UnifiedAccum{}
	spec := &AccumulatorSpec{Kind: AggSum, Col: 0}

	_, ok := acc.Result(spec)
	if ok {
		t.Fatal("expected NULL result for empty sum")
	}
}

func TestUnifiedAccum_SumOverflow(t *testing.T) {
	acc := &UnifiedAccum{}
	spec := &AccumulatorSpec{Kind: AggSum, Col: 0}

	batch := makeIntBatch([]int64{math.MaxInt64, 1})
	defer batch.Put()

	for i := 0; i < 2; i++ {
		acc.Update(spec, batch, i)
	}
	_, ok := acc.Result(spec)
	if ok {
		t.Fatal("expected NULL result for overflow")
	}
	if !acc.overflow {
		t.Fatal("expected overflow flag set")
	}
}

func TestUnifiedAccum_AvgInt(t *testing.T) {
	acc := &UnifiedAccum{}
	spec := &AccumulatorSpec{Kind: AggAvg, Col: 0}

	batch := makeIntBatch([]int64{2, 4, 6})
	defer batch.Put()

	for i := 0; i < 3; i++ {
		acc.Update(spec, batch, i)
	}
	val, ok := acc.Result(spec)
	if !ok {
		t.Fatal("expected non-null result")
	}
	// Integer division: 12 / 3 = 4
	if val.(int64) != 4 {
		t.Fatalf("expected 4, got %v", val)
	}
}

func TestUnifiedAccum_AvgEmpty(t *testing.T) {
	acc := &UnifiedAccum{}
	spec := &AccumulatorSpec{Kind: AggAvg, Col: 0}

	_, ok := acc.Result(spec)
	if ok {
		t.Fatal("expected NULL result for empty avg")
	}
}

func TestUnifiedAccum_MinInt(t *testing.T) {
	acc := &UnifiedAccum{}
	spec := &AccumulatorSpec{Kind: AggMin, Col: 0}

	batch := makeIntBatch([]int64{3, 1, 4, 1, 5})
	defer batch.Put()

	for i := 0; i < 5; i++ {
		acc.Update(spec, batch, i)
	}
	val, ok := acc.Result(spec)
	if !ok {
		t.Fatal("expected non-null result")
	}
	if val.(int64) != 1 {
		t.Fatalf("expected 1, got %v", val)
	}
}

func TestUnifiedAccum_MaxInt(t *testing.T) {
	acc := &UnifiedAccum{}
	spec := &AccumulatorSpec{Kind: AggMax, Col: 0}

	batch := makeIntBatch([]int64{3, 1, 4, 1, 5})
	defer batch.Put()

	for i := 0; i < 5; i++ {
		acc.Update(spec, batch, i)
	}
	val, ok := acc.Result(spec)
	if !ok {
		t.Fatal("expected non-null result")
	}
	if val.(int64) != 5 {
		t.Fatalf("expected 5, got %v", val)
	}
}

func TestUnifiedAccum_MinEmpty(t *testing.T) {
	acc := &UnifiedAccum{}
	spec := &AccumulatorSpec{Kind: AggMin, Col: 0}

	_, ok := acc.Result(spec)
	if ok {
		t.Fatal("expected NULL result for empty min")
	}
}

func TestUnifiedAccum_SumDistinct(t *testing.T) {
	acc := &UnifiedAccum{}
	spec := &AccumulatorSpec{Kind: AggSum, Col: 0, Distinct: true}

	batch := makeIntBatch([]int64{10, 20, 10, 30, 20})
	defer batch.Put()

	for i := 0; i < 5; i++ {
		acc.Update(spec, batch, i)
	}
	val, ok := acc.Result(spec)
	if !ok {
		t.Fatal("expected non-null result")
	}
	if val.(int64) != 60 { // 10 + 20 + 30
		t.Fatalf("expected 60 (distinct sum), got %v", val)
	}
}

func TestUnifiedAccum_GroupConcat(t *testing.T) {
	acc := &UnifiedAccum{}
	spec := &AccumulatorSpec{Kind: AggGroupConcat, Col: 0, Separator: ","}

	batch := makeStringBatch([]string{"a", "b", "c"})
	defer batch.Put()

	for i := 0; i < 3; i++ {
		acc.Update(spec, batch, i)
	}
	val, ok := acc.Result(spec)
	if !ok {
		t.Fatal("expected non-null result")
	}
	if val.(string) != "a,b,c" {
		t.Fatalf("expected 'a,b,c', got '%v'", val)
	}
}

func TestUnifiedAccum_GroupConcatEmpty(t *testing.T) {
	acc := &UnifiedAccum{}
	spec := &AccumulatorSpec{Kind: AggGroupConcat, Col: 0, Separator: ","}

	_, ok := acc.Result(spec)
	if ok {
		t.Fatal("expected NULL result for empty group_concat")
	}
}

func TestUnifiedAccum_Reset(t *testing.T) {
	acc := &UnifiedAccum{}
	spec := &AccumulatorSpec{Kind: AggSum, Col: 0}

	batch := makeIntBatch([]int64{1, 2, 3})
	defer batch.Put()

	for i := 0; i < 3; i++ {
		acc.Update(spec, batch, i)
	}
	val, ok := acc.Result(spec)
	if !ok || val.(int64) != 6 {
		t.Fatalf("first run: expected 6, got %v (ok=%v)", val, ok)
	}

	acc.Reset()

	// After reset, SUM should be NULL (empty)
	_, ok = acc.Result(spec)
	if ok {
		t.Fatal("after reset: expected NULL sum result")
	}

	// Add new values
	batch2 := makeIntBatch([]int64{10, 20})
	defer batch2.Put()
	for i := 0; i < 2; i++ {
		acc.Update(spec, batch2, i)
	}
	val, ok = acc.Result(spec)
	if !ok || val.(int64) != 30 {
		t.Fatalf("second run: expected 30, got %v (ok=%v)", val, ok)
	}
}

func TestUnifiedAccum_Merge(t *testing.T) {
	spec := &AccumulatorSpec{Kind: AggSum, Col: 0}

	a := &UnifiedAccum{}
	b := &UnifiedAccum{}

	batch := makeIntBatch([]int64{10, 20})
	defer batch.Put()

	for i := 0; i < 2; i++ {
		a.Update(spec, batch, i)
	}
	batch2 := makeIntBatch([]int64{30, 40})
	defer batch2.Put()
	for i := 0; i < 2; i++ {
		b.Update(spec, batch2, i)
	}

	a.Merge(spec, b)

	val, ok := a.Result(spec)
	if !ok {
		t.Fatal("expected non-null result")
	}
	if val.(int64) != 100 { // 10+20+30+40
		t.Fatalf("expected 100, got %v", val)
	}
}

func TestUnifiedAccum_MergeCountDistinct(t *testing.T) {
	spec := &AccumulatorSpec{Kind: AggCount, Col: 0, Distinct: true}

	a := &UnifiedAccum{}
	b := &UnifiedAccum{}

	batch := makeIntBatch([]int64{1, 2, 3})
	defer batch.Put()
	for i := 0; i < 3; i++ {
		a.Update(spec, batch, i)
	}

	batch2 := makeIntBatch([]int64{2, 3, 4})
	defer batch2.Put()
	for i := 0; i < 3; i++ {
		b.Update(spec, batch2, i)
	}

	a.Merge(spec, b)

	val, ok := a.Result(spec)
	if !ok {
		t.Fatal("expected non-null result")
	}
	// {1,2,3} ∪ {2,3,4} = {1,2,3,4} = 4 distinct
	if val.(int64) != 4 {
		t.Fatalf("expected 4 distinct after merge, got %v", val)
	}
}

// Helper: make an int batch for testing
func makeIntBatch(values []int64) *UT.Batch {
	b := UT.GetBatch(1)
	b.Pooled = false
	b.SetColumnName(0, "v")
	b.Cols[0].Type = LX.T_INT_KW
	b.Cols[0].Data.Ints = make([]int64, len(values))
	copy(b.Cols[0].Data.Ints, values)
	b.Cols[0].Nulls = make([]bool, len(values))
	b.Size = len(values)
	return b
}

// Helper: make a string batch for testing
func makeStringBatch(values []string) *UT.Batch {
	b := UT.GetBatch(1)
	b.Pooled = false
	b.SetColumnName(0, "v")
	b.Cols[0].Type = LX.T_TEXT
	b.Cols[0].Data.Strs = make([]string, len(values))
	copy(b.Cols[0].Data.Strs, values)
	b.Cols[0].Nulls = make([]bool, len(values))
	b.Size = len(values)
	return b
}

// Ensure AP.Value type is used (avoid unused import)
var _ = AP.Value{}
