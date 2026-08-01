package EX

import (
	"fmt"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/PS"
	"github.com/cyw0ng95/razordata/internal/SQF/RE"
)

// TestSplitPredicatesByTable_PreservesDistinctIN verifies
// REQ001111: when splitPredicatesByTable receives 4 distinct IN
// predicates plus 1 binary equality, all 5 are distributed to
// their respective DT.Tables (not deduplicated).
func TestSplitPredicatesByTable_PreservesDistinctIN(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	for ti := 1; ti <= 9; ti++ {
		ex.RegisterTable(fmt.Sprintf("t%d", ti), []string{"a", "b", "c", "d", "e", "v"})
	}
	stmt, err := PS.NewParser("SELECT count(*) FROM t9, t4, t8, t1, t3 WHERE b4 in (532,593,289,476,749,35,816) AND d9 in (808,662,597,682,628,568) AND e8 in (792,14,646) AND 729=a3 AND a1 in (622,380,862,52,640,776,268,536)").Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	sel := stmt.(*PS.Select)
	conjuncts := RE.SplitAnd(sel.Where)
	if len(conjuncts) != 5 {
		t.Fatalf("expected 5 conjuncts, got %d", len(conjuncts))
	}
	p := NewPlanner()
	allTables := []string{"t9", "t4", "t8", "t1", "t3"}
	pushed, cross := p.splitPredicatesByTable(conjuncts, allTables)
	totalPushed := 0
	for tbl, preds := range pushed {
		totalPushed += len(preds)
		t.Logf("pushed to %s: %d predicates", tbl, len(preds))
	}
	if totalPushed != 5 {
		t.Fatalf("expected 5 pushed predicates, got %d (cross=%d)", totalPushed, len(cross))
	}
	for _, tbl := range []string{"t1", "t3", "t4", "t8", "t9"} {
		if len(pushed[tbl]) == 0 {
			t.Errorf("table %s received no pushed predicates (bug)", tbl)
		}
	}
}
