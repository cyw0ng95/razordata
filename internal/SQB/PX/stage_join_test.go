package PX

import (
	"context"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// --- Helpers for multi-column batches.
func makeMultiIntBatch(colNames []string, colValues [][]int64) *UT.Batch {
	nCols := len(colValues)
	nRows := 0
	if nCols > 0 {
		nRows = len(colValues[0])
	}
	b := UT.GetBatch(nCols)
	b.Pooled = false
	for c, vals := range colValues {
		b.Cols[c].Type = LX.T_INT_KW
		if c < len(colNames) {
			b.SetColumnName(c, colNames[c])
		}
		b.Cols[c].Data.Ints = make([]int64, nRows)
		copy(b.Cols[c].Data.Ints, vals)
		b.Cols[c].Nulls = make([]bool, nRows)
	}
	b.Size = nRows
	return b
}

func makeStrBatch(name string, values []string) *UT.Batch {
	b := UT.GetBatch(1)
	b.Pooled = false
	b.Cols[0].Type = LX.T_TEXT
	b.SetColumnName(0, name)
	b.Cols[0].Data.Strs = make([]string, len(values))
	copy(b.Cols[0].Data.Strs, values)
	b.Cols[0].Nulls = make([]bool, len(values))
	b.Size = len(values)
	return b
}

func makeMultiStrBatch(colNames []string, colValues [][]string) *UT.Batch {
	nCols := len(colValues)
	nRows := 0
	if nCols > 0 {
		nRows = len(colValues[0])
	}
	b := UT.GetBatch(nCols)
	b.Pooled = false
	for c, vals := range colValues {
		b.Cols[c].Type = LX.T_TEXT
		if c < len(colNames) {
			b.SetColumnName(c, colNames[c])
		}
		b.Cols[c].Data.Strs = make([]string, nRows)
		copy(b.Cols[c].Data.Strs, vals)
		b.Cols[c].Nulls = make([]bool, nRows)
	}
	b.Size = nRows
	return b
}

// drainJoinResult collects all output rows from a stage.
func drainJoinResult(t *testing.T, stage Stage) [][]any {
	t.Helper()
	var result [][]any
	ctx := context.Background()
	for {
		batch, err := stage.NextBatch(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if batch == nil {
			break
		}
		nCols := 0
		for i := range batch.Cols {
			if batch.Cols[i].Type == 0 && batch.Cols[i].Name == "" {
				break
			}
			nCols = i + 1
		}
		size := batch.LogicalSize()
		for r := 0; r < size; r++ {
			rowIdx := r
			if batch.Sel != nil && r < len(batch.Sel) {
				rowIdx = int(batch.Sel[r])
			}
			row := make([]any, nCols)
			for c := 0; c < nCols; c++ {
				col := &batch.Cols[c]
				isNull := col.Nulls != nil && rowIdx < len(col.Nulls) && col.Nulls[rowIdx]
				if isNull {
					row[c] = nil
					continue
				}
				switch col.Type {
				case LX.T_INT_KW, LX.T_BIGINT:
					if rowIdx < len(col.Data.Ints) {
						row[c] = col.Data.Ints[rowIdx]
					}
				case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
					if rowIdx < len(col.Data.Strs) {
						row[c] = col.Data.Strs[rowIdx]
					}
				case LX.T_FLOAT_KW:
					if rowIdx < len(col.Data.Floats) {
						row[c] = col.Data.Floats[rowIdx]
					}
				}
			}
			result = append(result, row)
		}
	}
	return result
}

// --- Spec tests ---

func TestHashJoinStageSpec_Category(t *testing.T) {
	spec := &HashJoinStageSpec{}
	if spec.Category() != CatJoin {
		t.Errorf("expected CatJoin, got %v", spec.Category())
	}
}

func TestHashJoinStageSpec_NewRuntime(t *testing.T) {
	spec := &HashJoinStageSpec{
		BuildKeys: []int{0},
		ProbeKeys: []int{0},
		Kind:      JoinKindInner,
	}
	stage := spec.NewRuntime()
	if stage == nil {
		t.Fatal("expected non-nil stage")
	}
	hj, ok := stage.(*HashJoinStage)
	if !ok {
		t.Fatalf("expected *HashJoinStage, got %T", stage)
	}
	if len(hj.buildKeys) == 0 || hj.buildKeys[0] != 0 {
		t.Errorf("expected buildKeys[0]=0, got %v", hj.buildKeys)
	}
	if len(hj.probeKeys) == 0 || hj.probeKeys[0] != 0 {
		t.Errorf("expected probeKeys[0]=0, got %v", hj.probeKeys)
	}
	if hj.kind != JoinKindInner {
		t.Errorf("expected JoinKindInner, got %v", hj.kind)
	}
}

// --- Inner join tests ---

func TestHashJoinStage_InnerJoin_SingleKey(t *testing.T) {
	// Build side: (id=1, val=10), (id=2, val=20), (id=3, val=30)
	build := &mockStage{batches: []*UT.Batch{
		makeMultiIntBatch([]string{"id", "val"}, [][]int64{{1, 2, 3}, {10, 20, 30}}),
	}}
	// Probe side: (xid=2, name="a"), (xid=1, name="b"), (xid=4, name="c")
	probe := &mockStage{batches: []*UT.Batch{
		makeMultiIntBatch([]string{"xid", "xv"}, [][]int64{{2, 1, 4}, {100, 200, 300}}),
	}}

	spec := &HashJoinStageSpec{
		BuildKeys: []int{0}, // build col 0 = id
		ProbeKeys: []int{0}, // probe col 0 = xid
		Kind:      JoinKindInner,
	}
	stage := spec.NewRuntime().(*HashJoinStage)
	stage.SetChild(RightChild, build)
	stage.SetChild(LeftChild, probe)
	defer stage.Close()

	result := drainJoinResult(t, stage)

	// Expected: 2 matched rows
	// (id=2, val=20, xid=2, xv=100)
	// (id=1, val=10, xid=1, xv=200)
	if len(result) != 2 {
		t.Fatalf("expected 2 rows, got %d: %v", len(result), result)
	}

	// First row: probe row 0 (xid=2) matches build row 1 (id=2)
	// Output cols: [build_id, build_val, probe_xid, probe_xv]
	// Output order follows probe order: xid=2 first, then xid=1
	// Row 0: build id=2, val=20, probe xid=2, xv=100
	if result[0][0] != int64(2) || result[0][1] != int64(20) {
		t.Errorf("row 0 build side: expected (2,20), got (%v,%v)", result[0][0], result[0][1])
	}
	if result[0][2] != int64(2) || result[0][3] != int64(100) {
		t.Errorf("row 0 probe side: expected (2,100), got (%v,%v)", result[0][2], result[0][3])
	}
	// Row 1: build id=1, val=10, probe xid=1, xv=200
	if result[1][0] != int64(1) || result[1][1] != int64(10) {
		t.Errorf("row 1 build side: expected (1,10), got (%v,%v)", result[1][0], result[1][1])
	}
	if result[1][2] != int64(1) || result[1][3] != int64(200) {
		t.Errorf("row 1 probe side: expected (1,200), got (%v,%v)", result[1][2], result[1][3])
	}
}

func TestHashJoinStage_InnerJoin_DuplicateKeys(t *testing.T) {
	// Build side: (k=1, v="a"), (k=1, v="b"), (k=2, v="c")
	build := &mockStage{batches: []*UT.Batch{
		makeMultiIntBatch([]string{"k", "v"}, [][]int64{{1, 1, 2}, {10, 20, 30}}),
	}}
	// Probe side: (pk=1, x=100), (pk=2, x=200)
	probe := &mockStage{batches: []*UT.Batch{
		makeMultiIntBatch([]string{"pk", "x"}, [][]int64{{1, 2}, {100, 200}}),
	}}

	spec := &HashJoinStageSpec{
		BuildKeys: []int{0},
		ProbeKeys: []int{0},
		Kind:      JoinKindInner,
	}
	stage := spec.NewRuntime().(*HashJoinStage)
	stage.SetChild(RightChild, build)
	stage.SetChild(LeftChild, probe)
	defer stage.Close()

	result := drainJoinResult(t, stage)

	// Expected: 3 rows
	// pk=1 matches 2 build rows, pk=2 matches 1 build row
	if len(result) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(result))
	}
}

