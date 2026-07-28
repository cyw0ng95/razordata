//go:build !slt_corpus_full

package EX

import (
	"context"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	OP "github.com/cyw0ng95/razordata/internal/SQB/OP"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// TestDrainBatch_NilRoot verifies drainBatch returns error for nil root.
func TestDrainBatch_NilRoot(t *testing.T) {
	_, err := drainBatch(context.Background(), nil, nil)
	if err == nil {
		t.Error("expected error for nil root")
	}
}

// fakeBatch is a minimal BatchProducer for unit testing.
type fakeBatch struct {
	batches []*UT.Batch
	idx     int
}

func (f *fakeBatch) NextBatch(_ context.Context) (*UT.Batch, error) {
	if f.idx >= len(f.batches) {
		return nil, nil
	}
	b := f.batches[f.idx]
	f.idx++
	return b, nil
}

// Next satisfies pl.Operator (required by drainBatch parameter type).
func (f *fakeBatch) Next(_ context.Context) (DT.Row, error) {
	return DT.Row{}, DT.ErrNoRows
}

func (f *fakeBatch) Close() error { return nil }

// makeIntBatch creates a single-column int64 batch.
func makeIntBatch(vals []int64) *UT.Batch {
	b := UT.GetBatch(1)
	b.Size = 0
	for _, v := range vals {
		b.AppendRow(0, LX.T_INT_KW, v, false)
		b.AdvanceSize()
	}
	b.Pooled = false
	return b
}

func makeMultiColBatch(col0 []int64, col1 []string) *UT.Batch {
	b := UT.GetBatch(2)
	b.Size = 0
	n := len(col0)
	if len(col1) < n {
		n = len(col1)
	}
	for i := 0; i < n; i++ {
		b.AppendRow(0, LX.T_INT_KW, col0[i], false)
		b.AppendRow(1, LX.T_TEXT, col1[i], false)
		b.AdvanceSize()
	}
	b.SetColumnName(0, "id")
	b.SetColumnName(1, "name")
	b.Pooled = false
	return b
}

// TestDrainBatch_BatchProducer_singleBatch verifies drainBatch drains
// a BatchProducer with a single batch into rows.
func TestDrainBatch_BatchProducer_singleBatch(t *testing.T) {
	batch := makeIntBatch([]int64{1, 2, 3})
	fb := &fakeBatch{batches: []*UT.Batch{batch}}

	rows, err := drainBatch(context.Background(), fb, nil)
	if err != nil {
		t.Fatalf("drainBatch: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	for i, want := range []int64{1, 2, 3} {
		if rows[i].Data[0].I64 != want {
			t.Errorf("row %d: got %d, want %d", i, rows[i].Data[0].I64, want)
		}
	}
}

// TestDrainBatch_BatchProducer_multipleBatches verifies drainBatch drains
// multiple batches from a BatchProducer.
func TestDrainBatch_BatchProducer_multipleBatches(t *testing.T) {
	b1 := makeIntBatch([]int64{10, 20})
	b2 := makeIntBatch([]int64{30})
	fb := &fakeBatch{batches: []*UT.Batch{b1, b2}}

	rows, err := drainBatch(context.Background(), fb, nil)
	if err != nil {
		t.Fatalf("drainBatch: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	wants := []int64{10, 20, 30}
	for i, want := range wants {
		if rows[i].Data[0].I64 != want {
			t.Errorf("row %d: got %d, want %d", i, rows[i].Data[0].I64, want)
		}
	}
}

// TestDrainBatch_BatchProducer_empty verifies drainBatch returns empty
// slice when BatchProducer produces no batches.
func TestDrainBatch_BatchProducer_empty(t *testing.T) {
	fb := &fakeBatch{batches: nil}

	rows, err := drainBatch(context.Background(), fb, nil)
	if err != nil {
		t.Fatalf("drainBatch: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("got %d rows, want 0", len(rows))
	}
}

// TestDrainBatch_BatchProducer_multiColumn verifies multi-column batches
// are correctly converted to rows.
func TestDrainBatch_BatchProducer_multiColumn(t *testing.T) {
	batch := makeMultiColBatch([]int64{42, 99}, []string{"hello", "world"})
	fb := &fakeBatch{batches: []*UT.Batch{batch}}

	rows, err := drainBatch(context.Background(), fb, nil)
	if err != nil {
		t.Fatalf("drainBatch: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if rows[0].Data[0].I64 != 42 || rows[0].Data[1].S != "hello" {
		t.Errorf("row 0: got [%d, %s], want [42, hello]", rows[0].Data[0].I64, rows[0].Data[1].S)
	}
	if rows[1].Data[0].I64 != 99 || rows[1].Data[1].S != "world" {
		t.Errorf("row 1: got [%d, %s], want [99, world]", rows[1].Data[0].I64, rows[1].Data[1].S)
	}
}

// fakeRowOp is a minimal pl.Operator (non-BatchProducer) for testing
// the fallback drain path.
type fakeRowOp struct {
	rows []DT.Row
	idx  int
}

func (f *fakeRowOp) Next(_ context.Context) (DT.Row, error) {
	if f.idx >= len(f.rows) {
		return DT.Row{}, DT.ErrNoRows
	}
	r := f.rows[f.idx]
	f.idx++
	return r, nil
}

func (f *fakeRowOp) Close() error { return nil }

// TestDrainBatch_OperatorFallback verifies drainBatch falls back to Next()
// when the operator does not implement BatchProducer.
func TestDrainBatch_OperatorFallback(t *testing.T) {
	fo := &fakeRowOp{
		rows: []DT.Row{
			{Data: []DT.Value{{I64: 1}}},
			{Data: []DT.Value{{I64: 2}}},
			{Data: []DT.Value{{I64: 3}}},
		},
	}

	rows, err := drainBatch(context.Background(), fo, nil)
	if err != nil {
		t.Fatalf("drainBatch: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	for i, want := range []int64{1, 2, 3} {
		if rows[i].Data[0].I64 != want {
			t.Errorf("row %d: got %d, want %d", i, rows[i].Data[0].I64, want)
		}
	}
}

// TestDrainBatch_OperatorFallback_empty verifies drainBatch returns empty
// for a row operator with no rows.
func TestDrainBatch_OperatorFallback_empty(t *testing.T) {
	fo := &fakeRowOp{}

	rows, err := drainBatch(context.Background(), fo, nil)
	if err != nil {
		t.Fatalf("drainBatch: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("got %d rows, want 0", len(rows))
	}
}

// BenchmarkDrain_Prealloc_NoGrowslice benchmarks drainPlanRows with
// N = engineBatchSize rows, verifying zero growslice reallocations.
// REQ001637: pre-allocated output slice should eliminate all growslice copies.
func BenchmarkDrain_Prealloc_NoGrowslice(b *testing.B) {
	rows := make([]DT.Row, OP.EngineBatchSize())
	for i := range rows {
		rows[i] = DT.Row{Data: make([]DT.Value, 1)}
		rows[i].Data[0].I64 = int64(i)
	}
	fo := &fakeRowOp{rows: rows}
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fo.idx = 0
		out, err := drainBatch(ctx, fo, nil)
		if err != nil {
			b.Fatalf("drainBatch: %v", err)
		}
		if len(out) != OP.EngineBatchSize() {
			b.Fatalf("got %d rows, want %d", len(out), OP.EngineBatchSize())
		}
	}
}

// --- CompareRows tests (REQ002140 shadow validation) ---

func TestCompareRows_BothEmpty(t *testing.T) {
	mismatches := CompareRows(nil, nil)
	if mismatches != 0 {
		t.Fatalf("expected 0 mismatches, got %d", mismatches)
	}
}

func TestCompareRows_BothNonEmptyEqual(t *testing.T) {
	a := []DT.Row{
		{Data: []DT.Value{{Kind: DT.KindInt, I64: 1}}},
		{Data: []DT.Value{{Kind: DT.KindInt, I64: 2}}},
	}
	b := []DT.Row{
		{Data: []DT.Value{{Kind: DT.KindInt, I64: 1}}},
		{Data: []DT.Value{{Kind: DT.KindInt, I64: 2}}},
	}
	mismatches := CompareRows(a, b)
	if mismatches != 0 {
		t.Fatalf("expected 0 mismatches, got %d", mismatches)
	}
}

func TestCompareRows_CountDiffers(t *testing.T) {
	a := []DT.Row{
		{Data: []DT.Value{{Kind: DT.KindInt, I64: 1}}},
		{Data: []DT.Value{{Kind: DT.KindInt, I64: 2}}},
	}
	b := []DT.Row{
		{Data: []DT.Value{{Kind: DT.KindInt, I64: 1}}},
	}
	mismatches := CompareRows(a, b)
	if mismatches != 2 {
		t.Fatalf("expected 2 mismatches (1 + abs diff), got %d", mismatches)
	}
}

func TestCompareRows_ValueDiffers(t *testing.T) {
	a := []DT.Row{
		{Data: []DT.Value{{Kind: DT.KindInt, I64: 1}}},
		{Data: []DT.Value{{Kind: DT.KindInt, I64: 2}}},
	}
	b := []DT.Row{
		{Data: []DT.Value{{Kind: DT.KindInt, I64: 1}}},
		{Data: []DT.Value{{Kind: DT.KindInt, I64: 99}}},
	}
	mismatches := CompareRows(a, b)
	if mismatches != 1 {
		t.Fatalf("expected 1 mismatch, got %d", mismatches)
	}
}

func TestCompareRows_MultipleMismatches(t *testing.T) {
	a := []DT.Row{
		{Data: []DT.Value{{Kind: DT.KindInt, I64: 1}}},
		{Data: []DT.Value{{Kind: DT.KindInt, I64: 2}}},
		{Data: []DT.Value{{Kind: DT.KindInt, I64: 3}}},
	}
	b := []DT.Row{
		{Data: []DT.Value{{Kind: DT.KindInt, I64: 1}}},
		{Data: []DT.Value{{Kind: DT.KindInt, I64: 99}}},
		{Data: []DT.Value{{Kind: DT.KindInt, I64: 88}}},
	}
	mismatches := CompareRows(a, b)
	if mismatches != 2 {
		t.Fatalf("expected 2 mismatches, got %d", mismatches)
	}
}

func TestCompareRows_EmptyVsNonEmpty(t *testing.T) {
	a := []DT.Row{}
	b := []DT.Row{{Data: []DT.Value{{Kind: DT.KindInt, I64: 1}}}}
	mismatches := CompareRows(a, b)
	if mismatches == 0 {
		t.Fatalf("expected mismatch for length diff, got 0")
	}
}

// --- Pipeline opt-in tests (REQ002141) ---

func TestEnableDisablePipelinePath(t *testing.T) {
	e := NewExecutor()

	// Default: pipelineBuilder is nil.
	if e.pipelineBuilder != nil {
		t.Fatalf("expected pipelineBuilder nil by default")
	}

	// Enable sets pipelineBuilder non-nil.
	e.EnablePipelinePath()
	if e.pipelineBuilder == nil {
		t.Fatalf("expected pipelineBuilder non-nil after Enable")
	}

	// Calling Enable again is idempotent.
	e.EnablePipelinePath()
	if e.pipelineBuilder == nil {
		t.Fatalf("expected pipelineBuilder still non-nil")
	}

	// Disable sets pipelineBuilder to nil.
	e.DisablePipelinePath()
	if e.pipelineBuilder != nil {
		t.Fatalf("expected pipelineBuilder nil after Disable")
	}
}

func TestBuildPipeline_NilByDefault(t *testing.T) {
	e := NewExecutor()
	spec, err := e.BuildPipeline("SELECT 1")
	if err != nil {
		t.Fatalf("BuildPipeline: %v", err)
	}
	if spec != nil {
		t.Fatalf("expected nil spec when pipeline disabled, got %v", spec)
	}
}

func TestBuildPipeline_AfterEnable(t *testing.T) {
	e := NewExecutor()
	e.EnablePipelinePath()
	defer e.DisablePipelinePath()

	spec, err := e.BuildPipeline("SELECT 1")
	if err != nil {
		t.Fatalf("BuildPipeline: %v", err)
	}
	if spec == nil {
		t.Fatalf("expected non-nil spec after Enable")
	}
}
