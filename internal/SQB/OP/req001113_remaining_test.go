package OP

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

func TestReq001113_3TableCommaJoin_EquiPredicate(t *testing.T) {
	// Reproduce: FROM t6,t4,t9 WHERE b4=a9 AND per-table filters.
	// t6: d6 IN (822,66) — both rows match per-table
	// t4: no per-table filter
	// t9: a9 IN (335,811,964) — rows 1,2 match
	// Cross-table: b4=a9
	t6 := newMemOp([]pl.Row{
		rowWithCols("t6", []string{"a6", "b6", "c6", "d6", "e6", "x6"}, []int64{1, 2, 3, 822, 5, 6}),
		rowWithCols("t6", []string{"a6", "b6", "c6", "d6", "e6", "x6"}, []int64{7, 8, 9, 66, 10, 11}),
	})
	t4 := newMemOp([]pl.Row{
		rowWithCols("t4", []string{"a4", "b4", "c4", "d4", "e4", "x4"}, []int64{100, 335, 3, 4, 5, 6}),
		rowWithCols("t4", []string{"a4", "b4", "c4", "d4", "e4", "x4"}, []int64{101, 811, 8, 9, 10, 11}),
	})
	t9 := newMemOp([]pl.Row{
		rowWithCols("t9", []string{"a9", "b9", "c9", "d9", "e9", "x9"}, []int64{335, 2, 3, 4, 5, 6}),
		rowWithCols("t9", []string{"a9", "b9", "c9", "d9", "e9", "x9"}, []int64{811, 8, 9, 10, 11, 12}),
		rowWithCols("t9", []string{"a9", "b9", "c9", "d9", "e9", "x9"}, []int64{999, 20, 30, 40, 50, 60}),
	})

	// Inner join t4 ⋈ t9 ON b4=a9
	// Expected: t4(335) × t9(335) = 1 match, t4(811) × t9(811) = 1 match → 2 rows
	onB4A9 := func(outer, inner *pl.Row) (bool, error) {
		b4 := colVal(t, outer, "b4")
		a9 := colVal(t, inner, "a9")
		return b4 == a9, nil
	}
	nljInner := NewNestedLoopJoin(t4, t9, "t4", "t9", onB4A9, JoinKindInner)
	// Cross join t6 × (t4 ⋈ t9) — no predicate
	nljOuter := NewNestedLoopJoin(t6, nljInner, "t6", "", func(_, _ *pl.Row) (bool, error) { return true, nil }, JoinKindInner)

	var rows []pl.Row
	ctx := context.Background()
	for {
		row, err := nljOuter.Next(ctx)
		if errors.Is(err, pl.ErrNoRows) {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		rows = append(rows, row)
	}

	// t6 has 2 rows, t4⋈t9 has 2 rows → 4 crossed rows
	fmt.Printf("Got %d rows (expect 4)\n", len(rows))
	if len(rows) == 0 {
		t.Errorf("got 0 rows, expected > 0")
	}
	_ = nljOuter.Close()
}

func TestReq001113_3TableCommaJoin_HashJoin(t *testing.T) {
	// Same data, but use HashJoin for the inner equi-join t4 ⋈ t9 ON b4=a9.
	t4 := newMemOp([]pl.Row{
		rowKV("t4", "b4", 335),
		rowKV("t4", "b4", 811),
	})
	t9 := newMemOp([]pl.Row{
		rowKV("t9", "a9", 335),
		rowKV("t9", "a9", 811),
		rowKV("t9", "a9", 999),
	})

	hj := NewHashJoin(t4, t9, "t4", "t9", []string{"b4"}, []string{"a9"}, 0).WithKind(JoinKindInner)
	defer hj.Close()
	rows := collectHashJoin(t, hj)
	if len(rows) == 0 {
		t.Errorf("HashJoin got 0 rows, expected > 0")
	}
	fmt.Printf("HashJoin got %d rows (expect 2)\n", len(rows))
}

func TestReq001113_NLJ_CloseAndReuse(t *testing.T) {
	// Close NLJ then reuse — verifies sharedBuilt is properly reset.
	// After Close(), we replace child operators with fresh ones
	// (how SLT reuses the same NLJ across different query runs).
	left := newMemOp([]pl.Row{
		rowKV("t1", "k", 1),
		rowKV("t1", "k", 2),
	})
	right := newMemOp([]pl.Row{
		rowKV("t2", "k", 1),
		rowKV("t2", "k", 2),
	})
	on := func(outer, inner *pl.Row) (bool, error) {
		return colVal(t, outer, "k") == colVal(t, inner, "k"), nil
	}
	nlj := NewNestedLoopJoin(left, right, "t1", "t2", on, JoinKindInner)

	// First run
	rows1 := collectNLJ(t, nlj)
	if len(rows1) == 0 {
		t.Fatal("first run got 0 rows")
	}
	if err := nlj.Close(); err != nil {
		t.Fatal(err)
	}

	// Replace children with fresh operators (simulating plan reuse)
	left2 := newMemOp([]pl.Row{
		rowKV("t1", "k", 1),
		rowKV("t1", "k", 2),
	})
	right2 := newMemOp([]pl.Row{
		rowKV("t2", "k", 1),
		rowKV("t2", "k", 2),
	})
	nlj.left = left2
	nlj.right = right2

	// Second run (reuse after Close + fresh children)
	rows2 := collectNLJ(t, nlj)
	if len(rows2) == 0 {
		t.Fatal("second run got 0 rows after Close + fresh children — sharedBuilt leak")
	}
	if len(rows1) != len(rows2) {
		t.Errorf("rows1=%d rows2=%d — mismatch after Close+reuse", len(rows1), len(rows2))
	}
	_ = nlj.Close()
}

func rowKV(tbl string, col string, val int64) pl.Row {
	return pl.Row{
		Cols:  []string{tbl + "." + col, col},
		Types: []LX.TokenType{LX.T_INT, LX.T_INT},
		Data: []pl.Value{
			{Kind: pl.KindInt, I64: val},
			{Kind: pl.KindInt, I64: val},
		},
	}
}

func rowWithCols(tbl string, cols []string, vals []int64) pl.Row {
	prefixed := make([]string, len(cols))
	for i, c := range cols {
		prefixed[i] = tbl + "." + c
	}
	types := make([]LX.TokenType, len(vals))
	for i := range types {
		types[i] = LX.T_INT
	}
	data := make([]pl.Value, len(vals))
	for i, v := range vals {
		data[i] = pl.Value{Kind: pl.KindInt, I64: v}
	}
	return pl.Row{
		Cols:  append(prefixed, cols...),
		Types: append(types, types...),
		Data:  append(data, data...),
	}
}

func colVal(t *testing.T, row *pl.Row, col string) int64 {
	t.Helper()
	for i, c := range row.Cols {
		if c == col {
			return row.Data[i].I64
		}
	}
	t.Fatalf("column %s not found in %v", col, row.Cols)
	return 0
}

func collectNLJ(t *testing.T, nlj *NestedLoopJoin) []pl.Row {
	t.Helper()
	var out []pl.Row
	ctx := context.Background()
	for {
		row, err := nlj.Next(ctx)
		if errors.Is(err, pl.ErrNoRows) {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		out = append(out, row)
	}
	return out
}
