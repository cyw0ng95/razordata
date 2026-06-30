package EX

import (
	"context"
	"strconv"
	"sync"
	"testing"

	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
)

// makeParallelTestRows creates N rows with id=0..N-1 and value="row".
func makeParallelTestRows(n int) []pl.Row {
	rows := make([]pl.Row, n)
	for i := 0; i < n; i++ {
		rows[i] = Row{
			Cols:  []string{"id", "value"},
			Types: []LX.TokenType{LX.T_INT_KW, LX.T_TEXT},
			Data:  []Value{NewIntValue(int64(i)), DT.NewTextValue("row")},
		}
	}
	return rows
}

// TestParallelSeqScan_Basic verifies correct row count.
func TestParallelSeqScan_Basic(t *testing.T) {
	rows := makeParallelTestRows(100)
	src := &rowSourceForTest{rows: rows}
	schema := []string{"id", "value"}
	types := []LX.TokenType{LX.T_INT_KW, LX.T_TEXT}
	pool := 
UT.NewWorkerPool(4)
	defer pool.Close()

	scan := NewParallelSeqScan(src, schema, types, pool, rows)
	defer scan.Close()

	total := 0
	for {
		batch, err := scan.NextBatch(context.Background())
		if err != nil {
			t.Fatalf("NextBatch: %v", err)
		}
		if batch == nil {
			break
		}
		total += batch.LogicalSize()
		batch.Put()
	}
	if total != 100 {
		t.Errorf("total = %d, want 100", total)
	}
}

// TestParallelSeqScan_Empty verifies empty source.
func TestParallelSeqScan_Empty(t *testing.T) {
	pool := 
UT.NewWorkerPool(4)
	defer pool.Close()

	scan := NewParallelSeqScan(nil, []string{"id"}, []LX.TokenType{LX.T_INT_KW}, pool, nil)
	defer scan.Close()

	batch, err := scan.NextBatch(context.Background())
	if err != nil {
		t.Fatalf("NextBatch: %v", err)
	}
	if batch != nil {
		t.Error("expected nil batch for empty rows")
		batch.Put()
	}
}

// TestParallelSeqScan_Large scans 10K rows across 4 workers.
func TestParallelSeqScan_Large(t *testing.T) {
	rows := makeParallelTestRows(10000)
	src := &rowSourceForTest{rows: rows}
	schema := []string{"id"}
	types := []LX.TokenType{LX.T_INT_KW}
	pool := 
UT.NewWorkerPool(4)
	defer pool.Close()

	scan := NewParallelSeqScan(src, schema, types, pool, rows)
	defer scan.Close()

	total := 0
	for {
		batch, err := scan.NextBatch(context.Background())
		if err != nil {
			t.Fatalf("NextBatch: %v", err)
		}
		if batch == nil {
			break
		}
		total += batch.LogicalSize()
		batch.Put()
	}
	if total != 10000 {
		t.Errorf("total = %d, want 10000", total)
	}
}

// TestParallelIndexScan_Basic verifies index scan correctness.
func TestParallelIndexScan_Basic(t *testing.T) {
	rows := makeParallelTestRows(50)
	pred := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "id"},
		Op:    LX.T_GE,
		Right: &PS.NumberLiteral{Val: 25},
	}
	pool := 
UT.NewWorkerPool(4)
	defer pool.Close()

	scan := NewParallelIndexScan(rows, "id", []string{"id"}, []LX.TokenType{LX.T_INT_KW}, pred, pool)
	defer scan.Close()

	total := 0
	for {
		batch, err := scan.NextBatch(context.Background())
		if err != nil {
			t.Fatalf("NextBatch: %v", err)
		}
		if batch == nil {
			break
		}
		total += batch.LogicalSize()
		batch.Put()
	}
	// IDs 25-49 = 25 rows
	if total != 25 {
		t.Errorf("total = %d, want 25", total)
	}
}

// TestParallelIndexScan_NoPred verifies no-predicate path.
func TestParallelIndexScan_NoPred(t *testing.T) {
	rows := makeParallelTestRows(100)
	pool := 
UT.NewWorkerPool(4)
	defer pool.Close()

	scan := NewParallelIndexScan(rows, "id", []string{"id"}, []LX.TokenType{LX.T_INT_KW}, nil, pool)
	defer scan.Close()

	total := 0
	for {
		batch, err := scan.NextBatch(context.Background())
		if err != nil {
			t.Fatalf("NextBatch: %v", err)
		}
		if batch == nil {
			break
		}
		total += batch.LogicalSize()
		batch.Put()
	}
	if total != 100 {
		t.Errorf("total = %d, want 100", total)
	}
}

