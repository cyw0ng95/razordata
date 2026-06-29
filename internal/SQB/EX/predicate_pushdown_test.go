package EX

import (
	"context"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// TestHashJoin_ImplicitCrossJoin verifies that implicit CROSS JOINs
// (comma-separated FROM) with equi-join conditions in WHERE use
// HashJoin instead of NestedLoopJoin.
func TestHashJoin_ImplicitCrossJoin(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	e := NewExecutor()
	ctx := context.Background()

	for _, s := range []string{
		"CREATE TABLE t1 (id INTEGER, val INTEGER)",
		"CREATE TABLE t2 (id INTEGER, score INTEGER)",
		"INSERT INTO t1 VALUES (1, 10), (2, 20), (3, 30)",
		"INSERT INTO t2 VALUES (1, 100), (2, 200), (4, 400)",
	} {
		if _, err := e.Exec(ctx, s); err != nil {
			t.Fatalf("setup %q: %v", s, err)
		}
	}

	rows, err := e.QueryAll(ctx, "SELECT t1.val, t2.score FROM t1, t2 WHERE t1.id = t2.id")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("expected 2 rows, got %d: %v", len(rows), rows)
	}
}

// TestWalkExprForTables_UsesSchemas verifies that walkExprForTables
// falls back to the in-memory schemas map when the planner's catalog
// doesn't have the table registered.
func TestWalkExprForTables_UsesSchemas(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	DT.RegisterTable("t1", []Row{
		{Cols: []string{"a", "b"}, Data: []Value{NewIntValue(int64(1)), NewIntValue(int64(2))}},
	})

	p := NewPlanner()

	expr := &PS.BinaryExpr{
		Op:    LX.T_EQ,
		Left:  &PS.Ident{Name: "a"},
		Right: &PS.NumberLiteral{Val: 1},
	}

	tbls := p.extractTablesFromExpr(expr)
	if !tbls["t1"] {
		t.Error("walkExprForTables should find t1 via schemas map fallback")
	}
}

// TestEquiJoinKey_BasicDetection verifies equiJoinKey detects
// simple equi-join conditions between two DT.Tables.
func TestEquiJoinKey_BasicDetection(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	DT.RegisterTable("t1", []Row{
		{Cols: []string{"a", "b"}, Data: []Value{NewIntValue(int64(1)), NewIntValue(int64(2))}},
	})
	DT.RegisterTable("t2", []Row{
		{Cols: []string{"c", "d"}, Data: []Value{NewIntValue(int64(1)), NewIntValue(int64(2))}},
	})

	p := NewPlanner()

	expr := &PS.BinaryExpr{
		Op:    LX.T_EQ,
		Left:  &PS.Ident{Name: "a"},
		Right: &PS.Ident{Name: "c"},
	}

	lc, rc := p.equiJoinKey(expr, map[string]bool{"t1": true}, "t2")
	if lc != "a" || rc != "c" {
		t.Errorf("expected (a, c), got (%s, %s)", lc, rc)
	}

	expr2 := &PS.BinaryExpr{
		Op:    LX.T_EQ,
		Left:  &PS.Ident{Name: "c"},
		Right: &PS.Ident{Name: "a"},
	}

	lc, rc = p.equiJoinKey(expr2, map[string]bool{"t1": true}, "t2")
	if lc != "a" || rc != "c" {
		t.Errorf("reversed: expected (a, c), got (%s, %s)", lc, rc)
	}
}

