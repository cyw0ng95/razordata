package EX

import (
	"context"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// makeAggregateTestRows creates N rows with id=0..N-1 and value=id*10.
func makeAggregateTestRows(n int) []Row {
	rows := make([]Row, n)
	for i := 0; i < n; i++ {
		rows[i] = Row{
			Cols:  []string{"id", "value"},
			Types: []LX.TokenType{LX.T_TEXT},
			Data:  []Value{NewIntValue(int64(i)), NewIntValue(int64(i * 10))},
		}
	}
	return rows
}

// TestVectorizedCount_Basic verifies count on N rows.
func TestVectorizedCount_Basic(t *testing.T) {
	rows := makeAggregateTestRows(100)
	src := &rowSourceForTest{rows: rows}
	schema := []string{"id", "value"}
	types := []LX.TokenType{LX.T_INT_KW, LX.T_INT_KW}
	scan := NewVectorizedSeqScan(src, schema, types)
	defer scan.Close()

	cnt := NewVectorizedCount(scan)
	defer cnt.Close()

	batch, err := cnt.NextBatch(context.Background())
	if err != nil {
		t.Fatalf("NextBatch: %v", err)
	}
	if batch == nil {
		t.Fatal("expected non-nil batch")
	}
	defer batch.Put()

	if batch.LogicalSize() != 1 {
		t.Errorf("expected 1 result row, got %d", batch.LogicalSize())
	}
	idCol := batch.Cols[0].Data.Ints
	if idCol[0] != 100 {
		t.Errorf("count = %d, want 100", idCol[0])
	}
}

// TestVectorizedCount_Empty verifies count on empty source.
func TestVectorizedCount_Empty(t *testing.T) {
	rows := makeAggregateTestRows(0)
	src := &rowSourceForTest{rows: rows}
	schema := []string{"id"}
	types := []LX.TokenType{LX.T_INT_KW}
	scan := NewVectorizedSeqScan(src, schema, types)
	defer scan.Close()

	cnt := NewVectorizedCount(scan)
	defer cnt.Close()

	batch, err := cnt.NextBatch(context.Background())
	if err != nil {
		t.Fatalf("NextBatch: %v", err)
	}
	if batch == nil {
		t.Fatal("expected non-nil batch")
	}
	defer batch.Put()

	idCol := batch.Cols[0].Data.Ints
	if idCol[0] != 0 {
		t.Errorf("count = %d, want 0", idCol[0])
	}
}

// TestVectorizedSum_Int64 verifies int64 sum.
func TestVectorizedSum_Int64(t *testing.T) {
	rows := makeAggregateTestRows(10)
	src := &rowSourceForTest{rows: rows}
	schema := []string{"id", "value"}
	types := []LX.TokenType{LX.T_INT_KW, LX.T_INT_KW}
	scan := NewVectorizedSeqScan(src, schema, types)
	defer scan.Close()

	sum := NewVectorizedSum(scan, 1) // sum value column
	defer sum.Close()

	batch, err := sum.NextBatch(context.Background())
	if err != nil {
		t.Fatalf("NextBatch: %v", err)
	}
	if batch == nil {
		t.Fatal("expected non-nil batch")
	}
	defer batch.Put()

	// 0+10+20+...+90 = 450
	idCol := batch.Cols[0].Data.Ints
	if idCol[0] != 450 {
		t.Errorf("sum = %d, want 450", idCol[0])
	}
}

// TestVectorizedSum_Float64 verifies float64 sum.
func TestVectorizedSum_Float64(t *testing.T) {
	rows := []Row{}
	for i := 0; i < 10; i++ {
		rows = append(rows, Row{
			Cols:  []string{"x"},
			Types: []LX.TokenType{LX.T_TEXT},
			Data:  []Value{NewFloatValue(float64(i) * 1.5)},
		})
	}
	src := &rowSourceForTest{rows: rows}
	schema := []string{"x"}
	types := []LX.TokenType{LX.T_FLOAT_KW}
	scan := NewVectorizedSeqScan(src, schema, types)
	defer scan.Close()

	sum := NewVectorizedSum(scan, 0)
	defer sum.Close()

	batch, err := sum.NextBatch(context.Background())
	if err != nil {
		t.Fatalf("NextBatch: %v", err)
	}
	if batch == nil {
		t.Fatal("expected non-nil batch")
	}
	defer batch.Put()

	// 0 + 1.5 + 3.0 + 4.5 + 6.0 + 7.5 + 9.0 + 10.5 + 12.0 + 13.5 = 67.5
	idCol := batch.Cols[0].Data.Floats
	if idCol[0] != 67.5 {
		t.Errorf("sum = %f, want 67.5", idCol[0])
	}
}

// TestVectorizedAvg_Basic verifies average.
func TestVectorizedAvg_Basic(t *testing.T) {
	rows := makeAggregateTestRows(10) // values 0, 10, 20, ..., 90
	src := &rowSourceForTest{rows: rows}
	schema := []string{"id", "value"}
	types := []LX.TokenType{LX.T_INT_KW, LX.T_INT_KW}
	scan := NewVectorizedSeqScan(src, schema, types)
	defer scan.Close()

	avg := NewVectorizedAvg(scan, 1)
	defer avg.Close()

	batch, err := avg.NextBatch(context.Background())
	if err != nil {
		t.Fatalf("NextBatch: %v", err)
	}
	if batch == nil {
		t.Fatal("expected non-nil batch")
	}
	defer batch.Put()

	// avg of 0,10,...,90 = 45
	idCol := batch.Cols[0].Data.Floats
	if idCol[0] != 45.0 {
		t.Errorf("avg = %f, want 45.0", idCol[0])
	}
}

// TestVectorizedMin_Basic verifies min.
func TestVectorizedMin_Basic(t *testing.T) {
	rows := makeAggregateTestRows(10)
	src := &rowSourceForTest{rows: rows}
	schema := []string{"id", "value"}
	types := []LX.TokenType{LX.T_INT_KW, LX.T_INT_KW}
	scan := NewVectorizedSeqScan(src, schema, types)
	defer scan.Close()

	min := NewVectorizedMin(scan, 1)
	defer min.Close()

	batch, err := min.NextBatch(context.Background())
	if err != nil {
		t.Fatalf("NextBatch: %v", err)
	}
	if batch == nil {
		t.Fatal("expected non-nil batch")
	}
	defer batch.Put()

	idCol := batch.Cols[0].Data.Ints
	if idCol[0] != 0 {
		t.Errorf("min = %d, want 0", idCol[0])
	}
}

// TestVectorizedMax_Basic verifies max.
func TestVectorizedMax_Basic(t *testing.T) {
	rows := makeAggregateTestRows(10)
	src := &rowSourceForTest{rows: rows}
	schema := []string{"id", "value"}
	types := []LX.TokenType{LX.T_INT_KW, LX.T_INT_KW}
	scan := NewVectorizedSeqScan(src, schema, types)
	defer scan.Close()

	max := NewVectorizedMax(scan, 1)
	defer max.Close()

	batch, err := max.NextBatch(context.Background())
	if err != nil {
		t.Fatalf("NextBatch: %v", err)
	}
	if batch == nil {
		t.Fatal("expected non-nil batch")
	}
	defer batch.Put()

	idCol := batch.Cols[0].Data.Ints
	if idCol[0] != 90 {
		t.Errorf("max = %d, want 90", idCol[0])
	}
}

// TestVectorizedSum_LargeBatch verifies unrolling correctness.
func TestVectorizedSum_LargeBatch(t *testing.T) {
	const n = 1024
	rows := make([]Row, n)
	expected := int64(0)
	for i := 0; i < n; i++ {
		rows[i] = Row{
			Cols:  []string{"x"},
			Types: []LX.TokenType{LX.T_TEXT},
			Data:  []Value{NewIntValue(int64(i))},
		}
		expected += int64(i)
	}
	src := &rowSourceForTest{rows: rows}
	schema := []string{"x"}
	types := []LX.TokenType{LX.T_INT_KW}
	scan := NewVectorizedSeqScan(src, schema, types)
	defer scan.Close()

	sum := NewVectorizedSum(scan, 0)
	defer sum.Close()

	batch, err := sum.NextBatch(context.Background())
	if err != nil {
		t.Fatalf("NextBatch: %v", err)
	}
	if batch == nil {
		t.Fatal("expected non-nil batch")
	}
	defer batch.Put()

	idCol := batch.Cols[0].Data.Ints
	if idCol[0] != expected {
		t.Errorf("sum = %d, want %d", idCol[0], expected)
	}
}

// BenchmarkVectorizedSum_Int64 measures SUM throughput.
func BenchmarkVectorizedSum_Int64(b *testing.B) {
	const n = 10 * 1024
	rows := make([]Row, n)
	for i := 0; i < n; i++ {
		rows[i] = Row{
			Cols:  []string{"x"},
			Types: []LX.TokenType{LX.T_TEXT},
			Data:  []Value{NewIntValue(int64(i))},
		}
	}
	schema := []string{"x"}
	types := []LX.TokenType{LX.T_INT_KW}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		src := &rowSourceForTest{rows: rows}
		scan := NewVectorizedSeqScan(src, schema, types)
		sum := NewVectorizedSum(scan, 0)
		for {
			batch, _ := sum.NextBatch(context.Background())
			if batch == nil {
				break
			}
			batch.Put()
		}
		sum.Close()
		scan.Close()
	}
}

// BenchmarkRowSum_Fallback measures row-at-a-time SUM for comparison.
func BenchmarkRowSum_Fallback(b *testing.B) {
	const n = 10 * 1024
	rows := make([]Row, n)
	for i := 0; i < n; i++ {
		rows[i] = Row{
			Cols:  []string{"x"},
			Types: []LX.TokenType{LX.T_TEXT},
			Data:  []Value{NewIntValue(int64(i))},
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var sum int64
		for j := 0; j < n; j++ {
			sum += rows[j].Data[0].ToAny().(int64)
		}
	}
}
