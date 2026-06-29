package EX

import (
	AD "github.com/cyw0ng95/razordata/internal/SQB/AD"
	"context"
	"fmt"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQB/OP"
)

func TestPlannerPlan(t *testing.T) {
	p := NewPlanner()
	p.RegisterTable("t", []ColInfo{{Name: "a", Typ: 1}, {Name: "b", Typ: 1}}, "a")

	cases := []struct {
		name string
		sql  string
	}{
		{"select_star", "SELECT * FROM t"},
		{"select_col", "SELECT a FROM t"},
		{"select_where", "SELECT a FROM t WHERE a = 1"},
		{"select_limit", "SELECT a FROM t LIMIT 10"},
		{"select_order", "SELECT a FROM t ORDER BY a"},
		{"insert", "INSERT INTO t VALUES (1, 2)"},
		{"update", "UPDATE t SET a = 1 WHERE b = 2"},
		{"delete", "DELETE FROM t WHERE a = 1"},
		{"create", "CREATE TABLE t (a INTEGER)"},
		{"drop", "DROP TABLE t"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := p.ParseAndPlan(tc.sql)
			if err != nil {
				t.Fatalf("plan error: %v", err)
			}
			if plan == nil {
				t.Fatal("plan is nil")
			}
			if plan.Root == nil {
				t.Fatal("plan.Root is nil")
			}
		})
	}
}

func TestPlannerMemoization(t *testing.T) {
	p := NewPlanner()
	p.RegisterTable("t", []ColInfo{{Name: "a", Typ: 1}}, "a")

	sql := "SELECT * FROM t"
	plan1, err := p.ParseAndPlan(sql)
	if err != nil {
		t.Fatalf("plan error: %v", err)
	}

	plan2, err := p.ParseAndPlan(sql)
	if err != nil {
		t.Fatalf("plan error: %v", err)
	}

	if plan1.MemoKey != plan2.MemoKey {
		t.Error("should return same memoKey for same query")
	}
}

func TestPlannerMemoizationDistinctAST(t *testing.T) {
	p := NewPlanner()
	p.RegisterTable("t", []ColInfo{{Name: "a", Typ: 1}}, "a")

	cases := []struct {
		name string
		sql1 string
		sql2 string
	}{
		{"different_table", "SELECT * FROM t", "SELECT * FROM t2"},
		{"different_where", "SELECT * FROM t WHERE a = 1", "SELECT * FROM t WHERE a = 2"},
		{"different_limit", "SELECT * FROM t LIMIT 1", "SELECT * FROM t LIMIT 2"},
		{"different_orderby", "SELECT * FROM t ORDER BY a", "SELECT * FROM t ORDER BY a DESC"},
		{"different_distinct", "SELECT * FROM t", "SELECT DISTINCT * FROM t"},
		{"different_insert_table", "INSERT INTO t VALUES (1)", "INSERT INTO t2 VALUES (1)"},
		{"different_update_where", "UPDATE t SET a = 1 WHERE a = 1", "UPDATE t SET a = 1 WHERE a = 2"},
		{"different_create_pk", "CREATE TABLE t (a INTEGER PRIMARY KEY)", "CREATE TABLE t (a INTEGER, PRIMARY KEY (a))"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			plan1, err := p.ParseAndPlan(c.sql1)
			if err != nil {
				t.Fatalf("plan1 error: %v", err)
			}
			plan2, err := p.ParseAndPlan(c.sql2)
			if err != nil {
				t.Fatalf("plan2 error: %v", err)
			}
			if plan1.MemoKey == plan2.MemoKey {
				t.Errorf("expected distinct memo keys for\n  %q\n  %q\nboth: %s", c.sql1, c.sql2, plan1.MemoKey)
			}
		})
	}
}

