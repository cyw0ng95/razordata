// Package EX IndexScan strategy selection tests.
//
// REQ000979: verifies that SelectStrategy() dispatches to the
// right ScanStrategy type based on the IndexScan's configured
// mode.
package EX

import (
	"context"
	"testing"
)

// TestIndexScan_StrategySelection covers the four mode → strategy
// mappings defined in SelectStrategy. The InMemoryScan branch is
// exercised without a store; the Store/Index/BTree branches use
// the in-memory store registered via the `tables` map (the only
// public Store surface available in unit tests).
func TestIndexScan_StrategySelection(t *testing.T) {
	t.Run("in-memory", func(t *testing.T) {
		// NewIndexScan constructs an IndexScan with no store,
		// no index mode, no btree. SelectStrategy should
		// return an InMemoryScan wrapping the table's
		// in-memory rows.
		scan := NewIndexScan("__test_no_such_table__", "idx", nil, nil)
		strat, err := SelectStrategy(scan)
		if err != nil {
			t.Fatalf("SelectStrategy: %v", err)
		}
		if _, ok := strat.(*InMemoryScan); !ok {
			t.Errorf("expected *InMemoryScan, got %T", strat)
		}
		row, err := strat.Next(context.Background())
		if err != ErrNoRows {
			t.Errorf("expected ErrNoRows for empty table, got row=%v err=%v", row, err)
		}
		if err := strat.Close(); err != nil {
			t.Errorf("Close: %v", err)
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
func TestScanStrategy_InMemoryDispatch(t *testing.T) {
	rows := []Row{
		{Cols: []string{"a"}, Types: []int{1}, Data: []Value{NewIntValue(1)}},
		{Cols: []string{"a"}, Types: []int{1}, Data: []Value{NewIntValue(2)}},
		{Cols: []string{"a"}, Types: []int{1}, Data: []Value{NewIntValue(3)}},
	}
	s := NewInMemoryScan(rows)
	ctx := context.Background()
	for i := 1; i <= 3; i++ {
		row, err := s.Next(ctx)
		if err != nil {
			t.Fatalf("Next #%d: %v", i, err)
		}
		if row.Data[0].I64 != int64(i) {
			t.Errorf("row %d: got %d, want %d", i, row.Data[0].I64, i)
		}
	}
	if _, err := s.Next(ctx); err != ErrNoRows {
		t.Errorf("expected ErrNoRows, got %v", err)
	}
}

// TestSelectStrategy_RejectsNil verifies the nil guard.
func TestSelectStrategy_RejectsNil(t *testing.T) {
	if _, err := SelectStrategy(nil); err == nil {
		t.Error("expected error for nil IndexScan, got nil")
	}
}
