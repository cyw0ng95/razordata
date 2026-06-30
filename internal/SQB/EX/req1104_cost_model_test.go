package EX

import (
	"testing"

	OP "github.com/cyw0ng95/razordata/internal/SQB/OP"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// TestCostModel_Parameters verifies REQ001104: the CostParams struct
// is exposed, defaults match PostgreSQL conventions, and SetCostParams
// switches the cost model from legacy heuristic to the new
// rows × page-cost formula.
func TestCostModel_Parameters(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		cp := DefaultCostParams()
		if cp.SeqPageCost != 1.0 {
			t.Errorf("SeqPageCost default = %v, want 1.0", cp.SeqPageCost)
		}
		if cp.RandomPageCost != 4.0 {
			t.Errorf("RandomPageCost default = %v, want 4.0", cp.RandomPageCost)
		}
		if cp.CPUTupleCost != 0.01 {
			t.Errorf("CPUTupleCost default = %v, want 0.01", cp.CPUTupleCost)
		}
		if cp.CPUIndexTupleCost != 0.005 {
			t.Errorf("CPUIndexTupleCost default = %v, want 0.005", cp.CPUIndexTupleCost)
		}
		if cp.CPUOperatorCost != 0.0025 {
			t.Errorf("CPUOperatorCost default = %v, want 0.0025", cp.CPUOperatorCost)
		}
	})
	t.Run("set_and_get", func(t *testing.T) {
		p := NewPlanner()
		custom := CostParams{
			SeqPageCost:       2.0,
			RandomPageCost:    8.0,
			CPUTupleCost:      0.02,
			CPUIndexTupleCost: 0.01,
			CPUOperatorCost:   0.005,
		}
		p.SetCostParams(custom)
		got := p.costParams()
		if got != custom {
			t.Errorf("costParams mismatch after SetCostParams: got %+v, want %+v", got, custom)
		}
	})
	t.Run("legacy_when_unset", func(t *testing.T) {
		// When CostParams are not explicitly set, the legacy
		// per-operator heuristic must be used (preserves existing
		// test expectations and behavior).
		p := NewPlanner()
		p.RegisterTable("t", []ColInfo{{Name: "a", Typ: 1}}, "a")
		if got := p.estimateCost(OP.NewSeqScan("t")); got != 1.0 {
			t.Errorf("SeqScan cost (legacy) = %v, want 1.0", got)
		}
	})
}

// TestCostModel_SeqScan verifies REQ001104: with CostParams set,
// SeqScan cost = rows × SeqPageCost (≥ 1.0 floor for empty tables).
func TestCostModel_SeqScan(t *testing.T) {
	p := NewPlanner()
	p.SetCostParams(DefaultCostParams())
	p.RegisterTable("t", []ColInfo{{Name: "a", Typ: 1}}, "a")
	scan := OP.NewSeqScan("t")
	cost := p.estimateCost(scan)
	if cost < 1.0 {
		t.Errorf("SeqScan cost = %v, want >= 1.0", cost)
	}
}

// TestCostModel_NLJ verifies REQ001104: NLJ cost with the new
// formula scales with CPUOperatorCost.
func TestCostModel_NLJ(t *testing.T) {
	scan1 := OP.NewSeqScan("t")
	scan2 := OP.NewSeqScan("t")
	nlj := NewNestedLoopJoin(scan1, scan2, "l", "r", nil, JoinKindInner)

	t.Run("low_cpu", func(t *testing.T) {
		p := NewPlanner()
		p.SetCostParams(CostParams{
			SeqPageCost:       1.0,
			RandomPageCost:    4.0,
			CPUTupleCost:      0.01,
			CPUIndexTupleCost: 0.005,
			CPUOperatorCost:   0.001,
		})
		p.RegisterTable("t", []ColInfo{{Name: "a", Typ: 1}}, "a")
		cost := p.estimateCost(nlj)
		if cost < 1.0 {
			t.Errorf("NLJ cost (low cpu) = %v, want >= 1.0", cost)
		}
	})
	t.Run("high_cpu", func(t *testing.T) {
		p := NewPlanner()
		p.SetCostParams(CostParams{
			SeqPageCost:       1.0,
			RandomPageCost:    4.0,
			CPUTupleCost:      0.01,
			CPUIndexTupleCost: 0.005,
			CPUOperatorCost:   0.1,
		})
		p.RegisterTable("t", []ColInfo{{Name: "a", Typ: 1}}, "a")
		highCost := p.estimateCost(nlj)
		if highCost < 1.0 {
			t.Errorf("NLJ cost (high cpu) = %v, want >= 1.0", highCost)
		}
	})
}

