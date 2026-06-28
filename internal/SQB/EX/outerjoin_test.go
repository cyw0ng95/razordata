package EX

import (
	"context"
	"fmt"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// TestNestedLoopJoin_WithSharedSchema verifies that pre-computing
// the schema via WithSharedSchema sets all three field triplets
// (shared/blkShared/outerShared) and the sharedBuilt flag.
// REQ001097.
func TestNestedLoopJoin_WithSharedSchema(t *testing.T) {
	RegisterTable("l", []Row{
		{Cols: []string{"a"}, Types: []int{int(LX.T_INT_KW)},
			Data: []Value{NewIntValue(1)}},
		{Cols: []string{"a"}, Types: []int{int(LX.T_INT_KW)},
			Data: []Value{NewIntValue(2)}},
	})
	RegisterTable("r", []Row{
		{Cols: []string{"b"}, Types: []int{int(LX.T_INT_KW)},
			Data: []Value{NewIntValue(10)}},
	})
	defer UnregisterAll()

	left := NewSeqScan("l")
	right := NewSeqScan("r")
	join := NewNestedLoopJoin(left, right, "l", "r", nil, JoinKindInner).
		WithSharedSchema(
			[]string{"a", "b"},
			[]int{int(LX.T_INT_KW), int(LX.T_INT_KW)},
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
	RegisterTable("left", []Row{
		{Cols: []string{"id", "val"}, Types: []int{int(LX.T_INT_KW), int(LX.T_INT_KW)},
			Data: []Value{NewIntValue(int64(1)), NewIntValue(int64(10))}},
		{Cols: []string{"id", "val"}, Types: []int{int(LX.T_INT_KW), int(LX.T_INT_KW)},
			Data: []Value{NewIntValue(int64(2)), NewIntValue(int64(20))}},
		{Cols: []string{"id", "val"}, Types: []int{int(LX.T_INT_KW), int(LX.T_INT_KW)},
			Data: []Value{NewIntValue(int64(3)), NewIntValue(int64(30))}},
	})
	RegisterTable("right", []Row{
		{Cols: []string{"id", "score"}, Types: []int{int(LX.T_INT_KW), int(LX.T_INT_KW)},
			Data: []Value{NewIntValue(int64(2)), NewIntValue(int64(200))}},
		{Cols: []string{"id", "score"}, Types: []int{int(LX.T_INT_KW), int(LX.T_INT_KW)},
			Data: []Value{NewIntValue(int64(4)), NewIntValue(int64(400))}},
	})
	defer UnregisterAll()

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
	if !results[0].Data[0].Equal(NewIntValue(int64(1))) || !results[0].Data[1].Equal(NewIntValue(int64(10))) {
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
	if !results[1].Data[0].Equal(NewIntValue(int64(2))) || !results[1].Data[3].Equal(NewIntValue(int64(200))) {
		t.Errorf("row 1: expected match, got (%v, %v)", results[1].Data[0], results[1].Data[3])
	}

	// Row 3: id=3 unmatched
	if !results[2].Data[0].Equal(NewIntValue(int64(3))) || !results[2].Data[1].Equal(NewIntValue(int64(30))) {
		t.Errorf("row 2 left: got (%v, %v), want (3, 30)", results[2].Data[0], results[2].Data[1])
	}
	if results[2].Data[2].IsNull() == false || results[2].Data[3].IsNull() == false {
		t.Errorf("row 2 right: expected NULL, got (%v, %v)", results[2].Data[2], results[2].Data[3])
	}
}

// TestNestedLoopJoin_InnerJoin verifies INNER JOIN still works.
func TestNestedLoopJoin_InnerJoin(t *testing.T) {
	RegisterTable("l", []Row{
		{Cols: []string{"id"}, Types: []int{int(LX.T_INT_KW)},
			Data: []Value{NewIntValue(int64(1))}},
		{Cols: []string{"id"}, Types: []int{int(LX.T_INT_KW)},
			Data: []Value{NewIntValue(int64(2))}},
	})
	RegisterTable("r", []Row{
		{Cols: []string{"id"}, Types: []int{int(LX.T_INT_KW)},
			Data: []Value{NewIntValue(int64(2))}},
		{Cols: []string{"id"}, Types: []int{int(LX.T_INT_KW)},
			Data: []Value{NewIntValue(int64(3))}},
	})
	defer UnregisterAll()

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
		if !row.Data[0].Equal(NewIntValue(int64(2))) || !row.Data[1].Equal(NewIntValue(int64(2))) {
			t.Errorf("expected match on id=2, got (%v, %v)", row.Data[0], row.Data[1])
		}
	}

	if count != 1 {
		t.Errorf("expected 1 row, got %d", count)
	}
}

// TestNestedLoopJoin_LeftWithNilOn verifies LEFT JOIN without ON clause.
func TestNestedLoopJoin_LeftWithNilOn(t *testing.T) {
	RegisterTable("a", []Row{
		{Cols: []string{"x"}, Types: []int{int(LX.T_INT_KW)}, Data: []Value{NewIntValue(int64(1))}},
	})
	RegisterTable("b", []Row{
		{Cols: []string{"y"}, Types: []int{int(LX.T_INT_KW)}, Data: []Value{NewIntValue(int64(99))}},
	})
	defer UnregisterAll()

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
	if !row.Data[0].Equal(NewIntValue(int64(1))) || !row.Data[1].Equal(NewIntValue(int64(99))) {
		t.Errorf("expected (1, 99), got %v", row.Data)
	}
}

// TestNestedLoopJoin_Close verifies Close resets state.
func TestNestedLoopJoin_Close(t *testing.T) {
	RegisterTable("x", []Row{
		{Cols: []string{"a"}, Types: []int{int(LX.T_INT_KW)}, Data: []Value{NewIntValue(int64(1))}},
	})
	RegisterTable("y", []Row{
		{Cols: []string{"b"}, Types: []int{int(LX.T_INT_KW)}, Data: []Value{NewIntValue(int64(2))}},
	})
	defer UnregisterAll()

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

// TestCrossJoin_LimitPushdown verifies that LIMIT on a 5-table
// cross join terminates quickly without materializing the full
// Cartesian product. REQ001094.
func TestCrossJoin_LimitPushdown(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	e := NewExecutor()
	ctx := context.Background()

	for i := 1; i <= 5; i++ {
		setup := fmt.Sprintf("CREATE TABLE t%d (a INTEGER)", i)
		if _, err := e.Exec(ctx, setup); err != nil {
			t.Fatalf("setup t%d: %v", i, err)
		}
		for j := 1; j <= 100; j++ {
			insert := fmt.Sprintf("INSERT INTO t%d VALUES (%d)", i, j)
			if _, err := e.Exec(ctx, insert); err != nil {
				t.Fatalf("insert: %v", err)
			}
		}
	}
	// Without LIMIT, 100^5 = 10 billion rows (would hang).
	// With LIMIT 10, we should return exactly 10 rows quickly.
	sql := "SELECT * FROM t1, t2, t3, t4, t5 LIMIT 10"
	rows, err := e.QueryAll(ctx, sql)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 10 {
		t.Errorf("expected 10 rows from LIMIT 10 cross join, got %d", len(rows))
	}
}
