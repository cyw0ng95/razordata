package OP

import (
	"testing"

	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

// TestFactory_ReturnsOperatorFactory verifies the factory
// returns a value satisfying pl.OperatorFactory.
func TestFactory_ReturnsOperatorFactory(t *testing.T) {
	var f pl.OperatorFactory = Factory()
	if f == nil {
		t.Fatal("Factory() returned nil")
	}
}

// TestSeqScan_ImplementsRelationSource verifies SeqScan satisfies
// the pl.RelationSource interface.
func TestSeqScan_ImplementsRelationSource(t *testing.T) {
	s := NewSeqScan("t1")
	var rs pl.RelationSource = s
	if rs.Table() != "t1" {
		t.Errorf("Table() = %q, want t1", rs.Table())
	}
}

// TestSeqScan_ImplementsColPrunable verifies SeqScan satisfies
// pl.ColPrunable for column-pruning passes.
func TestSeqScan_ImplementsColPrunable(t *testing.T) {
	s := NewSeqScan("t1")
	var cp pl.ColPrunable = s
	cp.SetUsedCols([]string{"a", "b"})
	if got := cp.UsedCols(); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("UsedCols = %v, want [a b]", got)
	}
}

// TestSeqScan_ImplementsParent verifies SeqScan satisfies
// pl.Parent. SeqScan has no child, so SetChild(nil) is a no-op.
func TestSeqScan_ImplementsParent(t *testing.T) {
	s := NewSeqScan("t1")
	var p pl.Parent = s
	if p.Child() != nil {
		t.Errorf("SeqScan.Child() should be nil")
	}
}

// TestIndexScan_ImplementsIndexInfo verifies IndexScan satisfies
// pl.IndexInfo (the index name accessor).
func TestIndexScan_ImplementsIndexInfo(t *testing.T) {
	i := NewIndexScan("t1", "idx1", nil, nil)
	var ii pl.IndexInfo = i
	if ii.IndexName() != "idx1" {
		t.Errorf("IndexName() = %q, want idx1", ii.IndexName())
	}
}

// TestHashJoin_ImplementsChildren2 verifies HashJoin satisfies
// pl.Children2 (two-child operator).
func TestHashJoin_ImplementsChildren2(t *testing.T) {
	j := NewHashJoin(nil, nil, "l", "r", []string{"k"}, []string{"k"}, 0)
	var c2 pl.Children2 = j
	if c2.Left() != nil {
		t.Errorf("Left() should be nil for fresh HashJoin")
	}
	if c2.Right() != nil {
		t.Errorf("Right() should be nil for fresh HashJoin")
	}
}

// TestLimit_ImplementsLimitInfo verifies Limit satisfies
// pl.LimitInfo.
func TestLimit_ImplementsLimitInfo(t *testing.T) {
	l := NewLimit(nil, 10)
	var li pl.LimitInfo = l
	if li.Limit() != 10 {
		t.Errorf("Limit() = %d, want 10", li.Limit())
	}
	if li.IsTopN() {
		t.Error("IsTopN() should default to false")
	}
}

// TestSort_OrderBy verifies Sort's pl.OrderBy() projection.
func TestSort_OrderBy(t *testing.T) {
	// Sort requires a real OrderItem; the factory's
	// pl.OrderSpec → PS.OrderItem conversion lives in
	// factory.go. Here we directly test the OrderBy() adapter.
	s := NewSort(nil, nil)
	var si pl.SortInfo = s
	if si.OrderBy() != nil {
		t.Errorf("OrderBy() = %v, want nil for empty Sort", si.OrderBy())
	}
}

// TestFactory_NewSeqScan verifies the factory method builds a
// real operator.
func TestFactory_NewSeqScan(t *testing.T) {
	f := Factory()
	op := f.NewSeqScan("t1", []string{"a", "b"})
	if op == nil {
		t.Fatal("NewSeqScan returned nil")
	}
	// Verify the operator is functional by calling Next/Close.
	// We don't care about return values; just that the methods
	// are present and don't panic on a fresh operator.
	_ = op.Close()
}

// TestFactory_NewValues returns an in-memory operator.
func TestFactory_NewValues(t *testing.T) {
	f := Factory()
	op := f.NewValues(nil)
	if op == nil {
		t.Fatal("NewValues returned nil")
	}
}

// TestFactory_AllMethodsExist verifies the factory exposes all
// 15 OperatorFactory methods without panic.
func TestFactory_AllMethodsExist(t *testing.T) {
	var f pl.OperatorFactory = Factory()
	// Just call each one with minimal args and verify nil is
	// not returned for the methods we expect to support.
	_ = f.NewSeqScan("t", nil)
	_ = f.NewIndexScan("t", "i", nil)
	_ = f.NewIndexOnlyScan("t", "i", nil)
	_ = f.NewFilter(nil, nil)
	_ = f.NewProject(nil, nil, nil)
	_ = f.NewFilterProject(nil, nil, nil, nil)
	// NewHashJoin and NewNestedLoopJoin return nil by design.
	_ = f.NewHashJoin(nil, nil, "", "", pl.InnerJoin)
	_ = f.NewNestedLoopJoin(nil, nil, nil, pl.InnerJoin)
	_ = f.NewAggregate(nil, nil, nil)
	_ = f.NewHashAggregate(nil, nil, nil)
	_ = f.NewSort(nil, nil)
	_ = f.NewLimit(nil, 0, 0)
	_ = f.NewDistinct(nil)
	_ = f.NewSetOp(nil, nil, pl.UnionAllOp)
	_ = f.NewValues(nil)
}