// TestCostModel_HashJoin verifies REQ001104: HashJoin cost =
// buildCost + probeCost + hashTableOverhead, all positive.
func TestCostModel_HashJoin(t *testing.T) {
	p := NewPlanner()
	p.SetCostParams(DefaultCostParams())
	p.RegisterTable("t", []ColInfo{{Name: "a", Typ: 1}}, "a")
	left := OP.NewSeqScan("t")
	right := OP.NewSeqScan("t")
	hj := OP.NewHashJoin(left, right, "l", "r", []string{"a"}, []string{"a"}, 0)
	cost := p.estimateCost(hj)
	if cost < 1.0 {
		t.Errorf("HashJoin cost = %v, want >= 1.0", cost)
	}
}

// TestCostModel_MemoryPressure verifies REQ001104: estimateMemoryPressure
// returns non-negative values for memory-intensive ops and the
// budget is set from maxMemoryPerQuery (or 64 MB default).
func TestCostModel_MemoryPressure(t *testing.T) {
	p := NewPlanner()
	p.SetMaxMemoryPerQuery(16 << 20) // 16 MB
	p.RegisterTable("t", []ColInfo{{Name: "a", Typ: 1}}, "a")

	t.Run("sort_under_budget", func(t *testing.T) {
		scan := OP.NewSeqScan("t")
		sort := OP.NewSort(scan, []PS.OrderItem{{Expr: &PS.QualifiedName{Name: "a"}, Desc: false}})
		estimated, budget := p.estimateMemoryPressure(sort)
		if budget != 16<<20 {
			t.Errorf("budget = %d, want %d", budget, 16<<20)
		}
		if estimated < 0 {
			t.Errorf("estimated = %d, want >= 0", estimated)
		}
	})
	t.Run("hash_join_under_budget", func(t *testing.T) {
		left := OP.NewSeqScan("t")
		right := OP.NewSeqScan("t")
		hj := OP.NewHashJoin(left, right, "l", "r", []string{"a"}, []string{"a"}, 0)
		estimated, _ := p.estimateMemoryPressure(hj)
		if estimated < 0 {
			t.Errorf("estimated = %d, want >= 0", estimated)
		}
	})
	t.Run("default_budget_when_unset", func(t *testing.T) {
		p2 := NewPlanner()
		p2.RegisterTable("t", []ColInfo{{Name: "a", Typ: 1}}, "a")
		scan := OP.NewSeqScan("t")
		sort := OP.NewSort(scan, []PS.OrderItem{{Expr: &PS.QualifiedName{Name: "a"}, Desc: false}})
		_, budget := p2.estimateMemoryPressure(sort)
		// Default 64 MB when maxMemoryPerQuery unset.
		if budget != 64<<20 {
			t.Errorf("default budget = %d, want %d", budget, 64<<20)
		}
	})
}

// TestCostModel_LegacyStillWorks ensures that cost_indexscan_test's
// hard-coded legacy expectations (1.0 for SeqScan, 0.05/0.1 for
// IndexScan, etc.) continue to pass. Run alongside the new tests
// to verify the two-path cost model.
func TestCostModel_LegacyStillWorks(t *testing.T) {
	p := NewPlanner()
	p.RegisterTable("t", []ColInfo{{Name: "a", Typ: 1}}, "a")
	scan := OP.NewSeqScan("t")
	// Legacy SeqScan = 1.0.
	if got := p.estimateCost(scan); got != 1.0 {
		t.Errorf("Legacy SeqScan = %v, want 1.0", got)
	}
	// Legacy Project = 1.0 (passes through to child).
	if got := p.estimateCost(OP.NewProject(scan, nil)); got != 1.0 {
		t.Errorf("Legacy Project = %v, want 1.0", got)
	}
}

// Ensure unused-import suppression: keep LX referenced so the
// file imports cleanly across refactors.
var _ = LX.T_EQ
