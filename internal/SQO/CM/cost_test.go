package CM

import (
	"testing"

	AG "github.com/cyw0ng95/razordata/internal/SQB/AG"
	OP "github.com/cyw0ng95/razordata/internal/SQB/OP"
	WT "github.com/cyw0ng95/razordata/internal/SQB/WT"
	"github.com/cyw0ng95/razordata/internal/SQO/CP"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

func leafOp() pl.Operator {
	return OP.NewSeqScan("t")
}

func TestEstimate_Nil(t *testing.T) {
	cp := CP.Default()
	if got := Estimate(nil, cp); got != 0 {
		t.Fatalf("expected 0 for nil, got %v", got)
	}
}

func TestEstimate_SeqScan_Legacy(t *testing.T) {
	cp := CP.Default()
	if got := Estimate(OP.NewSeqScan("t"), cp); got != 1.0 {
		t.Fatalf("expected 1.0, got %v", got)
	}
}

func TestEstimate_IndexScan_Default_Legacy(t *testing.T) {
	cp := CP.Default()
	scan := OP.NewIndexScan("t", "idx", nil, nil)
	// NewIndexScan defaults to indexMode=false (range mode), cost 0.1
	if got := Estimate(scan, cp); got != 0.1 {
		t.Fatalf("expected 0.1, got %v", got)
	}
}

func TestEstimate_IndexScan_Range_Legacy(t *testing.T) {
	cp := CP.Default()
	scan := OP.NewIndexScan("t", "idx", []byte{1}, []byte{10})
	if got := Estimate(scan, cp); got != 0.1 {
		t.Fatalf("expected 0.1, got %v", got)
	}
}

func TestEstimate_BitmapHeapScan_Legacy(t *testing.T) {
	cp := CP.Default()
	idx1 := OP.NewIndexScan("t", "idx1", []byte{1}, nil)
	idx2 := OP.NewIndexScan("t", "idx2", []byte{2}, nil)
	bhs := OP.NewBitmapHeapScan("t", nil, []pl.Operator{idx1, idx2})
expected := 0.05*2 + 0.05
	got := Estimate(bhs, cp)
	if got < expected-0.0001 || got > expected+0.0001 {
		t.Fatalf("expected ~%v, got %v", expected, got)
	}
}

func TestEstimate_IndexOnlyScan_Legacy(t *testing.T) {
	cp := CP.Default()
	inner := OP.NewIndexScan("t", "idx", nil, nil)
	ios := OP.NewIndexOnlyScan(inner)
	if got := Estimate(ios, cp); got != 0.03 {
		t.Fatalf("expected 0.03, got %v", got)
	}
}

func TestEstimate_Filter_Legacy(t *testing.T) {
	cp := CP.Default()
	pred := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "a"},
		Right: &PS.NumberLiteral{Val: 42},
		Op:    LX.T_EQ,
	}
	filt := OP.NewFilter(leafOp(), pred, nil)
	// estimateLegacy(child) * 0.1 (eq selectivity) = 1.0 * 0.1 = 0.1
	if got := Estimate(filt, cp); got != 0.1 {
		t.Fatalf("expected 0.1 (1.0 * 0.1 sel), got %v", got)
	}
}

func TestEstimate_Project_Legacy(t *testing.T) {
	cp := CP.Default()
	proj := OP.NewProject(leafOp(), nil)
	if got := Estimate(proj, cp); got != 1.0 {
		t.Fatalf("expected 1.0 (child cost), got %v", got)
	}
}

func TestEstimate_Limit_Legacy(t *testing.T) {
	cp := CP.Default()
	lim := OP.NewLimit(leafOp(), 10)
	if got := Estimate(lim, cp); got != 1.0 {
		t.Fatalf("expected 1.0 (child cost), got %v", got)
	}
}

func TestEstimate_Offset_Legacy(t *testing.T) {
	cp := CP.Default()
	off := OP.NewOffset(leafOp(), 5)
	if got := Estimate(off, cp); got != 1.0 {
		t.Fatalf("expected 1.0 (child cost), got %v", got)
	}
}

func TestEstimate_Distinct_Legacy(t *testing.T) {
	cp := CP.Default()
	dist := OP.NewDistinct(leafOp())
	if got := Estimate(dist, cp); got != 1.0 {
		t.Fatalf("expected 1.0 (child cost), got %v", got)
	}
}