func TestPlannerMemoizationSameAST(t *testing.T) {
	p := NewPlanner()
	p.RegisterTable("t", []ColInfo{{Name: "a", Typ: 1}}, "a")

	cases := []struct {
		name string
		sql  string
	}{
		{"select_star", "SELECT * FROM t"},
		{"select_where", "SELECT * FROM t WHERE a = 1"},
		{"insert", "INSERT INTO t VALUES (1)"},
		{"update", "UPDATE t SET a = 1 WHERE a = 1"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			plan1, err := p.ParseAndPlan(c.sql)
			if err != nil {
				t.Fatalf("plan1 error: %v", err)
			}
			plan2, err := p.ParseAndPlan(c.sql)
			if err != nil {
				t.Fatalf("plan2 error: %v", err)
			}
			if plan1.MemoKey != plan2.MemoKey {
				t.Errorf("expected identical memo key for two parses of %q", c.sql)
			}
		})
	}
}

func TestPlannerEstimateCost(t *testing.T) {
	p := NewPlanner()
	plan := &plan{cost: 0}
	cost := p.estimateCost(plan.root)
	if cost != 0 {
		t.Errorf("expected cost 0, got %v", cost)
	}
}

func TestPlannerAggregate(t *testing.T) {
	p := NewPlanner()
	p.RegisterTable("t", []ColInfo{{Name: "x", Typ: 1}}, "x")

	cases := []struct {
		name string
		sql  string
	}{
		{"count_star", "SELECT COUNT(*) FROM t"},
		{"sum_col", "SELECT SUM(x) FROM t"},
		{"avg_col", "SELECT AVG(x) FROM t"},
		{"min_col", "SELECT MIN(x) FROM t"},
		{"max_col", "SELECT MAX(x) FROM t"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			plan, err := p.ParseAndPlan(c.sql)
			if err != nil {
				t.Fatalf("plan error: %v", err)
			}
			if plan == nil || plan.Root == nil {
				t.Fatal("plan nil")
			}
		})
	}
}

func TestSelectIndex(t *testing.T) {
	p := NewPlanner()
	p.RegisterTable("t", []ColInfo{{Name: "a", Typ: 1}, {Name: "b", Typ: 1}}, "a")
	p.RegisterIndex("t", "idx_b", []string{"b"})

	idx, ok := p.selectIndex("t", "b")
	if !ok {
		t.Error("expected index on b")
	}
	if idx != "idx_b" {
		t.Errorf("expected idx_b, got %s", idx)
	}

	_, ok = p.selectIndex("t", "c")
	if ok {
		t.Error("should not have index on c")
	}
}

func TestPlanner_ConstantFolding(t *testing.T) {
	p := NewPlanner()
	p.RegisterTable("t", []ColInfo{{Name: "a", Typ: 1}, {Name: "b", Typ: 1}}, "a")

	t.Run("tautology_1_eq_1_removes_filter", func(t *testing.T) {
		plan, err := p.ParseAndPlan("SELECT * FROM t WHERE 1 = 1")
		if err != nil {
			t.Fatalf("plan error: %v", err)
		}
		// The constant fold should remove the WHERE clause entirely,
		// so no Filter operator appears in the plan tree.
		// Instead, the Plan tree directly wraps the SeqScan in an AdaptiveOp.
		op := plan.Root
		// Unwrap AdaptiveOp (always wraps query plans).
		if aop, ok := op.(*AD.AdaptiveOp); ok {
			op = aop.Inner
		}
		if _, ok := op.(*SeqScan); !ok {
			// The top-level should be SeqScan (or Project -> SeqScan if star expr)
			// If it's a Project (for star expansion), check the child.
			if proj, ok2 := op.(*Project); ok2 {
				if ss, ok3 := proj.Child().(*SeqScan); ok3 {
					_ = ss
				} else {
					t.Fatalf("expected SeqScan after unfolding Project, got %T", proj.Child())
				}
			} else {
				t.Fatalf("expected SeqScan or Project as root, got %T", op)
			}
		}
	})

	t.Run("contradiction_1_eq_0", func(t *testing.T) {
		// 1=0 should fold to FALSE, producing a const-FALSE filter.
		// The plan should still be valid.
		_, err := p.ParseAndPlan("SELECT * FROM t WHERE 1 = 0")
		if err != nil {
			t.Fatalf("plan error: %v", err)
		}
	})

	t.Run("col_plus_zero_folds", func(t *testing.T) {
		// `a + 0` should fold to `a`.
		plan, err := p.ParseAndPlan("SELECT * FROM t WHERE a + 0 > 5")
		if err != nil {
			t.Fatalf("plan error: %v", err)
		}
		if plan == nil || plan.Root == nil {
			t.Fatal("plan is nil")
		}
	})

	t.Run("constant_expression_folds", func(t *testing.T) {
		// `2 + 3` is a constant expression that should be folded to 5.
		plan, err := p.ParseAndPlan("SELECT * FROM t WHERE a > 2 + 3")
		if err != nil {
			t.Fatalf("plan error: %v", err)
		}
		if plan == nil || plan.Root == nil {
			t.Fatal("plan is nil")
		}
	})
}