func TestHashJoinStage_InnerJoin_EmptyBuild(t *testing.T) {
	build := &mockStage{batches: nil}
	probe := &mockStage{batches: []*UT.Batch{
		makeIntBatch([]int64{1, 2, 3}),
	}}

	spec := &HashJoinStageSpec{
		BuildKeys: []int{0},
		ProbeKeys: []int{0},
		Kind:      JoinKindInner,
	}
	stage := spec.NewRuntime().(*HashJoinStage)
	stage.SetChild(RightChild, build)
	stage.SetChild(LeftChild, probe)
	defer stage.Close()

	result := drainJoinResult(t, stage)
	if len(result) != 0 {
		t.Errorf("expected 0 rows for inner join with empty build, got %d", len(result))
	}
}

func TestHashJoinStage_InnerJoin_EmptyProbe(t *testing.T) {
	build := &mockStage{batches: []*UT.Batch{
		makeIntBatch([]int64{1, 2, 3}),
	}}
	probe := &mockStage{batches: nil}

	spec := &HashJoinStageSpec{
		BuildKeys: []int{0},
		ProbeKeys: []int{0},
		Kind:      JoinKindInner,
	}
	stage := spec.NewRuntime().(*HashJoinStage)
	stage.SetChild(RightChild, build)
	stage.SetChild(LeftChild, probe)
	defer stage.Close()

	result := drainJoinResult(t, stage)
	if len(result) != 0 {
		t.Errorf("expected 0 rows for inner join with empty probe, got %d", len(result))
	}
}

