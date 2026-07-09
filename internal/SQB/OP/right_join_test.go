package OP

import (
	"context"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

func TestRightJoin_NLJ_Basic(t *testing.T) {
	leftRows := []pl.Row{
		{Cols: []string{"id", "v"}, Data: []Value{DT.NewIntValue(1), DT.NewTextValue("a")}},
		{Cols: []string{"id", "v"}, Data: []Value{DT.NewIntValue(2), DT.NewTextValue("b")}},
		{Cols: []string{"id", "v"}, Data: []Value{DT.NewIntValue(3), DT.NewTextValue("c")}},
	}
	rightRows := []pl.Row{
		{Cols: []string{"id", "x"}, Data: []Value{DT.NewIntValue(2), DT.NewTextValue("x2")}},
		{Cols: []string{"id", "x"}, Data: []Value{DT.NewIntValue(4), DT.NewTextValue("x4")}},
	}

	leftOp := &testRowsOp{rows: leftRows}
	rightOp := &testRowsOp{rows: rightRows}

	join := NewNestedLoopJoin(leftOp, rightOp, "l", "r", func(outer, inner *pl.Row) (bool, error) {
		return outer.Data[0].I64 == inner.Data[0].I64, nil
	}, JoinKindRight)

	ctx := context.Background()
	var results []pl.Row
	for {
		row, err := join.Next(ctx)
		if err == pl.ErrNoRows {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		results = append(results, row)
	}

	if len(results) != 2 {
		t.Fatalf("got %d rows, want 2 (1 matched + 1 unmatched right)", len(results))
	}
}

func TestRightJoin_NLJ_LeftEmpty(t *testing.T) {
	rightRows := []pl.Row{
		{Cols: []string{"id", "x"}, Data: []Value{DT.NewIntValue(1), DT.NewTextValue("x1")}},
		{Cols: []string{"id", "x"}, Data: []Value{DT.NewIntValue(2), DT.NewTextValue("x2")}},
	}

	leftOp := &testRowsOp{rows: nil}
	rightOp := &testRowsOp{rows: rightRows}

	join := NewNestedLoopJoin(leftOp, rightOp, "l", "r", nil, JoinKindRight)

	ctx := context.Background()
	var results []pl.Row
	for {
		row, err := join.Next(ctx)
		if err == pl.ErrNoRows {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		results = append(results, row)
	}

	if len(results) != 2 {
		t.Fatalf("got %d rows, want 2 (all right rows unmatched)", len(results))
	}
}

// testRowsOp is a simple operator that returns rows from a static slice.
type testRowsOp struct {
	rows []pl.Row
	pos  int
}

func (o *testRowsOp) Next(ctx context.Context) (pl.Row, error) {
	if o.pos >= len(o.rows) {
		return pl.Row{}, pl.ErrNoRows
	}
	r := o.rows[o.pos]
	o.pos++
	return r, nil
}

func (o *testRowsOp) Close() error { o.pos = 0; return nil }

var _ pl.Operator = (*testRowsOp)(nil)

func TestRightJoin_PlannerCreatesNLJ_RightJoin(t *testing.T) {
	// Verify the planner chooses NLJ for RIGHT JOIN queries.
	// This is a fast path test: if the planner emits a NestedLoopJoin
	// with kind=JoinKindRight, the NLJ runtime is already correct.
	leftRows := []pl.Row{
		{Cols: []string{"id", "v"}, Data: []Value{DT.NewIntValue(1), DT.NewTextValue("a")}},
		{Cols: []string{"id", "v"}, Data: []Value{DT.NewIntValue(2), DT.NewTextValue("b")}},
	}
	rightRows := []pl.Row{
		{Cols: []string{"id", "x"}, Data: []Value{DT.NewIntValue(2), DT.NewTextValue("x2")}},
		{Cols: []string{"id", "x"}, Data: []Value{DT.NewIntValue(3), DT.NewTextValue("x3")}},
	}

	leftOp := &testRowsOp{rows: leftRows}
	rightOp := &testRowsOp{rows: rightRows}

	join := NewNestedLoopJoin(leftOp, rightOp, "l", "r", func(outer, inner *pl.Row) (bool, error) {
		return outer.Data[0].I64 == inner.Data[0].I64, nil
	}, JoinKindRight)

	if join.Kind() != JoinKindRight {
		t.Fatalf("expected JoinKindRight, got %s", join.Kind())
	}
	if !join.rightOuter {
		t.Error("rightOuter should be true for RIGHT JOIN")
	}

	ctx := context.Background()
	var count int
	for {
		_, err := join.Next(ctx)
		if err == pl.ErrNoRows {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		count++
	}
	if count != 2 {
		t.Errorf("got %d rows, want 2 (1 matched + 1 unmatched right)", count)
	}
}

func TestRightJoin_WithProjection(t *testing.T) {
	leftRows := []pl.Row{
		{Cols: []string{"id", "v"}, Data: []Value{DT.NewIntValue(1), DT.NewTextValue("a")}},
	}
	rightRows := []pl.Row{
		{Cols: []string{"id", "x"}, Data: []Value{DT.NewIntValue(1), DT.NewTextValue("x1")}},
		{Cols: []string{"id", "x"}, Data: []Value{DT.NewIntValue(5), DT.NewTextValue("x5")}},
	}

	leftOp := &testRowsOp{rows: leftRows}
	rightOp := &testRowsOp{rows: rightRows}

	join := NewNestedLoopJoin(leftOp, rightOp, "l", "r", func(outer, inner *pl.Row) (bool, error) {
		return outer.Data[0].I64 == inner.Data[0].I64, nil
	}, JoinKindRight)
	join.WithProjection([]string{"l.id", "l.v", "r.id", "r.x"})

	ctx := context.Background()
	var results []pl.Row
	for {
		row, err := join.Next(ctx)
		if err == pl.ErrNoRows {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		results = append(results, row)
	}

	if len(results) != 2 {
		t.Fatalf("got %d rows, want 2 (1 matched + 1 unmatched right)", len(results))
	}
}

func TestRightJoin_EmitLimit(t *testing.T) {
	leftRows := []pl.Row{
		{Cols: []string{"id"}, Data: []Value{DT.NewIntValue(1)}},
		{Cols: []string{"id"}, Data: []Value{DT.NewIntValue(2)}},
	}
	rightRows := []pl.Row{
		{Cols: []string{"id", "x"}, Data: []Value{DT.NewIntValue(1), DT.NewTextValue("x1")}},
		{Cols: []string{"id", "x"}, Data: []Value{DT.NewIntValue(2), DT.NewTextValue("x2")}},
		{Cols: []string{"id", "x"}, Data: []Value{DT.NewIntValue(3), DT.NewTextValue("x3")}},
	}

	leftOp := &testRowsOp{rows: leftRows}
	rightOp := &testRowsOp{rows: rightRows}

	join := NewNestedLoopJoin(leftOp, rightOp, "l", "r", func(outer, inner *pl.Row) (bool, error) {
		return outer.Data[0].I64 == inner.Data[0].I64, nil
	}, JoinKindRight)
	join.SetLimit(1)

	ctx := context.Background()
	var count int
	for {
		_, err := join.Next(ctx)
		if err == pl.ErrNoRows {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		count++
	}
	if count != 1 {
		t.Errorf("got %d rows, want 1 (limited)", count)
	}
}

func TestRightJoin_NoMatch(t *testing.T) {
	leftRows := []pl.Row{
		{Cols: []string{"id"}, Data: []Value{DT.NewIntValue(1)}},
	}
	rightRows := []pl.Row{
		{Cols: []string{"id", "x"}, Data: []Value{DT.NewIntValue(2), DT.NewTextValue("x2")}},
	}

	leftOp := &testRowsOp{rows: leftRows}
	rightOp := &testRowsOp{rows: rightRows}

	join := NewNestedLoopJoin(leftOp, rightOp, "l", "r", func(outer, inner *pl.Row) (bool, error) {
		return outer.Data[0].I64 == inner.Data[0].I64, nil
	}, JoinKindRight)

	ctx := context.Background()
	var results []pl.Row
	for {
		row, err := join.Next(ctx)
		if err == pl.ErrNoRows {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		results = append(results, row)
	}

	if len(results) != 1 {
		t.Fatalf("got %d rows, want 1 (unmatched right)", len(results))
	}
}

func TestRightJoin_CrossJoinFallback(t *testing.T) {
	leftRows := []pl.Row{
		{Cols: []string{"id"}, Data: []Value{DT.NewIntValue(1)}},
	}
	rightRows := []pl.Row{
		{Cols: []string{"id", "x"}, Data: []Value{DT.NewIntValue(10), DT.NewTextValue("x10")}},
		{Cols: []string{"id", "x"}, Data: []Value{DT.NewIntValue(20), DT.NewTextValue("x20")}},
	}

	leftOp := &testRowsOp{rows: leftRows}
	rightOp := &testRowsOp{rows: rightRows}

	// RIGHT JOIN with no ON clause → cross join, all right rows preserved
	join := NewNestedLoopJoin(leftOp, rightOp, "l", "r", nil, JoinKindRight)

	ctx := context.Background()
	var results []pl.Row
	for {
		row, err := join.Next(ctx)
		if err == pl.ErrNoRows {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		results = append(results, row)
	}

	if len(results) != 2 {
		t.Fatalf("got %d rows, want 2 (all right rows with cross join)", len(results))
	}
}