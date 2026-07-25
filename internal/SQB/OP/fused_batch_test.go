package OP

import (
	"context"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// makeFusedBatch constructs a single-column batch with n int rows
// x = [1, 2, ..., n] for FusedBatchScan tests.
func makeFusedBatch(n int) *UT.Batch {
	return makeFusedBatchRange(1, n)
}

// makeFusedBatchRange constructs a single-column batch with n int rows
// x = [start, start+1, ..., start+n-1].
func makeFusedBatchRange(start, n int) *UT.Batch {
	b := UT.GetBatch(1)
	b.SetColumnName(0, "x")
	b.Cols[0].Type = LX.T_INT_KW
	b.Cols[0].Data.Ints = make([]int64, n)
	for i := 0; i < n; i++ {
		b.Cols[0].Data.Ints[i] = int64(start + i)
	}
	b.Size = n
	b.SetColMap(map[string]int{"x": 0})
	return b
}

// drainFused collects all logical rows from a FusedBatchScan into a
// slice of int64 values from column 0. Each batch's selected rows
// (via Sel or dense) are appended in order.
func drainFused(t *testing.T, f *FusedBatchScan) []int64 {
	t.Helper()
	var got []int64
	for {
		batch, err := f.NextBatch(context.Background())
		if err != nil {
			t.Fatalf("NextBatch: %v", err)
		}
		if batch == nil {
			break
		}
		n := batch.LogicalSize()
		for i := 0; i < n; i++ {
			phys := i
			if batch.Sel != nil && i < len(batch.Sel) {
				phys = int(batch.Sel[i])
			}
			if phys < len(batch.Cols[0].Data.Ints) {
				got = append(got, batch.Cols[0].Data.Ints[phys])
			}
		}
		batch.Put()
	}
	return got
}

// TestFusedBatchScan_FilterPassthrough verifies that a filter whose
// predicate matches all rows forwards every row. REQ002003.
func TestFusedBatchScan_FilterPassthrough(t *testing.T) {
	src := &fakeBatchProducer{batches: []*UT.Batch{makeFusedBatch(5)}}
	f := NewFusedBatchScan(src, &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "x"},
		Right: &PS.NumberLiteral{Val: 0},
		Op:    LX.T_GT,
	}, nil, nil, -1)
	defer f.Close()

	got := drainFused(t, f)
	want := []int64{1, 2, 3, 4, 5}
	if len(got) != len(want) {
		t.Fatalf("row count: got %d, want %d (got=%v)", len(got), len(want), got)
	}
	for i, v := range want {
		if got[i] != v {
			t.Errorf("row %d: got %d, want %d", i, got[i], v)
		}
	}
}

// TestFusedBatchScan_FilterDropsAll verifies that a filter matching
// no rows produces zero output rows. REQ002003.
func TestFusedBatchScan_FilterDropsAll(t *testing.T) {
	src := &fakeBatchProducer{batches: []*UT.Batch{makeFusedBatch(5)}}
	f := NewFusedBatchScan(src, &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "x"},
		Right: &PS.NumberLiteral{Val: 100},
		Op:    LX.T_GT,
	}, nil, nil, -1)
	defer f.Close()

	got := drainFused(t, f)
	if len(got) != 0 {
		t.Fatalf("expected 0 rows, got %d (got=%v)", len(got), got)
	}
}

// TestFusedBatchScan_FilterPartialMatch verifies that a filter with
// a partial match yields only the matching rows. REQ002003.
func TestFusedBatchScan_FilterPartialMatch(t *testing.T) {
	src := &fakeBatchProducer{batches: []*UT.Batch{makeFusedBatch(10)}}
	f := NewFusedBatchScan(src, &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "x"},
		Right: &PS.NumberLiteral{Val: 7},
		Op:    LX.T_GT,
	}, nil, nil, -1)
	defer f.Close()

	got := drainFused(t, f)
	want := []int64{8, 9, 10}
	if len(got) != len(want) {
		t.Fatalf("row count: got %d, want %d (got=%v)", len(got), len(want), got)
	}
	for i, v := range want {
		if got[i] != v {
			t.Errorf("row %d: got %d, want %d", i, got[i], v)
		}
	}
}

// TestFusedBatchScan_NoFilterPassthrough verifies that a nil predicate
// passes all rows through (SELECT * FROM t). REQ002003.
func TestFusedBatchScan_NoFilterPassthrough(t *testing.T) {
	src := &fakeBatchProducer{batches: []*UT.Batch{makeFusedBatch(5)}}
	f := NewFusedBatchScan(src, nil, nil, nil, -1)
	defer f.Close()

	got := drainFused(t, f)
	if len(got) != 5 {
		t.Fatalf("row count: got %d, want 5", len(got))
	}
}