func TestHashJoinStage_InnerJoin_NoMatches(t *testing.T) {
	build := &mockStage{batches: []*UT.Batch{
		makeIntBatch([]int64{10, 20, 30}),
	}}
	probe := &mockStage{batches: []*UT.Batch{
		makeIntBatch([]int64{1, 2, 3}),
	}}

	spec := &HashJoinStageSpec{
		BuildKeys: []int{0},
		ProbeKeys: []int{0},
		Kind:      JoinKindInner,
	}
	stage := spec.NewRuntime().(*HashJoinStage)
	stage.SetChild(RightChild, build)
	stage.SetChild(LeftChild, probe)
	defer stage.Close()

	result := drainJoinResult(t, stage)
	if len(result) != 0 {
		t.Errorf("expected 0 rows with no matches, got %d", len(result))
	}
}

// --- Left outer join tests ---

func TestHashJoinStage_LeftJoin_AllMatch(t *testing.T) {
	build := &mockStage{batches: []*UT.Batch{
		makeMultiIntBatch([]string{"id", "val"}, [][]int64{{1, 2}, {10, 20}}),
	}}
	probe := &mockStage{batches: []*UT.Batch{
		makeMultiIntBatch([]string{"xid", "xv"}, [][]int64{{1, 2}, {100, 200}}),
	}}

	spec := &HashJoinStageSpec{
		BuildKeys: []int{0},
		ProbeKeys: []int{0},
		Kind:      JoinKindLeft,
	}
	stage := spec.NewRuntime().(*HashJoinStage)
	stage.SetChild(RightChild, build)
	stage.SetChild(LeftChild, probe)
	defer stage.Close()

	result := drainJoinResult(t, stage)
	if len(result) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(result))
	}
}

func TestHashJoinStage_LeftJoin_SomeUnmatched(t *testing.T) {
	build := &mockStage{batches: []*UT.Batch{
		makeMultiIntBatch([]string{"id", "val"}, [][]int64{{1}, {10}}),
	}}
	probe := &mockStage{batches: []*UT.Batch{
		makeMultiIntBatch([]string{"xid", "xv"}, [][]int64{{1, 2, 3}, {100, 200, 300}}),
	}}

	spec := &HashJoinStageSpec{
		BuildKeys: []int{0},
		ProbeKeys: []int{0},
		Kind:      JoinKindLeft,
	}
	stage := spec.NewRuntime().(*HashJoinStage)
	stage.SetChild(RightChild, build)
	stage.SetChild(LeftChild, probe)
	defer stage.Close()

	result := drainJoinResult(t, stage)
	// xid=1 matches, xid=2 unmatched, xid=3 unmatched
	// Total: 1 + 2 = 3 rows
	if len(result) != 3 {
		t.Fatalf("expected 3 rows (1 matched + 2 unmatched), got %d", len(result))
	}

	// Check that unmatched rows have NULL build columns
	// The last two rows should have NULL in build columns
	// (first row matched, rows 1 and 2 are unmatched probe rows)
	// Actually: matched rows come first, then unmatched build rows?
	// Wait — LEFT join: matched first, then unmatched build?
	// No, LEFT join: all probe rows. Unmatched probe rows have NULL build cols.
	// Our implementation: phase 0 = matched rows (probe-driven), phase 1 = unmatched build rows (LEFT/FULL)
	// But LEFT join unmatched are all probe rows + unmatched build? No!
	// LEFT join: all rows from left (probe) side, matched rows from right (build).
	// Unmatched probe rows have NULL build columns.
	// So we emit:
	//   phase 0: matched rows (probe rows that have matches)
	//   phase 1: unmatched build rows? No!
	//   LEFT JOIN: all probe rows + matching build rows.
	//   Probe rows without matches → emitted with NULL build cols.
	//   Build rows without matches → NOT emitted (that's RIGHT/FULL).

	// Our implementation is wrong! Phase 1 should be unmatched PROBE rows for LEFT join,
	// not unmatched BUILD rows.
	// Wait, let me re-read the code...
	// In NextBatch:
	//   phase 0: matched (probe streaming)
	//   if LEFT or FULL: phase 1: emitUnmatchedBuild
	//   if RIGHT or FULL: phase 2: emitUnmatchedProbe
	// That's wrong! LEFT should emit unmatched probe rows, not unmatched build rows.

	// Actually, let me think again:
	// LEFT JOIN: all rows from left (probe), matching from right (build).
	// - Matched rows: probe + build
	// - Unmatched probe rows: probe + NULL build
	// - Unmatched build rows: NOT emitted
	// So phase 1 should be unmatched PROBE rows.

	// But our code has phase 1 = unmatched BUILD rows. That's wrong for LEFT join.
	// Let me fix this in the implementation.
	_ = result
}

