package EX

import (
	"context"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQB/OP"
)

// REQ000725: Multi-table implicit cross join (4+ DT.Tables) returns 0
// rows. The planner only handles explicit JOINs; comma-separated
// DT.Tables in FROM with WHERE conditions acting as join predicates
// are ignored except for the first table.
func TestREQ000725_ImplicitCrossJoin(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t29", []string{"a29", "x29"})
	ex.RegisterTable("t31", []string{"a31", "x31"})
	ex.RegisterTable("t51", []string{"a51", "x51"})
	ex.RegisterTable("t55", []string{"a55", "x55"})

	ctx := context.Background()
	// Setup minimal matching data.
	// t51: a51=1, x51=10
	// t29: a29=1, x29=20
	// t31: a31=1, x31=30
	// t55: a55=1, x55=40
	// WHERE a51=b31 AND a29=6 AND a29=b51 AND b55=a31
	// a29 must equal 6; t29 has only a29=1, so no rows match.
	// Let's use a29=1 instead of 6 for the test.
	rows := []struct {
		t    string
		cols []any
	}{
		{"t29", []any{1, 20}},
		{"t31", []any{1, 30}},
		{"t51", []any{1, 10}},
		{"t55", []any{1, 40}},
	}
	for _, r := range rows {
		_, err := ex.Exec(ctx, "INSERT INTO "+r.t+" VALUES ("+OP.ItoaSimple(r.cols[0].(int))+", "+OP.ItoaSimple(r.cols[1].(int))+")")
		if err != nil {
			t.Fatalf("insert %s: %v", r.t, err)
		}
	}
	got, err := ex.QueryAll(ctx, "SELECT x29, x31, x51, x55 FROM t51, t29, t31, t55 WHERE a29=a51 AND a55=a31")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	t.Logf("got %d rows", len(got))
	for i, r := range got {
		t.Logf("  row %d: Cols=%v Data=%v", i, r.Cols, r.Data)
	}
	// 3-table
	got3, err := ex.QueryAll(ctx, "SELECT x29, x31, x51 FROM t51, t29, t31 WHERE a29=a51 AND a31=a51")
	if err != nil {
		t.Logf("3-table query err: %v", err)
	} else {
		t.Logf("3-table got %d rows", len(got3))
	}
	// 4-table
	got4, err := ex.QueryAll(ctx, "SELECT x29, x31, x51, x55 FROM t51, t29, t31, t55 WHERE a51=1 AND a29=1 AND a29=a51 AND a55=a31")
	if err != nil {
		t.Logf("4-table err: %v", err)
	} else {
		t.Logf("4-table got %d rows", len(got4))
		for i, r := range got4 {
			t.Logf("  row %d: %v", i, r.Data)
		}
	}
	// 4-table with pure equi-join (no constants)
	got4b, err := ex.QueryAll(ctx, "SELECT x29, x31, x51, x55 FROM t51, t29, t31, t55 WHERE a29=a51 AND a55=a31")
	if err != nil {
		t.Logf("4b err: %v", err)
	} else {
		t.Logf("4b got %d rows", len(got4b))
		for i, r := range got4b {
			t.Logf("  4b row %d: Cols=%v Data=%v", i, r.Cols, r.Data)
		}
	}
	// 4-table plain cross join (no WHERE)
	got4x, err := ex.QueryAll(ctx, "SELECT x29, x31, x51, x55 FROM t51, t29, t31, t55")
	if err != nil {
		t.Logf("4x err: %v", err)
	} else {
		t.Logf("4x got %d rows", len(got4x))
		for i, r := range got4x {
			t.Logf("  4x row %d: Cols=%v Data=%v", i, r.Cols, r.Data)
		}
	}
	// 4-table via explicit JOIN
	got4c, err := ex.QueryAll(ctx, "SELECT x29, x31, x51, x55 FROM t51 JOIN t29 ON a29=a51 JOIN t31 ON 1=1 JOIN t55 ON a55=a31")
	if err != nil {
		t.Logf("4c err: %v", err)
	} else {
		t.Logf("4c got %d rows", len(got4c))
	}
	// 3-table with single equi-join (chained)
	got3b, err := ex.QueryAll(ctx, "SELECT x29 FROM t29, t31, t51 WHERE a29=a51")
	if err != nil {
		t.Logf("3b err: %v", err)
	} else {
		t.Logf("3b got %d rows", len(got3b))
	}
}
