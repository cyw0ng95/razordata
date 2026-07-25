package OP

import (
	"context"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// makePushTestBatch constructs a single-column batch with n int rows
// x = [1, 2, ..., n] for use in push pipeline tests.
func makePushTestBatch(n int) *UT.Batch {
	b := UT.GetBatch(1)
	b.SetColumnName(0, "x")
	b.Cols[0].Type = LX.T_INT_KW
	b.Cols[0].Data.Ints = make([]int64, n)
	for i := 0; i < n; i++ {
		b.Cols[0].Data.Ints[i] = int64(i + 1)
	}
	b.Size = n
	b.SetColMap(map[string]int{"x": 0})
	return b
}

// TestPushFilter_Passthrough verifies that a filter whose predicate
// matches all rows forwards the batch unchanged. REQ002002.
func TestPushFilter_Passthrough(t *testing.T) {
	sink := NewCollectingSink()
	defer sink.Close()
	src := &fakeBatchProducer{batches: []*UT.Batch{makePushTestBatch(3)}}
	f := NewPushFilter(&PS.BinaryExpr{
		Left:  &PS.Ident{Name: "x"},
		Right: &PS.NumberLiteral{Val: 0},
		Op:    LX.T_GT,
	})
	f.SetSource(src)
	f.SetSink(sink)
	if err := f.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	batch, err := sink.NextBatch(context.Background())
	if err != nil {
		t.Fatalf("NextBatch: %v", err)
	}
	if batch == nil {
		t.Fatal("expected batch, got nil")
	}
	if batch.Size != 3 {
		t.Fatalf("Size: got %d, want 3", batch.Size)
	}
	batch.Put()
	// EOF
	if b, _ := sink.NextBatch(context.Background()); b != nil {
		t.Fatal("expected EOF, got batch")
	}
}

// TestPushFilter_DropsNonMatching verifies that a filter whose
// predicate matches no rows drops the batch and pushes nothing to
// the sink. REQ002002.
func TestPushFilter_DropsNonMatching(t *testing.T) {
	sink := NewCollectingSink()
	defer sink.Close()
	src := &fakeBatchProducer{batches: []*UT.Batch{makePushTestBatch(3)}}
	f := NewPushFilter(&PS.BinaryExpr{
		Left:  &PS.Ident{Name: "x"},
		Right: &PS.NumberLiteral{Val: 100},
		Op:    LX.T_GT,
	})
	f.SetSource(src)
	f.SetSink(sink)
	if err := f.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Sink should be empty (no batches pushed).
	batch, _ := sink.NextBatch(context.Background())
	if batch != nil {
		t.Fatalf("expected nil batch, got %+v", batch)
		batch.Put()
	}
}

// TestPushFilter_PartialMatch verifies that a filter with a partial
// match forwards the batch with a selection vector. REQ002002.
func TestPushFilter_PartialMatch(t *testing.T) {
	sink := NewCollectingSink()
	defer sink.Close()
	// x = [1, 2, 3, 4, 5]; predicate x > 2 selects [3, 4, 5].
	src := &fakeBatchProducer{batches: []*UT.Batch{makePushTestBatch(5)}}
	f := NewPushFilter(&PS.BinaryExpr{
		Left:  &PS.Ident{Name: "x"},
		Right: &PS.NumberLiteral{Val: 2},
		Op:    LX.T_GT,
	})
	f.SetSource(src)
	f.SetSink(sink)
	if err := f.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	batch, _ := sink.NextBatch(context.Background())
	if batch == nil {
		t.Fatal("expected batch, got nil")
	}
	defer batch.Put()
	if batch.Size != 3 {
		t.Fatalf("Size: got %d, want 3", batch.Size)
	}
	if batch.Sel == nil {
		t.Fatal("expected non-nil Sel for partial match")
	}
	// Verify selected rows are 3, 4, 5 (indices 2, 3, 4).
	want := []int64{3, 4, 5}
	for i, idx := range batch.Sel {
		got := batch.Cols[0].Data.Ints[idx]
		if got != want[i] {
			t.Errorf("Sel[%d]: got %d, want %d", i, got, want[i])
		}
	}
}

// TestPushLimit_Truncates verifies that PushLimit truncates an
// incoming batch to the limit and signals stop. REQ002002.
func TestPushLimit_Truncates(t *testing.T) {
	sink := NewCollectingSink()
	defer sink.Close()
	src := &fakeBatchProducer{batches: []*UT.Batch{makePushTestBatch(10)}}
	l := NewPushLimit(3)
	l.SetSource(src)
	l.SetSink(sink)
	if err := l.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	batch, _ := sink.NextBatch(context.Background())
	if batch == nil {
		t.Fatal("expected batch, got nil")
	}
	defer batch.Put()
	if batch.Size != 3 {
		t.Fatalf("Size: got %d, want 3 (truncated to limit)", batch.Size)
	}
	// EOF after the truncated batch.
	if b, _ := sink.NextBatch(context.Background()); b != nil {
		t.Fatal("expected EOF after limit reached")
		b.Put()
	}
}

// TestPushLimit_PassthroughUnlimited verifies that a negative limit
// passes all batches through unchanged. REQ002002.
func TestPushLimit_PassthroughUnlimited(t *testing.T) {
	sink := NewCollectingSink()
	defer sink.Close()
	src := &fakeBatchProducer{batches: []*UT.Batch{
		makePushTestBatch(3),
		makePushTestBatch(2),
	}}
	l := NewPushLimit(-1)
	l.SetSource(src)
	l.SetSink(sink)
	if err := l.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Both batches should be buffered.
	b1, _ := sink.NextBatch(context.Background())
	if b1 == nil {
		t.Fatal("expected first batch")
	}
	if b1.Size != 3 {
		t.Fatalf("first batch Size: got %d, want 3", b1.Size)
	}
	b1.Put()
	b2, _ := sink.NextBatch(context.Background())
	if b2 == nil {
		t.Fatal("expected second batch")
	}
	if b2.Size != 2 {
		t.Fatalf("second batch Size: got %d, want 2", b2.Size)
	}
	b2.Put()
	if b, _ := sink.NextBatch(context.Background()); b != nil {
		t.Fatal("expected EOF after both batches")
		b.Put()
	}
}

// TestPushPipeline_EndToEnd verifies that a Filter→Project→Limit
// pipeline produces the same rows as the equivalent pull-based chain.
// REQ002002.
func TestPushPipeline_EndToEnd(t *testing.T) {
	// Source: x = [1..10].
	src := &fakeBatchProducer{batches: []*UT.Batch{makePushTestBatch(10)}}

	// Filter: x > 3 (selects [4, 5, 6, 7, 8, 9, 10] = 7 rows).
	f := NewPushFilter(&PS.BinaryExpr{
		Left:  &PS.Ident{Name: "x"},
		Right: &PS.NumberLiteral{Val: 3},
		Op:    LX.T_GT,
	})
	// Project: SELECT x (passthrough column ref).
	p := NewPushProject(
		[]PS.Expr{&PS.Ident{Name: "x"}},
		[]string{"x"},
	)
	// Limit: 3 rows.
	l := NewPushLimit(3)

	pipeline := NewPushPipeline(src, f, p, l)
	defer pipeline.Close()
	if err := pipeline.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Drain sink.
	var got []int64
	for {
		batch, _ := pipeline.Sink().NextBatch(context.Background())
		if batch == nil {
			break
		}
		for i := 0; i < batch.LogicalSize(); i++ {
			phys := i
			if batch.Sel != nil {
				phys = int(batch.Sel[i])
			}
			if phys < len(batch.Cols[0].Data.Ints) {
				got = append(got, batch.Cols[0].Data.Ints[phys])
			}
		}
		batch.Put()
	}
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

// TestPushToPullAdapter_BatchProducer verifies that the adapter
// satisfies BatchProducer and yields batches one per NextBatch call.
// REQ002002.
func TestPushToPullAdapter_BatchProducer(t *testing.T) {
	src := &fakeBatchProducer{batches: []*UT.Batch{
		makePushTestBatch(3),
		makePushTestBatch(2),
	}}
	pipeline := NewPushPipeline(src) // no ops — direct source→sink
	adapter := NewPushToPullAdapter(pipeline)
	defer adapter.Close()

	// First call runs the pipeline and returns the first batch.
	b1, err := adapter.NextBatch(context.Background())
	if err != nil {
		t.Fatalf("NextBatch 1: %v", err)
	}
	if b1 == nil || b1.Size != 3 {
		t.Fatalf("batch 1: got %+v, want Size=3", b1)
	}
	b1.Put()
	b2, err := adapter.NextBatch(context.Background())
	if err != nil {
		t.Fatalf("NextBatch 2: %v", err)
	}
	if b2 == nil || b2.Size != 2 {
		t.Fatalf("batch 2: got %+v, want Size=2", b2)
	}
	b2.Put()
	// EOF.
	b3, err := adapter.NextBatch(context.Background())
	if err != nil {
		t.Fatalf("NextBatch 3: %v", err)
	}
	if b3 != nil {
		t.Fatal("expected EOF (nil), got batch")
	}
}

// TestPushPipeline_StopOnLimit verifies that when PushLimit signals
// stop, the source is not drained past the batch that satisfied the
// limit. REQ002002.
func TestPushPipeline_StopOnLimit(t *testing.T) {
	// Source: 3 batches of 10 rows each.
	src := &fakeBatchProducer{batches: []*UT.Batch{
		makePushTestBatch(10),
		makePushTestBatch(10),
		makePushTestBatch(10),
	}}
	l := NewPushLimit(5)
	pipeline := NewPushPipeline(src, l)
	defer pipeline.Close()
	if err := pipeline.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Only the first batch should be buffered (truncated to 5).
	b1, _ := pipeline.Sink().NextBatch(context.Background())
	if b1 == nil {
		t.Fatal("expected first batch")
	}
	if b1.Size != 5 {
		t.Fatalf("Size: got %d, want 5", b1.Size)
	}
	b1.Put()
	if b, _ := pipeline.Sink().NextBatch(context.Background()); b != nil {
		t.Fatal("expected EOF after limit satisfied; source was over-drained")
		b.Put()
	}
	// Verify the source still has 2 batches unread.
	if src.idx != 1 {
		t.Errorf("source over-drained: idx=%d, want 1", src.idx)
	}
}
