package PX

import (
	"bytes"
	"context"
	"testing"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

// collationMockPlanner implements only LookupCollation; other QueryPlanner
// methods are left as nil (panic if invoked). REQ002163 test support.
type collationMockPlanner struct {
	pl.QueryPlanner
	colls map[string]pl.CollateFunc
}

func (m *collationMockPlanner) LookupCollation(name string) pl.CollateFunc {
	return m.colls[name]
}

// nocaseCollation compares byte slices case-insensitively.
func nocaseCollation(a, b []byte) int {
	ua := bytes.ToUpper(a)
	ub := bytes.ToUpper(b)
	return bytes.Compare(ua, ub)
}

func TestSortStageSpec_Category(t *testing.T) {
	spec := &SortStageSpec{SortCols: []int{0}, Desc: []bool{false}}
	if spec.Category() != CatMapReduce {
		t.Errorf("expected CatMapReduce, got %v", spec.Category())
	}
}

func TestSortStage_Ascending(t *testing.T) {
	child := &mockStage{batches: []*UT.Batch{
		makeIntBatch([]int64{5, 1, 3, 2, 4}),
	}}
	stage := &SortStage{
		child:     child,
		sortCols:  []int{0},
		desc:      []bool{false},
		batchSize: 1024,
	}
	defer stage.Close()

	batch, err := stage.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected non-nil batch")
	}
	if batch.Size != 5 {
		t.Fatalf("expected 5 rows, got %d", batch.Size)
	}
	expected := []int64{1, 2, 3, 4, 5}
	for i, v := range expected {
		if batch.Cols[0].Data.Ints[i] != v {
			t.Fatalf("row %d: expected %d, got %d", i, v, batch.Cols[0].Data.Ints[i])
		}
	}
}

func TestSortStage_Descending(t *testing.T) {
	child := &mockStage{batches: []*UT.Batch{
		makeIntBatch([]int64{1, 3, 2}),
	}}
	stage := &SortStage{
		child:     child,
		sortCols:  []int{0},
		desc:      []bool{true},
		batchSize: 1024,
	}
	defer stage.Close()

	batch, err := stage.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected non-nil batch")
	}
	expected := []int64{3, 2, 1}
	for i, v := range expected {
		if batch.Cols[0].Data.Ints[i] != v {
			t.Fatalf("row %d: expected %d, got %d", i, v, batch.Cols[0].Data.Ints[i])
		}
	}
}

func TestSortStage_MultipleBatches(t *testing.T) {
	child := &mockStage{batches: []*UT.Batch{
		makeIntBatch([]int64{5, 1}),
		makeIntBatch([]int64{3, 2, 4}),
	}}
	stage := &SortStage{
		child:     child,
		sortCols:  []int{0},
		desc:      []bool{false},
		batchSize: 3, // small batch size to produce multiple output batches
	}
	defer stage.Close()

	// First batch: [1, 2, 3]
	batch, err := stage.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil || batch.Size != 3 {
		t.Fatalf("expected 3 rows, got %d", batch.Size)
	}
	if batch.Cols[0].Data.Ints[0] != 1 || batch.Cols[0].Data.Ints[2] != 3 {
		t.Fatalf("expected [1,2,3], got %v", batch.Cols[0].Data.Ints[:3])
	}

	// Second batch: [4, 5]
	batch, err = stage.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil || batch.Size != 2 {
		t.Fatalf("expected 2 rows, got %d", batch.Size)
	}
	if batch.Cols[0].Data.Ints[0] != 4 || batch.Cols[0].Data.Ints[1] != 5 {
		t.Fatalf("expected [4,5], got %v", batch.Cols[0].Data.Ints[:2])
	}

	// EOF
	batch, err = stage.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch != nil {
		t.Fatal("expected nil at EOF")
	}
}

func TestSortStage_EmptyInput(t *testing.T) {
	child := &mockStage{batches: nil}
	stage := &SortStage{
		child:     child,
		sortCols:  []int{0},
		desc:      []bool{false},
		batchSize: 1024,
	}
	defer stage.Close()

	batch, err := stage.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch != nil {
		t.Fatal("expected nil batch for empty input")
	}
}

func TestSortStage_Reset(t *testing.T) {
	child := &mockStage{batches: []*UT.Batch{
		makeIntBatch([]int64{3, 1, 2}),
	}}
	stage := &SortStage{
		child:     child,
		sortCols:  []int{0},
		desc:      []bool{false},
		batchSize: 1024,
	}
	defer stage.Close()

	batch, _ := stage.NextBatch(context.Background())
	if batch == nil || batch.Cols[0].Data.Ints[0] != 1 {
		t.Fatal("first execution failed")
	}

	if err := stage.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Reset clears the drained flag and result, but the child is consumed.
	// Verify that the state is cleared correctly.
	if stage.drained {
		t.Fatal("expected drained=false after reset")
	}
	if stage.result != nil {
		t.Fatal("expected result=nil after reset")
	}
	if stage.pos != 0 {
		t.Fatalf("expected pos=0 after reset, got %d", stage.pos)
	}
}

func TestSortStage_SetChild(t *testing.T) {
	stage := &SortStage{}
	child := &mockStage{}
	stage.SetChild(SingleChild, child)
	if stage.child != child {
		t.Fatal("expected child to be set")
	}
}

func TestSortStage_Close(t *testing.T) {
	child := &mockStage{}
	stage := &SortStage{child: child}
	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}
	if !child.closed {
		t.Fatal("expected child to be closed")
	}
}

