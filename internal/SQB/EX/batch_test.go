package EX

import (
	"sync"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// TestBatchPoolBasic verifies GetBatch/Put round-trip.
func TestBatchPoolBasic(t *testing.T) {
	b := GetBatch(3)
	if b == nil {
		t.Fatal("GetBatch returned nil")
	}
	if b.Pooled != true {
		t.Error("expected Pooled=true")
	}
	if b.Size != 0 {
		t.Errorf("expected Size=0, got %d", b.Size)
	}
	if b.Sel != nil {
		t.Error("expected Sel=nil")
	}
	if len(b.Cols) < 3 {
		t.Errorf("expected >= 3 Cols, got %d", len(b.Cols))
	}
	b.Put()
}

// TestBatchPoolReuse verifies that a batch returned to the pool
// can be retrieved again with reset state.
func TestBatchPoolReuse(t *testing.T) {
	b1 := GetBatch(2)
	b1.Size = 100
	b1.Sel = []uint16{0, 1, 2}
	b1.Cols[0].Data = ColumnData{Ints: []int64{1, 2, 3}}
	b1.Put()

	b2 := GetBatch(2)
	if b2.Size != 0 {
		t.Errorf("reused batch: expected Size=0, got %d", b2.Size)
	}
	if b2.Sel != nil {
		t.Error("reused batch: expected Sel=nil")
	}
	if b2.Cols[0].Data.Ints != nil || b2.Cols[0].Data.Floats != nil || b2.Cols[0].Data.Strs != nil || b2.Cols[0].Data.Bools != nil {
		t.Error("reused batch: expected Cols[0].Data to be zero")
	}
	b2.Put()
}

// TestBatchPoolConcurrent verifies pool safety under concurrency.
func TestBatchPoolConcurrent(t *testing.T) {
	const n = 100
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b := GetBatch(2)
			b.Size = 10
			b.Put()
		}()
	}
	wg.Wait()
}

// TestBatchOversized verifies that batches exceeding MaxColumns
// are not pooled (direct allocation).
func TestBatchOversized(t *testing.T) {
	b := GetBatch(MaxColumns + 10)
	if b.Pooled {
		t.Error("oversized batch should not be Pooled")
	}
	if len(b.Cols) < MaxColumns+10 {
		t.Errorf("expected %d Cols, got %d", MaxColumns+10, len(b.Cols))
	}
	// Put should be a no-op
	b.Put()
}

// TestBatchAppendRow verifies typed append operations.
func TestBatchAppendRow(t *testing.T) {
	b := GetBatch(3)
	defer b.Put()

	b.AppendRow(0, LX.T_INT_KW, int64(42), false)
	b.AppendRow(1, LX.T_TEXT, "hello", false)
	b.AppendRow(2, LX.T_BOOL, true, false)
	b.AdvanceSize()

	if b.Size != 1 {
		t.Errorf("expected Size=1, got %d", b.Size)
	}
	col0 := b.Cols[0].Data.Ints
	if col0[0] != 42 {
		t.Errorf("col[0][0] = %d; want 42", col0[0])
	}
	col1 := b.Cols[1].Data.Strs
	if col1[0] != "hello" {
		t.Errorf("col[1][0] = %q; want hello", col1[0])
	}
	col2 := b.Cols[2].Data.Bools
	if !col2[0] {
		t.Error("col[2][0] = false; want true")
	}
}

// TestBatchAppendNull verifies null bitmap handling.
func TestBatchAppendNull(t *testing.T) {
	b := GetBatch(1)
	defer b.Put()

	b.AppendRow(0, LX.T_INT_KW, int64(0), true)
	b.AdvanceSize()

	if !b.Cols[0].Nulls[0] {
		t.Error("expected Nulls[0]=true")
	}
}

// TestBatchIsFull verifies the full batch detection.
func TestBatchIsFull(t *testing.T) {
	b := GetBatch(1)
	defer b.Put()

	if b.IsFull() {
		t.Error("empty batch should not be full")
	}

	for i := 0; i < BatchSize; i++ {
		b.AppendRow(0, LX.T_INT_KW, int64(i), false)
		b.AdvanceSize()
	}

	if !b.IsFull() {
		t.Error("batch at capacity should be full")
	}
}

// TestBatchLogicalSize verifies the selection vector accounting.
func TestBatchLogicalSize(t *testing.T) {
	b := GetBatch(1)
	defer b.Put()
	b.Size = 10
	b.Sel = []uint16{0, 5, 9}
	if got := b.LogicalSize(); got != 3 {
		t.Errorf("LogicalSize with Sel = %d; want 3", got)
	}
	b.Sel = nil
	if got := b.LogicalSize(); got != 10 {
		t.Errorf("LogicalSize without Sel = %d; want 10", got)
	}
}

