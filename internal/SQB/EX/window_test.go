package EX

import (
	"context"
	"testing"

	AG "github.com/cyw0ng95/razordata/internal/SQB/AG"
	"github.com/cyw0ng95/razordata/internal/SQB/DT"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
)

type staticOperator struct {
	rows []pl.Row
	idx  int
}

func (s *staticOperator) Next(_ context.Context) (pl.Row, error) {
	if s.idx >= len(s.rows) {
		return pl.Row{}, DT.ErrNoRows
	}
	r := s.rows[s.idx]
	s.idx++
	return r, nil
}

func (s *staticOperator) Close() error { return nil }

func makeRow(cols []string, data []any) DT.Row {
	return pl.Row{Cols: cols, Data: valueFromAnySlice(data, nil)}
}

func TestWindow_RowNumber(t *testing.T) {
	input := &staticOperator{}
	input.rows = []pl.Row{
		makeRow([]string{"id"}, []any{int64(1)}),
		makeRow([]string{"id"}, []any{int64(2)}),
		makeRow([]string{"id"}, []any{int64(3)}),
	}

	spec := &PS.WindowSpec{
		OrderBy: []PS.OrderItem{{Expr: &PS.Ident{Name: "id"}}},
	}
	op :=
		AG.NewWindowOperator(input, "ROW_NUMBER", nil, spec, []string{"id"})

	expected := []int64{1, 2, 3}
	for i, want := range expected {
		row, err := op.Next(context.Background())
		if err != nil {
			t.Fatalf("row %d: %v", i, err)
		}
		got := row.Data[len(row.Data)-1]
		if !got.Equal(NewIntValue(want)) {
			t.Errorf("row %d: got %v, want %v", i, got, want)
		}
	}
	_, err := op.Next(context.Background())
	if err != DT.ErrNoRows {
		t.Errorf("expected ErrNoRows, got %v", err)
	}
}