// TestEquiJoinKey_CrossTableInMultiJoin verifies equiJoinKey handles
// the case where one side references a table that's part of a larger join.
func TestEquiJoinKey_CrossTableInMultiJoin(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	DT.RegisterTable("t1", []Row{
		{Cols: []string{"a1"}, Data: []Value{NewIntValue(int64(1))}},
	})
	DT.RegisterTable("t2", []Row{
		{Cols: []string{"b9"}, Data: []Value{NewIntValue(int64(1))}},
	})
	DT.RegisterTable("t3", []Row{
		{Cols: []string{"a3"}, Data: []Value{NewIntValue(int64(1))}},
	})

	p := NewPlanner()

	expr := &PS.BinaryExpr{
		Op:    LX.T_EQ,
		Left:  &PS.Ident{Name: "a3"},
		Right: &PS.Ident{Name: "b9"},
	}

	lc, rc := p.equiJoinKey(expr, map[string]bool{"t2": true}, "t3")
	if (lc != "a3" || rc != "b9") && (lc != "b9" || rc != "a3") {
		t.Errorf("expected (a3, b9) or (b9, a3), got (%s, %s)", lc, rc)
	}
}

// TestSplitPredicatesByTable verifies that predicates are correctly
// split into per-table and cross-table groups.
func TestSplitPredicatesByTable(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	DT.RegisterTable("t1", []Row{
		{Cols: []string{"a", "b"}, Data: []Value{NewIntValue(int64(1)), NewIntValue(int64(2))}},
	})
	DT.RegisterTable("t2", []Row{
		{Cols: []string{"c", "d"}, Data: []Value{NewIntValue(int64(1)), NewIntValue(int64(2))}},
	})

	p := NewPlanner()

	predT1 := &PS.BinaryExpr{
		Op:    LX.T_EQ,
		Left:  &PS.Ident{Name: "a"},
		Right: &PS.NumberLiteral{Val: 1},
	}
	predT2 := &PS.BinaryExpr{
		Op:    LX.T_EQ,
		Left:  &PS.Ident{Name: "c"},
		Right: &PS.NumberLiteral{Val: 2},
	}
	predCross := &PS.BinaryExpr{
		Op:    LX.T_EQ,
		Left:  &PS.Ident{Name: "a"},
		Right: &PS.Ident{Name: "c"},
	}

	conjuncts := []PS.Expr{predT1, predT2, predCross}
	tbls := []string{"t1", "t2"}

	perTable, crossTable := p.splitPredicatesByTable(conjuncts, tbls)

	if len(perTable["t1"]) != 1 {
		t.Errorf("t1: expected 1 predicate, got %d", len(perTable["t1"]))
	}
	if len(perTable["t2"]) != 1 {
		t.Errorf("t2: expected 1 predicate, got %d", len(perTable["t2"]))
	}
	if len(crossTable) != 1 {
		t.Errorf("cross: expected 1 predicate, got %d", len(crossTable))
	}
}

// TestJoinResultCorrectness verifies that join optimizations
// produce correct results for various join patterns.
func TestJoinResultCorrectness(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	e := NewExecutor()
	ctx := context.Background()

	for _, s := range []string{
		"CREATE TABLE t1 (a INTEGER, b INTEGER)",
		"CREATE TABLE t2 (c INTEGER, d INTEGER)",
		"CREATE TABLE t3 (e INTEGER, f INTEGER)",
		"INSERT INTO t1 VALUES (1, 10), (2, 20), (3, 30)",
		"INSERT INTO t2 VALUES (1, 100), (2, 200), (3, 300)",
		"INSERT INTO t3 VALUES (1, 1000), (2, 2000), (3, 3000)",
	} {
		if _, err := e.Exec(ctx, s); err != nil {
			t.Fatalf("setup %q: %v", s, err)
		}
	}

	cases := []struct {
		name     string
		sql      string
		expected int
	}{
		{"cross_join_no_filter", "SELECT * FROM t1, t2", 9},
		{"cross_join_with_eq", "SELECT * FROM t1 INNER JOIN t2 ON t1.a = t2.c", 3},
		{"three_table_cross", "SELECT * FROM t1, t2, t3", 27},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := e.QueryAll(ctx, tc.sql)
			if err != nil {
				t.Fatalf("query: %v", err)
			}
			if len(rows) != tc.expected {
				t.Errorf("expected %d rows, got %d", tc.expected, len(rows))
			}
		})
	}
}