func TestEstimate_Sort_Legacy(t *testing.T) {
	cp := CP.Default()
	keys := []PS.OrderItem{{Expr: &PS.Ident{Name: "a"}, Desc: false}}
	sort := OP.NewSort(leafOp(), keys)
	// child=1, min floor 1 => 1 * (1 + log2(1)) = 1 * (1 + 0) = 1.0
	if got := Estimate(sort, cp); got != 1.0 {
		t.Fatalf("expected 1.0, got %v", got)
	}
}

func TestEstimate_Sort_Deep_Legacy(t *testing.T) {
	cp := CP.Default()
	// Build a tree with cost > 1 so log2ish matters:
	// Sort(HashJoin(SeqScan, SeqScan))
	// HashJoin cost = 1+1=2, floor doesn't matter
	// Sort cost = 2 * (1 + log2(2)) = 2 * 2 = 4
	left := leafOp()
	right := leafOp()
	hj := OP.NewHashJoin(left, right, "l", "r", []string{"k"}, []string{"k"}, 16)
	keys := []PS.OrderItem{{Expr: &PS.Ident{Name: "a"}}}
	sort := OP.NewSort(hj, keys)
	if got := Estimate(sort, cp); got != 4.0 {
		t.Fatalf("expected 4.0, got %v", got)
	}
}

func TestEstimate_Aggregate_Legacy(t *testing.T) {
	cp := CP.Default()
	agg := AG.NewAggregate(leafOp(), nil, nil)
	if got := Estimate(agg, cp); got != 2.0 {
		t.Fatalf("expected 2.0 (child + 1), got %v", got)
	}
}

func TestEstimate_HashAggregate_Legacy(t *testing.T) {
	cp := CP.Default()
	hagg := AG.NewHashAggregate(leafOp(), nil, nil)
	if got := Estimate(hagg, cp); got != 2.0 {
		t.Fatalf("expected 2.0 (child + 1), got %v", got)
	}
}

func TestEstimate_NestedLoopJoin_Legacy(t *testing.T) {
	cp := CP.Default()
	nlj := OP.NewNestedLoopJoin(leafOp(), leafOp(), "l", "r", nil, OP.JoinKindInner)
	// 1.0 * 1.0 = 1.0
	if got := Estimate(nlj, cp); got != 1.0 {
		t.Fatalf("expected 1.0, got %v", got)
	}
}

func TestEstimate_NestedLoopJoin_Deep_Legacy(t *testing.T) {
	cp := CP.Default()
	// Two IndexScans (0.1 each, default indexMode=false): 0.1 * 0.1 = 0.01
	left := OP.NewIndexScan("t", "idx", nil, nil)
	right := OP.NewIndexScan("t", "idx", nil, nil)
	nlj := OP.NewNestedLoopJoin(left, right, "l", "r", nil, OP.JoinKindInner)
	if got, want := Estimate(nlj, cp), 0.01; got < want-0.0001 || got > want+0.0001 {
		t.Fatalf("expected ~%v, got %v", want, got)
	}
}

func TestEstimate_HashJoin_Legacy(t *testing.T) {
	cp := CP.Default()
	hj := OP.NewHashJoin(leafOp(), leafOp(), "l", "r", []string{"k"}, []string{"k"}, 16)
	// min(1, 1) + min(1, 1) = 2.0
	if got := Estimate(hj, cp); got != 2.0 {
		t.Fatalf("expected 2.0, got %v", got)
	}
}

func TestEstimate_MergeJoin_Legacy(t *testing.T) {
	cp := CP.Default()
	mj := OP.NewMergeJoin(leafOp(), leafOp(), "l", "r", []string{"k"}, []string{"k"})
	// min(1, 1) + min(1, 1) + 1 = 3.0
	if got := Estimate(mj, cp); got != 3.0 {
		t.Fatalf("expected 3.0, got %v", got)
	}
}

func TestEstimate_Insert_Legacy(t *testing.T) {
	cp := CP.Default()
	ins := WT.NewInsert("t", []string{"id"}, [][]PS.Expr{
		{&PS.NumberLiteral{Val: 1}},
	}, nil, nil)
	if got := Estimate(ins, cp); got != 1.0 {
		t.Fatalf("expected 1.0, got %v", got)
	}
}

func TestEstimate_Delete_Legacy(t *testing.T) {
	cp := CP.Default()
	del := WT.NewDelete("t", nil, leafOp(), nil)
	if got := Estimate(del, cp); got != 1.0 {
		t.Fatalf("expected 1.0, got %v", got)
	}
}

func TestEstimate_Update_Legacy(t *testing.T) {
	cp := CP.Default()
	upd := WT.NewUpdate("t", []PS.Pair{
		{Col: "a", Val: &PS.NumberLiteral{Val: 1}},
	}, nil, leafOp(), nil)
	if got := Estimate(upd, cp); got != 1.0 {
		t.Fatalf("expected 1.0, got %v", got)
	}
}

