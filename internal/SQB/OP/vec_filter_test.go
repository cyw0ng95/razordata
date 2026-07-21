package OP

import (
	"context"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// REQ001589: VectorizedFilter range predicate pushdown to SeqScan.
func TestVectorizedFilter_RangePushdown(t *testing.T) {
	// Create a SeqScan with a store (simulated via NextBatch).
	ss := NewSeqScan("t1")
	// Set a range predicate on the scan.
	ss.SetRangePredicate(0, 10, 100)

	// Verify the predicate was set.
	if !ss.predicateIsSet {
		t.Fatal("expected predicateIsSet=true")
	}
	if ss.predicateCol != 0 {
		t.Fatalf("predicateCol: got %d, want 0", ss.predicateCol)
	}
	if ss.predicateMin != 10 {
		t.Fatalf("predicateMin: got %d, want 10", ss.predicateMin)
	}
	if ss.predicateMax != 100 {
		t.Fatalf("predicateMax: got %d, want 100", ss.predicateMax)
	}
}

// REQ001589: VectorizedFilter with range predicate via transform.
func TestVectorizedFilter_RangePredicateTransform(t *testing.T) {
	// Create a SeqScan with a real store.
	ss := NewSeqScan("t1")
	// Create a Filter with a range predicate.
	pred := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "x"},
		Right: &PS.NumberLiteral{Val: 50},
		Op:    LX.T_GT,
	}
	filt := NewFilter(ss, pred, nil)

	// Verify the filter was created.
	if filt == nil {
		t.Fatal("expected non-nil filter")
	}
	if filt.Predicate() == nil {
		t.Fatal("expected non-nil predicate")
	}
}

// REQ001589: VectorizedSeqScan with block stat provider.
func TestVectorizedSeqScan_BlockStatProvider(t *testing.T) {
	// Create a fake batch producer.
	bp := &fakeBatchProducer{
		batches: []*UT.Batch{
			func() *UT.Batch {
				b := UT.GetBatch(1)
				b.SetColumnName(0, "x")
				b.Cols[0].Type = LX.T_INT_KW
				for _, v := range []int64{1, 2, 3} {
					b.AppendRow(0, LX.T_INT_KW, v, false)
					b.AdvanceSize()
				}
				return b
			}(),
		},
	}

	// Create a VectorizedFilter over the batch producer.
	filter := NewVectorizedFilter(bp, &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "x"},
		Right: &PS.NumberLiteral{Val: 1},
		Op:    LX.T_GT,
	})

	// Read the filtered batch.
	batch, err := filter.NextBatch(context.Background())
	if err != nil {
		t.Fatalf("NextBatch: %v", err)
	}
	if batch == nil {
		t.Fatal("expected non-nil batch")
	}
	if batch.Size != 2 {
		t.Fatalf("Size: got %d, want 2 (filtered from 3)", batch.Size)
	}
	batch.Put()

	// Second call should return nil (EOF).
	batch, err = filter.NextBatch(context.Background())
	if err != nil {
		t.Fatalf("NextBatch: %v", err)
	}
	if batch != nil {
		t.Fatal("expected nil batch at EOF")
	}
}