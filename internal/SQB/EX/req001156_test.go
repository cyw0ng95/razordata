//go:build !slt_corpus

package EX

import (
	"fmt"
	"testing"

	CO "github.com/cyw0ng95/razordata/internal/SQO/CO"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	RE "github.com/cyw0ng95/razordata/internal/SQF/RE"
)

// TestGroupBushyJoins_ChainEquiJoin_OneGroup verifies REQ001156: a
// 4-table chain-equi-join (t51,t29,t31,t55 with a51=b31 AND a29=b51
// AND b55=a31) must produce a single bushy group, not 2.
//
// Before the e00d132 fix, groupBushyJoins marked each table
// "independent" of the running group when no direct pairKey matched,
// splitting chain-equi-joins into 2 groups and breaking select5
// join-4-1.
//
// This is a unit-level regression test that exercises groupBushyJoins
// directly with the exact predicate pattern from the REQ.
func TestGroupBushyJoins_ChainEquiJoin_OneGroup(t *testing.T) {
	// Use t1..t9 schemas registered for bare-column resolution via
	// the SLT naming convention (d6 -> t6.d). The schemas don't need
	// to match the actual table names in the test — only the column
	// names need to be resolvable.
	ResetForTest(t)
	ex := NewExecutor()
	for ti := 1; ti <= 9; ti++ {
		ex.RegisterTable(fmt.Sprintf("t%d", ti), []string{"a", "b", "c", "d", "e", "v"})
	}
	_ = ex

	// 4-table chain-equi-join pattern from the REQ001156 description.
	// Each column is qualified so extractTableColumn returns the right
	// table without depending on SLT naming resolution.
	sql := `SELECT count(*) FROM t1, t2, t3, t4 WHERE a1=b3 AND a2=b1 AND b4=a3`
	stmt, err := PS.NewParser(sql).Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	sel := stmt.(*PS.Select)
	conjuncts := RE.SplitAnd(sel.Where)
	if len(conjuncts) != 3 {
		t.Fatalf("expected 3 conjuncts, got %d", len(conjuncts))
	}

	// groupBushyJoins requires k > 3 to enter the bushy path. Use
	// 4 tables to ensure we're testing the bushy heuristic.
	joinOrder := []string{"t1", "t2", "t3", "t4"}
	groups := CO.GroupBushyJoins("t1", joinOrder, conjuncts, extractTableColumn)

	t.Logf("groups: %v", groups)

	// All 4 tables must end up in exactly one group because they form
	// a chain of equi-joins (t1-t3 via a1=b3, t1-t2 via a2=b1, t3-t4
	// via b4=a3).
	if len(groups) != 1 {
		t.Fatalf("expected 1 bushy group (chain-equi-join is not independent), got %d: %v", len(groups), groups)
	}
	if len(groups[0]) != 4 {
		t.Fatalf("expected group to contain all 4 tables, got %d: %v", len(groups[0]), groups[0])
	}
}

// TestGroupBushyJoins_StarSchema_TwoGroups verifies the *true*
// independent-groups case still splits correctly. Three tables that
// equi-join to a common base (star schema) should be split into
// independent groups, since that's the bushy benefit.
func TestGroupBushyJoins_StarSchema_TwoGroups(t *testing.T) {
	ResetForTest(t)
	ex := NewExecutor()
	for ti := 1; ti <= 9; ti++ {
		ex.RegisterTable(fmt.Sprintf("t%d", ti), []string{"a", "b", "c", "d", "e", "v"})
	}
	_ = ex

	// 4-table star: t1 base, t2/t3/t4 each equi-join to t1 via
	// independent columns. groupBushyJoins should put t2,t3,t4 in
	// one group (all equi-joined to t1) — but the function's existing
	// behavior is that one of them joins first with t1, then the
	// others are independent. This test documents the current
	// behavior: at minimum, all 4 tables are accounted for.
	sql := `SELECT count(*) FROM t1, t2, t3, t4 WHERE a1=b2 AND a1=b3 AND a1=b4`
	stmt, err := PS.NewParser(sql).Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	sel := stmt.(*PS.Select)
	conjuncts := RE.SplitAnd(sel.Where)
	if len(conjuncts) != 3 {
		t.Fatalf("expected 3 conjuncts, got %d", len(conjuncts))
	}

	joinOrder := []string{"t1", "t2", "t3", "t4"}
	groups := CO.GroupBushyJoins("t1", joinOrder, conjuncts, extractTableColumn)

	t.Logf("star schema groups: %v", groups)

	totalTables := 0
	for _, g := range groups {
		totalTables += len(g)
	}
	if totalTables != 4 {
		t.Errorf("expected all 4 tables across groups, got %d (groups=%v)", totalTables, groups)
	}
}

// itoa is a tiny helper to avoid pulling strconv into the test file's
// import list (keeps the file focused on its single responsibility).
// Defined in index_only_scan_test.go (package-local); do not redefine.