func TestWindow_DenseRank(t *testing.T) {
	input := &staticOperator{}
	input.rows = []pl.Row{
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
	op :=
		AG.NewWindowOperator(input, "DENSE_RANK", nil, spec, []string{"score"})

	expected := []int64{1, 2, 2, 3}
	for i, want := range expected {
		row, err := op.Next(context.Background())
		if err != nil {
			t.Fatalf("row %d: %v", i, err)
		}
		got := row.Data[len(row.Data)-1]
		if !got.Equal(NewIntValue(want)) {
			t.Errorf("row %d: got %v, want %v", i, got, want)
		}
	}
}

func TestWindow_Partition(t *testing.T) {
	input := &staticOperator{}
	input.rows = []pl.Row{
		makeRow([]string{"dept", "salary"}, []any{"eng", int64(100)}),
		makeRow([]string{"dept", "salary"}, []any{"eng", int64(200)}),
		makeRow([]string{"dept", "salary"}, []any{"sales", int64(150)}),
		makeRow([]string{"dept", "salary"}, []any{"sales", int64(150)}),
	}

	spec := &PS.WindowSpec{
		PartitionBy: []PS.Expr{&PS.Ident{Name: "dept"}},
		OrderBy:     []PS.OrderItem{{Expr: &PS.Ident{Name: "salary"}}},
	}
	op :=
		AG.NewWindowOperator(input, "ROW_NUMBER", nil, spec, []string{"dept", "salary"})

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
	input.rows = []pl.Row{
		makeRow([]string{"val"}, []any{int64(10)}),
		makeRow([]string{"val"}, []any{int64(20)}),
		makeRow([]string{"val"}, []any{int64(30)}),
	}

	spec := &PS.WindowSpec{
		OrderBy: []PS.OrderItem{{Expr: &PS.Ident{Name: "val"}}},
	}
	op :=
		AG.NewWindowOperator(input, "LAG", []PS.Expr{&PS.Ident{Name: "val"}}, spec, []string{"val"})

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
	input.rows = []pl.Row{
		makeRow([]string{"val"}, []any{int64(10)}),
		makeRow([]string{"val"}, []any{int64(20)}),
		makeRow([]string{"val"}, []any{int64(30)}),
	}

	spec := &PS.WindowSpec{
		OrderBy: []PS.OrderItem{{Expr: &PS.Ident{Name: "val"}}},
	}
	op :=
		AG.NewWindowOperator(input, "LEAD", []PS.Expr{&PS.Ident{Name: "val"}}, spec, []string{"val"})

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
	op :=
		AG.NewWindowOperator(input, "ROW_NUMBER", nil, spec, []string{})
	_, err := op.Next(context.Background())
	if err != DT.ErrNoRows {
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
	rows := []pl.Row{
		{Cols: []string{"id", "val"}, Data: valueFromAnySlice([]any{int64(1), int64(1)}, nil)},
		{Cols: []string{"id", "val"}, Data: valueFromAnySlice([]any{int64(2), int64(1)}, nil)},
		{Cols: []string{"id", "val"}, Data: valueFromAnySlice([]any{int64(3), int64(2)}, nil)},
		{Cols: []string{"id", "val"}, Data: valueFromAnySlice([]any{int64(4), int64(3)}, nil)},
		{Cols: []string{"id", "val"}, Data: valueFromAnySlice([]any{int64(5), int64(3)}, nil)},
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

	op :=
		AG.NewWindowOperator(input, "SUM", []PS.Expr{&PS.Ident{Name: "val"}}, spec, []string{"id", "val"})

	// RANGE UNBOUNDED PRECEDING to CURRENT ROW:
	// DT.Row 1 (val=1): peers=[1,1], SUM=2
	// DT.Row 2 (val=1): peers=[1,1], SUM=2
	// DT.Row 3 (val=2): peers=[1,1,2], SUM=4
	// DT.Row 4 (val=3): peers=[1,1,2,3,3], SUM=10
	// DT.Row 5 (val=3): peers=[1,1,2,3,3], SUM=10
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
	if err != DT.ErrNoRows {
		t.Errorf("expected ErrNoRows, got %v", err)
	}
}

// REQ001348: PERCENT_RANK = (rank-1)/(rows-1).
// Ties share a rank; per-row ties collapse to zero.
func TestWindow_PercentRank(t *testing.T) {
	input := &staticOperator{}
	input.rows = []pl.Row{
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
	op := AG.NewWindowOperator(input, "PERCENT_RANK", nil, spec, []string{"score"})
	// ranks: 1, 2, 2, 4 → percent_rank: 0/3, 1/3, 1/3, 3/3
	expected := []float64{0, 1.0 / 3, 1.0 / 3, 1.0}
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
}

// REQ001349: CUME_DIST = (# value <= current) / total.
func TestWindow_CumeDist(t *testing.T) {
	input := &staticOperator{}
	input.rows = []pl.Row{
		makeRow([]string{"v"}, []any{int64(10)}),
		makeRow([]string{"v"}, []any{int64(20)}),
		makeRow([]string{"v"}, []any{int64(20)}),
		makeRow([]string{"v"}, []any{int64(30)}),
	}
	for i := range input.rows {
		input.rows[i].Cols = []string{"v"}
	}
	spec := &PS.WindowSpec{
		OrderBy: []PS.OrderItem{{Expr: &PS.Ident{Name: "v"}}},
	}
	op := AG.NewWindowOperator(input, "CUME_DIST", nil, spec, []string{"v"})
	// 10 → 1/4, 20/20 → 3/4, 30 → 4/4
	expected := []float64{0.25, 0.75, 0.75, 1.0}
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
}

// REQ001350: NTILE(n) distributes rows into n buckets evenly.
func TestWindow_Ntile(t *testing.T) {
	input := &staticOperator{}
	input.rows = []pl.Row{
		makeRow([]string{"id"}, []any{int64(1)}),
		makeRow([]string{"id"}, []any{int64(2)}),
		makeRow([]string{"id"}, []any{int64(3)}),
		makeRow([]string{"id"}, []any{int64(4)}),
		makeRow([]string{"id"}, []any{int64(5)}),
	}
	for i := range input.rows {
		input.rows[i].Cols = []string{"id"}
	}
	spec := &PS.WindowSpec{
		OrderBy: []PS.OrderItem{{Expr: &PS.Ident{Name: "id"}}},
	}
	op := AG.NewWindowOperator(input, "NTILE",
		[]PS.Expr{&PS.NumberLiteral{Val: 3}},
		spec, []string{"id"})
	// ceil(rank * 3 / 5): rank 1→1, 2→2, 3→2, 4→3, 5→3
	expected := []int64{1, 2, 2, 3, 3}
	for i, want := range expected {
		row, err := op.Next(context.Background())
		if err != nil {
			t.Fatalf("row %d: %v", i, err)
		}
		got, ok := row.Data[len(row.Data)-1].ToAny().(int64)
		if !ok {
			t.Fatalf("row %d: expected int64, got %s", i, row.Data[len(row.Data)-1].Kind)
		}
		if got != want {
			t.Errorf("row %d: got %v, want %v", i, got, want)
		}
	}
}

// REQ001351: FIRST_VALUE over the default (whole-partition) frame.
func TestWindow_FirstValue(t *testing.T) {
	input := &staticOperator{}
	input.rows = []pl.Row{
		makeRow([]string{"v"}, []any{int64(10)}),
		makeRow([]string{"v"}, []any{int64(20)}),
		makeRow([]string{"v"}, []any{int64(30)}),
	}
	for i := range input.rows {
		input.rows[i].Cols = []string{"v"}
	}
	spec := &PS.WindowSpec{
		OrderBy: []PS.OrderItem{{Expr: &PS.Ident{Name: "v"}}},
	}
	op := AG.NewWindowOperator(input, "FIRST_VALUE",
		[]PS.Expr{&PS.Ident{Name: "v"}}, spec, []string{"v"})
	for i := 0; i < 3; i++ {
		row, err := op.Next(context.Background())
		if err != nil {
			t.Fatalf("row %d: %v", i, err)
		}
		if got, _ := row.Data[len(row.Data)-1].ToAny().(int64); got != 10 {
			t.Errorf("row %d: got %v, want 10", i, got)
		}
	}
}

// REQ001352: NTH_VALUE returns the value at the n-th position in the frame.
func TestWindow_NthValue(t *testing.T) {
	makeInput := func() *staticOperator {
		in := &staticOperator{}
		in.rows = []pl.Row{
			makeRow([]string{"v"}, []any{int64(10)}),
			makeRow([]string{"v"}, []any{int64(20)}),
			makeRow([]string{"v"}, []any{int64(30)}),
		}
		for i := range in.rows {
			in.rows[i].Cols = []string{"v"}
		}
		return in
	}
	spec := &PS.WindowSpec{
		OrderBy: []PS.OrderItem{{Expr: &PS.Ident{Name: "v"}}},
	}
	t.Run("in_range", func(t *testing.T) {
		op := AG.NewWindowOperator(makeInput(), "NTH_VALUE",
			[]PS.Expr{&PS.Ident{Name: "v"}, &PS.NumberLiteral{Val: 2}},
			spec, []string{"v"})
		row, err := op.Next(context.Background())
		if err != nil {
			t.Fatalf("row 0: %v", err)
		}
		// All rows see the 2nd value (20) because the default frame is the
		// whole partition.
		if got, _ := row.Data[len(row.Data)-1].ToAny().(int64); got != 20 {
			t.Errorf("row 0: got %v, want 20", got)
		}
	})
	t.Run("out_of_range", func(t *testing.T) {
		op := AG.NewWindowOperator(makeInput(), "NTH_VALUE",
			[]PS.Expr{&PS.Ident{Name: "v"}, &PS.NumberLiteral{Val: 5}},
			spec, []string{"v"})
		row, err := op.Next(context.Background())
		if err != nil {
			t.Fatalf("row 0: %v", err)
		}
		if row.Data[len(row.Data)-1].ToAny() != nil {
			t.Errorf("row 0: expected NULL, got %v", row.Data[len(row.Data)-1])
		}
	})
}

// REQ001353: GROUPS frame unit — offsets count peer groups.
func TestWindow_GroupsFrame(t *testing.T) {
	input := &staticOperator{}
	input.rows = []pl.Row{
		makeRow([]string{"v"}, []any{int64(10)}),
		makeRow([]string{"v"}, []any{int64(10)}),
		makeRow([]string{"v"}, []any{int64(20)}),
		makeRow([]string{"v"}, []any{int64(20)}),
	}
	for i := range input.rows {
		input.rows[i].Cols = []string{"v"}
	}
	spec := &PS.WindowSpec{
		OrderBy: []PS.OrderItem{{Expr: &PS.Ident{Name: "v"}}},
		Frame: &PS.WindowFrame{
			Type:  "GROUPS",
			Start: PS.FrameBound{Type: "PRECEDING", Offset: &PS.NumberLiteral{Val: 1}},
			End:   PS.FrameBound{Type: "CURRENT_ROW"},
		},
	}
	op := AG.NewWindowOperator(input, "SUM",
		[]PS.Expr{&PS.Ident{Name: "v"}}, spec, []string{"v"})
	// Group1=[10,10] sum=20; Group2=[20,20] sum=40.
	// GROUPS 1 PRECEDING..CURRENT_ROW: rows in {prev group, current group}.
	expected := []float64{20, 20, 60, 60}
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
}

// REQ001354: EXCLUDE CURRENT_ROW inside a window frame.
func TestWindow_ExcludeCurrentRow(t *testing.T) {
	input := &staticOperator{}
	input.rows = []pl.Row{
		makeRow([]string{"v"}, []any{int64(10)}),
		makeRow([]string{"v"}, []any{int64(20)}),
	}
	for i := range input.rows {
		input.rows[i].Cols = []string{"v"}
	}
	spec := &PS.WindowSpec{
		OrderBy: []PS.OrderItem{{Expr: &PS.Ident{Name: "v"}}},
		Frame: &PS.WindowFrame{
			Type:    "ROWS",
			Start:   PS.FrameBound{Type: "UNBOUNDED_PRECEDING"},
			End:     PS.FrameBound{Type: "UNBOUNDED_FOLLOWING"},
			Exclude: "CURRENT_ROW",
		},
	}
	op := AG.NewWindowOperator(input, "FIRST_VALUE",
		[]PS.Expr{&PS.Ident{Name: "v"}}, spec, []string{"v"})
	row, err := op.Next(context.Background())
	if err != nil {
		t.Fatalf("row 0: %v", err)
	}
	// For row 0 (v=10): excluding CURRENT_ROW leaves only row 1 in the
	// frame, so FIRST_VALUE returns 20.
	if got, _ := row.Data[len(row.Data)-1].ToAny().(int64); got != 20 {
		t.Errorf("row 0: got %v, want 20", got)
	}
}