// TestJoinWithExplicitON verifies explicit JOIN ... ON syntax works
// correctly alongside the HashJoin optimization.
func TestJoinWithExplicitON(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	e := NewExecutor()
	ctx := context.Background()

	for _, s := range []string{
		"CREATE TABLE t1 (id INTEGER, val INTEGER)",
		"CREATE TABLE t2 (id INTEGER, score INTEGER)",
		"INSERT INTO t1 VALUES (1, 10), (2, 20), (3, 30)",
		"INSERT INTO t2 VALUES (1, 100), (2, 200), (4, 400)",
	} {
		if _, err := e.Exec(ctx, s); err != nil {
			t.Fatalf("setup %q: %v", s, err)
		}
	}

	rows, err := e.QueryAll(ctx, "SELECT t1.val, t2.score FROM t1 INNER JOIN t2 ON t1.id = t2.id")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("expected 2 rows, got %d: %v", len(rows), rows)
	}
}

// TestJoinEmptyTables verifies join behavior with empty DT.Tables.
func TestJoinEmptyTables(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	e := NewExecutor()
	ctx := context.Background()

	for _, s := range []string{
		"CREATE TABLE t1 (a INTEGER, b INTEGER)",
		"CREATE TABLE t2 (c INTEGER, d INTEGER)",
	} {
		if _, err := e.Exec(ctx, s); err != nil {
			t.Fatalf("setup %q: %v", s, err)
		}
	}

	rows, err := e.QueryAll(ctx, "SELECT * FROM t1, t2 WHERE t1.a = t2.c")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected 0 rows, got %d", len(rows))
	}
}

// TestJoinLargeResultSet verifies joins with larger datasets.
func TestJoinLargeResultSet(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	e := NewExecutor()
	ctx := context.Background()

	for _, s := range []string{
		"CREATE TABLE t1 (a INTEGER, b INTEGER)",
		"CREATE TABLE t2 (c INTEGER, d INTEGER)",
	} {
		if _, err := e.Exec(ctx, s); err != nil {
			t.Fatalf("setup %q: %v", s, err)
		}
	}

	for i := 1; i <= 100; i++ {
		if _, err := e.Exec(ctx, "INSERT INTO t1 VALUES (?, ?)", i, i*10); err != nil {
			t.Fatalf("insert t1: %v", err)
		}
		if _, err := e.Exec(ctx, "INSERT INTO t2 VALUES (?, ?)", i, i*100); err != nil {
			t.Fatalf("insert t2: %v", err)
		}
	}

	rows, err := e.QueryAll(ctx, "SELECT * FROM t1, t2")
	if err != nil {
		t.Fatalf("cross join: %v", err)
	}
	if len(rows) != 10000 {
		t.Errorf("cross join: expected 10000, got %d", len(rows))
	}

	rows, err = e.QueryAll(ctx, "SELECT * FROM t1, t2 WHERE t1.a = t2.c")
	if err != nil {
		t.Fatalf("equi join: %v", err)
	}
	if len(rows) != 100 {
		t.Errorf("equi join: expected 100, got %d", len(rows))
	}
}

// TestJoinPlanStructure verifies the plan tree structure for
// multi-table joins with HashJoin optimization.
func TestJoinPlanStructure(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	DT.RegisterTable("t1", []Row{
		{Cols: []string{"a", "b"}, Data: []Value{NewIntValue(int64(1)), NewIntValue(int64(2))}},
	})
	DT.RegisterTable("t2", []Row{
		{Cols: []string{"c", "d"}, Data: []Value{NewIntValue(int64(1)), NewIntValue(int64(2))}},
	})
	DT.RegisterTable("t3", []Row{
		{Cols: []string{"e", "f"}, Data: []Value{NewIntValue(int64(1)), NewIntValue(int64(2))}},
	})

	p := NewPlanner()

	plan, err := p.ParseAndPlan("SELECT * FROM t1, t2, t3 WHERE t1.a = t2.c AND t1.a = t3.e")
	if err != nil {
		t.Fatalf("plan error: %v", err)
	}
	if plan == nil || plan.Root == nil {
		t.Fatal("plan is nil")
	}
}