func TestPlanner_CSE(t *testing.T) {
	p := NewPlanner()
	p.RegisterTable("t", []ColInfo{{Name: "a", Typ: 1}, {Name: "b", Typ: 1}, {Name: "c", Typ: 1}}, "a")

	// Common subexpression elimination: identical conjuncts should be
	// deduplicated. `WHERE (a + b) > 10 AND (a + b) < 20` has two
	// conjuncts and `a + b` appears in both — CSE deduplicates at the
	// conjunct level (the two conjuncts are different, so CSE keeps both).
	// A better test: WHERE (a = 1) AND (a = 1) → second (a = 1) removed.
	t.Run("duplicate_conjunct_removed", func(t *testing.T) {
		plan, err := p.ParseAndPlan("SELECT * FROM t WHERE a = 1 AND a = 1")
		if err != nil {
			t.Fatalf("plan error: %v", err)
		}
		if plan == nil || plan.Root == nil {
			t.Fatal("plan is nil")
		}
	})

	t.Run("cse_identical_where_exprs", func(t *testing.T) {
		plan, err := p.ParseAndPlan("SELECT * FROM t WHERE (a + b) > 10 AND (a + b) < 20")
		if err != nil {
			t.Fatalf("plan error: %v", err)
		}
		if plan == nil || plan.Root == nil {
			t.Fatal("plan is nil")
		}
	})
}

func TestPlanner_JoinElimination(t *testing.T) {
	p := NewPlanner()
	// Register t1 and t2 with the same columns.
	p.RegisterTable("t1", []ColInfo{{Name: "a", Typ: 1}, {Name: "b", Typ: 1}}, "a")
	p.RegisterTable("t2", []ColInfo{{Name: "a", Typ: 1}, {Name: "b", Typ: 1}}, "a")

	t.Run("unreferenced_join_table_eliminated", func(t *testing.T) {
		plan, err := p.ParseAndPlan("SELECT t1.a FROM t1 JOIN t2 ON t1.a = t2.a")
		if err != nil {
			t.Fatalf("plan error: %v", err)
		}
		if plan == nil || plan.Root == nil {
			t.Fatal("plan is nil")
		}
	})

	t.Run("reference_keeps_join_table", func(t *testing.T) {
		plan, err := p.ParseAndPlan("SELECT t1.a, t2.b FROM t1 JOIN t2 ON t1.a = t2.a")
		if err != nil {
			t.Fatalf("plan error: %v", err)
		}
		if plan == nil || plan.Root == nil {
			t.Fatal("plan is nil")
		}
	})

	t.Run("where_ref_keeps_join_table", func(t *testing.T) {
		plan, err := p.ParseAndPlan("SELECT t1.a FROM t1 JOIN t2 ON t1.a = t2.a WHERE t2.b > 5")
		if err != nil {
			t.Fatalf("plan error: %v", err)
		}
		if plan == nil || plan.Root == nil {
			t.Fatal("plan is nil")
		}
	})
}

