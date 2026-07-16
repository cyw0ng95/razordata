package AG

import (
	"context"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// REQ001447: VectorizedWindowFunc computes ROW_NUMBER correctly.
func TestVectorizedWindowFunc_RowNumber(t *testing.T) {
	batches := []*UT.Batch{
		func() *UT.Batch {
			b := UT.GetBatch(1)
			b.SetColumnName(0, "x")
			b.Cols[0].Type = LX.T_INT_KW
			for _, v := range []int64{10, 20, 30} {
				b.AppendRow(0, LX.T_INT_KW, v, false)
				b.AdvanceSize()
			}
			return b
		}(),
	}
	child := &fakeWindowProducer{batches: batches}
	w := NewVectorizedWindowFunc(child, "ROW_NUMBER", &PS.WindowSpec{}, []string{"x"}, []LX.TokenType{LX.T_INT_KW})

	batch, err := w.NextBatch(context.Background())
	if err != nil {
		t.Fatalf("NextBatch: %v", err)
	}
	if batch == nil {
		t.Fatal("expected non-nil batch")
	}
	if batch.Size != 3 {
		t.Errorf("Size: got %d, want 3", batch.Size)
	}
	// Window column is at index nCols (last user column).
	nCols := len(w.cols)
	if nCols >= len(batch.Cols) {
		t.Fatalf("nCols(%d) >= len(batch.Cols)(%d)", nCols, len(batch.Cols))
	}
	wCol := &batch.Cols[nCols]
	if wCol.Type != LX.T_BIGINT {
		t.Errorf("Window col type: got %v, want BIGINT", wCol.Type)
	}
	if len(wCol.Data.Ints) < 3 {
		t.Fatalf("Window ints len: got %d, want >= 3", len(wCol.Data.Ints))
	}
	want := []int64{1, 2, 3}
	for i, w := range want {
		if wCol.Data.Ints[i] != w {
			t.Errorf("window[%d]: got %d, want %d", i, wCol.Data.Ints[i], w)
		}
	}
}

// REQ001447: VectorizedWindowFunc handles empty input.
func TestVectorizedWindowFunc_EmptyInput(t *testing.T) {
	child := &fakeWindowProducer{batches: []*UT.Batch{}}
	w := NewVectorizedWindowFunc(child, "ROW_NUMBER", &PS.WindowSpec{}, []string{"x"}, []LX.TokenType{LX.T_INT_KW})

	batch, err := w.NextBatch(context.Background())
	if err != nil {
		t.Fatalf("NextBatch: %v", err)
	}
	if batch != nil {
		t.Fatal("expected nil batch for empty input")
	}
}

// REQ001447: VectorizedWindowFuncMultiCol materializes columns correctly.
func TestVectorizedWindowFuncMultiCol_Materializes(t *testing.T) {
	batches := []*UT.Batch{
		func() *UT.Batch {
			b := UT.GetBatch(1)
			b.SetColumnName(0, "x")
			b.Cols[0].Type = LX.T_INT_KW
			for _, v := range []int64{5, 3, 1} {
				b.AppendRow(0, LX.T_INT_KW, v, false)
				b.AdvanceSize()
			}
			return b
		}(),
	}
	child := &fakeWindowProducer{batches: batches}
	w := NewVectorizedWindowFuncMultiCol(child, "ROW_NUMBER", &PS.WindowSpec{}, []string{"x"}, []LX.TokenType{LX.T_INT_KW})

	batch, err := w.NextBatch(context.Background())
	if err != nil {
		t.Fatalf("NextBatch: %v", err)
	}
	if batch == nil {
		t.Fatal("expected non-nil batch")
	}
	if batch.Size != 3 {
		t.Errorf("Size: got %d, want 3", batch.Size)
	}
	// Input column should have original values.
	if len(batch.Cols[0].Data.Ints) < 3 {
		t.Fatalf("col 0 ints len: got %d, want >= 3", len(batch.Cols[0].Data.Ints))
	}
}

// REQ001447: sortIndicesByCol sorts correctly.
func TestSortIndicesByCol(t *testing.T) {
	data := []int64{30, 10, 20}
	indices := []int{0, 1, 2}
	sortIndicesByCol(data, indices)
	want := []int{1, 2, 0}
	for i, w := range want {
		if indices[i] != w {
			t.Errorf("indices[%d]: got %d, want %d", i, indices[i], w)
		}
	}
}

// REQ001447: compareWindowValues compares correctly.
func TestCompareWindowValues(t *testing.T) {
	tests := []struct {
		a, b   any
		want   int
	}{
		{int64(1), int64(2), -1},
		{int64(2), int64(1), 1},
		{int64(3), int64(3), 0},
		{"abc", "abd", -1},
		{"xyz", "abc", 1},
		{float64(1.5), float64(2.5), -1},
	}
	for _, tc := range tests {
		got := compareWindowValues(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("compare(%v, %v): got %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

// fakeWindowProducer implements BatchProducer for window tests.
type fakeWindowProducer struct {
	batches []*UT.Batch
	idx     int
}

func (f *fakeWindowProducer) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if f.idx >= len(f.batches) {
		return nil, nil
	}
	b := f.batches[f.idx]
	f.idx++
	return b, nil
}

func (f *fakeWindowProducer) Close() error { return nil }
