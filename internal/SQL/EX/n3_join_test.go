package EX

import (
	"testing"
)

func TestN3JoinOrdering_NoJoins(t *testing.T) {
	p := NewPlanner()
	order := p.n3JoinOrdering("t1", nil, nil, nil)
	if len(order) != 1 || order[0] != "t1" {
		t.Fatalf("expected [t1], got %v", order)
	}
}

func TestN3JoinOrdering_SingleJoin(t *testing.T) {
	p := NewPlanner()
	p.RegisterTable("t1", []ColInfo{{Name: "id", Typ: 1}}, "id")
	p.RegisterTable("t2", []ColInfo{{Name: "id", Typ: 1}}, "id")

	joinTables := []joinTableInfo{{name: "t2"}}
	order := p.n3JoinOrdering("t1", joinTables, nil, nil)
	if len(order) != 2 || order[0] != "t1" || order[1] != "t2" {
		t.Fatalf("expected [t1 t2], got %v", order)
	}
}

func TestN3JoinOrdering_ThreeTablesDefault(t *testing.T) {
	p := NewPlanner()
	p.RegisterTable("t1", []ColInfo{{Name: "id", Typ: 1}}, "id")
	p.RegisterTable("t2", []ColInfo{{Name: "id", Typ: 1}}, "id")
	p.RegisterTable("t3", []ColInfo{{Name: "id", Typ: 1}}, "id")

	joinTables := []joinTableInfo{{name: "t2"}, {name: "t3"}}
	order := p.n3JoinOrdering("t1", joinTables, nil, nil)
	if len(order) != 3 {
		t.Fatalf("expected 3 tables, got %v", order)
	}
	if order[0] != "t1" {
		t.Fatalf("expected base table t1 first, got %s", order[0])
	}
}

func TestN3JoinOrdering_EdgeCases(t *testing.T) {
	p := NewPlanner()

	// Empty join table list.
	order := p.n3JoinOrdering("t1", []joinTableInfo{}, nil, nil)
	if len(order) != 1 || order[0] != "t1" {
		t.Fatalf("expected [t1], got %v", order)
	}
}

func TestEstimateJoinCost_CrossJoin(t *testing.T) {
	p := NewPlanner()
	cost := p.estimateJoinCost(100, 200, nil, false)
	expected := float64(100 * 200)
	if cost != expected {
		t.Fatalf("expected %v, got %v", expected, cost)
	}
}

func TestEstimateJoinCost_WithIndex(t *testing.T) {
	p := NewPlanner()
	cost := p.estimateJoinCost(100, 200, nil, true)
	expected := float64(100*200) * 0.2
	if cost != expected {
		t.Fatalf("expected %v, got %v", expected, cost)
	}
}

func TestEstimateJoinCost_MinCostFloor(t *testing.T) {
	p := NewPlanner()
	cost := p.estimateJoinCost(1, 1, nil, false)
	if cost < 1 {
		t.Fatalf("expected cost >= 1, got %v", cost)
	}
}

func TestJoinResultRows_CrossJoin(t *testing.T) {
	result := joinResultRows(100, 200, nil)
	if result != float64(100*200) {
		t.Fatalf("expected %v, got %v", float64(20000), result)
	}
}

func TestJoinResultRows_MinFloor(t *testing.T) {
	result := joinResultRows(1, 1, nil)
	if result < 1 {
		t.Fatalf("expected result >= 1, got %v", result)
	}
}

func TestGetTableRowCount_Default(t *testing.T) {
	p := NewPlanner()
	count := p.getTableRowCount("nonexistent")
	if count != 100 {
		t.Fatalf("expected default 100, got %v", count)
	}
}

func TestEstimateJoinPredicateSelectivity(t *testing.T) {
	// Nil predicate.
	sel := estimateJoinPredicateSelectivity(nil)
	if sel != 1.0 {
		t.Fatalf("expected 1.0 for nil, got %v", sel)
	}
}
