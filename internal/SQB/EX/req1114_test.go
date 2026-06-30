//go:build !slt_corpus_full

package EX

import (
	"context"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
)

// TestINList_Join_ProjectedValues reproduces REQ001114: IN-list predicate
// on cross-join column produces NULL projected values.
// Uses exact SLT data from select4.test L29056-L29101.
func TestINList_Join_ProjectedValues(t *testing.T) {
	ResetForTest(t)

	ex := NewExecutor()
	ctx := context.Background()

	ex.RegisterTableWithPK("t3", []string{"a3", "b3", "c3", "d3", "e3", "x3"}, "")
	ex.RegisterTableWithPK("t7", []string{"a7", "b7", "c7", "d7", "e7", "x7"}, "")

	// Insert t3 rows matching a3 IN (637,591,710,644) with exact SLT data
	if _, err := ex.Exec(ctx, "INSERT INTO t3 VALUES (637, 549, 476, 645, 401, 'table tn3 row 29')"); err != nil {
		t.Fatalf("insert t3 row 29: %v", err)
	}
	if _, err := ex.Exec(ctx, "INSERT INTO t3 VALUES (591, 41, 452, 403, 807, 'table tn3 row 44')"); err != nil {
		t.Fatalf("insert t3 row 44: %v", err)
	}
	if _, err := ex.Exec(ctx, "INSERT INTO t3 VALUES (710, 790, 81, 923, 147, 'table tn3 row 91')"); err != nil {
		t.Fatalf("insert t3 row 91: %v", err)
	}
	if _, err := ex.Exec(ctx, "INSERT INTO t3 VALUES (644, 740, 395, 482, 406, 'table tn3 row 81')"); err != nil {
		t.Fatalf("insert t3 row 81: %v", err)
	}
	// Insert t7 row matching e7=280 with exact SLT data
	if _, err := ex.Exec(ctx, "INSERT INTO t7 VALUES (894, 996, 606, 105, 280, 'table tn7 row 103')"); err != nil {
		t.Fatalf("insert t7: %v", err)
	}

	// Cross-join with IN-list predicate — should produce 4 rows
	rows, err := ex.QueryAll(ctx, "SELECT e3+491, a7 FROM t3, t7 WHERE a3 IN (637,591,710,644) AND e7=280")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 4 {
		t.Fatalf("expected 4 rows, got %d", len(rows))
	}

	// Expected: e3+491 values [892, 1298, 638, 897] all with a7=894
	got := make(map[int64]bool)
	for _, r := range rows {
		if r.Data[0].Kind != DT.KindInt {
			t.Errorf("expected KindInt for col 0, got %v", r.Data[0].Kind)
		}
		if r.Data[1].I64 != 894 {
			t.Errorf("a7: expected 894, got %d", r.Data[1].I64)
		}
		got[r.Data[0].I64] = true
	}
	for _, want := range []int64{892, 1298, 638, 897} {
		if !got[want] {
			t.Errorf("missing expected e3+491 value %d", want)
		}
	}
}