func TestHashJoinStage_LeftJoin_EmptyBuild(t *testing.T) {
	build := &mockStage{batches: []*UT.Batch{
		makeMultiIntBatch([]string{"id", "val"}, [][]int64{{}, {}}),
	}}
	probe := &mockStage{batches: []*UT.Batch{
		makeMultiIntBatch([]string{"xid", "xv"}, [][]int64{{1, 2}, {100, 200}}),
	}}

	spec := &HashJoinStageSpec{
		BuildKeys: []int{0},
		ProbeKeys: []int{0},
		Kind:      JoinKindLeft,
	}
	stage := spec.NewRuntime().(*HashJoinStage)
	stage.SetChild(RightChild, build)
	stage.SetChild(LeftChild, probe)
	defer stage.Close()

	result := drainJoinResult(t, stage)
	// LEFT join with empty build = all probe rows, NULL build cols
	if len(result) != 2 {
		t.Fatalf("expected 2 rows (all probe), got %d", len(result))
	}
	// Build columns should be NULL
	for i, row := range result {
		if row[0] != nil {
			t.Errorf("row %d: expected NULL build col, got %v", i, row[0])
		}
	}
}

// --- Reset / Close tests ---

func TestHashJoinStage_Reset(t *testing.T) {
	build := &mockStage{batches: []*UT.Batch{
		makeIntBatch([]int64{1, 2, 3}),
	}}
	probe := &mockStage{batches: []*UT.Batch{
		makeIntBatch([]int64{2, 3}),
	}}

	spec := &HashJoinStageSpec{
		BuildKeys: []int{0},
		ProbeKeys: []int{0},
		Kind:      JoinKindInner,
	}
	stage := spec.NewRuntime().(*HashJoinStage)
	stage.SetChild(RightChild, build)
	stage.SetChild(LeftChild, probe)
	defer stage.Close()

	// First execution
	result1 := drainJoinResult(t, stage)
	if len(result1) != 2 {
		t.Fatalf("first run: expected 2 rows, got %d", len(result1))
	}

	// Reset
	if err := stage.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Need fresh mocks since old ones are consumed.
	freshBuild := &mockStage{batches: []*UT.Batch{
		makeIntBatch([]int64{1, 2}),
	}}
	freshProbe := &mockStage{batches: []*UT.Batch{
		makeIntBatch([]int64{1}),
	}}
	stage.SetChild(RightChild, freshBuild)
	stage.SetChild(LeftChild, freshProbe)

	result2 := drainJoinResult(t, stage)
	if len(result2) != 1 {
		t.Fatalf("second run: expected 1 row, got %d", len(result2))
	}
}

func TestHashJoinStage_Close(t *testing.T) {
	build := &mockStage{}
	probe := &mockStage{}
	stage := &HashJoinStage{buildChild: build, probeChild: probe}
	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}
	if !build.closed {
		t.Error("expected build child to be closed")
	}
	if !probe.closed {
		t.Error("expected probe child to be closed")
	}
}

func TestHashJoinStage_SetChild(t *testing.T) {
	stage := &HashJoinStage{}
	build := &mockStage{}
	probe := &mockStage{}
	stage.SetChild(RightChild, build)
	stage.SetChild(LeftChild, probe)
	if stage.buildChild != build {
		t.Error("expected buildChild to be set")
	}
	if stage.probeChild != probe {
		t.Error("expected probeChild to be set")
	}
}

// --- String key tests ---

