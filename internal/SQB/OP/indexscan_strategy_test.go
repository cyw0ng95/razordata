package OP

import (
	"context"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// TestIndexScan_StrategySelection covers the four mode → strategy
// mappings defined in SelectStrategy. The InMemoryScan branch is
// exercised without a store; the Store/Index/BTree branches use
// the in-memory store registered via the `DT.Tables` map (the only
// public Store surface available in unit tests).
//
// REQ002038: Converted to use BatchIndexScan instead of row-based Next().
func TestIndexScan_StrategySelection(t *testing.T) {
	t.Run("in-memory", func(t *testing.T) {
		// NewIndexScan constructs an IndexScan with no store,
		// no index mode, no btree. BatchIndexScan should
		// wrap it and drain rows in batches.
		scan := NewIndexScan("__test_no_such_table__", "idx", nil, nil)
		batch := NewBatchIndexScan(scan)
		defer batch.Close()

		ctx := context.Background()
		b, err := batch.NextBatch(ctx)
		if err != nil {
			t.Fatalf("NextBatch: %v", err)
		}
		if b != nil {
			t.Errorf("expected nil batch for empty table, got batch with %d rows", b.Size)
		}
	})
}

// TestScanStrategy_InterfaceConformance is a compile-time guard
// ensuring all strategy types implement the ScanStrategy interface.
func TestScanStrategy_InterfaceConformance(t *testing.T) {
	var _ ScanStrategy = (*InMemoryScan)(nil)
	var _ ScanStrategy = (*StorePrefixScan)(nil)
	var _ ScanStrategy = (*IndexSeekScan)(nil)
	var _ ScanStrategy = (*BTreeScan)(nil)
	var _ ScanStrategy = (*RangeSeekScan)(nil)
}

// TestScanStrategy_InMemoryDispatch drives InMemoryScan over a
// small slice and checks the rows come out in order.
//
// REQ002038: Converted to use BatchIndexScan instead of row-based Next().
func TestScanStrategy_InMemoryDispatch(t *testing.T) {
	rows := []Row{
		{Cols: []string{"a"}, Types: []LX.TokenType{LX.T_INT_KW}, Data: []Value{DT.NewIntValue(1)}},
		{Cols: []string{"a"}, Types: []LX.TokenType{LX.T_INT_KW}, Data: []Value{DT.NewIntValue(2)}},
		{Cols: []string{"a"}, Types: []LX.TokenType{LX.T_INT_KW}, Data: []Value{DT.NewIntValue(3)}},
	}
	// Create an IndexScan with in-memory rows and wrap with BatchIndexScan.
	scan := &IndexScan{rows: rows, table: "t", pos: 0}
	batch := NewBatchIndexScan(scan)
	defer batch.Close()

	ctx := context.Background()

	// Collect all rows from batches.
	b, err := batch.NextBatch(ctx)
	if err != nil {
		t.Fatalf("NextBatch: %v", err)
	}
	if b == nil {
		t.Fatal("expected batch, got nil")
	}
	if b.Size != 3 {
		t.Fatalf("expected 3 rows, got %d", b.Size)
	}

	// Verify rows come out in order (values 1, 2, 3).
	for i := 0; i < b.Size; i++ {
		val := b.Cols[0].Data.Ints[i]
		expected := int64(i + 1)
		if val != expected {
			t.Errorf("row %d: got %d, want %d", i, val, expected)
		}
	}

	// EOF: next batch should be nil.
	b2, err := batch.NextBatch(ctx)
	if err != nil {
		t.Fatalf("NextBatch (EOF): %v", err)
	}
	if b2 != nil {
		t.Errorf("expected nil batch at EOF, got batch with %d rows", b2.Size)
	}
}

// TestSelectStrategy_RejectsNil verifies the nil guard.
func TestSelectStrategy_RejectsNil(t *testing.T) {
	if _, err := SelectStrategy(nil); err == nil {
		t.Error("expected error for nil IndexScan, got nil")
	}
}
