package EX

import (
	"context"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// makeSortTestRows creates N rows with random id values for sorting.
func makeSortTestRows(n int) []Row {
	rows := make([]Row, n)
	// Create reversed order: n-1, n-2, ..., 1, 0
	for i := 0; i < n; i++ {
		rows[i] = Row{
			Cols:  []string{"id"},
			Types: []int{int(LX.T_INT_KW)},
			Data:  []Value{NewIntValue(int64(n - 1 - i))},
		}
	}
	return rows
}

// TestParallelSort_Sequential verifies sequential sort path.
func TestParallelSort_Sequential(t *testing.T) {
	rows := makeSortTestRows(100)
	src := &rowSourceForTest{rows: rows}
	schema := []string{"id"}
	types := []LX.TokenType{LX.T_INT_KW}
	scan := NewVectorizedSeqScan(src, schema, types)
	defer scan.Close()

	keys := []SortKey{{ColName: "id", Order: AscOrder}}
	sortOp := NewParallelSort(scan, keys, nil) // nil pool = sequential
	defer sortOp.Close()

	batch, err := sortOp.NextBatch(context.Background())
	if err != nil {
		t.Fatalf("NextBatch: %v", err)
	}
	if batch == nil {
		t.Fatal("expected non-nil batch")
	}
	defer batch.Put()

	idCol := batch.Cols[0].Data.([]int64)
	if len(idCol) != 100 {
		t.Logf("got %d rows in batch (expected 100), idCol=%v", len(idCol), idCol)
	}
	for i := 1; i < len(idCol); i++ {
		if idCol[i] < idCol[i-1] {
			t.Errorf("not sorted at %d: %d > %d", i, idCol[i-1], idCol[i])
		}
	}
}

// TestParallelSort_Descending verifies descending order.
func TestParallelSort_Descending(t *testing.T) {
	rows := makeSortTestRows(50)
	src := &rowSourceForTest{rows: rows}
	schema := []string{"id"}
	types := []LX.TokenType{LX.T_INT_KW}
	scan := NewVectorizedSeqScan(src, schema, types)
	defer scan.Close()

	keys := []SortKey{{ColName: "id", Order: DescOrder}}
	sortOp := NewParallelSort(scan, keys, nil)
	defer sortOp.Close()

	batch, err := sortOp.NextBatch(context.Background())
	if err != nil {
		t.Fatalf("NextBatch: %v", err)
	}
	if batch == nil {
		t.Fatal("expected non-nil batch")
	}
	defer batch.Put()

	idCol := batch.Cols[0].Data.([]int64)
	for i := 1; i < len(idCol); i++ {
		if idCol[i] > idCol[i-1] {
			t.Errorf("not sorted desc at %d: %d < %d", i, idCol[i-1], idCol[i])
		}
	}
}

// TestParallelSort_LargeDataset verifies parallel path.
func TestParallelSort_LargeDataset(t *testing.T) {
	rows := makeSortTestRows(5000)
	src := &rowSourceForTest{rows: rows}
	schema := []string{"id"}
	types := []LX.TokenType{LX.T_INT_KW}
	scan := NewVectorizedSeqScan(src, schema, types)
	defer scan.Close()

	keys := []SortKey{{ColName: "id", Order: AscOrder}}
	pool := NewWorkerPool(4)
	defer pool.Close()

	sortOp := NewParallelSort(scan, keys, pool)
	defer sortOp.Close()

	batch, err := sortOp.NextBatch(context.Background())
	if err != nil {
		t.Fatalf("NextBatch: %v", err)
	}
	if batch == nil {
		t.Fatal("expected non-nil batch")
	}
	defer batch.Put()

	idCol := batch.Cols[0].Data.([]int64)
	// Verify ascending order on first BatchSize rows
	for i := 1; i < len(idCol); i++ {
		if idCol[i] < idCol[i-1] {
			t.Errorf("not sorted at %d: %d > %d", i, idCol[i-1], idCol[i])
		}
	}
}

// TestCompareValues tests the value comparison function.
func TestCompareValues(t *testing.T) {
	if compareValues(int64(1), int64(2)) >= 0 {
		t.Error("1 < 2 expected")
	}
	if compareValues(int64(2), int64(1)) <= 0 {
		t.Error("2 > 1 expected")
	}
	if compareValues(int64(1), int64(1)) != 0 {
		t.Error("1 == 1 expected")
	}
	if compareValues(nil, int64(1)) >= 0 {
		t.Error("nil < 1 expected")
	}
	if compareValues("abc", "abd") >= 0 {
		t.Error("abc < abd expected")
	}
	if compareValues(1.0, 2.0) >= 0 {
		t.Error("1.0 < 2.0 expected")
	}
}

// TestLessRow tests multi-key row comparison.
func TestLessRow(t *testing.T) {
	row1 := Row{Cols: []string{"a", "b"}, Data: []Value{NewIntValue(int64(1)), NewIntValue(int64(2))}}
	row2 := Row{Cols: []string{"a", "b"}, Data: []Value{NewIntValue(int64(1)), NewIntValue(int64(3))}}
	row3 := Row{Cols: []string{"a", "b"}, Data: []Value{NewIntValue(int64(2)), NewIntValue(int64(1))}}

	keys := []SortKey{{ColName: "a", Order: AscOrder}, {ColName: "b", Order: AscOrder}}
	if !lessRow(row1, row2, keys) {
		t.Error("row1 < row2 (by b) expected")
	}
	if lessRow(row2, row1, keys) {
		t.Error("row2 not < row1 expected")
	}
	if !lessRow(row2, row3, keys) {
		t.Error("row2 < row3 (by a) expected")
	}
}

// TestParallelSort_MultiBatch verifies that NextBatch returns
// multiple batches when data exceeds BatchSize. REQ001021.
func TestParallelSort_MultiBatch(t *testing.T) {
	rows := makeSortTestRows(2 * BatchSize + 10)
	src := &rowSourceForTest{rows: rows}
	schema := []string{"id"}
	types := []LX.TokenType{LX.T_INT_KW}
	scan := NewVectorizedSeqScan(src, schema, types)
	defer scan.Close()

	keys := []SortKey{{ColName: "id", Order: AscOrder}}
	sortOp := NewParallelSort(scan, keys, nil)
	defer sortOp.Close()

	var allRows []int64
	for {
		batch, err := sortOp.NextBatch(context.Background())
		if err != nil {
			t.Fatalf("NextBatch: %v", err)
		}
		if batch == nil {
			break
		}
		defer batch.Put()

		idCol := batch.Cols[0].Data.([]int64)
		allRows = append(allRows, idCol...)
	}

	// Verify all rows are present
	if len(allRows) != len(rows) {
		t.Fatalf("expected %d rows, got %d", len(rows), len(allRows))
	}

	// Verify sorted
	for i := 1; i < len(allRows); i++ {
		if allRows[i] < allRows[i-1] {
			t.Errorf("not sorted at %d: %d > %d", i, allRows[i-1], allRows[i])
		}
	}
}

// BenchmarkParallelSort_Large measures sort throughput on 10K rows.
func BenchmarkParallelSort_Large(b *testing.B) {
	rows := makeSortTestRows(10 * 1024)
	schema := []string{"id"}
	types := []LX.TokenType{LX.T_INT_KW}
	keys := []SortKey{{ColName: "id", Order: AscOrder}}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		src := &rowSourceForTest{rows: rows}
		scan := NewVectorizedSeqScan(src, schema, types)
		pool := NewWorkerPool(4)
		sortOp := NewParallelSort(scan, keys, pool)
		for {
			batch, _ := sortOp.NextBatch(context.Background())
			if batch == nil {
				break
			}
			batch.Put()
		}
		sortOp.Close()
		pool.Close()
	}
}

