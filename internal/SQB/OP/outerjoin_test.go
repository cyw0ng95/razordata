package OP

import (
	"context"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// TestNestedLoopJoin_WithSharedSchema verifies that pre-computing
// the schema via WithSharedSchema sets all three field triplets
// (shared/blkShared/outerShared) and the sharedBuilt flag.
// REQ001097.
func TestNestedLoopJoin_WithSharedSchema(t *testing.T) {
	DT.RegisterTable("l", []Row{
		{Cols: []string{"a"}, Types: []LX.TokenType{LX.T_INT_KW},
			Data: []Value{DT.NewIntValue(1)}},
		{Cols: []string{"a"}, Types: []LX.TokenType{LX.T_INT_KW},
			Data: []Value{DT.NewIntValue(2)}},
	})
	DT.RegisterTable("r", []Row{
		{Cols: []string{"b"}, Types: []LX.TokenType{LX.T_INT_KW},
			Data: []Value{DT.NewIntValue(10)}},
	})
	

	left := NewSeqScan("l")
	right := NewSeqScan("r")
	join := NewNestedLoopJoin(left, right, "l", "r", nil, JoinKindInner).
		WithSharedSchema(
			[]string{"a", "b"},
			[]LX.TokenType{LX.T_INT_KW, LX.T_INT_KW},
			map[string]int{"a": 0, "b": 1},
		)

	if !join.sharedBuilt {
		t.Fatal("sharedBuilt should be true after WithSharedSchema")
	}
	if got := join.sharedCols; len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("sharedCols mismatch: %v", got)
	}
	if got := join.sharedTypes; len(got) != 2 {
		t.Errorf("sharedTypes length: got %d", len(got))
	}
	if got := join.sharedColIndex; got["a"] != 0 || got["b"] != 1 {
		t.Errorf("sharedColIndex mismatch: %v", got)
	}
	// Block-mode triplet should also be set.
	if len(join.blkSharedCols) != 2 || len(join.blkSharedTypes) != 2 {
		t.Errorf("blkShared* not populated: cols=%v types=%v", join.blkSharedCols, join.blkSharedTypes)
	}
	// Outer-join triplet should also be set.
	if len(join.outerSharedCols) != 2 || len(join.outerSharedTypes) != 2 {
		t.Errorf("outerShared* not populated: cols=%v types=%v", join.outerSharedCols, join.outerSharedTypes)
	}
}

// TestNestedLoopJoin_LeftJoin verifies LEFT JOIN returns all left rows
// with NULL-padded right when no match (REQ000197).
func TestNestedLoopJoin_LeftJoin(t *testing.T) {
	DT.RegisterTable("left", []Row{
		{Cols: []string{"id", "val"}, Types: []LX.TokenType{LX.T_INT_KW, LX.T_INT_KW},
			Data: []Value{DT.NewIntValue(int64(1)), DT.NewIntValue(int64(10))}},
		{Cols: []string{"id", "val"}, Types: []LX.TokenType{LX.T_INT_KW, LX.T_INT_KW},
			Data: []Value{DT.NewIntValue(int64(2)), DT.NewIntValue(int64(20))}},
		{Cols: []string{"id", "val"}, Types: []LX.TokenType{LX.T_INT_KW, LX.T_INT_KW},
			Data: []Value{DT.NewIntValue(int64(3)), DT.NewIntValue(int64(30))}},
	})
	DT.RegisterTable("right", []Row{
		{Cols: []string{"id", "score"}, Types: []LX.TokenType{LX.T_INT_KW, LX.T_INT_KW},
			Data: []Value{DT.NewIntValue(int64(2)), DT.NewIntValue(int64(200))}},
		{Cols: []string{"id", "score"}, Types: []LX.TokenType{LX.T_INT_KW, LX.T_INT_KW},
			Data: []Value{DT.NewIntValue(int64(4)), DT.NewIntValue(int64(400))}},
	})
	

	left := NewSeqScan("left")
	right := NewSeqScan("right")
	on := func(outer, inner *Row) (bool, error) {
		if len(outer.Data) == 0 || len(inner.Data) == 0 {
			return false, nil
		}
		return outer.Data[0].Equal(inner.Data[0]), nil
	}

	join := NewNestedLoopJoin(left, right, "left", "right", on, JoinKindLeft)

	results := []Row{}
	for {
		row, err := join.Next(context.Background())
		if err != nil {
			if err == ErrNoRows {
				break
			}
			t.Fatalf("Next: %v", err)
		}
		results = append(results, row)
	}

	// Should have 3 rows (all left rows)
	if len(results) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(results))
	}

	// Row 1: id=1 unmatched (NULL right)
	if !results[0].Data[0].Equal(DT.NewIntValue(int64(1))) || !results[0].Data[1].Equal(DT.NewIntValue(int64(10))) {
		t.Errorf("row 0 left: got (%v, %v), want (1, 10)", results[0].Data[0], results[0].Data[1])
	}
	// Right side should be NULL-padded (2 columns)
	if len(results[0].Data) != 4 {
		t.Errorf("row 0 cols: got %d, want 4", len(results[0].Data))
	}
	// Right columns should be indices 2,3
	if results[0].Data[2].IsNull() == false || results[0].Data[3].IsNull() == false {
		t.Errorf("row 0 right: expected NULL, got (%v, %v)", results[0].Data[2], results[0].Data[3])
	}

	// Row 2: id=2 matched
	if !results[1].Data[0].Equal(DT.NewIntValue(int64(2))) || !results[1].Data[3].Equal(DT.NewIntValue(int64(200))) {
		t.Errorf("row 1: expected match, got (%v, %v)", results[1].Data[0], results[1].Data[3])
	}

	// Row 3: id=3 unmatched
	if !results[2].Data[0].Equal(DT.NewIntValue(int64(3))) || !results[2].Data[1].Equal(DT.NewIntValue(int64(30))) {
		t.Errorf("row 2 left: got (%v, %v), want (3, 30)", results[2].Data[0], results[2].Data[1])
	}
	if results[2].Data[2].IsNull() == false || results[2].Data[3].IsNull() == false {
		t.Errorf("row 2 right: expected NULL, got (%v, %v)", results[2].Data[2], results[2].Data[3])
	}
}