// TestFusedBatchScan_ProjectPassthrough verifies that projecting a
// single column ref produces the expected values. REQ002003.
func TestFusedBatchScan_ProjectPassthrough(t *testing.T) {
	src := &fakeBatchProducer{batches: []*UT.Batch{makeFusedBatch(5)}}
	f := NewFusedBatchScan(src, nil,
		[]PS.Expr{&PS.Ident{Name: "x"}},
		[]string{"x"},
		-1)
	defer f.Close()

	got := drainFused(t, f)
	want := []int64{1, 2, 3, 4, 5}
	if len(got) != len(want) {
		t.Fatalf("row count: got %d, want %d", len(got), len(want))
	}
	for i, v := range want {
		if got[i] != v {
			t.Errorf("row %d: got %d, want %d", i, got[i], v)
		}
	}
}

// TestFusedBatchScan_LimitTruncates verifies that a limit truncates
// the output to the specified number of rows. REQ002003.
func TestFusedBatchScan_LimitTruncates(t *testing.T) {
	src := &fakeBatchProducer{batches: []*UT.Batch{makeFusedBatch(10)}}
	f := NewFusedBatchScan(src, nil, nil, nil, 3)
	defer f.Close()

	got := drainFused(t, f)
	want := []int64{1, 2, 3}
	if len(got) != len(want) {
		t.Fatalf("row count: got %d, want %d (got=%v)", len(got), len(want), got)
	}
	for i, v := range want {
		if got[i] != v {
			t.Errorf("row %d: got %d, want %d", i, got[i], v)
		}
	}
}

// TestFusedBatchScan_LimitSpansBatches verifies that a limit spanning
// multiple input batches accumulates correctly. REQ002003.
func TestFusedBatchScan_LimitSpansBatches(t *testing.T) {
	// Two batches of 5 rows each (x = 1..5, 6..10); limit 7 → [1..7].
	src := &fakeBatchProducer{batches: []*UT.Batch{
		makeFusedBatchRange(1, 5),
		makeFusedBatchRange(6, 5),
	}}
	f := NewFusedBatchScan(src, nil, nil, nil, 7)
	defer f.Close()

	got := drainFused(t, f)
	want := []int64{1, 2, 3, 4, 5, 6, 7}
	if len(got) != len(want) {
		t.Fatalf("row count: got %d, want %d (got=%v)", len(got), len(want), got)
	}
	for i, v := range want {
		if got[i] != v {
			t.Errorf("row %d: got %d, want %d", i, got[i], v)
		}
	}
}

// TestFusedBatchScan_FilterProjectLimitEndToEnd verifies the full
// fused pipeline: filter x > 3, project x, limit 3 → [4, 5, 6].
// REQ002003.
func TestFusedBatchScan_FilterProjectLimitEndToEnd(t *testing.T) {
	src := &fakeBatchProducer{batches: []*UT.Batch{makeFusedBatch(10)}}
	f := NewFusedBatchScan(src,
		&PS.BinaryExpr{
			Left:  &PS.Ident{Name: "x"},
			Right: &PS.NumberLiteral{Val: 3},
			Op:    LX.T_GT,
		},
		[]PS.Expr{&PS.Ident{Name: "x"}},
		[]string{"x"},
		3)
	defer f.Close()

	got := drainFused(t, f)
	want := []int64{4, 5, 6}
	if len(got) != len(want) {
		t.Fatalf("row count: got %d, want %d (got=%v)", len(got), len(want), got)
	}
	for i, v := range want {
		if got[i] != v {
			t.Errorf("row %d: got %d, want %d", i, got[i], v)
		}
	}
}

// TestFusedBatchScan_EmptySource verifies that an empty source yields
// no output batches. REQ002003.
func TestFusedBatchScan_EmptySource(t *testing.T) {
	src := &fakeBatchProducer{batches: nil}
	f := NewFusedBatchScan(src, nil, nil, nil, -1)
	defer f.Close()

	got := drainFused(t, f)
	if len(got) != 0 {
		t.Fatalf("expected 0 rows for empty source, got %d", len(got))
	}
}

// TestFusedBatchScan_LimitLargerThanInput verifies that a limit larger
// than the total row count returns all rows. REQ002003.
func TestFusedBatchScan_LimitLargerThanInput(t *testing.T) {
	src := &fakeBatchProducer{batches: []*UT.Batch{makeFusedBatch(5)}}
	f := NewFusedBatchScan(src, nil, nil, nil, 100)
	defer f.Close()

	got := drainFused(t, f)
	if len(got) != 5 {
		t.Fatalf("row count: got %d, want 5", len(got))
	}
}

// TestFusedBatchScan_NegativeLimitUnlimited verifies that a negative
// limit is treated as unlimited. REQ002003.
func TestFusedBatchScan_NegativeLimitUnlimited(t *testing.T) {
	src := &fakeBatchProducer{batches: []*UT.Batch{
		makeFusedBatch(3),
		makeFusedBatch(2),
	}}
	f := NewFusedBatchScan(src, nil, nil, nil, -1)
	defer f.Close()

	got := drainFused(t, f)
	if len(got) != 5 {
		t.Fatalf("row count: got %d, want 5", len(got))
	}
}

