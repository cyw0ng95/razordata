package EX

import (
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// TestPlanner_ViewMerging verifies REQ001078: when a view is a simple
// single-table SELECT with no aggregation/DISTINCT/GROUP BY/ORDER BY/
// LIMIT/OFFSET/HAVING, it is merged into the outer SELECT instead of
// being wrapped as a subquery.
func TestPlanner_ViewMerging(t *testing.T) {
	t.Run("mergeable_view_plans", func(t *testing.T) {
		// A view with a single base table and WHERE can be merged.
		// Direct planner+view registration — no engine needed.
		ResetForTest(t)
		p := NewPlanner()
		p.RegisterTable("t", []DT.ColInfo{{Name: "a", Typ: 1}, {Name: "b", Typ: 1}}, "a")
		viewSel := &PS.Select{
			From:  "t",
			Cols:  []PS.Expr{&PS.QualifiedName{Name: "a"}, &PS.QualifiedName{Name: "b"}},
			Where: &PS.BinaryExpr{Op: LX.T_GT, Left: &PS.QualifiedName{Name: "b"}, Right: &PS.NumberLiteral{Val: int64(5)}},
		}
		DT.RegisterView("v", viewSel)
		// Outer WHERE must combine with view's WHERE.
		plan, err := p.ParseAndPlan("SELECT a FROM v WHERE a > 0")
		if err != nil {
			t.Fatalf("plan error: %v", err)
		}
		if plan == nil || plan.Root == nil {
			t.Fatal("plan is nil")
		}
	})
	t.Run("non_mergeable_view_with_aggregation", func(t *testing.T) {
		// Views with aggregation must NOT be merged.
		ResetForTest(t)
		p := NewPlanner()
		p.RegisterTable("t", []DT.ColInfo{{Name: "a", Typ: 1}, {Name: "b", Typ: 1}}, "a")
		viewSel := &PS.Select{
			From: "t",
			Cols: []PS.Expr{
				&PS.QualifiedName{Name: "a"},
				&PS.AliasedExpr{Expr: &PS.AggregateFunc{Name: "MAX", Arg: &PS.QualifiedName{Name: "b"}}, Alias: "m"},
			},
			GroupBy: []PS.Expr{&PS.QualifiedName{Name: "a"}},
		}
		DT.RegisterView("v", viewSel)
		plan, err := p.ParseAndPlan("SELECT m FROM v WHERE m > 5")
		if err != nil {
			t.Fatalf("plan error: %v", err)
		}
		if plan == nil || plan.Root == nil {
			t.Fatal("plan is nil")
		}
	})
	t.Run("non_mergeable_view_with_order_by", func(t *testing.T) {
		ResetForTest(t)
		p := NewPlanner()
		p.RegisterTable("t", []DT.ColInfo{{Name: "a", Typ: 1}, {Name: "b", Typ: 1}}, "a")
		viewSel := &PS.Select{
			From:    "t",
			Cols:    []PS.Expr{&PS.QualifiedName{Name: "a"}},
			OrderBy: []PS.OrderItem{{Expr: &PS.QualifiedName{Name: "a"}}},
		}
		DT.RegisterView("v", viewSel)
		plan, err := p.ParseAndPlan("SELECT a FROM v WHERE a > 5")
		if err != nil {
			t.Fatalf("plan error: %v", err)
		}
		if plan == nil || plan.Root == nil {
			t.Fatal("plan is nil")
		}
	})
	t.Run("non_mergeable_view_with_distinct", func(t *testing.T) {
		ResetForTest(t)
		p := NewPlanner()
		p.RegisterTable("t", []DT.ColInfo{{Name: "a", Typ: 1}, {Name: "b", Typ: 1}}, "a")
		viewSel := &PS.Select{
			From:     "t",
			Cols:     []PS.Expr{&PS.QualifiedName{Name: "a"}},
			Distinct: true,
		}
		DT.RegisterView("v", viewSel)
		plan, err := p.ParseAndPlan("SELECT a FROM v")
		if err != nil {
			t.Fatalf("plan error: %v", err)
		}
		if plan == nil || plan.Root == nil {
			t.Fatal("plan is nil")
		}
	})
	t.Run("non_mergeable_view_with_limit", func(t *testing.T) {
		ResetForTest(t)
		p := NewPlanner()
		p.RegisterTable("t", []DT.ColInfo{{Name: "a", Typ: 1}, {Name: "b", Typ: 1}}, "a")
		viewSel := &PS.Select{
			From:  "t",
			Cols:  []PS.Expr{&PS.QualifiedName{Name: "a"}},
			Limit: &PS.NumberLiteral{Val: int64(10)},
		}
		DT.RegisterView("v", viewSel)
		plan, err := p.ParseAndPlan("SELECT a FROM v WHERE a > 5")
		if err != nil {
			t.Fatalf("plan error: %v", err)
		}
		if plan == nil || plan.Root == nil {
			t.Fatal("plan is nil")
		}
	})
	t.Run("non_mergeable_view_with_subquery_from", func(t *testing.T) {
		// View whose FROM is itself a derived table — not mergeable.
		ResetForTest(t)
		p := NewPlanner()
		p.RegisterTable("t", []DT.ColInfo{{Name: "a", Typ: 1}, {Name: "b", Typ: 1}}, "a")
		viewSel := &PS.Select{
			From:         "t",
			SubqueryFrom: &PS.Select{From: "t", Cols: []PS.Expr{&PS.QualifiedName{Name: "a"}}},
			Cols:         []PS.Expr{&PS.QualifiedName{Name: "a"}},
		}
		DT.RegisterView("v", viewSel)
		plan, err := p.ParseAndPlan("SELECT a FROM v WHERE a > 0")
		if err != nil {
			t.Fatalf("plan error: %v", err)
		}
		if plan == nil || plan.Root == nil {
			t.Fatal("plan is nil")
		}
	})
	t.Run("isViewMergeable_helper", func(t *testing.T) {
		// Unit-test the mergeable predicate directly across the full
		// truth table of disqualifying conditions.
		aggInCols := []PS.Expr{&PS.AggregateFunc{Name: "MAX", Arg: &PS.QualifiedName{Name: "b"}}}
		selAggInCols := &PS.Select{From: "t", Cols: aggInCols}
		selSimple := &PS.Select{From: "t"}
		selSubFrom := &PS.Select{From: "t", SubqueryFrom: &PS.Select{}}
		selDistinct := &PS.Select{From: "t", Distinct: true}
		selGroupBy := &PS.Select{From: "t", GroupBy: []PS.Expr{&PS.QualifiedName{Name: "a"}}}
		selOrderBy := &PS.Select{From: "t", OrderBy: []PS.OrderItem{{Expr: &PS.QualifiedName{Name: "a"}}}}
		selLimit := &PS.Select{From: "t", Limit: &PS.NumberLiteral{Val: int64(10)}}
		selOffset := &PS.Select{From: "t", Offset: &PS.NumberLiteral{Val: int64(0)}}
		selHaving := &PS.Select{From: "t", Having: &PS.NumberLiteral{Val: int64(1)}}
		// Computed column (e.g. v * 2 AS doubled) — not mergeable.
		selComputed := &PS.Select{
			From: "t",
			Cols: []PS.Expr{
				&PS.QualifiedName{Name: "id"},
				&PS.AliasedExpr{
					Expr:  &PS.BinaryExpr{Op: LX.T_STAR, Left: &PS.QualifiedName{Name: "v"}, Right: &PS.NumberLiteral{Val: int64(2)}},
					Alias: "doubled",
				},
			},
		}
		// Star expr — not mergeable.
		selStar := &PS.Select{From: "t", Cols: []PS.Expr{&PS.StarExpr{}}}
		// Aliased column from a plain Ident — mergeable.
		selAliased := &PS.Select{
			From: "t",
			Cols: []PS.Expr{&PS.AliasedExpr{Expr: &PS.QualifiedName{Name: "a"}, Alias: "x"}},
		}
		cases := []struct {
			name string
			sel  *PS.Select
			want bool
		}{
			{"nil", nil, false},
			{"simple", selSimple, true},
			{"subquery_from", selSubFrom, false},
			{"distinct", selDistinct, false},
			{"groupby", selGroupBy, false},
			{"orderby", selOrderBy, false},
			{"limit", selLimit, false},
			{"offset", selOffset, false},
			{"having", selHaving, false},
			{"agg_in_cols", selAggInCols, false},
			{"computed_col", selComputed, false},
			{"star", selStar, false},
			{"aliased_simple", selAliased, true},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				got := isViewMergeable(tc.sel)
				if got != tc.want {
					t.Fatalf("isViewMergeable: got %v, want %v", got, tc.want)
				}
			})
		}
	})
	t.Run("mergeViewIntoOuter_helper", func(t *testing.T) {
		// Verify the merge helper copies outer fields and merges WHEREs.
		ResetForTest(t)
		viewSel := &PS.Select{
			From:  "t",
			Where: &PS.BinaryExpr{Op: LX.T_GT, Left: &PS.QualifiedName{Name: "a"}, Right: &PS.NumberLiteral{Val: int64(0)}},
		}
		outerSel := &PS.Select{
			Cols:  []PS.Expr{&PS.QualifiedName{Name: "a"}},
			Where: &PS.BinaryExpr{Op: LX.T_LT, Left: &PS.QualifiedName{Name: "a"}, Right: &PS.NumberLiteral{Val: int64(100)}},
		}
		merged := mergeViewIntoOuter(outerSel, viewSel)
		if merged.From != "t" {
			t.Fatalf("From: got %q, want t", merged.From)
		}
		be, ok := merged.Where.(*PS.BinaryExpr)
		if !ok {
			t.Fatalf("merged.Where must be BinaryExpr, got %T", merged.Where)
		}
		if be.Op != LX.T_AND {
			t.Fatalf("merged.Where.Op: got %v, want AND", be.Op)
		}
		if merged.SubqueryFrom != nil {
			t.Fatal("merged.SubqueryFrom must be nil after merge")
		}
	})
}
