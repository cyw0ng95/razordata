package OP

import (
	"context"
	"testing"

	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

// TestMergeJoin_Basic verifies REQ001102: an inner sort-merge join
// over two pre-sorted inputs emits the cartesian product of rows
// sharing the same key.
func TestMergeJoin_Basic(t *testing.T) {
	left := newSortedRowsOp([]string{"k"}, []pl.Row{
		{Cols: []string{"k"}, Data: []pl.Value{NewIntValue(1)}},
		{Cols: []string{"k"}, Data: []pl.Value{NewIntValue(2)}},
		{Cols: []string{"k"}, Data: []pl.Value{NewIntValue(3)}},
	})
	right := newSortedRowsOp([]string{"k"}, []pl.Row{
		{Cols: []string{"k"}, Data: []pl.Value{NewIntValue(2)}},
		{Cols: []string{"k"}, Data: []pl.Value{NewIntValue(3)}},
		{Cols: []string{"k"}, Data: []pl.Value{NewIntValue(4)}},
	})
	mj := NewMergeJoin(left, right, "l", "r", []string{"k"}, []string{"k"})
	defer mj.Close()
	ctx := context.Background()
	var rows []pl.Row
	for {
		r, err := mj.Next(ctx)
		if err != nil {
			break
		}
		rows = append(rows, r)
	}
	// Matches: (2,2), (2,3), (3,3) — left.k=2 right=[2,3]; left.k=3 right=[3,4]
	// Expected pairs: (2,2), (3,3).
	if len(rows) != 2 {
		t.Fatalf("expected 2 matched rows, got %d", len(rows))
	}
}

func TestMergeJoin_LeftOuter(t *testing.T) {
	left := newSortedRowsOp([]string{"k"}, []pl.Row{
		{Cols: []string{"k"}, Data: []pl.Value{NewIntValue(1)}},
		{Cols: []string{"k"}, Data: []pl.Value{NewIntValue(2)}},
		{Cols: []string{"k"}, Data: []pl.Value{NewIntValue(3)}},
	})
	right := newSortedRowsOp([]string{"k"}, []pl.Row{
		{Cols: []string{"k"}, Data: []pl.Value{NewIntValue(2)}},
		{Cols: []string{"k"}, Data: []pl.Value{NewIntValue(4)}},
	})
	mj := NewMergeJoin(left, right, "l", "r", []string{"k"}, []string{"k"}).WithKind(JoinKindLeft)
	defer mj.Close()
	ctx := context.Background()
	count := 0
	for {
		_, err := mj.Next(ctx)
		if err != nil {
			break
		}
		count++
	}
	// left:1 unmatched, 2 matches 2, 3 unmatched.
	if count != 3 {
		t.Fatalf("expected 3 rows (LEFT OUTER), got %d", count)
	}
}

func TestMergeJoin_RightOuter(t *testing.T) {
	left := newSortedRowsOp([]string{"k"}, []pl.Row{
		{Cols: []string{"k"}, Data: []pl.Value{NewIntValue(1)}},
		{Cols: []string{"k"}, Data: []pl.Value{NewIntValue(2)}},
	})
	right := newSortedRowsOp([]string{"k"}, []pl.Row{
		{Cols: []string{"k"}, Data: []pl.Value{NewIntValue(2)}},
		{Cols: []string{"k"}, Data: []pl.Value{NewIntValue(3)}},
	})
	mj := NewMergeJoin(left, right, "l", "r", []string{"k"}, []string{"k"}).WithKind(JoinKindRight)
	defer mj.Close()
	ctx := context.Background()
	count := 0
	for {
		_, err := mj.Next(ctx)
		if err != nil {
			break
		}
		count++
	}
	// right:2 matches 2, 3 unmatched.
	if count != 2 {
		t.Fatalf("expected 2 rows (RIGHT OUTER), got %d", count)
	}
}

func TestMergeJoin_FullOuter(t *testing.T) {
	left := newSortedRowsOp([]string{"k"}, []pl.Row{
		{Cols: []string{"k"}, Data: []pl.Value{NewIntValue(1)}},
		{Cols: []string{"k"}, Data: []pl.Value{NewIntValue(2)}},
	})
	right := newSortedRowsOp([]string{"k"}, []pl.Row{
		{Cols: []string{"k"}, Data: []pl.Value{NewIntValue(2)}},
		{Cols: []string{"k"}, Data: []pl.Value{NewIntValue(3)}},
	})
	mj := NewMergeJoin(left, right, "l", "r", []string{"k"}, []string{"k"}).WithKind(JoinKindFull)
	defer mj.Close()
	ctx := context.Background()
	count := 0
	for {
		_, err := mj.Next(ctx)
		if err != nil {
			break
		}
		count++
	}
	// FULL OUTER: 1 unmatched, 2 matched, 3 unmatched.
	if count != 3 {
		t.Fatalf("expected 3 rows (FULL OUTER), got %d", count)
	}
}

func TestMergeJoin_MultiKey(t *testing.T) {
	left := newSortedRowsOp([]string{"a", "b"}, []pl.Row{
		{Cols: []string{"a", "b"}, Data: []pl.Value{NewIntValue(1), NewIntValue(1)}},
		{Cols: []string{"a", "b"}, Data: []pl.Value{NewIntValue(1), NewIntValue(2)}},
		{Cols: []string{"a", "b"}, Data: []pl.Value{NewIntValue(2), NewIntValue(1)}},
	})
	right := newSortedRowsOp([]string{"a", "b"}, []pl.Row{
		{Cols: []string{"a", "b"}, Data: []pl.Value{NewIntValue(1), NewIntValue(1)}},
		{Cols: []string{"a", "b"}, Data: []pl.Value{NewIntValue(1), NewIntValue(3)}},
		{Cols: []string{"a", "b"}, Data: []pl.Value{NewIntValue(2), NewIntValue(1)}},
	})
	mj := NewMergeJoin(left, right, "l", "r", []string{"a", "b"}, []string{"a", "b"})
	defer mj.Close()
	ctx := context.Background()
	count := 0
	for {
		_, err := mj.Next(ctx)
		if err != nil {
			break
		}
		count++
	}
	// Matches: (1,1)-(1,1), (2,1)-(2,1) = 2 pairs.
	if count != 2 {
		t.Fatalf("expected 2 multi-key matches, got %d", count)
	}
}

func TestMergeJoin_Close(t *testing.T) {
	left := newSortedRowsOp([]string{"k"}, []pl.Row{
		{Cols: []string{"k"}, Data: []pl.Value{NewIntValue(1)}},
	})
	right := newSortedRowsOp([]string{"k"}, []pl.Row{
		{Cols: []string{"k"}, Data: []pl.Value{NewIntValue(1)}},
	})
	mj := NewMergeJoin(left, right, "l", "r", []string{"k"}, []string{"k"})
	if err := mj.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestIsJoinOp_IncludesMergeJoin(t *testing.T) {
	// REQ000843/REQ001102: isJoinOp must recognize MergeJoin so
	// tryHashCrossJoin skips it (the bushy group already materialized
	// the right side).
	left := newSortedRowsOp([]string{"k"}, []pl.Row{
		{Cols: []string{"k"}, Data: []pl.Value{NewIntValue(1)}},
	})
	right := newSortedRowsOp([]string{"k"}, []pl.Row{
		{Cols: []string{"k"}, Data: []pl.Value{NewIntValue(1)}},
	})
	mj := NewMergeJoin(left, right, "l", "r", []string{"k"}, []string{"k"})
	if !isJoinOp(mj) {
		t.Fatal("isJoinOp must recognize MergeJoin")
	}
}

// sortedRowsOp is a test-only operator that emits a fixed slice of
// pre-sorted rows. Used as left/right inputs to MergeJoin tests.
type sortedRowsOp struct {
	rows []pl.Row
	pos  int
	done bool
}

func newSortedRowsOp(cols []string, rows []pl.Row) *sortedRowsOp {
	// Stamp Cols/Types onto each row.
	for i := range rows {
		if rows[i].Cols == nil {
			rows[i].Cols = append([]string(nil), cols...)
		}
	}
	return &sortedRowsOp{rows: rows}
}

func (s *sortedRowsOp) Next(ctx context.Context) (pl.Row, error) {
	if s.done {
		return pl.Row{}, ErrNoRows
	}
	if s.pos >= len(s.rows) {
		s.done = true
		return pl.Row{}, ErrNoRows
	}
	r := s.rows[s.pos]
	s.pos++
	return r, nil
}

func (s *sortedRowsOp) Close() error {
	return nil
}

// NewIntValue is a thin wrapper that avoids importing the larger
// engine package just for a value constructor.
func NewIntValue(v int64) pl.Value {
	return pl.Value{Kind: pl.KindInt, I64: v}
}