// TestParallelSeqScan_ConcurrentReaders verifies pool safety.
func TestParallelSeqScan_ConcurrentReaders(t *testing.T) {
	rows := makeParallelTestRows(1000)
	src := &rowSourceForTest{rows: rows}
	schema := []string{"id"}
	types := []LX.TokenType{LX.T_INT_KW}
	pool := 
UT.NewWorkerPool(4)
	defer pool.Close()

	var wg sync.WaitGroup
	for r := 0; r < 3; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			scan := NewParallelSeqScan(src, schema, types, pool, rows)
			defer scan.Close()
			total := 0
	for {
				batch, _ := scan.NextBatch(context.Background())
				if batch == nil {
					break
				}
				total += batch.LogicalSize()
				batch.Put()
			}
		}()
	}
	wg.Wait()
}

// BenchmarkParallelSeqScan measures parallel scan throughput.
func BenchmarkParallelSeqScan(b *testing.B) {
	rows := makeParallelTestRows(10 * 1024)
	schema := []string{"id"}
	types := []LX.TokenType{LX.T_INT_KW}
	src := &rowSourceForTest{rows: rows}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pool := 
UT.NewWorkerPool(4)
		scan := NewParallelSeqScan(src, schema, types, pool, rows)
		for {
			batch, _ := scan.NextBatch(context.Background())
			if batch == nil {
				break
			}
			batch.Put()
		}
		scan.Close()
		pool.Close()
	}
}

// BenchmarkParallelSeqScanScaling measures scaling with worker count.
func BenchmarkParallelSeqScanScaling(b *testing.B) {
	rows := makeParallelTestRows(10 * 1024)
	schema := []string{"id"}
	types := []LX.TokenType{LX.T_INT_KW}
	src := &rowSourceForTest{rows: rows}

	for _, workers := range []int{1, 2, 4, 8} {
		b.Run("workers="+strconv.Itoa(workers), func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				pool := 
UT.NewWorkerPool(workers)
				scan := NewParallelSeqScan(src, schema, types, pool, rows)
				for {
					batch, _ := scan.NextBatch(context.Background())
					if batch == nil {
						break
					}
					batch.Put()
				}
				scan.Close()
				pool.Close()
			}
		})
	}
}
// rowSourceForTest is a simple in-memory row iterator for tests.
type rowSourceForTest struct {
	rows []Row
	pos  int
}

func (r *rowSourceForTest) Next(ctx context.Context) (Row, error) {
	if r.pos >= len(r.rows) {
		return Row{}, ErrNoRows
	}
	row := r.rows[r.pos]
	r.pos++
	return row, nil
}

func (r *rowSourceForTest) Close() error { return nil }

// makeTestRows creates N rows with id=0..N-1 and value="row_i".
func makeTestRows(n int) []Row {
	rows := make([]Row, n)
	for i := 0; i < n; i++ {
		rows[i] = Row{
			Cols:  []string{"id", "value"},
			Types: []LX.TokenType{LX.T_INT_KW, LX.T_TEXT},
			Data:  []Value{NewIntValue(int64(i)), NewTextValue("row")},
		}
	}
	return rows
}

// TestVectorizedSeqScan verifies the vectorized scan produces
// columnar batches with correct data.
func TestVectorizedSeqScan(t *testing.T) {
	rows := makeTestRows(2500) // > 2 batches
	src := &rowSourceForTest{rows: rows}
	schema := []string{"id", "value"}
	types := []LX.TokenType{LX.T_INT_KW, LX.T_TEXT}

	scan := NewVectorizedSeqScan(src, schema, types)
	defer scan.Close()

	ctx := context.Background()
	totalRows := 0
	batches := 0
	for {
		batch, err := scan.NextBatch(ctx)
		if err != nil {
			t.Fatalf("NextBatch: %v", err)
		}
		if batch == nil {
			break
		}
		batches++
		totalRows += batch.LogicalSize()

		// Verify column data is populated
		idCol := batch.Cols[0].Data.Ints
		valCol := batch.Cols[1].Data.Strs
		for i := 0; i < batch.Size; i++ {
			expectedID := totalRows - batch.LogicalSize() + i
			if idCol[i] != int64(expectedID) {
				t.Errorf("batch %d row %d: id=%d, want %d", batches-1, i, idCol[i], expectedID)
			}
			if valCol[i] != "row" {
				t.Errorf("batch %d row %d: value=%q, want row", batches-1, i, valCol[i])
			}
		}
		batch.Put()
	}

	if totalRows != 2500 {
		t.Errorf("total rows: got %d, want 2500", totalRows)
	}
	// 2500 rows / 1024 = 3 batches (1024 + 1024 + 452)
	if batches != 3 {
		t.Errorf("batches: got %d, want 3", batches)
	}
}