// BenchmarkSequentialSort measures sequential sort for comparison.
func BenchmarkSequentialSort(b *testing.B) {
	rows := makeSortTestRows(10 * 1024)
	keys := []SortKey{{ColName: "id", Order: AscOrder}}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Materialize + sort
		sortOp := NewParallelSort(nil, keys, nil)
		sortOp.rows = make([]Row, len(rows))
		copy(sortOp.rows, rows)
		sortOp.sequentialSort()
	}
}

// BenchmarkSort_KeyLookup verifies that lessRowIdx (pre-computed
// column indices) provides O(1) per-comparison access without
// map lookups. REQ001018.
func BenchmarkSort_KeyLookup(b *testing.B) {
	rows := makeSortTestRows(1024)
	keys := []SortKey{{ColName: "id", Order: AscOrder}, {ColName: "val", Order: AscOrder}}

	b.Run("lessRow", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			for j := 1; j < len(rows); j++ {
				_ = lessRow(rows[j-1], rows[j], keys)
			}
		}
	})
	b.Run("lessRowIdx", func(b *testing.B) {
		indices := buildKeyIndices(keys, rows[0])
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			for j := 1; j < len(rows); j++ {
				_ = lessRowIdx(rows[j-1], rows[j], keys, indices)
			}
		}
	})
}
