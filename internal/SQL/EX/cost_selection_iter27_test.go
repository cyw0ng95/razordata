//go:build !slt_corpus_full

package EX

import (
	"fmt"
	"strings"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQL/LX"
	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

// TestCost_BasedScanSelection_PrefersIndex verifies REQ000156:
// when an index exists on a WHERE column, the planner should
// prefer IndexScan (lower cost) over SeqScan.
func TestCost_BasedScanSelection_PrefersIndex(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "a"}, "id")
	ex.RegisterIndex("t", "idx_a", []string{"a"})
	RegisterIndexWithID("t", RegisteredIndex{Name: "idx_a", Columns: []string{"a"}})
	plan, err := ex.Explain("SELECT * FROM t WHERE a = 'x'")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, "IndexScan") {
		t.Errorf("expected IndexScan in plan, got:\n%s", plan)
	}
	if strings.Contains(plan, "SeqScan") {
		t.Errorf("did not expect SeqScan in plan, got:\n%s", plan)
	}
}

// TestCost_BasedScanSelection_NoIndexOnColumn verifies REQ000156:
// when no index exists on the WHERE column, the planner falls
// back to SeqScan.
func TestCost_BasedScanSelection_NoIndexOnColumn(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "a"}, "id")
	plan, err := ex.Explain("SELECT * FROM t WHERE a = 'x'")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, "SeqScan") {
		t.Errorf("expected SeqScan in plan, got:\n%s", plan)
	}
	if strings.Contains(plan, "IndexScan") {
		t.Errorf("did not expect IndexScan in plan (no index), got:\n%s", plan)
	}
}

// TestCost_BasedScanSelection_HighSelectivityRange verifies REQ000156:
// range predicates (>, <, BETWEEN) on an indexed column also
// prefer IndexScan via the cost model.
func TestCost_BasedScanSelection_HighSelectivityRange(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "a"}, "id")
	ex.RegisterIndex("t", "idx_a", []string{"a"})
	RegisterIndexWithID("t", RegisteredIndex{Name: "idx_a", Columns: []string{"a"}})
	plan, err := ex.Explain("SELECT * FROM t WHERE a BETWEEN 1 AND 10")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, "IndexScan") {
		t.Errorf("expected IndexScan for BETWEEN on indexed column, got:\n%s", plan)
	}
}

// TestPickCheaperScan_NoIndex verifies the helper returns the
// original scan when no index exists.
func TestPickCheaperScan_NoIndex(t *testing.T) {
	p := NewPlanner()
	p.RegisterTable("t", []ColInfo{{Name: "a"}}, "")
	seq := NewSeqScan("t")
	where := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "a"},
		Op:    int(LX.T_EQ),
		Right: &PS.NumberLiteral{Val: 1},
	}
	alt, ok := p.pickCheaperScan("t", where, seq)
	if ok {
		t.Error("expected ok=false when no index exists")
	}
	if alt != seq {
		t.Error("expected original scan returned unchanged")
	}
}

// TestPickCheaperScan_WithIndex verifies the helper returns the
// IndexScan when one is cheaper.
func TestPickCheaperScan_WithIndex(t *testing.T) {
	p := NewPlanner()
	p.RegisterTable("t", []ColInfo{{Name: "a"}}, "")
	p.RegisterIndex("t", "idx_a", []string{"a"})
	RegisterIndexWithID("t", RegisteredIndex{Name: "idx_a", Columns: []string{"a"}})
	defer func() {
		storeMu.Lock()
		delete(registeredIndexes, "t")
		storeMu.Unlock()
	}()
	seq := NewSeqScan("t")
	where := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "a"},
		Op:    int(LX.T_EQ),
		Right: &PS.NumberLiteral{Val: 1},
	}
	alt, ok := p.pickCheaperScan("t", where, seq)
	if !ok {
		t.Error("expected ok=true when an index exists on the WHERE column")
	}
	if alt == seq {
		t.Error("expected a different (cheaper) scan to be returned")
	}
	if !strings.Contains(fmt.Sprintf("%T", alt), "IndexScan") {
		t.Errorf("expected IndexScan type, got %T", alt)
	}
}

// TestPickCheaperScan_HighSelectivityRange verifies the helper
// returns the IndexScan for a range predicate on an indexed column.
func TestPickCheaperScan_HighSelectivityRange(t *testing.T) {
	p := NewPlanner()
	p.RegisterTable("t", []ColInfo{{Name: "a"}}, "")
	p.RegisterIndex("t", "idx_a", []string{"a"})
	RegisterIndexWithID("t", RegisteredIndex{Name: "idx_a", Columns: []string{"a"}})
	defer func() {
		storeMu.Lock()
		delete(registeredIndexes, "t")
		storeMu.Unlock()
	}()
	seq := NewSeqScan("t")
	where := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "a"},
		Op:    int(LX.T_GT),
		Right: &PS.NumberLiteral{Val: 5},
	}
	alt, ok := p.pickCheaperScan("t", where, seq)
	if !ok {
		t.Error("expected ok=true for range on indexed column")
	}
	if alt == seq {
		t.Error("expected a different scan to be returned")
	}
}