// TestVectorizedSeqScan_EmptySource verifies EOF on empty source.
func TestVectorizedSeqScan_EmptySource(t *testing.T) {
	src := &rowSourceForTest{rows: nil}
	scan := NewVectorizedSeqScan(src, []string{"x"}, []LX.TokenType{LX.T_INT_KW})
	defer scan.Close()

	batch, err := scan.NextBatch(context.Background())
	if err != nil {
		t.Fatalf("NextBatch: %v", err)
	}
	if batch != nil {
		t.Errorf("expected nil batch for empty source, got size %d", batch.Size)
		batch.Put()
	}
}

// TestVectorizedFilter_AllMatch verifies that all-match returns
// a batch with the full selection vector (all indices present).
func TestVectorizedFilter_AllMatch(t *testing.T) {
	rows := makeTestRows(10)
	src := &rowSourceForTest{rows: rows}
	scan := NewVectorizedSeqScan(src, []string{"id"}, []LX.TokenType{LX.T_INT_KW})
	defer scan.Close()

	// Filter: id >= 0 (all rows match)
	filter := NewVectorizedFilter(scan, &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "id"},
		Op:    LX.T_GE,
		Right: &PS.NumberLiteral{Val: 0},
	})
	defer filter.Close()

	batch, err := filter.NextBatch(context.Background())
	if err != nil {
		t.Fatalf("NextBatch: %v", err)
	}
	if batch == nil {
		t.Fatal("expected non-nil batch")
	}
	defer batch.Put()

	// All match: should have full selection [0, 1, ..., 9]
	if batch.LogicalSize() != 10 {
		t.Errorf("expected 10 rows, got %d", batch.LogicalSize())
	}
	if len(batch.Sel) != 10 {
		t.Errorf("expected Sel length 10, got %d", len(batch.Sel))
	}
}

// TestVectorizedFilter_NoMatch verifies that no-match drops the batch.
func TestVectorizedFilter_NoMatch(t *testing.T) {
	rows := makeTestRows(5)
	src := &rowSourceForTest{rows: rows}
	scan := NewVectorizedSeqScan(src, []string{"id"}, []LX.TokenType{LX.T_INT_KW})
	defer scan.Close()

	// Filter: id > 100 (no rows match)
	filter := NewVectorizedFilter(scan, &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "id"},
		Op:    LX.T_GT,
		Right: &PS.NumberLiteral{Val: 100},
	})
	defer filter.Close()

	batch, err := filter.NextBatch(context.Background())
	if err != nil {
		t.Fatalf("NextBatch: %v", err)
	}
	if batch != nil {
		t.Errorf("expected nil batch for no-match, got size %d", batch.LogicalSize())
		batch.Put()
	}
}

// TestVectorizedFilter_PartialMatch verifies selection vector.
func TestVectorizedFilter_PartialMatch(t *testing.T) {
	rows := makeTestRows(10)
	src := &rowSourceForTest{rows: rows}
	scan := NewVectorizedSeqScan(src, []string{"id"}, []LX.TokenType{LX.T_INT_KW})
	defer scan.Close()

	// Filter: id < 5 (matches 0, 1, 2, 3, 4)
	filter := NewVectorizedFilter(scan, &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "id"},
		Op:    LX.T_LT,
		Right: &PS.NumberLiteral{Val: 5},
	})
	defer filter.Close()

	batch, err := filter.NextBatch(context.Background())
	if err != nil {
		t.Fatalf("NextBatch: %v", err)
	}
	if batch == nil {
		t.Fatal("expected non-nil batch")
	}
	defer batch.Put()

	if len(batch.Sel) != 5 {
		t.Errorf("expected 5 matches, got %d", len(batch.Sel))
	}
}

// TestVectorizedFilter_MultiBatch verifies filter across multiple batches.
func TestVectorizedFilter_MultiBatch(t *testing.T) {
	rows := makeTestRows(2500)
	src := &rowSourceForTest{rows: rows}
	scan := NewVectorizedSeqScan(src, []string{"id"}, []LX.TokenType{LX.T_INT_KW})
	defer scan.Close()

	// Filter: id >= 1000
	filter := NewVectorizedFilter(scan, &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "id"},
		Op:    LX.T_GE,
		Right: &PS.NumberLiteral{Val: 1000},
	})
	defer filter.Close()

	totalMatched := 0
	for {
		batch, err := filter.NextBatch(context.Background())
		if err != nil {
			t.Fatalf("NextBatch: %v", err)
		}
		if batch == nil {
			break
		}
		totalMatched += batch.LogicalSize()
		batch.Put()
	}
	// IDs 1000..2499 = 1500 rows
	if totalMatched != 1500 {
		t.Errorf("expected 1500 matches, got %d", totalMatched)
	}
}

