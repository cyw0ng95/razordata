package EX

import (
	"context"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQL/LX"
	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

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
			Types: []int{int(LX.T_INT_KW), int(LX.T_TEXT)},
			Data:  []any{int64(i), "row"},
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
		idCol := batch.Cols[0].Data.([]int64)
		valCol := batch.Cols[1].Data.([]string)
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
		Op:    int(LX.T_GE),
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
		Op:    int(LX.T_GT),
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
		Op:    int(LX.T_LT),
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
		Op:    int(LX.T_GE),
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
	types := []int{int(LX.T_INT_KW), int(LX.T_TEXT), int(LX.T_BOOL)}
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
		Op:    int(LX.T_GE),
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
		Op:    int(LX.T_GE),
		Right: &PS.NumberLiteral{Val: 512},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, row := range rows {
			_, _ = Eval(pred, &row, nil)
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
			Types: []int{int(LX.T_INT_KW)},
			Data:  []any{int64(i)},
		}
	}
	schema := []string{"id"}
	types := []LX.TokenType{LX.T_INT_KW}
	pred := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "id"},
		Op:    int(LX.T_GE),
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
	batch := GetBatch(1)
	defer batch.Put()
	for i := 0; i < n; i++ {
		batch.AppendRow(0, LX.T_INT_KW, int64(i), false)
		batch.AdvanceSize()
	}
	col := batch.Cols[0]

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = compareInt64ColLit(col, 512, int(LX.T_GT), n)
	}
}
