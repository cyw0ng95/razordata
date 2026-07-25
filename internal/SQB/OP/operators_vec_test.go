package OP

import (
	"context"
	"testing"

	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// testBatchProducer is a simple BatchProducer that returns batches
// from a pre-built slice, one at a time.
type testBatchProducer struct {
	batches []*UT.Batch
	idx     int
}

func (s *testBatchProducer) NextBatch(_ context.Context) (*UT.Batch, error) {
	if s.idx >= len(s.batches) {
		return nil, nil
	}
	b := s.batches[s.idx]
	s.idx++
	return b, nil
}

// Close releases any un-consumed batches back to the pool. REQ001999:
// without this, partially-drained producers (e.g. when a join short-
// circuits on an empty build side) leak pooled batches across test
// cases, which under GOMEMLIMIT can compound into OOM during long
// equivalence runs. Consumed batches are already Put()ed by the
// joining operator, so we only release the tail starting at s.idx.
func (s *testBatchProducer) Close() error {
	for i := s.idx; i < len(s.batches); i++ {
		if s.batches[i] != nil {
			s.batches[i].Put()
		}
	}
	s.batches = nil
	return nil
}

func makeTestBatchForProject(n int) *UT.Batch {
	b := UT.GetBatch(2)
	b.SetColumnName(0, "x")
	b.SetColumnName(1, "y")
	b.Cols[0].Type = LX.T_INT_KW
	b.Cols[0].Data.Ints = make([]int64, n)
	b.Cols[1].Type = LX.T_INT_KW
	b.Cols[1].Data.Ints = make([]int64, n)
	for i := 0; i < n; i++ {
		b.Cols[0].Data.Ints[i] = int64(i + 1)
		b.Cols[1].Data.Ints[i] = int64((i + 1) * 10)
	}
	b.Size = n
	b.SetColMap(map[string]int{"x": 0, "y": 1})
	return b
}

func TestVectorizedProject_ColumnRef(t *testing.T) {
	src := &testBatchProducer{
		batches: []*UT.Batch{makeTestBatchForProject(4)},
	}
	proj := NewVectorizedProject(src, []PS.Expr{&PS.Ident{Name: "x"}}, []string{"x"})

	batch, err := proj.NextBatch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch == nil {
		t.Fatal("expected batch, got nil")
	}
	if batch.Size != 4 {
		t.Fatalf("expected size 4, got %d", batch.Size)
	}
	for i := 0; i < 4; i++ {
		v := UT.BatchValueAt(batch.Cols[0], i)
		if v != int64(i+1) {
			t.Errorf("row %d: expected %d, got %v", i, i+1, v)
		}
	}
	batch.Put()

	batch2, err := proj.NextBatch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch2 != nil {
		t.Fatal("expected nil (EOF), got batch")
	}
}

func TestVectorizedProject_Arithmetic(t *testing.T) {
	src := &testBatchProducer{
		batches: []*UT.Batch{makeTestBatchForProject(3)},
	}
	// x + 10
	expr := &PS.BinaryExpr{
		Op:   LX.T_PLUS,
		Left: &PS.Ident{Name: "x"},
		Right: &PS.NumberLiteral{Val: 10},
	}
	proj := NewVectorizedProject(src, []PS.Expr{expr}, []string{"result"})

	batch, err := proj.NextBatch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch == nil {
		t.Fatal("expected batch, got nil")
	}
	if batch.Size != 3 {
		t.Fatalf("expected size 3, got %d", batch.Size)
	}
	expected := []int64{11, 12, 13}
	for i, want := range expected {
		v := UT.BatchValueAt(batch.Cols[0], i)
		if v != want {
			t.Errorf("row %d: expected %d, got %v", i, want, v)
		}
	}
	batch.Put()
}

func TestVectorizedProject_MultiColumn(t *testing.T) {
	src := &testBatchProducer{
		batches: []*UT.Batch{makeTestBatchForProject(3)},
	}
	proj := NewVectorizedProject(
		src,
		[]PS.Expr{&PS.Ident{Name: "x"}, &PS.Ident{Name: "y"}},
		[]string{"x", "y"},
	)

	batch, err := proj.NextBatch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch == nil {
		t.Fatal("expected batch, got nil")
	}
	if batch.Cols[0].Name != "x" || batch.Cols[1].Name != "y" {
		t.Fatalf("expected columns named x,y, got %q,%q", batch.Cols[0].Name, batch.Cols[1].Name)
	}
	if batch.Size != 3 {
		t.Fatalf("expected size 3, got %d", batch.Size)
	}
	for i := 0; i < 3; i++ {
		xv := UT.BatchValueAt(batch.Cols[0], i)
		yv := UT.BatchValueAt(batch.Cols[1], i)
		if xv != int64(i+1) {
			t.Errorf("row %d x: expected %d, got %v", i, i+1, xv)
		}
		if yv != int64((i+1)*10) {
			t.Errorf("row %d y: expected %d, got %v", i, (i+1)*10, yv)
		}
	}
	batch.Put()
}

func TestVectorizedProject_EOF(t *testing.T) {
	src := &testBatchProducer{batches: nil}
	proj := NewVectorizedProject(src, []PS.Expr{&PS.Ident{Name: "x"}}, []string{"x"})

	batch, err := proj.NextBatch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch != nil {
		t.Fatal("expected nil (EOF), got batch")
	}

	// Second call should also return nil
	batch2, err := proj.NextBatch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch2 != nil {
		t.Fatal("expected nil (EOF), got batch")
	}
}

func TestVectorizedProject_WithFilteredBatch(t *testing.T) {
	srcBatch := makeTestBatchForProject(6)
	// Simulate a filter that kept rows 0, 2, 4
	srcBatch.Sel = []uint16{0, 2, 4}
	srcBatch.Size = 6 // physical size stays 6

	src := &testBatchProducer{batches: []*UT.Batch{srcBatch}}
	proj := NewVectorizedProject(src, []PS.Expr{&PS.Ident{Name: "x"}}, []string{"x"})

	batch, err := proj.NextBatch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch == nil {
		t.Fatal("expected batch, got nil")
	}
	// Output size should be LogicalSize of child (3), not physical (6)
	if batch.Size != 3 {
		t.Fatalf("expected logical size 3, got %d", batch.Size)
	}
	expected := []int64{1, 3, 5} // x values at rows 0, 2, 4
	for i, want := range expected {
		v := UT.BatchValueAt(batch.Cols[0], i)
		if v != want {
			t.Errorf("row %d: expected %d, got %v", i, want, v)
		}
	}
	batch.Put()
}
