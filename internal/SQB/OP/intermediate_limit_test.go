package OP

import (
	"context"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// offsetFakeBatchProducer yields a fixed set of batches then returns nil.
type offsetFakeBatchProducer struct {
	batches []*UT.Batch
	idx     int
}

func (f *offsetFakeBatchProducer) NextBatch(ctx context.Context) (*UT.Batch, error) {
	_ = ctx
	if f.idx >= len(f.batches) {
		return nil, nil
	}
	b := f.batches[f.idx]
	f.idx++
	return b, nil
}

func (f *offsetFakeBatchProducer) Close() error {
	return nil
}

func makeOffsetIntBatch(n int, start int64) *UT.Batch {
	ints := make([]int64, n)
	for i := range ints {
		ints[i] = start + int64(i)
	}
	return makeIntBatch([]string{"x"}, ints)
}

func collectOffsetInts(t *testing.T, bp UT.BatchProducer) []int64 {
	t.Helper()
	var vals []int64
	for {
		batch, err := bp.NextBatch(context.Background())
		if err != nil {
			t.Fatalf("NextBatch: %v", err)
		}
		if batch == nil {
			break
		}
		for i := 0; i < batch.Size; i++ {
			vals = append(vals, batch.Cols[0].Data.Ints[i])
		}
	}
	return vals
}

// REQ001980: basic offset within a single batch.
func TestVectorizedOffset_SingleBatch(t *testing.T) {
	bp := &offsetFakeBatchProducer{
		batches: []*UT.Batch{makeOffsetIntBatch(10, 1)},
	}
	off := NewVectorizedOffset(bp, 3)
	vals := collectOffsetInts(t, off)
	expected := []int64{4, 5, 6, 7, 8, 9, 10}
	if len(vals) != len(expected) {
		t.Fatalf("got %d rows, want %d", len(vals), len(expected))
	}
	for i, v := range vals {
		if v != expected[i] {
			t.Errorf("row %d: got %d, want %d", i, v, expected[i])
		}
	}
}

// REQ001980: offset that spans multiple full batches plus a partial.
func TestVectorizedOffset_MultiBatch(t *testing.T) {
	bp := &offsetFakeBatchProducer{
		batches: []*UT.Batch{
			makeOffsetIntBatch(5, 1),
			makeOffsetIntBatch(5, 6),
			makeOffsetIntBatch(5, 11),
		},
	}
	off := NewVectorizedOffset(bp, 7)
	vals := collectOffsetInts(t, off)
	expected := []int64{8, 9, 10, 11, 12, 13, 14, 15}
	if len(vals) != len(expected) {
		t.Fatalf("got %d rows, want %d", len(vals), len(expected))
	}
	for i, v := range vals {
		if v != expected[i] {
			t.Errorf("row %d: got %d, want %d", i, v, expected[i])
		}
	}
}

// REQ001980: offset equal to total rows -> empty result.
func TestVectorizedOffset_OffsetEqualsTotal(t *testing.T) {
	bp := &offsetFakeBatchProducer{
		batches: []*UT.Batch{makeOffsetIntBatch(5, 1)},
	}
	off := NewVectorizedOffset(bp, 5)
	vals := collectOffsetInts(t, off)
	if len(vals) != 0 {
		t.Fatalf("got %d rows, want 0", len(vals))
	}
}

// REQ001980: offset greater than total rows -> empty result.
func TestVectorizedOffset_OffsetExceedsTotal(t *testing.T) {
	bp := &offsetFakeBatchProducer{
		batches: []*UT.Batch{makeOffsetIntBatch(5, 1)},
	}
	off := NewVectorizedOffset(bp, 100)
	vals := collectOffsetInts(t, off)
	if len(vals) != 0 {
		t.Fatalf("got %d rows, want 0", len(vals))
	}
}

// REQ001980: zero offset -> pass through all rows unchanged.
func TestVectorizedOffset_ZeroOffset(t *testing.T) {
	bp := &offsetFakeBatchProducer{
		batches: []*UT.Batch{makeOffsetIntBatch(5, 1)},
	}
	off := NewVectorizedOffset(bp, 0)
	vals := collectOffsetInts(t, off)
	expected := []int64{1, 2, 3, 4, 5}
	if len(vals) != len(expected) {
		t.Fatalf("got %d rows, want %d", len(vals), len(expected))
	}
	for i, v := range vals {
		if v != expected[i] {
			t.Errorf("row %d: got %d, want %d", i, v, expected[i])
		}
	}
}

// REQ001980: negative offset is treated as zero.
func TestVectorizedOffset_NegativeOffset(t *testing.T) {
	bp := &offsetFakeBatchProducer{
		batches: []*UT.Batch{makeOffsetIntBatch(5, 1)},
	}
	off := NewVectorizedOffset(bp, -5)
	vals := collectOffsetInts(t, off)
	expected := []int64{1, 2, 3, 4, 5}
	if len(vals) != len(expected) {
		t.Fatalf("got %d rows, want %d", len(vals), len(expected))
	}
	for i, v := range vals {
		if v != expected[i] {
			t.Errorf("row %d: got %d, want %d", i, v, expected[i])
		}
	}
}

// REQ001980: offset with selection vector (filtered batch).
func TestVectorizedOffset_WithSelVector(t *testing.T) {
	batch := makeOffsetIntBatch(10, 1)
	// Keep only even-indexed rows: 0, 2, 4, 6, 8 (values 1, 3, 5, 7, 9)
	batch.Sel = []uint16{0, 2, 4, 6, 8}
	bp := &offsetFakeBatchProducer{
		batches: []*UT.Batch{batch},
	}
	off := NewVectorizedOffset(bp, 2)
	vals := collectOffsetInts(t, off)
	expected := []int64{5, 7, 9}
	if len(vals) != len(expected) {
		t.Fatalf("got %d rows, want %d", len(vals), len(expected))
	}
	for i, v := range vals {
		if v != expected[i] {
			t.Errorf("row %d: got %d, want %d", i, v, expected[i])
		}
	}
}

// REQ001980: offset with null values.
func TestVectorizedOffset_WithNulls(t *testing.T) {
	b := UT.GetBatch(1)
	b.SetColumnName(0, "x")
	b.Cols[0].Type = LX.T_INT_KW
	b.Cols[0].Data.Ints = []int64{1, 2, 3, 4, 5}
	b.Cols[0].Nulls = []bool{false, true, false, true, false}
	b.Size = 5
	bp := &offsetFakeBatchProducer{
		batches: []*UT.Batch{b},
	}
	off := NewVectorizedOffset(bp, 2)

	var resultVals []int64
	var resultNulls []bool
	for {
		batch, err := off.NextBatch(context.Background())
		if err != nil {
			t.Fatalf("NextBatch: %v", err)
		}
		if batch == nil {
			break
		}
		for i := 0; i < batch.Size; i++ {
			resultVals = append(resultVals, batch.Cols[0].Data.Ints[i])
			if batch.Cols[0].Nulls != nil {
				resultNulls = append(resultNulls, batch.Cols[0].Nulls[i])
			}
		}
	}

	expectedVals := []int64{3, 4, 5}
	expectedNulls := []bool{false, true, false}
	if len(resultVals) != len(expectedVals) {
		t.Fatalf("got %d rows, want %d", len(resultVals), len(expectedVals))
	}
	for i := range resultVals {
		if resultVals[i] != expectedVals[i] {
			t.Errorf("row %d val: got %d, want %d", i, resultVals[i], expectedVals[i])
		}
		if resultNulls[i] != expectedNulls[i] {
			t.Errorf("row %d null: got %v, want %v", i, resultNulls[i], expectedNulls[i])
		}
	}
}

// BenchmarkVectorizedOffset benchmarks the vectorized offset with a large skip.
func BenchmarkVectorizedOffset_LargeOffset(b *testing.B) {
	const totalRows = 100000
	const offset = 90000
	const batchSize = 1024

	// Build the int64 data once and reuse it for batch construction.
	numBatches := (totalRows + batchSize - 1) / batchSize
	batchData := make([][]int64, numBatches)
	for i := range batchData {
		start := i * batchSize
		n := batchSize
		if start+n > totalRows {
			n = totalRows - start
		}
		ints := make([]int64, n)
		for j := range ints {
			ints[j] = int64(start + j + 1)
		}
		batchData[i] = ints
	}

	buildBatches := func() []*UT.Batch {
		batches := make([]*UT.Batch, numBatches)
		for idx, ints := range batchData {
			b := UT.GetBatch(1)
			b.SetColumnName(0, "x")
			b.Cols[0].Type = LX.T_INT_KW
			b.Cols[0].Data.Ints = ints
			b.Size = len(ints)
			b.Pooled = false // don't return these to the pool
			batches[idx] = b
		}
		return batches
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		batches := buildBatches()
		bp := &offsetFakeBatchProducer{batches: batches, idx: 0}
		off := NewVectorizedOffset(bp, offset)
		count := 0
		for {
			batch, _ := off.NextBatch(context.Background())
			if batch == nil {
				break
			}
			count += batch.Size
		}
		if count != totalRows-offset {
			b.Fatalf("expected %d rows, got %d", totalRows-offset, count)
		}
	}
}
