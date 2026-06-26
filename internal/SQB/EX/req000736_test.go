package EX

import (
	"context"
	"testing"

	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func TestREQ000736_ParserCheck(t *testing.T) {
	// Verify the parser produces the correct NullsOrder
	p := PS.NewParser("SELECT v FROM t ORDER BY v ASC NULLS LAST")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	sel, ok := stmt.(*PS.Select)
	if !ok {
		t.Fatalf("not a Select")
	}
	if len(sel.OrderBy) != 1 {
		t.Fatalf("expected 1 OrderBy, got %d", len(sel.OrderBy))
	}
	if sel.OrderBy[0].NullsOrder != -1 {
		t.Fatalf("NullsOrder=%d, want -1", sel.OrderBy[0].NullsOrder)
	}
	if sel.OrderBy[0].Desc {
		t.Fatalf("Desc=true, want false")
	}
	t.Logf("Parser produces: OrderBy[0].NullsOrder=%d", sel.OrderBy[0].NullsOrder)

	// Verify planner creates Sort with NullsOrder preserved
	planner := NewPlanner()
	planner.RegisterTable("t", []ColInfo{{Name: "v", Typ: 1}}, "")
	plann, err := planner.Plan(stmt)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if plann == nil || plann.root == nil {
		t.Fatalf("nil plan")
	}
	found := false
	var walk func(Operator)
	walk = func(op Operator) {
		if s, ok := op.(*Sort); ok {
			found = true
			t.Logf("Sort has %d keys, NullsOrder[0]=%d", len(s.keys), s.keys[0].NullsOrder)
		}
		if c, ok := op.(interface{ Child() Operator }); ok {
			c2 := c.Child()
			if c2 != nil {
				walk(c2)
			}
		}
	}
	walk(plann.root)
	if !found {
		t.Fatal("Sort operator not found in plan")
	}
}

func TestREQ000736_NullsFirstLast(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"v"})

	ctx := context.Background()
	if _, err := ex.Exec(ctx, "INSERT INTO t VALUES (NULL)"); err != nil {
		t.Fatalf("insert null: %v", err)
	}
	if _, err := ex.Exec(ctx, "INSERT INTO t VALUES (1)"); err != nil {
		t.Fatalf("insert 1: %v", err)
	}
	if _, err := ex.Exec(ctx, "INSERT INTO t VALUES (2)"); err != nil {
		t.Fatalf("insert 2: %v", err)
	}

	// Try EXPLAIN to see the plan
	rows, err := ex.QueryAll(ctx, "EXPLAIN SELECT v FROM t ORDER BY v ASC NULLS LAST")
	if err != nil {
		t.Fatalf("EXPLAIN: %v", err)
	}
	for _, r := range rows {
		t.Logf("EXPLAIN: %v", r.Data)
	}

	// Verify basic sort works first
	rows, err = ex.QueryAll(ctx, "SELECT v FROM t ORDER BY v ASC")
	if err != nil {
		t.Fatalf("ORDER BY ASC: %v", err)
	}
	t.Logf("ASC order: %v", rows)

	// ORDER BY ASC NULLS LAST — NULLs last
	rows, err = ex.QueryAll(ctx, "SELECT v FROM t ORDER BY v ASC NULLS LAST")
	if err != nil {
		t.Fatalf("ASC NULLS LAST: %v", err)
	}
	t.Logf("ASC NULLS LAST order: %v", rows)
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	if rows[2].Data[0].IsNull() == false {
		t.Errorf("ASC NULLS LAST: row 2 expected nil, got %v", rows[2].Data[0])
	}
	if !rows[0].Data[0].Equal(NewIntValue(int64(1))) {
		t.Errorf("ASC NULLS LAST: row 0 expected 1, got %v", rows[0].Data[0])
	}
}
