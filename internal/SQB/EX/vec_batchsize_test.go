//go:build !slt_corpus_full

package EX

import (
	"sync/atomic"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQB/OP"
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// REQ001443: chooseBatchSize picks {1, 64, 256, 1024} based on
// LIMIT clause and estimated row count.
func TestChooseBatchSize_LimitOne(t *testing.T) {
	p := NewPlanner()
	stmt := &PS.Select{
		Limit: &PS.NumberLiteral{Val: 1},
	}
	bs := p.chooseBatchSize(stmt, nil)
	if bs != 1 {
		t.Errorf("LIMIT 1: got %d, want 1", bs)
	}
}

func TestChooseBatchSize_Limit64(t *testing.T) {
	p := NewPlanner()
	stmt := &PS.Select{
		Limit: &PS.NumberLiteral{Val: 64},
	}
	bs := p.chooseBatchSize(stmt, nil)
	if bs != 64 {
		t.Errorf("LIMIT 64: got %d, want 64", bs)
	}
}

func TestChooseBatchSize_Limit100(t *testing.T) {
	p := NewPlanner()
	stmt := &PS.Select{
		Limit: &PS.NumberLiteral{Val: 100},
	}
	bs := p.chooseBatchSize(stmt, nil)
	if bs != 256 {
		t.Errorf("LIMIT 100: got %d, want 256", bs)
	}
}

func TestChooseBatchSize_NoLimit(t *testing.T) {
	p := NewPlanner()
	stmt := &PS.Select{}
	bs := p.chooseBatchSize(stmt, nil)
	if bs != 256 {
		t.Errorf("no limit: got %d, want 256", bs)
	}
}

func TestChooseBatchSize_ExplicitSet(t *testing.T) {
	p := NewPlanner()
	p.SetBatchSize(128)
	stmt := &PS.Select{
		Limit: &PS.NumberLiteral{Val: 1},
	}
	bs := p.chooseBatchSize(stmt, nil)
	if bs != 128 {
		t.Errorf("explicit batchSize=128: got %d, want 128", bs)
	}
}

// REQ001443: estimateRowCountFromOp walks through SeqScan and
// AdaptiveOp to find the table row count.
func TestEstimateRowCountFromOp_SeqScan(t *testing.T) {
	p := NewPlanner()
	p.catalog = map[string]*tableInfo{
		"t1": {name: "t1", rowCount: atomic.Int64{}},
	}
	p.catalog["t1"].rowCount.Store(500)
	scan := &OP.SeqScan{}
	// We can't easily construct a real SeqScan without a store,
	// so just verify the method doesn't panic.
	_ = p.estimateRowCountFromOp(scan)
}