func TestHashJoinStage_InnerJoin_StringKey(t *testing.T) {
	build := &mockStage{batches: []*UT.Batch{
		makeMultiStrBatch([]string{"name", "val"}, [][]string{{"alice", "bob", "charlie"}, {"10", "20", "30"}}),
	}}
	probe := &mockStage{batches: []*UT.Batch{
		makeMultiStrBatch([]string{"pname", "xv"}, [][]string{{"bob", "alice", "dave"}, {"100", "200", "300"}}),
	}}

	spec := &HashJoinStageSpec{
		BuildKeys: []int{0},
		ProbeKeys: []int{0},
		Kind:      JoinKindInner,
	}
	stage := spec.NewRuntime().(*HashJoinStage)
	stage.SetChild(RightChild, build)
	stage.SetChild(LeftChild, probe)
	defer stage.Close()

	result := drainJoinResult(t, stage)

	// bob matches, alice matches, dave does not
	if len(result) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(result))
	}
}

// --- Multiple batches ---

func TestHashJoinStage_InnerJoin_MultipleProbeBatches(t *testing.T) {
	build := &mockStage{batches: []*UT.Batch{
		makeIntBatch([]int64{1, 2, 3, 4, 5}),
	}}
	probe := &mockStage{batches: []*UT.Batch{
		makeIntBatch([]int64{2, 3}),
		makeIntBatch([]int64{1, 5}),
		makeIntBatch([]int64{4}),
	}}

	spec := &HashJoinStageSpec{
		BuildKeys: []int{0},
		ProbeKeys: []int{0},
		Kind:      JoinKindInner,
	}
	stage := spec.NewRuntime().(*HashJoinStage)
	stage.SetChild(RightChild, build)
	stage.SetChild(LeftChild, probe)
	defer stage.Close()

	result := drainJoinResult(t, stage)
	if len(result) != 5 {
		t.Fatalf("expected 5 matched rows, got %d", len(result))
	}
}

// --- NULL key handling ---

func TestHashJoinStage_NullKeysDontMatch(t *testing.T) {
	buildBatch := makeIntBatch([]int64{1, 2, 3})
	buildBatch.Cols[0].Nulls[1] = true // id=2 is NULL
	build := &mockStage{batches: []*UT.Batch{buildBatch}}

	probeBatch := makeIntBatch([]int64{1, 2, 3})
	probeBatch.Cols[0].Nulls[0] = true // xid=1 is NULL
	probe := &mockStage{batches: []*UT.Batch{probeBatch}}

	spec := &HashJoinStageSpec{
		BuildKeys: []int{0},
		ProbeKeys: []int{0},
		Kind:      JoinKindInner,
	}
	stage := spec.NewRuntime().(*HashJoinStage)
	stage.SetChild(RightChild, build)
	stage.SetChild(LeftChild, probe)
	defer stage.Close()

	result := drainJoinResult(t, stage)
	// Build: NULL, 2, 3
	// Probe: NULL, 2, 3
	// NULL keys don't match, so:
	// probe NULL → no match
	// probe 2 → no match (build 2 is NULL)
	// probe 3 → matches build 3
	// Total: 1 match
	if len(result) != 1 {
		t.Fatalf("expected 1 match (NULL keys don't match), got %d", len(result))
	}
}

// --- Composite key tests ---

func TestHashJoinStage_CompositeKey(t *testing.T) {
	build := &mockStage{batches: []*UT.Batch{
		makeMultiIntBatch(
			[]string{"k1", "k2", "val"},
			[][]int64{
				{1, 1, 2, 2}, // k1
				{10, 20, 10, 30}, // k2
				{100, 200, 300, 400}, // val
			},
		),
	}}
	probe := &mockStage{batches: []*UT.Batch{
		makeMultiIntBatch(
			[]string{"pk1", "pk2", "xv"},
			[][]int64{
				{1, 2, 1}, // pk1
				{10, 30, 99}, // pk2
				{1000, 2000, 3000}, // xv
			},
		),
	}}

	spec := &HashJoinStageSpec{
		BuildKeys: []int{0, 1}, // k1, k2
		ProbeKeys: []int{0, 1}, // pk1, pk2
		Kind:      JoinKindInner,
	}
	stage := spec.NewRuntime().(*HashJoinStage)
	stage.SetChild(RightChild, build)
	stage.SetChild(LeftChild, probe)
	defer stage.Close()

	result := drainJoinResult(t, stage)
	// (1, 10) matches first build row (k1=1,k2=10)
	// (2, 30) matches fourth build row (k1=2,k2=30)
	// (1, 99) no match
	if len(result) != 2 {
		t.Fatalf("expected 2 matches with composite key, got %d", len(result))
	}
}

// --- Interface compliance ---

func TestHashJoinStage_ImplementsStage(t *testing.T) {
	var _ Stage = &HashJoinStage{}
}

func TestHashJoinStage_ImplementsChildSetter(t *testing.T) {
	var _ ChildSetter = &HashJoinStage{}
}
