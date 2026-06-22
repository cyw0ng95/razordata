package EX

import (
	"context"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

type staticOperator struct {
	rows []Row
	idx  int
}

func (s *staticOperator) Next(_ context.Context) (Row, error) {
	if s.idx >= len(s.rows) {
		return Row{}, ErrNoRows
	}
	r := s.rows[s.idx]
	s.idx++
	return r, nil
}

func (s *staticOperator) Close() error { return nil }

func makeRow(cols []string, data []any) Row {
	return Row{Cols: cols, Data: valueFromAnySlice(data)}
}

func TestWindow_RowNumber(t *testing.T) {
	input := &staticOperator{}
	input.rows = []Row{
		makeRow([]string{"id"}, []any{int64(1)}),
		makeRow([]string{"id"}, []any{int64(2)}),
		makeRow([]string{"id"}, []any{int64(3)}),
	}

	spec := &PS.WindowSpec{
		OrderBy: []PS.OrderItem{{Expr: &PS.Ident{Name: "id"}}},
	}
	op := NewWindowOperator(input, "ROW_NUMBER", nil, spec, []string{"id"})

	expected := []int64{1, 2, 3}
	for i, want := range expected {
		row, err := op.Next(context.Background())
		if err != nil {
			t.Fatalf("row %d: %v", i, err)
		}
		got := row.Data[len(row.Data)-1]
		if got != NewIntValue(want) {
			t.Errorf("row %d: got %v, want %v", i, got, want)
		}
	}
	_, err := op.Next(context.Background())
	if err != ErrNoRows {
		t.Errorf("expected ErrNoRows, got %v", err)
	}
}

func TestWindow_DenseRank(t *testing.T) {
	input := &staticOperator{}
	input.rows = []Row{
		makeRow([]string{"score"}, []any{int64(100)}),
		makeRow([]string{"score"}, []any{int64(90)}),
		makeRow([]string{"score"}, []any{int64(90)}),
		makeRow([]string{"score"}, []any{int64(80)}),
	}
	for i := range input.rows {
		input.rows[i].Cols = []string{"score"}
	}

	spec := &PS.WindowSpec{
		OrderBy: []PS.OrderItem{{Expr: &PS.Ident{Name: "score"}, Desc: true}},
	}
	op := NewWindowOperator(input, "DENSE_RANK", nil, spec, []string{"score"})

	expected := []int64{1, 2, 2, 3}
	for i, want := range expected {
		row, err := op.Next(context.Background())
		if err != nil {
			t.Fatalf("row %d: %v", i, err)
		}
		got := row.Data[len(row.Data)-1]
		if got != NewIntValue(want) {
			t.Errorf("row %d: got %v, want %v", i, got, want)
		}
	}
}

func TestWindow_Partition(t *testing.T) {
	input := &staticOperator{}
	input.rows = []Row{
		makeRow([]string{"dept", "salary"}, []any{"eng", int64(100)}),
		makeRow([]string{"dept", "salary"}, []any{"eng", int64(200)}),
		makeRow([]string{"dept", "salary"}, []any{"sales", int64(150)}),
		makeRow([]string{"dept", "salary"}, []any{"sales", int64(150)}),
	}

	spec := &PS.WindowSpec{
		PartitionBy: []PS.Expr{&PS.Ident{Name: "dept"}},
		OrderBy:     []PS.OrderItem{{Expr: &PS.Ident{Name: "salary"}}},
	}
	op := NewWindowOperator(input, "ROW_NUMBER", nil, spec, []string{"dept", "salary"})

	// Each partition gets its own ROW_NUMBER starting at 1
	got := make([]int64, 4)
	for i := 0; i < 4; i++ {
		row, err := op.Next(context.Background())
		if err != nil {
			t.Fatalf("row %d: %v", i, err)
		}
		got[i] = row.Data[len(row.Data)-1].ToAny().(int64)
	}

	// eng: 1,2  sales: 1,2 (order within partition by salary)
	// eng rows come first or sales rows come first (map order non-deterministic)
	// but within each partition, ROW_NUMBER is 1,2
	if got[0] != 1 || got[1] != 2 || got[2] != 1 || got[3] != 2 {
		t.Errorf("expected [1,2,1,2] in some order, got %v", got)
	}
}

func TestWindow_Lag(t *testing.T) {
	input := &staticOperator{}
	input.rows = []Row{
		makeRow([]string{"val"}, []any{int64(10)}),
		makeRow([]string{"val"}, []any{int64(20)}),
		makeRow([]string{"val"}, []any{int64(30)}),
	}

	spec := &PS.WindowSpec{
		OrderBy: []PS.OrderItem{{Expr: &PS.Ident{Name: "val"}}},
	}
	op := NewWindowOperator(input, "LAG", []PS.Expr{&PS.Ident{Name: "val"}}, spec, []string{"val"})

	// LAG(val): first row -> nil, second -> 10, third -> 20
	expected := []any{nil, int64(10), int64(20)}
	for i, want := range expected {
		row, err := op.Next(context.Background())
		if err != nil {
			t.Fatalf("row %d: %v", i, err)
		}
		got := row.Data[len(row.Data)-1]
		if got.ToAny() != want {
			t.Errorf("row %d: got %v, want %v", i, got, want)
		}
	}
}

func TestWindow_Lead(t *testing.T) {
	input := &staticOperator{}
	input.rows = []Row{
		makeRow([]string{"val"}, []any{int64(10)}),
		makeRow([]string{"val"}, []any{int64(20)}),
		makeRow([]string{"val"}, []any{int64(30)}),
	}

	spec := &PS.WindowSpec{
		OrderBy: []PS.OrderItem{{Expr: &PS.Ident{Name: "val"}}},
	}
	op := NewWindowOperator(input, "LEAD", []PS.Expr{&PS.Ident{Name: "val"}}, spec, []string{"val"})

	// LEAD(val): first -> 20, second -> 30, third -> nil
	expected := []any{int64(20), int64(30), nil}
	for i, want := range expected {
		row, err := op.Next(context.Background())
		if err != nil {
			t.Fatalf("row %d: %v", i, err)
		}
		got := row.Data[len(row.Data)-1]
		if got.ToAny() != want {
			t.Errorf("row %d: got %v, want %v", i, got, want)
		}
	}
}

func TestWindow_EmptyInput(t *testing.T) {
	input := &staticOperator{}
	spec := &PS.WindowSpec{}
	op := NewWindowOperator(input, "ROW_NUMBER", nil, spec, []string{})
	_, err := op.Next(context.Background())
	if err != ErrNoRows {
		t.Errorf("expected ErrNoRows, got %v", err)
	}
}

// TestWindow_RangeFrame verifies RANGE window frame with peer-group
// detection (REQ000686). RANGE frames include all rows that have the
// same ORDER BY value as the current row (peer group).
func TestWindow_RangeFrame(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	// Input: 5 rows with ORDER BY val: [1, 1, 2, 3, 3]
	rows := []Row{
		{Cols: []string{"id", "val"}, Data: valueFromAnySlice([]any{int64(1), int64(1)})},
		{Cols: []string{"id", "val"}, Data: valueFromAnySlice([]any{int64(2), int64(1)})},
		{Cols: []string{"id", "val"}, Data: valueFromAnySlice([]any{int64(3), int64(2)})},
		{Cols: []string{"id", "val"}, Data: valueFromAnySlice([]any{int64(4), int64(3)})},
		{Cols: []string{"id", "val"}, Data: valueFromAnySlice([]any{int64(5), int64(3)})},
	}
	input := &staticOperator{rows: rows}

	spec := &PS.WindowSpec{
		PartitionBy: []PS.Expr{},
		OrderBy: []PS.OrderItem{
			{Expr: &PS.Ident{Name: "val"}, Desc: false},
		},
		Frame: &PS.WindowFrame{
			Type:  "RANGE",
			Start: PS.FrameBound{Type: "UNBOUNDED_PRECEDING"},
			End:   PS.FrameBound{Type: "CURRENT_ROW"},
		},
	}

	op := NewWindowOperator(input, "SUM", []PS.Expr{&PS.Ident{Name: "val"}}, spec, []string{"id", "val"})

	// RANGE UNBOUNDED PRECEDING to CURRENT ROW:
	// Row 1 (val=1): peers=[1,1], SUM=2
	// Row 2 (val=1): peers=[1,1], SUM=2
	// Row 3 (val=2): peers=[1,1,2], SUM=4
	// Row 4 (val=3): peers=[1,1,2,3,3], SUM=10
	// Row 5 (val=3): peers=[1,1,2,3,3], SUM=10
	// Note: WindowOperator.Next() reuses outData buffer, so we must
	// check values during iteration, not after collecting all rows.
	// SUM returns float64.
	expected := []float64{2, 2, 4, 10, 10}
	for i, want := range expected {
		row, err := op.Next(context.Background())
		if err != nil {
			t.Fatalf("row %d: %v", i, err)
		}
		got, ok := row.Data[len(row.Data)-1].ToAny().(float64)
		if !ok {
			t.Fatalf("row %d: expected float64, got %s", i, row.Data[len(row.Data)-1].Kind)
		}
		if got != want {
			t.Errorf("row %d: got %v, want %v", i, got, want)
		}
	}
	_, err := op.Next(context.Background())
	if err != ErrNoRows {
		t.Errorf("expected ErrNoRows, got %v", err)
	}
}