// TestSchemaFromRowSchema verifies the int -> TokenType conversion.
func TestSchemaFromRowSchema(t *testing.T) {
	types := []LX.TokenType{LX.T_INT_KW, LX.T_TEXT, LX.T_BOOL}
	got := SchemaFromRowSchema(types)
	if len(got) != 3 {
		t.Fatalf("expected 3 types, got %d", len(got))
	}
	if got[0] != LX.T_INT_KW {
		t.Errorf("got[0] = %v, want T_INT_KW", got[0])
	}
	if got[1] != LX.T_TEXT {
		t.Errorf("got[1] = %v, want T_TEXT", got[1])
	}
	if got[2] != LX.T_BOOL {
		t.Errorf("got[2] = %v, want T_BOOL", got[2])
	}
}

// BenchmarkVectorizedFilter measures filter performance on
// a 1024-row batch with 50% selectivity.
func BenchmarkVectorizedFilter(b *testing.B) {
	rows := makeTestRows(1024)
	schema := []string{"id"}
	types := []LX.TokenType{LX.T_INT_KW}
	pred := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "id"},
		Op:    LX.T_GE,
		Right: &PS.NumberLiteral{Val: 512},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		src := &rowSourceForTest{rows: rows}
		scan := NewVectorizedSeqScan(src, schema, types)
		filter := NewVectorizedFilter(scan, pred)
		for {
			batch, _ := filter.NextBatch(context.Background())
			if batch == nil {
				break
			}
			batch.Put()
		}
		filter.Close()
	}
}

// BenchmarkRowFilter_Fallback measures the row-at-a-time fallback
// for comparison with the vectorized path.
func BenchmarkRowFilter_Fallback(b *testing.B) {
	rows := makeTestRows(1024)
	pred := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "id"},
		Op:    LX.T_GE,
		Right: &PS.NumberLiteral{Val: 512},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, row := range rows {
			_, _ = EV.EvalValue(pred, &row, nil)
		}
	}
}

// BenchmarkVectorizedFilter_MultiBatch measures the full
// scan + filter pipeline across 10 batches (10K rows).
// This shows the real-world speedup of vectorized execution
// over the row-at-a-time fallback for the Eval-based path.
func BenchmarkVectorizedFilter_MultiBatch(b *testing.B) {
	const n = 10 * 1024
	rows := make([]Row, n)
	for i := 0; i < n; i++ {
		rows[i] = Row{
			Cols:  []string{"id"},
			Types: []LX.TokenType{LX.T_INT_KW},
			Data:  []Value{NewIntValue(int64(i))},
		}
	}
	schema := []string{"id"}
	types := []LX.TokenType{LX.T_INT_KW}
	pred := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "id"},
		Op:    LX.T_GE,
		Right: &PS.NumberLiteral{Val: 5000},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		src := &rowSourceForTest{rows: rows}
		scan := NewVectorizedSeqScan(src, schema, types)
		filter := NewVectorizedFilter(scan, pred)
		for {
			batch, _ := filter.NextBatch(context.Background())
			if batch == nil {
				break
			}
			batch.Put()
		}
		filter.Close()
	}
}

// BenchmarkEvalDirect_Int64GT measures the pure vectorized
// fast path (compareInt64ColLit) on 1024 rows.
// This is the "raw SIMD-like" path, free of scan/alloc overhead.
func BenchmarkEvalDirect_Int64GT(b *testing.B) {
	const n = 1024
	batch := UT.GetBatch(1)
	defer batch.Put()
	for i := 0; i < n; i++ {
		batch.AppendRow(0, LX.T_INT_KW, int64(i), false)
		batch.AdvanceSize()
	}
	col := batch.Cols[0]

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = EV.CompareInt64ColLit(col, 512, LX.T_GT, n)
	}
}
func makeSortTestRows(n int) []pl.Row {
	rows := make([]pl.Row, n)
	// Create reversed order: n-1, n-2, ..., 1, 0
	for i := 0; i < n; i++ {
		rows[i] = pl.Row{
			Cols:  []string{"id"},
			Types: []LX.TokenType{LX.T_INT_KW},
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

	idCol := batch.Cols[0].Data.Ints
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

	idCol := batch.Cols[0].Data.Ints
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
	pool := 
UT.NewWorkerPool(4)
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

	idCol := batch.Cols[0].Data.Ints
	// Verify ascending order on first UT.BatchSize rows
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
// multiple batches when data exceeds UT.BatchSize. REQ001021.
func TestParallelSort_MultiBatch(t *testing.T) {
	rows := makeSortTestRows(2*UT.BatchSize + 10)
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

		idCol := batch.Cols[0].Data.Ints
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
		pool := 
UT.NewWorkerPool(4)
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
