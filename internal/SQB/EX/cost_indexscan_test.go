package EX

import (
	"context"
	"strings"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func TestPlanner_EstimateCost_PerOperator(t *testing.T) {
	p := NewPlanner()
	p.RegisterTable("t", []ColInfo{{Name: "a", Typ: 1}}, "a")

	// SeqScan: 1.0
	if got := p.estimateCost(NewSeqScan("t")); got != 1.0 {
		t.Errorf("SeqScan cost = %v, want 1.0", got)
	}
	// IndexScan: 0.1
	if got := p.estimateCost(NewIndexScan("t", "idx", nil, nil)); got != 0.1 {
		t.Errorf("IndexScan cost = %v, want 0.1", got)
	}
	// Project, Limit, Offset: pass-through to child cost
	scan := NewSeqScan("t")
	proj := NewProject(scan, nil)
	if got := p.estimateCost(proj); got != 1.0 {
		t.Errorf("Project cost = %v, want 1.0 (child SeqScan)", got)
	}
	lim := NewLimit(scan, 10)
	if got := p.estimateCost(lim); got != 1.0 {
		t.Errorf("Limit cost = %v, want 1.0 (child SeqScan)", got)
	}
	off := NewOffset(scan, 5)
	if got := p.estimateCost(off); got != 1.0 {
		t.Errorf("Offset cost = %v, want 1.0 (child SeqScan)", got)
	}
}

func TestPlanner_EstimateCost_FilterSelectivity(t *testing.T) {
	p := NewPlanner()
	scan := NewSeqScan("t")
	// col = literal without stats: 0.5 (uniform fallback)
	filterEQ := NewFilter(scan, &PS.BinaryExpr{
		Op:    LX.T_EQ,
		Left:  &PS.Ident{Name: "a"},
		Right: &PS.NumberLiteral{Val: 1},
	})
	if got := p.estimateCost(filterEQ); got != 0.5 {
		t.Errorf("Filter(col=lit) cost = %v, want 0.5 (no stats)", got)
	}
	// generic predicate: 0.5
	filterGeneric := NewFilter(scan, &PS.BinaryExpr{
		Op:    LX.T_GT,
		Left:  &PS.Ident{Name: "a"},
		Right: &PS.Ident{Name: "b"},
	})
	if got := p.estimateCost(filterGeneric); got != 0.5 {
		t.Errorf("Filter(generic) cost = %v, want 0.5", got)
	}
}

func TestPlanner_EstimateCost_Sort(t *testing.T) {
	p := NewPlanner()
	scan := NewSeqScan("t")
	sort := NewSort(scan, []PS.OrderItem{{Expr: &PS.Ident{Name: "a"}, Desc: false}})
	// child = 1.0, log2(1) = 0, so 1 * (1 + 0) = 1
	if got := p.estimateCost(sort); got != 1.0 {
		t.Errorf("Sort cost = %v, want 1.0", got)
	}
}

func TestIndexScan_WithStore_ReadsRows(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "a"}, "id")
	ex.RegisterIndex("t", "idx_a", []string{"a"})
	ctx := context.Background()
	for _, s := range []string{
		"INSERT INTO t VALUES (1, 'x')",
		"INSERT INTO t VALUES (2, 'y')",
		"INSERT INTO t VALUES (3, 'x')",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	plan, err := ex.Explain("SELECT * FROM t WHERE a = 'x'")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, "Search") {
		t.Errorf("expected Search (IndexScan) in plan, got:\n%s", plan)
	}
	rows, err := ex.QueryAll(ctx, "SELECT id FROM t WHERE a = 'x'")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Errorf("expected 2 rows, got %d", len(rows))
	}
}