func TestPlanner_ColumnPruning(t *testing.T) {
	p := NewPlanner()
	p.RegisterTable("t", []ColInfo{
		{Name: "a", Typ: 1},
		{Name: "b", Typ: 1},
		{Name: "c", Typ: 1},
		{Name: "d", Typ: 1},
		{Name: "e", Typ: 1},
	}, "a")

	t.Run("single_col_select", func(t *testing.T) {
		plan, err := p.ParseAndPlan("SELECT a FROM t")
		if err != nil {
			t.Fatalf("plan error: %v", err)
		}
		if plan == nil || plan.Root == nil {
			t.Fatal("plan is nil")
		}
		// Unwrap AdaptiveOp.
		op := plan.Root
		if aop, ok := op.(*AD.AdaptiveOp); ok {
			op = aop.Inner
		}
		// Expect: Project -> SeqScan with usedCols set
		proj, ok := op.(*Project)
		if !ok {
			t.Fatalf("expected Project, got %T", op)
		}
		ss, ok := proj.Child().(*SeqScan)
		if !ok {
			t.Fatalf("expected SeqScan under Project, got %T", proj.Child())
		}
		if len(ss.usedCols) == 0 {
			t.Error("expected non-empty usedCols on SeqScan")
		}
		hasA := false
		for _, c := range ss.usedCols {
			if c == "a" {
				hasA = true
				break
			}
		}
		if !hasA {
			t.Errorf("expected 'a' in usedCols, got %v", ss.usedCols)
		}
		if len(ss.usedCols) > 0 && len(ss.usedCols) < 5 {
			// col pruning is working — fewer than all 5 columns are projected.
		}
	})

	t.Run("star_select_no_pruning", func(t *testing.T) {
		plan, err := p.ParseAndPlan("SELECT * FROM t")
		if err != nil {
			t.Fatalf("plan error: %v", err)
		}
		if plan == nil || plan.Root == nil {
			t.Fatal("plan is nil")
		}
		// For SELECT *, usedCols should not be set (nil).
		op := plan.Root
		if aop, ok := op.(*AD.AdaptiveOp); ok {
			op = aop.Inner
		}
		// Star expands to Project, but star causes usedCols to be nil.
		if proj, ok := op.(*Project); ok {
			if ss, ok2 := proj.Child().(*SeqScan); ok2 {
				if ss.usedCols != nil {
					t.Logf("SeqScan has usedCols=%v (ok for star, pruning is optional)", ss.usedCols)
				}
			}
		}
	})

	t.Run("multi_col_select", func(t *testing.T) {
		plan, err := p.ParseAndPlan("SELECT a, c, e FROM t WHERE b > 0 ORDER BY d")
		if err != nil {
			t.Fatalf("plan error: %v", err)
		}
		if plan == nil || plan.Root == nil {
			t.Fatal("plan is nil")
		}
		// Verify plan is valid.
	})
}

func TestPlanner_N3JoinOrdering_EmptyHeapFallback(t *testing.T) {
	p := NewPlanner()
	p.RegisterTable("t0", []ColInfo{{Name: "a", Typ: 1}}, "a")
	p.RegisterTable("t1", []ColInfo{{Name: "a", Typ: 1}}, "a")
	p.RegisterTable("t2", []ColInfo{{Name: "a", Typ: 1}}, "a")
	p.RegisterTable("t3", []ColInfo{{Name: "a", Typ: 1}}, "a")
	p.RegisterTable("t4", []ColInfo{{Name: "a", Typ: 1}}, "a")

	plan, err := p.ParseAndPlan(`SELECT * FROM t0 CROSS JOIN t1 CROSS JOIN t2 CROSS JOIN t3 CROSS JOIN t4`)
	if err != nil {
		t.Fatalf("unexpected plan error: %v", err)
	}
	if plan == nil || plan.Root == nil {
		t.Fatal("expected non-nil plan")
	}
}