// TestFusedBatchScan_DoubleCloseIdempotent verifies that calling Close
// twice does not panic or error. REQ002003.
func TestFusedBatchScan_DoubleCloseIdempotent(t *testing.T) {
	src := &fakeBatchProducer{batches: []*UT.Batch{makeFusedBatch(3)}}
	f := NewFusedBatchScan(src, nil, nil, nil, -1)
	if err := f.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// TestFusedBatchScan_EOFAfterExhausted verifies that repeated NextBatch
// calls after EOF return (nil, nil) without error. REQ002003.
func TestFusedBatchScan_EOFAfterExhausted(t *testing.T) {
	src := &fakeBatchProducer{batches: []*UT.Batch{makeFusedBatch(3)}}
	f := NewFusedBatchScan(src, nil, nil, nil, -1)
	defer f.Close()

	// Drain all rows.
	got := drainFused(t, f)
	if len(got) != 3 {
		t.Fatalf("row count: got %d, want 3", len(got))
	}
	// Subsequent calls should return nil.
	b1, err := f.NextBatch(context.Background())
	if err != nil || b1 != nil {
		t.Fatalf("post-EOF call 1: got batch=%v err=%v, want nil/nil", b1, err)
	}
	b2, err := f.NextBatch(context.Background())
	if err != nil || b2 != nil {
		t.Fatalf("post-EOF call 2: got batch=%v err=%v, want nil/nil", b2, err)
	}
}

// TestFusedBatchScan_FilterEmptyBatchThenData verifies that when the
// filter drops an entire first batch, the operator continues to the
// next and returns its matching rows. REQ002003.
func TestFusedBatchScan_FilterEmptyBatchThenData(t *testing.T) {
	// First batch: x = [1..5], filter x > 7 → no matches.
	// Second batch: x = [6..10], filter x > 7 → [8, 9, 10].
	src := &fakeBatchProducer{batches: []*UT.Batch{
		makeFusedBatchRange(1, 5),
		makeFusedBatchRange(6, 5),
	}}
	f := NewFusedBatchScan(src, &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "x"},
		Right: &PS.NumberLiteral{Val: 7},
		Op:    LX.T_GT,
	}, nil, nil, -1)
	defer f.Close()

	got := drainFused(t, f)
	want := []int64{8, 9, 10}
	if len(got) != len(want) {
		t.Fatalf("row count: got %d, want %d (got=%v)", len(got), len(want), got)
	}
	for i, v := range want {
		if got[i] != v {
			t.Errorf("row %d: got %d, want %d", i, got[i], v)
		}
	}
}

// TestFusedBatchScan_LimitZero verifies that a zero limit produces
// no output rows. REQ002003.
func TestFusedBatchScan_LimitZero(t *testing.T) {
	src := &fakeBatchProducer{batches: []*UT.Batch{makeFusedBatch(5)}}
	f := NewFusedBatchScan(src, nil, nil, nil, 0)
	defer f.Close()

	got := drainFused(t, f)
	if len(got) != 0 {
		t.Fatalf("expected 0 rows for limit=0, got %d", len(got))
	}
}

// TestFusedBatchScan_ProjectAfterFilterCompacts verifies that when a
// filter sets a Sel vector, the projected output is densely packed
// (no gaps from the dropped rows). REQ002003.
func TestFusedBatchScan_ProjectAfterFilterCompacts(t *testing.T) {
	// x = [1..10], filter x > 7 → [8, 9, 10], project x.
	src := &fakeBatchProducer{batches: []*UT.Batch{makeFusedBatch(10)}}
	f := NewFusedBatchScan(src,
		&PS.BinaryExpr{
			Left:  &PS.Ident{Name: "x"},
			Right: &PS.NumberLiteral{Val: 7},
			Op:    LX.T_GT,
		},
		[]PS.Expr{&PS.Ident{Name: "x"}},
		[]string{"x"},
		-1)
	defer f.Close()

	batch, err := f.NextBatch(context.Background())
	if err != nil {
		t.Fatalf("NextBatch: %v", err)
	}
	if batch == nil {
		t.Fatal("expected batch, got nil")
	}
	defer batch.Put()
	if batch.Size != 3 {
		t.Fatalf("Size: got %d, want 3", batch.Size)
	}
	// Output column should be densely packed: [8, 9, 10].
	want := []int64{8, 9, 10}
	for i, v := range want {
		if i >= len(batch.Cols[0].Data.Ints) {
			t.Fatalf("column too short: len=%d", len(batch.Cols[0].Data.Ints))
		}
		if batch.Cols[0].Data.Ints[i] != v {
			t.Errorf("row %d: got %d, want %d", i, batch.Cols[0].Data.Ints[i], v)
		}
	}
}