// BenchmarkBatchPool measures pool allocation cost.
func BenchmarkBatchPool(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		batch := GetBatch(3)
		batch.Put()
	}
}

// TestSelRange_RoundTrip verifies selToRanges followed by
// rangesToSel recovers the original selection vector exactly.
// Covers: empty input, single element, full contiguous run,
// runs with gaps, out-of-order (defensive — current callers
// produce sorted sel, but the helpers must not corrupt data).
func TestSelRange_RoundTrip(t *testing.T) {
	cases := []struct {
		name string
		sel  []uint16
	}{
		{"empty", nil},
		{"single", []uint16{7}},
		{"contiguous_full", []uint16{0, 1, 2, 3, 4, 5}},
		{"two_runs", []uint16{0, 1, 2, 10, 11, 12}},
		{"many_runs", []uint16{0, 2, 4, 6, 8, 10}},
		{"wrap_around_end", []uint16{65530, 65531, 65532, 65533, 65534, 65535}},
		{"single_elem_runs", []uint16{1, 3, 5, 7}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ranges := selToRanges(tc.sel)
			got := rangesToSel(ranges)
			if !uint16SlicesEqual(got, tc.sel) {
				t.Errorf("round-trip mismatch:\n got  %v\n want %v", got, tc.sel)
			}
		})
	}
}

// TestSelToRanges_Shape verifies the exact range layout for
// representative inputs. Pins the contiguity contract: a gap
// of >1 between consecutive indices MUST split into a new range;
// equal-or-greater-by-1 must NOT split.
func TestSelToRanges_Shape(t *testing.T) {
	cases := []struct {
		name   string
		sel    []uint16
		expect []SelRange
	}{
		{
			name:   "empty",
			sel:    nil,
			expect: nil,
		},
		{
			name:   "single",
			sel:    []uint16{3},
			expect: []SelRange{{Start: 3, End: 3}},
		},
		{
			name:   "full_run",
			sel:    []uint16{0, 1, 2, 3, 4},
			expect: []SelRange{{Start: 0, End: 4}},
		},
		{
			name:   "gap_of_two",
			sel:    []uint16{0, 1, 4, 5},
			expect: []SelRange{{Start: 0, End: 1}, {Start: 4, End: 5}},
		},
		{
			name:   "no_runs",
			sel:    []uint16{1, 3, 5, 7},
			expect: []SelRange{{Start: 1, End: 1}, {Start: 3, End: 3}, {Start: 5, End: 5}, {Start: 7, End: 7}},
		},
		{
			name:   "max_uint16_boundary",
			sel:    []uint16{65534, 65535},
			expect: []SelRange{{Start: 65534, End: 65535}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := selToRanges(tc.sel)
			if !selRangeSlicesEqual(got, tc.expect) {
				t.Errorf("selToRanges(%v):\n got  %v\n want %v", tc.sel, got, tc.expect)
			}
		})
	}
}

// TestRangesToSel_Empty verifies that the empty-input contract
// returns a nil slice (not an empty non-nil slice) so callers
// can use the result interchangeably with a zero-value Sel.
func TestRangesToSel_Empty(t *testing.T) {
	if got := rangesToSel(nil); got != nil {
		t.Errorf("rangesToSel(nil) = %v; want nil", got)
	}
	if got := rangesToSel([]SelRange{}); got != nil {
		t.Errorf("rangesToSel([]) = %v; want nil", got)
	}
}

// TestSelRange_Aliasing verifies that selToRanges returns a
// freshly-allocated slice that does not alias the input. Mutating
// the result must not change the input and vice versa.
func TestSelRange_Aliasing(t *testing.T) {
	sel := []uint16{0, 1, 2, 5, 6}
	ranges := selToRanges(sel)
	ranges[0].Start = 99
	if sel[0] == 99 {
		t.Error("selToRanges aliased input: mutating output changed input")
	}
	sel[0] = 42
	if ranges[0].Start == 42 {
		t.Error("ranges aliased input: mutating input changed output")
	}
}

func uint16SlicesEqual(a, b []uint16) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func selRangeSlicesEqual(a, b []SelRange) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// BenchmarkBatchPoolParallel measures pool allocation under concurrency.
func BenchmarkBatchPoolParallel(b *testing.B) {
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			batch := GetBatch(3)
			batch.Put()
		}
	})
}
