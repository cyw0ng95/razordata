package AG

import (
	"context"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// REQ001646: VectorizedWindowOperator computes ROW_NUMBER correctly.
func TestVectorizedWindow_RowNumber(t *testing.T) {
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
	w := NewVectorizedWindowOperator(child, "ROW_NUMBER", nil, &PS.WindowSpec{}, []string{"x"})

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
	// Window column is at index 1 (last column).
	wCol := &batch.Cols[1]
	if wCol.Type != LX.T_INT_KW {
		t.Errorf("Window col type: got %v, want INT_KW", wCol.Type)
	}
	want := []int64{1, 2, 3}
	for i, w := range want {
		if wCol.Data.Ints[i] != w {
			t.Errorf("window[%d]: got %d, want %d", i, wCol.Data.Ints[i], w)
		}
	}
}

// REQ001646: VectorizedWindowOperator handles empty input.
func TestVectorizedWindow_EmptyInput(t *testing.T) {
	child := &fakeWindowProducer{batches: []*UT.Batch{}}
	w := NewVectorizedWindowOperator(child, "ROW_NUMBER", nil, &PS.WindowSpec{}, []string{"x"})

	batch, err := w.NextBatch(context.Background())
	if err != nil {
		t.Fatalf("NextBatch: %v", err)
	}
	if batch != nil {
		t.Fatal("expected nil batch for empty input")
	}
}

// REQ001646: RANK function with ORDER BY.
func TestVectorizedWindow_Rank(t *testing.T) {
	batches := []*UT.Batch{
		func() *UT.Batch {
			b := UT.GetBatch(1)
			b.SetColumnName(0, "x")
			b.Cols[0].Type = LX.T_INT_KW
			for _, v := range []int64{10, 10, 20, 30} {
				b.AppendRow(0, LX.T_INT_KW, v, false)
				b.AdvanceSize()
			}
			return b
		}(),
	}
	child := &fakeWindowProducer{batches: batches}
	w := NewVectorizedWindowOperator(child, "RANK", nil, &PS.WindowSpec{OrderBy: []PS.OrderItem{{Expr: &PS.Ident{Name: "x"}}}}, []string{"x"})

	batch, err := w.NextBatch(context.Background())
	if err != nil {
		t.Fatalf("NextBatch: %v", err)
	}
	if batch == nil {
		t.Fatal("expected non-nil batch")
	}
	wCol := &batch.Cols[1]
	want := []int64{1, 1, 3, 4}
	for i, w := range want {
		if wCol.Data.Ints[i] != w {
			t.Errorf("window[%d]: got %d, want %d", i, wCol.Data.Ints[i], w)
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