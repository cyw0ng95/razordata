package EX

import (
	"context"
	"fmt"
	"testing"
)

// TestSelect4Plan5Table_NoOOM is the end-to-end regression test for
// REQ001111: select4-style 5-table cross-joins with single-table
// IN-list predicates on every table must NOT OOM. Before the fix,
// eliminateCommonSubexpressions collapsed all IN predicates to a
// single one (because exprHash fell through to "%T" for *PS.InExpr),
// so only 2 of 5 tables received pushed-down filters. The 5-table
// cross-join then materialized 30^5 = 24.3M rows and triggered a
// 50 GB HashJoin.dataBuf pre-allocation that OOM-killed the process.
func TestSelect4Plan5Table_NoOOM(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	for ti := 1; ti <= 9; ti++ {
		tbl := fmt.Sprintf("t%d", ti)
		ex.RegisterTable(tbl, []string{"a", "b", "c", "d", "e", "v"})
		ctx := context.Background()
		for i := 0; i < 30; i++ {
			sql := fmt.Sprintf("INSERT INTO %s VALUES (%d,%d,%d,%d,%d,'row %d')", tbl, i, i+1, i+2, i+3, i+4, i)
			ex.Exec(ctx, sql)
		}
	}
	ctx := context.Background()
	rows, err := ex.QueryAll(ctx,
		"SELECT count(*) FROM t9, t4, t8, t1, t3 WHERE b4 in (532,593,289,476,749,35,816) AND d9 in (808,662,597,682,628,568) AND e8 in (792,14,646) AND 729=a3 AND a1 in (622,380,862,52,640,776,268,536)")
	if err != nil {
		t.Fatalf("query failed (likely OOM): %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("expected 1 row (count(*)), got %d", len(rows))
	}
	_ = context.Background
}

// TestSelect4Plan6Table_NoOOM extends the 5-table case to 6 tables
// with 6 IN-list predicates — same shape but stress-tests the CSE
// dedup with one more level.
func TestSelect4Plan6Table_NoOOM(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	for ti := 1; ti <= 9; ti++ {
		tbl := fmt.Sprintf("t%d", ti)
		ex.RegisterTable(tbl, []string{"a", "b", "c", "d", "e", "v"})
		ctx := context.Background()
		for i := 0; i < 20; i++ {
			sql := fmt.Sprintf("INSERT INTO %s VALUES (%d,%d,%d,%d,%d,'row %d')", tbl, i, i+1, i+2, i+3, i+4, i)
			ex.Exec(ctx, sql)
		}
	}
	ctx := context.Background()
	rows, err := ex.QueryAll(ctx,
		"SELECT count(*) FROM t1, t2, t3, t4, t5, t6 WHERE a1 in (1,2,3) AND b2 in (4,5,6) AND c3 in (7,8,9) AND d4 in (10,11,12) AND e5 in (13,14,15) AND a6 in (16,17,18)")
	if err != nil {
		t.Fatalf("query failed (likely OOM): %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("expected 1 row (count(*)), got %d", len(rows))
	}
}