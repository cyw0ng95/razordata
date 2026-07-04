package UT

import (
	"context"
	"errors"
	"testing"

	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// testBatchProducerSeq is a simple BatchProducer that returns batches in sequence.
type testBatchProducerSeq struct {
	batches []*Batch
	idx     int
	closed  bool
}

func (s *testBatchProducerSeq) NextBatch(_ context.Context) (*Batch, error) {
	if s.idx >= len(s.batches) {
		return nil, nil
	}
	b := s.batches[s.idx]
	s.idx++
	return b, nil
}

func (s *testBatchProducerSeq) Close() error {
	s.closed = true
	return nil
}

func TestBatchToRowAdapter_Basic(t *testing.T) {
	b := GetBatch(3)
	b.SetColumnName(0, "id")
	b.SetColumnName(1, "name")
	b.SetColumnName(2, "score")

	b.Cols[0].Type = LX.T_INT_KW
	b.Cols[0].Data.Ints = []int64{10, 20, 30}
	b.Cols[1].Type = LX.T_TEXT
	b.Cols[1].Data.Strs = []string{"alice", "bob", "charlie"}
	b.Cols[2].Type = LX.T_FLOAT_KW
	b.Cols[2].Data.Floats = []float64{1.1, 2.2, 3.3}
	b.Size = 3

	src := &testBatchProducerSeq{batches: []*Batch{b}}
	adapter := NewBatchToRowAdapter(src)
	ctx := context.Background()

	// Row 0
	row, err := adapter.Next(ctx)
	if err != nil {
		t.Fatalf("row 0: unexpected error: %v", err)
	}
	if len(row.Cols) != 3 || row.Cols[0] != "id" || row.Cols[1] != "name" || row.Cols[2] != "score" {
		t.Errorf("row 0: unexpected cols: %v", row.Cols)
	}
	if row.Data[0].I64 != 10 || row.Data[1].S != "alice" || row.Data[2].F64 != 1.1 {
		t.Errorf("row 0: unexpected data: %v", row.Data)
	}

	// Row 1
	row, err = adapter.Next(ctx)
	if err != nil {
		t.Fatalf("row 1: unexpected error: %v", err)
	}
	if row.Data[0].I64 != 20 || row.Data[1].S != "bob" || row.Data[2].F64 != 2.2 {
		t.Errorf("row 1: unexpected data: %v", row.Data)
	}

	// Row 2
	row, err = adapter.Next(ctx)
	if err != nil {
		t.Fatalf("row 2: unexpected error: %v", err)
	}
	if row.Data[0].I64 != 30 || row.Data[1].S != "charlie" || row.Data[2].F64 != 3.3 {
		t.Errorf("row 2: unexpected data: %v", row.Data)
	}

	// EOF
	_, err = adapter.Next(ctx)
	if !errors.Is(err, pl.ErrNoRows) {
		t.Errorf("expected ErrNoRows at EOF, got: %v", err)
	}

	if err := adapter.Close(); err != nil {
		t.Fatalf("unexpected close error: %v", err)
	}
}

func TestBatchToRowAdapter_MultipleBatches(t *testing.T) {
	b1 := GetBatch(2)
	b1.SetColumnName(0, "x")
	b1.Cols[0].Type = LX.T_INT_KW
	b1.Cols[0].Data.Ints = []int64{1, 2}
	b1.Size = 2

	b2 := GetBatch(2)
	b2.SetColumnName(0, "x")
	b2.Cols[0].Type = LX.T_INT_KW
	b2.Cols[0].Data.Ints = []int64{3, 4}
	b2.Size = 2

	src := &testBatchProducerSeq{batches: []*Batch{b1, b2}}
	adapter := NewBatchToRowAdapter(src)
	ctx := context.Background()

	expected := []int64{1, 2, 3, 4}
	for i, want := range expected {
		row, err := adapter.Next(ctx)
		if err != nil {
			t.Fatalf("row %d: unexpected error: %v", i, err)
		}
		if row.Data[0].I64 != want {
			t.Errorf("row %d: expected %d, got %d", i, want, row.Data[0].I64)
		}
	}

	_, err := adapter.Next(ctx)
	if !errors.Is(err, pl.ErrNoRows) {
		t.Errorf("expected ErrNoRows at EOF, got: %v", err)
	}

	if err := adapter.Close(); err != nil {
		t.Fatalf("unexpected close error: %v", err)
	}
}

func TestBatchToRowAdapter_EmptySource(t *testing.T) {
	src := &testBatchProducerSeq{batches: nil}
	adapter := NewBatchToRowAdapter(src)
	ctx := context.Background()

	_, err := adapter.Next(ctx)
	if !errors.Is(err, pl.ErrNoRows) {
		t.Errorf("expected ErrNoRows from empty source, got: %v", err)
	}

	if err := adapter.Close(); err != nil {
		t.Fatalf("unexpected close error: %v", err)
	}
}

func TestBatchToRowAdapter_WithSelection(t *testing.T) {
	b := GetBatch(4)
	b.SetColumnName(0, "id")
	b.Cols[0].Type = LX.T_INT_KW
	b.Cols[0].Data.Ints = []int64{10, 20, 30, 40}
	b.Size = 4
	b.Sel = []uint16{1, 3} // Only rows at physical indices 1 and 3

	src := &testBatchProducerSeq{batches: []*Batch{b}}
	adapter := NewBatchToRowAdapter(src)
	ctx := context.Background()

	// Row 0: physical index 1
	row, err := adapter.Next(ctx)
	if err != nil {
		t.Fatalf("row 0: unexpected error: %v", err)
	}
	if row.Data[0].I64 != 20 {
		t.Errorf("row 0: expected 20, got %d", row.Data[0].I64)
	}

	// Row 1: physical index 3
	row, err = adapter.Next(ctx)
	if err != nil {
		t.Fatalf("row 1: unexpected error: %v", err)
	}
	if row.Data[0].I64 != 40 {
		t.Errorf("row 1: expected 40, got %d", row.Data[0].I64)
	}

	// EOF
	_, err = adapter.Next(ctx)
	if !errors.Is(err, pl.ErrNoRows) {
		t.Errorf("expected ErrNoRows at EOF, got: %v", err)
	}

	if err := adapter.Close(); err != nil {
		t.Fatalf("unexpected close error: %v", err)
	}
}

func TestBatchToRowAdapter_Close(t *testing.T) {
	src := &testBatchProducerSeq{batches: nil}
	adapter := NewBatchToRowAdapter(src)

	if err := adapter.Close(); err != nil {
		t.Fatalf("unexpected close error: %v", err)
	}
	if !src.closed {
		t.Error("expected source to be closed")
	}

	// Double close is safe.
	if err := adapter.Close(); err != nil {
		t.Fatalf("unexpected close error on double close: %v", err)
	}
}

func TestBatchToRowAdapter_NullValues(t *testing.T) {
	b := GetBatch(3)
	b.SetColumnName(0, "a")
	b.SetColumnName(1, "b")

	b.Cols[0].Type = LX.T_INT_KW
	b.Cols[0].Data.Ints = []int64{1, 0, 3}
	b.Cols[0].Nulls = []bool{false, true, false}

	b.Cols[1].Type = LX.T_TEXT
	b.Cols[1].Data.Strs = []string{"x", "y", "z"}
	b.Cols[1].Nulls = []bool{true, false, true}

	b.Size = 3

	src := &testBatchProducerSeq{batches: []*Batch{b}}
	adapter := NewBatchToRowAdapter(src)
	ctx := context.Background()

	// Row 0: a=1 (not null), b=NULL
	row, err := adapter.Next(ctx)
	if err != nil {
		t.Fatalf("row 0: unexpected error: %v", err)
	}
	if row.Data[0].I64 != 1 {
		t.Errorf("row 0: expected a=1, got %d", row.Data[0].I64)
	}
	if !row.Data[1].IsNull() {
		t.Errorf("row 0: expected b=NULL, got %v", row.Data[1])
	}

	// Row 1: a=NULL, b="y"
	row, err = adapter.Next(ctx)
	if err != nil {
		t.Fatalf("row 1: unexpected error: %v", err)
	}
	if !row.Data[0].IsNull() {
		t.Errorf("row 1: expected a=NULL, got %v", row.Data[0])
	}
	if row.Data[1].S != "y" {
		t.Errorf("row 1: expected b=\"y\", got %q", row.Data[1].S)
	}

	// Row 2: a=3 (not null), b=NULL
	row, err = adapter.Next(ctx)
	if err != nil {
		t.Fatalf("row 2: unexpected error: %v", err)
	}
	if row.Data[0].I64 != 3 {
		t.Errorf("row 2: expected a=3, got %d", row.Data[0].I64)
	}
	if !row.Data[1].IsNull() {
		t.Errorf("row 2: expected b=NULL, got %v", row.Data[1])
	}

	// EOF
	_, err = adapter.Next(ctx)
	if !errors.Is(err, pl.ErrNoRows) {
		t.Errorf("expected ErrNoRows at EOF, got: %v", err)
	}

	if err := adapter.Close(); err != nil {
		t.Fatalf("unexpected close error: %v", err)
	}
}
