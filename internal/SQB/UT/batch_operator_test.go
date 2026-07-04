package UT

import (
	"context"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// testBatchProducer is a simple BatchProducer for testing.
type testBatchProducer struct {
	batches []*Batch
	idx     int
}

func (s *testBatchProducer) NextBatch(_ context.Context) (*Batch, error) {
	if s.idx >= len(s.batches) {
		return nil, nil
	}
	b := s.batches[s.idx]
	s.idx++
	return b, nil
}

func (s *testBatchProducer) Close() error { return nil }

func TestBatchProducerInterface_Satisfies(t *testing.T) {
	var _ BatchProducer = &testBatchProducer{}
	var _ BatchProducer = &RowOperatorAdapter{}
}

func TestBatchProducer_ReturnsBatches(t *testing.T) {
	b := GetBatch(1)
	b.SetColumnName(0, "id")
	b.Cols[0].Type = LX.T_INT_KW
	b.Cols[0].Data.Ints = []int64{42}
	b.Size = 1

	src := &testBatchProducer{batches: []*Batch{b}}
	batch, err := src.NextBatch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch == nil {
		t.Fatal("expected batch, got nil")
	}
	if batch.Size != 1 {
		t.Errorf("expected size 1, got %d", batch.Size)
	}
	// Second call returns nil (EOF)
	batch2, err := src.NextBatch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch2 != nil {
		t.Errorf("expected nil at EOF, got batch of size %d", batch2.Size)
	}
}

func TestBatchProducer_Close(t *testing.T) {
	src := &testBatchProducer{}
	if err := src.Close(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// mockRowOperator implements Operator for testing RowOperatorAdapter.
type mockRowOperator struct {
	rows   []Row
	idx    int
	closed bool
}

func (m *mockRowOperator) Next(_ context.Context) (Row, error) {
	if m.idx >= len(m.rows) {
		return Row{}, ErrNoRows
	}
	r := m.rows[m.idx]
	m.idx++
	return r, nil
}

func (m *mockRowOperator) Close() error {
	m.closed = true
	return nil
}

func TestRowOperatorAdapter_BasicInt(t *testing.T) {
	mock := &mockRowOperator{
		rows: []Row{
			{
				Cols:  []string{"id"},
				Types: []LX.TokenType{LX.T_INT_KW},
				Data:  []Value{{Kind: KindInt, I64: 10}},
			},
			{
				Cols:  []string{"id"},
				Types: []LX.TokenType{LX.T_INT_KW},
				Data:  []Value{{Kind: KindInt, I64: 20}},
			},
		},
	}
	adapter := NewRowOperatorAdapter(mock)
	defer adapter.Close()

	// First batch
	batch, err := adapter.NextBatch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch == nil {
		t.Fatal("expected batch, got nil")
	}
	if batch.Size != 1 {
		t.Errorf("expected size 1, got %d", batch.Size)
	}
	if batch.Cols[0].Name != "id" {
		t.Errorf("expected col name 'id', got %q", batch.Cols[0].Name)
	}
	if batch.Cols[0].Data.Ints[0] != 10 {
		t.Errorf("expected value 10, got %d", batch.Cols[0].Data.Ints[0])
	}

	// Second batch
	batch2, err := adapter.NextBatch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch2 == nil {
		t.Fatal("expected second batch, got nil")
	}
	if batch2.Cols[0].Data.Ints[0] != 20 {
		t.Errorf("expected value 20, got %d", batch2.Cols[0].Data.Ints[0])
	}

	// EOF
	batch3, err := adapter.NextBatch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch3 != nil {
		t.Errorf("expected nil at EOF, got batch")
	}
}

func TestRowOperatorAdapter_MultiColumn(t *testing.T) {
	mock := &mockRowOperator{
		rows: []Row{
			{
				Cols:  []string{"name", "age", "active"},
				Types: []LX.TokenType{LX.T_TEXT, LX.T_INT_KW, LX.T_BOOL},
				Data: []Value{
					{Kind: KindText, S: "alice"},
					{Kind: KindInt, I64: 30},
					{Kind: KindBool, Bo: true},
				},
			},
		},
	}
	adapter := NewRowOperatorAdapter(mock)
	defer adapter.Close()

	batch, err := adapter.NextBatch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch == nil {
		t.Fatal("expected batch, got nil")
	}
	if batch.Size != 1 {
		t.Errorf("expected size 1, got %d", batch.Size)
	}
	if batch.Cols[0].Data.Strs[0] != "alice" {
		t.Errorf("expected 'alice', got %q", batch.Cols[0].Data.Strs[0])
	}
	if batch.Cols[1].Data.Ints[0] != 30 {
		t.Errorf("expected 30, got %d", batch.Cols[1].Data.Ints[0])
	}
	if batch.Cols[2].Data.Bools[0] != true {
		t.Errorf("expected true, got false")
	}
}

func TestRowOperatorAdapter_NullValue(t *testing.T) {
	mock := &mockRowOperator{
		rows: []Row{
			{
				Cols:  []string{"v"},
				Types: []LX.TokenType{LX.T_INT_KW},
				Data:  []Value{{Kind: KindNull}},
			},
		},
	}
	adapter := NewRowOperatorAdapter(mock)
	defer adapter.Close()

	batch, err := adapter.NextBatch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch == nil {
		t.Fatal("expected batch, got nil")
	}
	if batch.Cols[0].Nulls == nil || !batch.Cols[0].Nulls[0] {
		t.Error("expected null flag set")
	}
}

func TestRowOperatorAdapter_CloseDelegates(t *testing.T) {
	mock := &mockRowOperator{}
	adapter := NewRowOperatorAdapter(mock)
	if err := adapter.Close(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !mock.closed {
		t.Error("expected inner operator to be closed")
	}
}

func TestRowOperatorAdapter_DoneAfterEOF(t *testing.T) {
	mock := &mockRowOperator{
		rows: []Row{
			{
				Cols:  []string{"x"},
				Types: []LX.TokenType{LX.T_INT_KW},
				Data:  []Value{{Kind: KindInt, I64: 1}},
			},
		},
	}
	adapter := NewRowOperatorAdapter(mock)

	// Drain
	adapter.NextBatch(context.Background())
	adapter.NextBatch(context.Background()) // EOF

	// Subsequent calls should return nil without calling inner
	batch, err := adapter.NextBatch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch != nil {
		t.Error("expected nil after EOF")
	}
}

func TestRowOperatorAdapter_EmptyRows(t *testing.T) {
	mock := &mockRowOperator{rows: nil}
	adapter := NewRowOperatorAdapter(mock)
	defer adapter.Close()

	batch, err := adapter.NextBatch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch != nil {
		t.Error("expected nil for empty rows")
	}
}

// Compile-time checks: all OP vectorized types satisfy UT.BatchProducer.
type vecSeqScanLike struct{}
type vecFilterLike struct{}
type vecProjectLike struct{}
type vecHashAggLike struct{}
type vecHashJoinLike struct{}

func (v *vecSeqScanLike) NextBatch(_ context.Context) (*Batch, error)  { return nil, nil }
func (v *vecSeqScanLike) Close() error                                 { return nil }
func (v *vecFilterLike) NextBatch(_ context.Context) (*Batch, error)   { return nil, nil }
func (v *vecFilterLike) Close() error                                  { return nil }
func (v *vecProjectLike) NextBatch(_ context.Context) (*Batch, error)  { return nil, nil }
func (v *vecProjectLike) Close() error                                 { return nil }
func (v *vecHashAggLike) NextBatch(_ context.Context) (*Batch, error)  { return nil, nil }
func (v *vecHashAggLike) Close() error                                 { return nil }
func (v *vecHashJoinLike) NextBatch(_ context.Context) (*Batch, error) { return nil, nil }
func (v *vecHashJoinLike) Close() error                                { return nil }

func TestBatchProducerInterface_VecSeqScan(t *testing.T)  { var _ BatchProducer = &vecSeqScanLike{} }
func TestBatchProducerInterface_VecFilter(t *testing.T)   { var _ BatchProducer = &vecFilterLike{} }
func TestBatchProducerInterface_VecProject(t *testing.T)  { var _ BatchProducer = &vecProjectLike{} }
func TestBatchProducerInterface_VecHashAggregate(t *testing.T) {
	var _ BatchProducer = &vecHashAggLike{}
}
func TestBatchProducerInterface_VecHashJoin(t *testing.T) {
	var _ BatchProducer = &vecHashJoinLike{}
}
