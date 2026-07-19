package OP

import (
	"context"
	"testing"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

// rowProducer is a simple pl.Operator that yields a fixed set of rows.
type rowProducer struct {
	rows []pl.Row
	pos int
}

func (r *rowProducer) Next(ctx context.Context) (pl.Row, error) {
	if r.pos >= len(r.rows) {
		return pl.Row{}, ErrNoRows
	}
	row := r.rows[r.pos]
	r.pos++
	return row, nil
}

func (r *rowProducer) Close() error { return nil }

func makeTestRow(cols []string, ints []int64) pl.Row {
	data := make([]pl.Value, len(ints))
	for i, v := range ints {
		data[i] = NewIntValue(v)
	}
	return pl.Row{Cols: cols, Data: data}
}

func TestBatchIndexScan_InnerEquiJoin(t *testing.T) {
	cols := []string{"k", "v"}
	rows := []pl.Row{
		makeTestRow(cols, []int64{1, 100}),
		makeTestRow(cols, []int64{2, 200}),
		makeTestRow(cols, []int64{3, 300}),
	}
	is := &IndexScan{rows: rows, table: "t", pos: 0}

	j := NewBatchIndexScan(is)
	defer j.Close()

	ctx := context.Background()
	batch, err := j.NextBatch(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch == nil {
		t.Fatal("expected batch, got nil")
	}
	if batch.Size != 3 {
		t.Fatalf("expected 3 rows, got %d", batch.Size)
	}

	// Verify column data.
	for i := 0; i < batch.Size; i++ {
		k := UT.BatchValueAt(batch.Cols[0], i).(int64)
		v := UT.BatchValueAt(batch.Cols[1], i).(int64)
		expectedV := int64((k) * 100)
		if v != expectedV {
			t.Errorf("row %d: expected v=%d, got v=%d", i, expectedV, v)
		}
	}

	batch.Put()

	// EOF.
	batch2, _ := j.NextBatch(ctx)
	if batch2 != nil {
		t.Fatal("expected nil (EOF), got batch")
	}
}

func TestBatchIndexScan_EmptyRows(t *testing.T) {
	is := &IndexScan{rows: nil, table: "t"}

	j := NewBatchIndexScan(is)
	defer j.Close()

	ctx := context.Background()
	batch, err := j.NextBatch(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch != nil {
		t.Fatal("expected nil for empty rows, got batch")
	}
}

func TestBatchIndexScan_MultiBatch(t *testing.T) {
	// Create enough rows to fill 2+ batches.
	cols := []string{"k"}
	rows := make([]pl.Row, 0, UT.BatchSize*2+50)
	for i := int64(0); i < UT.BatchSize*2+50; i++ {
		rows = append(rows, makeTestRow(cols, []int64{i}))
	}
	is := &IndexScan{rows: rows, table: "t", pos: 0}

	j := NewBatchIndexScan(is)
	defer j.Close()

	ctx := context.Background()
	totalRows := 0
	batchCount := 0
	for {
		batch, err := j.NextBatch(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if batch == nil {
			break
		}
		totalRows += batch.Size
		batchCount++
		batch.Put()
	}

	expectedRows := UT.BatchSize*2 + 50
	if totalRows != expectedRows {
		t.Fatalf("expected %d rows, got %d", expectedRows, totalRows)
	}
	if batchCount < 3 {
		t.Fatalf("expected at least 3 batches, got %d", batchCount)
	}
}

func TestBatchIndexScan_TextValues(t *testing.T) {
	cols := []string{"name"}
	rows := []pl.Row{
		{Cols: cols, Data: []pl.Value{{Kind: pl.KindText, S: "alpha"}}},
		{Cols: cols, Data: []pl.Value{{Kind: pl.KindText, S: "beta"}}},
		{Cols: cols, Data: []pl.Value{{Kind: pl.KindText, S: "gamma"}}},
	}
	is := &IndexScan{rows: rows, table: "t", pos: 0}

	j := NewBatchIndexScan(is)
	defer j.Close()

	ctx := context.Background()
	batch, err := j.NextBatch(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch == nil {
		t.Fatal("expected batch, got nil")
	}
	if batch.Size != 3 {
		t.Fatalf("expected 3 rows, got %d", batch.Size)
	}

	expected := []string{"alpha", "beta", "gamma"}
	for i := 0; i < batch.Size; i++ {
		v := UT.BatchValueAt(batch.Cols[0], i).(string)
		if v != expected[i] {
			t.Errorf("row %d: expected %s, got %s", i, expected[i], v)
		}
	}

	batch.Put()
}

func TestBatchIndexScan_MixedTypes(t *testing.T) {
	cols := []string{"id", "name", "score"}
	rows := []pl.Row{
		{Cols: cols, Data: []pl.Value{
			{Kind: pl.KindInt, I64: 1},
			{Kind: pl.KindText, S: "first"},
			{Kind: pl.KindFloat, F64: 1.5},
		}},
		{Cols: cols, Data: []pl.Value{
			{Kind: pl.KindInt, I64: 2},
			{Kind: pl.KindText, S: "second"},
			{Kind: pl.KindFloat, F64: 2.5},
		}},
	}
	is := &IndexScan{rows: rows, table: "t", pos: 0}

	j := NewBatchIndexScan(is)
	defer j.Close()

	ctx := context.Background()
	batch, err := j.NextBatch(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch == nil {
		t.Fatal("expected batch, got nil")
	}
	if batch.Size != 2 {
		t.Fatalf("expected 2 rows, got %d", batch.Size)
	}

	// Verify type dispatch works correctly.
	id0 := UT.BatchValueAt(batch.Cols[0], 0).(int64)
	name0 := UT.BatchValueAt(batch.Cols[1], 0).(string)
	score0 := UT.BatchValueAt(batch.Cols[2], 0).(float64)
	if id0 != 1 || name0 != "first" || score0 != 1.5 {
		t.Errorf("row 0: got id=%d name=%s score=%f", id0, name0, score0)
	}

	batch.Put()
}

// Ensure column types are correctly preserved.
func TestBatchIndexScan_TypesPreserved(t *testing.T) {
	cols := []string{"i", "f", "s", "b"}
	rows := []pl.Row{
		{Cols: cols, Data: []pl.Value{
			{Kind: pl.KindInt, I64: 42},
			{Kind: pl.KindFloat, F64: 3.14},
			{Kind: pl.KindText, S: "hello"},
			{Kind: pl.KindBool, Bo: true},
		}},
	}
	is := &IndexScan{rows: rows, table: "t", pos: 0}

	j := NewBatchIndexScan(is)
	defer j.Close()

	ctx := context.Background()
	batch, err := j.NextBatch(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if batch.Cols[0].Type != LX.T_INT_KW {
		t.Errorf("col 0 type: expected INT, got %d", batch.Cols[0].Type)
	}
	if batch.Cols[1].Type != LX.T_FLOAT_KW {
		t.Errorf("col 1 type: expected FLOAT, got %d", batch.Cols[1].Type)
	}
	if batch.Cols[2].Type != LX.T_TEXT {
		t.Errorf("col 2 type: expected TEXT, got %d", batch.Cols[2].Type)
	}
	if batch.Cols[3].Type != LX.T_BOOL {
		t.Errorf("col 3 type: expected BOOL, got %d", batch.Cols[3].Type)
	}

	batch.Put()
}