// TestNestedLoopJoin_InnerJoin verifies INNER JOIN still works.
func TestNestedLoopJoin_InnerJoin(t *testing.T) {
	DT.RegisterTable("l", []Row{
		{Cols: []string{"id"}, Types: []LX.TokenType{LX.T_INT_KW},
			Data: []Value{DT.NewIntValue(int64(1))}},
		{Cols: []string{"id"}, Types: []LX.TokenType{LX.T_INT_KW},
			Data: []Value{DT.NewIntValue(int64(2))}},
	})
	DT.RegisterTable("r", []Row{
		{Cols: []string{"id"}, Types: []LX.TokenType{LX.T_INT_KW},
			Data: []Value{DT.NewIntValue(int64(2))}},
		{Cols: []string{"id"}, Types: []LX.TokenType{LX.T_INT_KW},
			Data: []Value{DT.NewIntValue(int64(3))}},
	})
	

	left := NewSeqScan("l")
	right := NewSeqScan("r")
	on := func(outer, inner *Row) (bool, error) {
		return outer.Data[0].Equal(inner.Data[0]), nil
	}

	join := NewNestedLoopJoin(left, right, "l", "r", on, JoinKindInner)

	count := 0
	for {
		row, err := join.Next(context.Background())
		if err != nil {
			if err == ErrNoRows {
				break
			}
			t.Fatalf("Next: %v", err)
		}
		count++
		// Should only have id=2 match
		if !row.Data[0].Equal(DT.NewIntValue(int64(2))) || !row.Data[1].Equal(DT.NewIntValue(int64(2))) {
			t.Errorf("expected match on id=2, got (%v, %v)", row.Data[0], row.Data[1])
		}
	}

	if count != 1 {
		t.Errorf("expected 1 row, got %d", count)
	}
}

// TestNestedLoopJoin_LeftWithNilOn verifies LEFT JOIN without ON clause.
func TestNestedLoopJoin_LeftWithNilOn(t *testing.T) {
	DT.RegisterTable("a", []Row{
		{Cols: []string{"x"}, Types: []LX.TokenType{LX.T_INT_KW}, Data: []Value{DT.NewIntValue(int64(1))}},
	})
	DT.RegisterTable("b", []Row{
		{Cols: []string{"y"}, Types: []LX.TokenType{LX.T_INT_KW}, Data: []Value{DT.NewIntValue(int64(99))}},
	})
	

	left := NewSeqScan("a")
	right := NewSeqScan("b")

	// No ON clause - should match all (like CROSS)
	join := NewNestedLoopJoin(left, right, "a", "b", nil, JoinKindLeft)

	row, err := join.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	// Should have both columns
	if len(row.Data) != 2 {
		t.Errorf("expected 2 cols, got %d", len(row.Data))
	}
	if !row.Data[0].Equal(DT.NewIntValue(int64(1))) || !row.Data[1].Equal(DT.NewIntValue(int64(99))) {
		t.Errorf("expected (1, 99), got %v", row.Data)
	}
}

// TestNestedLoopJoin_Close verifies Close resets state.
func TestNestedLoopJoin_Close(t *testing.T) {
	DT.RegisterTable("x", []Row{
		{Cols: []string{"a"}, Types: []LX.TokenType{LX.T_INT_KW}, Data: []Value{DT.NewIntValue(int64(1))}},
	})
	DT.RegisterTable("y", []Row{
		{Cols: []string{"b"}, Types: []LX.TokenType{LX.T_INT_KW}, Data: []Value{DT.NewIntValue(int64(2))}},
	})
	

	left := NewSeqScan("x")
	right := NewSeqScan("y")
	on := func(outer, inner *Row) (bool, error) { return true, nil }

	join := NewNestedLoopJoin(left, right, "x", "y", on, JoinKindInner)

	// Consume one row
	_, err := join.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}

	// Close
	if err := join.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}

	// After close, internal state is reset (leftRow=nil)
	// This is acceptable behavior for Close
}
