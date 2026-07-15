package OP

import (
	"context"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// REQ001441: VectorizedSort sorts a batch and emits in sorted order.
func TestVectorizedSort_Basic(t *testing.T) {
	// Create a fake BatchProducer that yields one batch with 5 rows.
	bp := &fakeBatchProducer{
		batches: []*UT.Batch{
			func() *UT.Batch {
				b := UT.GetBatch(2)
				b.SetColumnName(0, "id")
				b.SetColumnName(1, "val")
				b.Cols[0].Type = LX.T_INT_KW
				b.Cols[1].Type = LX.T_TEXT
				for _, v := range []int64{3, 1, 4, 1, 5} {
					b.AppendRow(0, LX.T_INT_KW, v, false)
					b.AppendRow(1, LX.T_TEXT, "v"+intToStr2(int(v)), false)
					b.AdvanceSize()
				}
				return b
			}(),
		},
	}
	keys := []PS.OrderItem{{Expr: &PS.Ident{Name: "id"}, Desc: false}}
	sortOp := NewVectorizedSort(bp, keys)
	
	// Collect all output rows.
	var allRows []*UT.Batch
	for {
		batch, err := sortOp.NextBatch(context.Background())
		if err != nil {
			t.Fatalf("NextBatch: %v", err)
		}
		if batch == nil {
			break
		}
		allRows = append(allRows, batch)
	}
	
	// Concatenate all output batches.
	var allVals []int64
	for _, b := range allRows {
		for i := 0; i < b.Size; i++ {
			val := b.Cols[0].Data.Ints[i]
			allVals = append(allVals, val)
		}
	}
	
	// Expected sorted: 1, 1, 3, 4, 5
	expected := []int64{1, 1, 3, 4, 5}
	if len(allVals) != len(expected) {
		t.Fatalf("got %d rows, want %d", len(allVals), len(expected))
	}
	for i, v := range allVals {
		if v != expected[i] {
			t.Errorf("row %d: got %d, want %d", i, v, expected[i])
		}
	}
}

// REQ001441: VectorizedLimit caps output to n rows.
func TestVectorizedLimit_Basic(t *testing.T) {
	bp := &fakeBatchProducer{
		batches: []*UT.Batch{
			func() *UT.Batch {
				b := UT.GetBatch(1)
				b.SetColumnName(0, "x")
				b.Cols[0].Type = LX.T_INT_KW
				for i := int64(1); i <= 10; i++ {
					b.AppendRow(0, LX.T_INT_KW, i, false)
					b.AdvanceSize()
				}
				return b
			}(),
		},
	}
	limitOp := NewVectorizedLimit(bp, 3)
	
	var vals []int64
	for {
		batch, err := limitOp.NextBatch(context.Background())
		if err != nil {
			t.Fatalf("NextBatch: %v", err)
		}
		if batch == nil {
			break
		}
		for i := 0; i < batch.Size; i++ {
			vals = append(vals, batch.Cols[0].Data.Ints[i])
		}
	}
	
	expected := []int64{1, 2, 3}
	if len(vals) != len(expected) {
		t.Fatalf("got %d rows, want %d", len(vals), len(expected))
	}
	for i, v := range vals {
		if v != expected[i] {
			t.Errorf("row %d: got %d, want %d", i, v, expected[i])
		}
	}
}

// fakeBatchProducer yields a fixed set of batches then returns nil.
type fakeBatchProducer struct {
	batches []*UT.Batch
	idx     int
}

func (f *fakeBatchProducer) NextBatch(ctx context.Context) (*UT.Batch, error) {
	_ = ctx
	if f.idx >= len(f.batches) {
		return nil, nil
	}
	b := f.batches[f.idx]
	f.idx++
	return b, nil
}

func (f *fakeBatchProducer) Close() error {
	return nil
}

func intToStr2(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
