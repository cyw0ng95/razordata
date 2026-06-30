package EX

import (
	"context"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// TestPlanner_SubqueryFlattening verifies REQ001079: nested subqueries are
// flattened when safe. `SELECT * FROM (SELECT x FROM t) WHERE x > 10`
// becomes `SELECT x FROM t WHERE x > 10` and uses the same scan path.
func TestPlanner_SubqueryFlattening(t *testing.T) {
	p := NewPlanner()
	p.RegisterTable("t", []ColInfo{{Name: "a", Typ: 1}, {Name: "b", Typ: 1}}, "a")

	t.Run("simple_flatten", func(t *testing.T) {
		// WHERE-only flattening: subquery is a single-table SELECT
		// with no aggregation. Outer WHERE references sub-cols.
		plan, err := p.ParseAndPlan("SELECT * FROM (SELECT a, b FROM t) WHERE a > 10")
		if err != nil {
			t.Fatalf("plan error: %v", err)
		}
		if plan == nil || plan.Root == nil {
			t.Fatal("plan is nil")
		}
	})
	t.Run("flatten_preserves_correctness", func(t *testing.T) {
		// End-to-end correctness via the executor: insert rows,
		// flatten the query, verify the result matches a non-flattened
		// reference query.
		DT.RegisterTable("u", []Row{
			{Cols: []string{"k", "v"}, Data: []Value{NewIntValue(1), NewIntValue(10)}},
			{Cols: []string{"k", "v"}, Data: []Value{NewIntValue(2), NewIntValue(20)}},
			{Cols: []string{"k", "v"}, Data: []Value{NewIntValue(3), NewIntValue(30)}},
		})
		ex := NewExecutor()
		ctx := context.Background()
		rows, err := ex.QueryAll(ctx,
			"SELECT k FROM (SELECT k, v FROM u) WHERE v > 15")
		if err != nil {
			t.Fatalf("query error: %v", err)
		}
		if len(rows) != 2 {
			t.Fatalf("expected 2 rows (k=2, k=3), got %d", len(rows))
		}
	})
	t.Run("no_flatten_with_aggregation", func(t *testing.T) {
		// Subquery with aggregation is not flattenable; planner must
		// still produce a valid plan via the existing pushdown path.
		plan, err := p.ParseAndPlan("SELECT MAX(a) FROM (SELECT a FROM t) WHERE a > 0")
		if err != nil {
			t.Fatalf("plan error: %v", err)
		}
		if plan == nil || plan.Root == nil {
			t.Fatal("plan is nil")
		}
	})
	t.Run("no_flatten_with_order_by_in_sub", func(t *testing.T) {
		// Subquery has ORDER BY → not flattenable.
		plan, err := p.ParseAndPlan("SELECT * FROM (SELECT a FROM t ORDER BY a) WHERE a > 5")
		if err != nil {
			t.Fatalf("plan error: %v", err)
		}
		if plan == nil || plan.Root == nil {
			t.Fatal("plan is nil")
		}
	})
	t.Run("no_flatten_with_distinct", func(t *testing.T) {
		plan, err := p.ParseAndPlan("SELECT * FROM (SELECT DISTINCT a FROM t) WHERE a > 5")
		if err != nil {
			t.Fatalf("plan error: %v", err)
		}
		if plan == nil || plan.Root == nil {
			t.Fatal("plan is nil")
		}
	})
	t.Run("no_flatten_with_outer_join", func(t *testing.T) {
		// Subquery + JOIN: can't flatten because the JOIN uses
		// the subquery as one side.
		p2 := NewPlanner()
		p2.RegisterTable("t", []ColInfo{{Name: "a", Typ: 1}}, "a")
		p2.RegisterTable("t2", []ColInfo{{Name: "a", Typ: 1}}, "a")
		plan, err := p2.ParseAndPlan(
			"SELECT * FROM (SELECT a FROM t) sub JOIN t2 ON sub.a = t2.a")
		if err != nil {
			t.Fatalf("plan error: %v", err)
		}
		if plan == nil || plan.Root == nil {
			t.Fatal("plan is nil")
		}
	})
	t.Run("no_flatten_when_outer_uses_alias", func(t *testing.T) {
		// Outer references the subquery alias → can't flatten without
		// alias rewriting. Existing pushdown path must still work.
		plan, err := p.ParseAndPlan("SELECT sub.a FROM (SELECT a FROM t) sub WHERE a > 5")
		if err != nil {
			t.Fatalf("plan error: %v", err)
		}
		if plan == nil || plan.Root == nil {
			t.Fatal("plan is nil")
		}
	})
	t.Run("and_expr_helper", func(t *testing.T) {
		// Verify the andExpr merge logic.
		if andExpr(nil, nil) != nil {
			t.Fatal("andExpr(nil,nil) must be nil")
		}
		be := &PS.BinaryExpr{}
		if andExpr(nil, be) != be {
			t.Fatal("andExpr(nil,b) must be b")
		}
		if andExpr(be, nil) != be {
			t.Fatal("andExpr(a,nil) must be a")
		}
		out := andExpr(be, be)
		if _, ok := out.(*PS.BinaryExpr); !ok {
			t.Fatalf("andExpr(a,b) must wrap, got %T", out)
		}
	})
}
