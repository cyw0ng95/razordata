package OP

import (
	"context"
	"testing"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
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

	// Wrap with batch adapter to use NextBatch
	batchOp := UT.NewRowOperatorAdapter(mj)
	var totalRows int
	for {
		batch, err := batchOp.NextBatch(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if batch == nil {
			break
		}
		totalRows += batch.Size
		batch.Put()
	}
	// Matches: (2,2), (2,3), (3,3) — left.k=2 right=[2,3]; left.k=3 right=[3,4]
	// Expected pairs: (2,2), (3,3).
	if totalRows != 2 {
		t.Fatalf("expected 2 matched rows, got %d", totalRows)
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

	batchOp := UT.NewRowOperatorAdapter(mj)
	count := 0
	for {
		batch, err := batchOp.NextBatch(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if batch == nil {
			break
		}
		count += batch.Size
		batch.Put()
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

	batchOp := UT.NewRowOperatorAdapter(mj)
	count := 0
	for {
		batch, err := batchOp.NextBatch(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if batch == nil {
			break
		}
		count += batch.Size
		batch.Put()
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

	batchOp := UT.NewRowOperatorAdapter(mj)
	count := 0
	for {
		batch, err := batchOp.NextBatch(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if batch == nil {
			break
		}
		count += batch.Size
		batch.Put()
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

	batchOp := UT.NewRowOperatorAdapter(mj)
	count := 0
	for {
		batch, err := batchOp.NextBatch(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if batch == nil {
			break
		}
		count += batch.Size
		batch.Put()
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

// NextBatch returns a single-row batch from the internal row slice.
// This allows sortedRowsOp to satisfy BatchProducer for tests that
// want to verify batch-based consumption paths.
func (s *sortedRowsOp) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if s.done || s.pos >= len(s.rows) {
		s.done = true
		return nil, nil
	}
	r := s.rows[s.pos]
	s.pos++

	n := len(r.Cols)
	b := UT.GetBatch(n)
	for i, name := range r.Cols {
		b.SetColumnName(i, name)
	}
	for i, v := range r.Data {
		isNull := v.Kind == pl.KindNull
		var raw any
		if !isNull {
			switch v.Kind {
			case pl.KindInt:
				raw = v.I64
			case pl.KindFloat:
				raw = v.F64
			case pl.KindText:
				raw = v.S
			case pl.KindBool:
				raw = v.Bo
			}
		}
		typ := LX.T_INT_KW
		if i < len(r.Types) {
			typ = r.Types[i]
		}
		b.AppendRow(i, typ, raw, isNull)
	}
	b.AdvanceSize()
	return b, nil
}

func (s *sortedRowsOp) Close() error {
	return nil
}

// NewIntValue is a thin wrapper that avoids importing the larger
// engine package just for a value constructor.
func NewIntValue(v int64) pl.Value {
	return pl.Value{Kind: pl.KindInt, I64: v}
}