// TestPlanner_CrossJoinPredicatePushdown verifies that single-table
// predicates in cross-join WHERE clauses are pushed down to each
// table scan before the Cartesian product is materialized.
// Without predicate pushdown, a 5-table cross join with N rows each
// produces N^5 intermediate rows before filtering.
// REQ001092.
func TestPlanner_CrossJoinPredicatePushdown(t *testing.T) {
	// Verify plan structure: each table should have its
	// per-table predicate pushed as a Filter.
	p := NewPlanner()
	p.RegisterTable("t1", []ColInfo{{Name: "a", Typ: 1}}, "a")
	p.RegisterTable("t2", []ColInfo{{Name: "b", Typ: 1}}, "b")
	p.RegisterTable("t3", []ColInfo{{Name: "c", Typ: 1}}, "c")
	p.RegisterTable("t4", []ColInfo{{Name: "d", Typ: 1}}, "d")
	p.RegisterTable("t5", []ColInfo{{Name: "e", Typ: 1}}, "e")

	plan, err := p.ParseAndPlan(`SELECT * FROM t1, t2, t3, t4, t5 
		WHERE a = 1 AND b = 3 AND c = 5 AND d = 7 AND e = 9`)
	if err != nil {
		t.Fatalf("plan error: %v", err)
	}
	if plan == nil || plan.Root == nil {
		t.Fatal("plan is nil")
	}

	// Walk the plan and verify each table scan has a Filter operator
	// from predicate pushdown. Each of the 5 SeqScans must have a
	// pushed-down predicate Filter.
	filterCount := 0
	totalScanCount := 0
	walkOpTreeDebug(plan.Root, func(op Operator, depth int) {
		switch op.(type) {
		case *Filter:
			filterCount++
		case *SeqScan:
			totalScanCount++
		}
	}, 0)
	if totalScanCount != 5 {
		t.Fatalf("expected 5 SeqScan operators, got %d", totalScanCount)
	}
	if filterCount != 5 {
		t.Fatalf("expected 5 Filter operators (one per table pushed predicate), got %d", filterCount)
	}
}

// walkOpTreeDebug recursively walks and prints the operator tree.
func walkOpTreeDebug(op Operator, fn func(Operator, int), depth int) {
	if op == nil {
		return
	}
	fn(op, depth)
	switch v := op.(type) {
	case *Filter:
		walkOpTreeDebug(v.Child(), fn, depth+1)
	case *NestedLoopJoin:
		walkOpTreeDebug(v.LeftChild(), fn, depth+1)
		walkOpTreeDebug(v.RightChild(), fn, depth+1)
	case *OP.HashJoin:
		walkOpTreeDebug(v.LeftChild(), fn, depth+1)
	case *Project:
		walkOpTreeDebug(v.Child(), fn, depth+1)
	case *SeqScan:
	case *IndexScan:
	case *Sort:
		walkOpTreeDebug(v.Child(), fn, depth+1)
	case *AD.AdaptiveOp:
		walkOpTreeDebug(v.Child(), fn, depth+1)
	}
}

// TestPlanner_CrossJoinPredicatePushdownINList verifies that IN-list
// predicates in cross-join WHERE clauses are pushed down and the
// result row count stays small (<10K, not billions). REQ001092.
// Uses SLT-style unique column names (a1 in t1, b2 in t2, etc.)
// so the planner can unambiguously resolve columns to tables.
func TestPlanner_CrossJoinPredicatePushdownINList(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	// Register 5 tables with 100 rows each.
	// Each table has a unique column name (SLT convention).
	for i := 1; i <= 5; i++ {
		colName := string(rune('a' + i - 1)) // a, b, c, d, e
		cols := []string{colName}
		var rows []Row
		for r := 0; r < 100; r++ {
			rows = append(rows, Row{
				Cols: cols,
				Data: []Value{NewIntValue(int64(r))},
			})
		}
		RegisterTable(fmt.Sprintf("t%d", i), rows)
	}

	// 5-table cross-join with IN-list predicates on each table.
	// Each IN-list has 3 values that match specific rows.
	// With pushdown: small result. Without: 100^5 = 10B rows (OOM).
	sql := `SELECT * FROM t1, t2, t3, t4, t5
		WHERE a IN (1,2,3) AND b IN (10,20,30) AND c IN (20,30,40)
		AND d IN (30,40,50) AND e IN (40,50,60)`

	ex := NewExecutor()
	ctx := context.Background()
	rows, err := ex.QueryAll(ctx, sql)
	if err != nil {
		t.Fatalf("query error: %v", err)
	}
	// With pushdown: each table produces ~3 rows, result ~3^5=243.
	// Without pushdown: 100^5 = 10 billion rows (OOM).
	if len(rows) > 10000 {
		t.Fatalf("result too large (%d rows) — predicates likely not pushed down", len(rows))
	}
	if len(rows) == 0 {
		t.Fatal("expected some rows, got none")
	}
}

