package PL

import (
	"testing"
)

func TestPlannerPlan(t *testing.T) {
	p := NewPlanner()
	p.RegisterTable("t", []colInfo{{name: "a", typ: 1}, {name: "b", typ: 1}}, "a")

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
			if plan.root == nil {
				t.Fatal("plan.root is nil")
			}
		})
	}
}

func TestPlannerMemoization(t *testing.T) {
	p := NewPlanner()
	p.RegisterTable("t", []colInfo{{name: "a", typ: 1}}, "a")

	sql := "SELECT * FROM t"
	plan1, err := p.ParseAndPlan(sql)
	if err != nil {
		t.Fatalf("plan error: %v", err)
	}

	plan2, err := p.ParseAndPlan(sql)
	if err != nil {
		t.Fatalf("plan error: %v", err)
	}

	if plan1.memoKey != plan2.memoKey {
		t.Error("should return same memoKey for same query")
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

func TestSelectIndex(t *testing.T) {
	p := NewPlanner()
	p.RegisterTable("t", []colInfo{{name: "a", typ: 1}, {name: "b", typ: 1}}, "a")
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
