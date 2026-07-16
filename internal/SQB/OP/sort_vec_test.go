package OP

import (
	"context"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SQB/UT"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// REQ001441: VectorizedSort sorts a batch and emits in sorted order.
func TestVectorizedSort_Basic(t *testing.T) {
	// Create a fake BatchProducer that yields one batch with 5 rows.
	bp := &fakeBatchProducer{
		batches: []*UT.Batch{
			func() *UT.Batch {
				b := UT.GetBatch(2)
				b.SetColumnName(0, "id")
				b.SetColumnName(1, "val")
				b.Cols[0].Type = LX.T_INT_KW
				b.Cols[1].Type = LX.T_TEXT
				for _, v := range []int64{3, 1, 4, 1, 5} {
					b.AppendRow(0, LX.T_INT_KW, v, false)
					b.AppendRow(1, LX.T_TEXT, "v"+intToStr2(int(v)), false)
					b.AdvanceSize()
				}
				return b
			}(),
		},
	}
	keys := []PS.OrderItem{{Expr: &PS.Ident{Name: "id"}, Desc: false}}
	sortOp := NewVectorizedSort(bp, keys)
	
	// Collect all output rows.
	var allRows []*UT.Batch
	for {
		batch, err := sortOp.NextBatch(context.Background())
		if err != nil {
			t.Fatalf("NextBatch: %v", err)
		}
		if batch == nil {
			break
		}
		allRows = append(allRows, batch)
	}
	
	// Concatenate all output batches.
	var allVals []int64
	for _, b := range allRows {
		for i := 0; i < b.Size; i++ {
			val := b.Cols[0].Data.Ints[i]
			allVals = append(allVals, val)
		}
	}
	
	// Expected sorted: 1, 1, 3, 4, 5
	expected := []int64{1, 1, 3, 4, 5}
	if len(allVals) != len(expected) {
		t.Fatalf("got %d rows, want %d", len(allVals), len(expected))
	}
	for i, v := range allVals {
		if v != expected[i] {
			t.Errorf("row %d: got %d, want %d", i, v, expected[i])
		}
	}
}

// REQ001441: VectorizedLimit caps output to n rows.
func TestVectorizedLimit_Basic(t *testing.T) {
	bp := &fakeBatchProducer{
		batches: []*UT.Batch{
			func() *UT.Batch {
				b := UT.GetBatch(1)
				b.SetColumnName(0, "x")
				b.Cols[0].Type = LX.T_INT_KW
				for i := int64(1); i <= 10; i++ {
					b.AppendRow(0, LX.T_INT_KW, i, false)
					b.AdvanceSize()
				}
				return b
			}(),
		},
	}
	limitOp := NewVectorizedLimit(bp, 3)
	
	var vals []int64
	for {
		batch, err := limitOp.NextBatch(context.Background())
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
	
	expected := []int64{1, 2, 3}
	if len(vals) != len(expected) {
		t.Fatalf("got %d rows, want %d", len(vals), len(expected))
	}
	for i, v := range vals {
		if v != expected[i] {
			t.Errorf("row %d: got %d, want %d", i, v, expected[i])
		}
	}
}

// fakeBatchProducer yields a fixed set of batches then returns nil.
type fakeBatchProducer struct {
	batches []*UT.Batch
	idx     int
}

func (f *fakeBatchProducer) NextBatch(ctx context.Context) (*UT.Batch, error) {
	_ = ctx
	if f.idx >= len(f.batches) {
		return nil, nil
	}
	b := f.batches[f.idx]
	f.idx++
	return b, nil
}

func (f *fakeBatchProducer) Close() error {
	return nil
}

func intToStr2(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// REQ001450: VectorizedSeqScan respects column pruning.
func TestVectorizedSeqScan_ColumnPruning(t *testing.T) {
	// Create a mock source that yields rows with 3 columns.
	rows := []map[string]any{
		{"a": int64(1), "b": "x", "c": int64(10)},
		{"a": int64(2), "b": "y", "c": int64(20)},
	}
	source := &mockRowSource{rows: rows, schema: []string{"a", "b", "c"}, types: []LX.TokenType{LX.T_INT_KW, LX.T_TEXT, LX.T_INT_KW}}
	
	// Create vectorized SeqScan requesting only columns 0 and 2.
	vss := NewVectorizedSeqScanWithCols(source, []string{"a", "b", "c"}, []LX.TokenType{LX.T_INT_KW, LX.T_TEXT, LX.T_INT_KW}, []int{0, 2})
	
	var batches []*UT.Batch
	for {
		batch, err := vss.NextBatch(context.Background())
		if err != nil {
			t.Fatalf("NextBatch: %v", err)
		}
		if batch == nil {
			break
		}
		batches = append(batches, batch)
	}
	
	if len(batches) != 1 {
		t.Fatalf("expected 1 batch, got %d", len(batches))
	}
	
	b := batches[0]
	// Should have only 2 columns (a and c), not 3.
	colCount := 0
	for _, c := range b.Cols {
		if c.Type != 0 {
			colCount++
		}
	}
	if colCount != 2 {
		t.Errorf("expected 2 columns, got %d", colCount)
	}
	
	// Verify values.
	if b.Size != 2 {
		t.Errorf("expected 2 rows, got %d", b.Size)
	}
	if b.Size > 0 && len(b.Cols) > 0 && b.Cols[0].Type == LX.T_INT_KW {
		if b.Cols[0].Data.Ints[0] != 1 {
			t.Errorf("col 0 row 0: got %d, want 1", b.Cols[0].Data.Ints[0])
		}
		if b.Cols[0].Data.Ints[1] != 2 {
			t.Errorf("col 0 row 1: got %d, want 2", b.Cols[0].Data.Ints[1])
		}
	}
}

// mockRowSource implements pl.Operator for testing VectorizedSeqScan.
type mockRowSource struct {
	rows   []map[string]any
	schema []string
	types  []LX.TokenType
	idx    int
}

func (m *mockRowSource) Next(ctx context.Context) (pl.Row, error) {
	if m.idx >= len(m.rows) {
		return pl.Row{}, ErrNoRows
	}
	row := m.rows[m.idx]
	m.idx++
	data := make([]pl.Value, len(m.schema))
	cols := make([]string, len(m.schema))
	types := make([]LX.TokenType, len(m.schema))
	for i, name := range m.schema {
		cols[i] = name
		types[i] = m.types[i]
		if v, ok := row[name]; ok {
			switch vv := v.(type) {
			case int64:
				data[i] = pl.Value{Kind: pl.KindInt, I64: vv}
			case string:
				data[i] = pl.Value{Kind: pl.KindText, S: vv}
			default:
				data[i] = pl.Value{Kind: pl.KindNull}
			}
		} else {
			data[i] = pl.Value{Kind: pl.KindNull}
		}
	}
	return pl.Row{Data: data, Cols: cols, Types: types}, nil
}
func (m *mockRowSource) Close() error                        { return nil }
func (m *mockRowSource) WithParams(p []any) pl.Operator      { return m }
func (m *mockRowSource) Predicate() PS.Expr                  { return nil }
func (m *mockRowSource) Children() []pl.Operator             { return nil }
func (m *mockRowSource) Reset(ctx context.Context) error     { m.idx = 0; return nil }
func (m *mockRowSource) SetParams(p []any)                   {}
func (m *mockRowSource) Schema() *DT.StoreSchema             { return nil }