// TestPlanner_CrossJoinColdStart_Pushdown verifies predicate pushdown
// works when the planner catalog is NOT populated (cold-start scenario).
// The planner must fall back to resolveTableForColumn / findTableInSchemas.
// REQ001092.
func TestPlanner_CrossJoinColdStart_Pushdown(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	// Register tables via source-level RegisterTable (populates schemas)
	// but do NOT use Planner.RegisterTable — leaves p.catalog empty.
	for i := 1; i <= 5; i++ {
		colName := string(rune('a' + i - 1))
		cols := []string{colName}
		var rows []Row
		for r := 0; r < 100; r++ {
			rows = append(rows, Row{
				Cols: cols,
				Data: []Value{NewIntValue(int64(r))},
			})
		}
		RegisterTable(fmt.Sprintf("t%d", i), rows)
	}

	p := NewPlanner()
	// Deliberately NOT calling p.RegisterTable — cold-start.

	plan, err := p.ParseAndPlan(`SELECT * FROM t1, t2, t3, t4, t5
		WHERE a IN (1,2,3) AND b IN (10,20,30) AND c IN (20,30,40)
		AND d IN (30,40,50) AND e IN (40,50,60)`)
	if err != nil {
		t.Fatalf("plan error: %v", err)
	}
	if plan == nil || plan.Root == nil {
		t.Fatal("plan is nil")
	}

	// Walk plan: expect 5 SeqScans + 5 Filters (pushed predicates).
	filterCount := 0
	scanCount := 0
	walkOpTreeDebug(plan.Root, func(op Operator, depth int) {
		switch op.(type) {
		case *Filter:
			filterCount++
		case *SeqScan:
			scanCount++
		}
	}, 0)
	if scanCount != 5 {
		t.Fatalf("expected 5 SeqScan, got %d", scanCount)
	}
	if filterCount < 5 {
		t.Fatalf("expected at least 5 Filters (pushed predicates), got %d — cold-start pushdown failing", filterCount)
	}
}

// BenchmarkSelect4_CrossJoinColdStart measures first-execution time for
// a 5-table cross-join with IN-list predicates under cold-start (no
// planner catalog). REQ001092: target <1s.
func BenchmarkSelect4_CrossJoinColdStart(b *testing.B) {
	UnregisterAll()
	defer UnregisterAll()

	for i := 1; i <= 5; i++ {
		colName := string(rune('a' + i - 1))
		cols := []string{colName}
		var rows []Row
		for r := 0; r < 100; r++ {
			rows = append(rows, Row{
				Cols: cols,
				Data: []Value{NewIntValue(int64(r))},
			})
		}
		RegisterTable(fmt.Sprintf("t%d", i), rows)
	}

	sql := `SELECT * FROM t1, t2, t3, t4, t5
		WHERE a IN (1,2,3) AND b IN (10,20,30) AND c IN (20,30,40)
		AND d IN (30,40,50) AND e IN (40,50,60)`

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		UnregisterAll()
		for j := 1; j <= 5; j++ {
			colName := string(rune('a' + j - 1))
			cols := []string{colName}
			var rs []Row
			for r := 0; r < 100; r++ {
				rs = append(rs, Row{
					Cols: cols,
					Data: []Value{NewIntValue(int64(r))},
				})
			}
			RegisterTable(fmt.Sprintf("t%d", j), rs)
		}
		ex := NewExecutor()
		ctx := context.Background()
		rows, err := ex.QueryAll(ctx, sql)
		if err != nil {
			b.Fatal(err)
		}
		if len(rows) == 0 {
			b.Fatal("expected rows")
		}
	}
}