func TestEstimate_CreateTable_Legacy(t *testing.T) {
	cp := CP.Default()
	ct := WT.NewCreateTable(&PS.CreateTable{Name: "t"})
	if got := Estimate(ct, cp); got != 1.0 {
		t.Fatalf("expected 1.0, got %v", got)
	}
}

func TestEstimate_DropTable_Legacy(t *testing.T) {
	cp := CP.Default()
	dt := WT.NewDropTable(&PS.DropTable{Name: "t"})
	if got := Estimate(dt, cp); got != 1.0 {
		t.Fatalf("expected 1.0, got %v", got)
	}
}

func TestEstimate_UnknownType_Legacy(t *testing.T) {
	cp := CP.Default()
	if got := Estimate(leafOp(), cp); got != 1.0 {
		t.Fatalf("expected 1.0 (default), got %v", got)
	}
}

func TestEstimate_AbsoluteTime_DelegatesToPGStyle(t *testing.T) {
	cp := CP.Default()
	cp.AbsoluteTime = true
	// SeqScan in PG-style returns cp.SeqPageCost = 1.0
	if got := Estimate(leafOp(), cp); got != 1.0 {
		t.Fatalf("expected 1.0, got %v", got)
	}
}

func TestEstimate_SeqScan_PGStyle(t *testing.T) {
	cp := CP.Default()
	cp.AbsoluteTime = true
	if got := Estimate(OP.NewSeqScan("t"), cp); got != cp.SeqPageCost {
		t.Fatalf("expected %v, got %v", cp.SeqPageCost, got)
	}
}

func TestEstimate_IndexScan_Default_PGStyle(t *testing.T) {
	cp := CP.Default()
	cp.AbsoluteTime = true
	scan := OP.NewIndexScan("t", "idx", nil, nil)
	// indexMode=false → range cost: 2 * (CPUIndexTupleCost + RandomPageCost/100)
	expected := 2 * (cp.CPUIndexTupleCost + cp.RandomPageCost/100)
	if got := Estimate(scan, cp); got != expected {
		t.Fatalf("expected %v, got %v", expected, got)
	}
}

func TestEstimate_IndexScan_Range_PGStyle(t *testing.T) {
	cp := CP.Default()
	cp.AbsoluteTime = true
	scan := OP.NewIndexScan("t", "idx", []byte{1}, []byte{10})
	expected := 2 * (cp.CPUIndexTupleCost + cp.RandomPageCost/100)
	if got := Estimate(scan, cp); got != expected {
		t.Fatalf("expected %v, got %v", expected, got)
	}
}

func TestEstimate_Project_PGStyle(t *testing.T) {
	cp := CP.Default()
	cp.AbsoluteTime = true
	proj := OP.NewProject(leafOp(), nil)
	expected := cp.SeqPageCost + cp.CPUTupleCost
	if got := Estimate(proj, cp); got != expected {
		t.Fatalf("expected %v, got %v", expected, got)
	}
}

func TestEstimate_NestedLoopJoin_PGStyle(t *testing.T) {
	cp := CP.Default()
	cp.AbsoluteTime = true
	nlj := OP.NewNestedLoopJoin(leafOp(), leafOp(), "l", "r", nil, OP.JoinKindInner)
	expected := cp.SeqPageCost * (cp.SeqPageCost + cp.CPUOperatorCost)
	if got := Estimate(nlj, cp); got != expected {
		t.Fatalf("expected %v, got %v", expected, got)
	}
}

func TestEstimate_MergeJoin_PGStyle(t *testing.T) {
	cp := CP.Default()
	cp.AbsoluteTime = true
	mj := OP.NewMergeJoin(leafOp(), leafOp(), "l", "r", []string{"k"}, []string{"k"})
	expected := cp.SeqPageCost + cp.SeqPageCost + cp.CPUTupleCost
	if got := Estimate(mj, cp); got != expected {
		t.Fatalf("expected %v, got %v", expected, got)
	}
}

func TestLog2ish_EdgeCases(t *testing.T) {
	tests := []struct {
		input float64
		want  float64
	}{
		{0, 0},
		{1, 0},
		{2, 1},
		{4, 2},
		{8, 3},
		{100, 7},
		{1024, 10},
	}
	for _, tc := range tests {
		if got := log2ish(tc.input); got != tc.want {
			t.Errorf("log2ish(%v) = %v, want %v", tc.input, got, tc.want)
		}
	}
}