func TestSortStageSpec_NewRuntime(t *testing.T) {
	spec := &SortStageSpec{SortCols: []int{0}, Desc: []bool{false}, BatchSize: 512}
	stage := spec.NewRuntime().(*SortStage)
	if stage.batchSize != 512 {
		t.Fatalf("expected batchSize=512, got %d", stage.batchSize)
	}
}

func TestSortStageSpec_NewRuntime_DefaultBatchSize(t *testing.T) {
	spec := &SortStageSpec{SortCols: []int{0}, Desc: []bool{false}}
	stage := spec.NewRuntime().(*SortStage)
	if stage.batchSize != UT.BatchSize {
		t.Fatalf("expected batchSize=%d, got %d", UT.BatchSize, stage.batchSize)
	}
}

// TestSortStage_Collation verifies the native SortStage applies a
// registered COLLATE function for text keys (REQ002163). ASC + COLLATE
// nocase must yield case-insensitive order, with ties broken by the
// collation function's own rule (bytes.ToUpper then bytes.Compare).
func TestSortStage_Collation(t *testing.T) {
	child := &mockStage{batches: []*UT.Batch{
		makeStrBatch("x", []string{"BANANA", "apple", "Cherry", "Apple"}),
	}}
	stage := &SortStage{
		child:      child,
		sortCols:   []int{0},
		desc:       []bool{false},
		collations: []string{"nocase"},
		batchSize:  1024,
	}
	stage.PropagatePlanner(&collationMockPlanner{
		colls: map[string]pl.CollateFunc{"nocase": nocaseCollation},
	})
	defer stage.Close()

	batch, err := stage.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected non-nil batch")
	}
	if batch.Size != 4 {
		t.Fatalf("expected 4 rows, got %d", batch.Size)
	}
	got := batch.Cols[0].Data.Strs[:batch.Size]
	// Case-insensitive ascending: apple/Apple tie (both uppercase to
	// "APPLE", bytes.Compare returns 0), then BANANA, then Cherry. A
	// stable sort preserves the original input order for ties — "apple"
	// (input index 1) precedes "Apple" (input index 3). This matches the
	// EX-level TestRegisterCollation_CollateIndexOrderBy expectation.
	expected := []string{"apple", "Apple", "BANANA", "Cherry"}
	for i, want := range expected {
		if got[i] != want {
			t.Errorf("row %d: expected %q, got %q", i, want, got[i])
		}
	}
}

// TestSortStage_CollationDesc verifies DESC + COLLATE inverts the
// collation-aware order (REQ002163).
func TestSortStage_CollationDesc(t *testing.T) {
	child := &mockStage{batches: []*UT.Batch{
		makeStrBatch("x", []string{"a", "B", "c", "D"}),
	}}
	stage := &SortStage{
		child:      child,
		sortCols:   []int{0},
		desc:       []bool{true},
		collations: []string{"nocase"},
		batchSize:  1024,
	}
	stage.PropagatePlanner(&collationMockPlanner{
		colls: map[string]pl.CollateFunc{"nocase": nocaseCollation},
	})
	defer stage.Close()

	batch, err := stage.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected non-nil batch")
	}
	got := batch.Cols[0].Data.Strs[:batch.Size]
	// DESC case-insensitive: D/c tie (uppercased D/C, D>C) → D, c;
	// then B/a tie → B, a. Expected: D, c, B, a.
	expected := []string{"D", "c", "B", "a"}
	for i, want := range expected {
		if got[i] != want {
			t.Errorf("row %d: expected %q, got %q", i, want, got[i])
		}
	}
}

// TestSortStage_CollationUnregisteredFallBack verifies that a COLLATE
// name with no registered function falls back to binary comparison
// (REQ002163).
func TestSortStage_CollationUnregisteredFallBack(t *testing.T) {
	child := &mockStage{batches: []*UT.Batch{
		makeStrBatch("x", []string{"z", "a", "m"}),
	}}
	stage := &SortStage{
		child:      child,
		sortCols:   []int{0},
		desc:       []bool{false},
		collations: []string{"nonexistent"},
		batchSize:  1024,
	}
	// Planner with an empty collation map → LookupCollation returns nil.
	stage.PropagatePlanner(&collationMockPlanner{colls: map[string]pl.CollateFunc{}})
	defer stage.Close()

	batch, err := stage.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected non-nil batch")
	}
	got := batch.Cols[0].Data.Strs[:batch.Size]
	expected := []string{"a", "m", "z"}
	for i, want := range expected {
		if got[i] != want {
			t.Errorf("row %d: expected %q, got %q", i, want, got[i])
		}
	}
}

// TestSortStage_NullsOrderFirst verifies NULLS FIRST ordering (REQ002163).
func TestSortStage_NullsOrderFirst(t *testing.T) {
	b := makeStrBatch("x", []string{"b", "a"})
	// Mark row 0 as NULL.
	b.Cols[0].Nulls[0] = true
	child := &mockStage{batches: []*UT.Batch{b}}
	stage := &SortStage{
		child:      child,
		sortCols:   []int{0},
		desc:       []bool{false},
		nullsOrder: []int8{1}, // NULLS FIRST
		batchSize:  1024,
	}
	defer stage.Close()

	batch, err := stage.NextBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("expected non-nil batch")
	}
	// NULL first, then "a".
	if !batch.Cols[0].Nulls[0] {
		t.Error("expected row 0 to be NULL (NULLS FIRST)")
	}
	if batch.Cols[0].Data.Strs[1] != "a" {
		t.Errorf("expected row 1 = %q, got %q", "a", batch.Cols[0].Data.Strs[1])
	}
}
