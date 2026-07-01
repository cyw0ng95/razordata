package EX

import (
	"context"
	"strconv"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	OP "github.com/cyw0ng95/razordata/internal/SQB/OP"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// REQ000722: WHERE with comparison on indexed DT.Tables returning 0
// rows via driver path. Guard test.
func TestREQ000722_WhereOnIndexReturnsRows(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex, eng := newEngineExecutor(t)
	defer eng.Close()

	ctx := context.Background()
	if _, err := ex.Exec(ctx, "CREATE TABLE tab1 (pk INTEGER PRIMARY KEY, col0 INTEGER)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := ex.Exec(ctx, "CREATE INDEX idx_col0 ON tab1(col0)"); err != nil {
		t.Fatalf("create index: %v", err)
	}
	for i := 1; i <= 100; i++ {
		if _, err := ex.Exec(ctx, "INSERT INTO tab1 VALUES ("+strconv.Itoa(i)+", "+strconv.Itoa(i*10)+")"); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	rows, err := ex.QueryAll(ctx, "SELECT pk FROM tab1 WHERE col0 <= 605 ORDER BY pk DESC")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) == 0 {
		t.Errorf("REQ000722: store-path WHERE on indexed table returned 0 rows; bug regressed?")
	}
	_ = eng
}

// REQ000723: NOT(...) filter returns wrong results on indexed DT.Tables.
func TestREQ000723_NOTFilterOnIndex(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex, eng := newEngineExecutor(t)
	defer eng.Close()

	ctx := context.Background()
	if _, err := ex.Exec(ctx, "CREATE TABLE tab0 (pk INTEGER PRIMARY KEY, col0 INTEGER)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := ex.Exec(ctx, "CREATE INDEX idx ON tab0(col0)"); err != nil {
		t.Fatalf("create index: %v", err)
	}
	for i := 1; i <= 10; i++ {
		if _, err := ex.Exec(ctx, "INSERT INTO tab0 VALUES ("+strconv.Itoa(i)+", "+strconv.Itoa(i*10)+")"); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	rows, err := ex.QueryAll(ctx, "SELECT pk FROM tab0 WHERE NOT ((col0 > 68))")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) == 0 {
		t.Errorf("expected >0 rows, got 0")
	}
	_ = eng
}

// REQ000724: Complex OR/AND/IN on indexed DT.Tables.
func TestREQ000724_ComplexORANDIN(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex, eng := newEngineExecutor(t)
	defer eng.Close()

	ctx := context.Background()
	if _, err := ex.Exec(ctx, "CREATE TABLE tab1 (pk INTEGER PRIMARY KEY, col0 INTEGER, col1 REAL)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	for i := 1; i <= 10; i++ {
		if _, err := ex.Exec(ctx, "INSERT INTO tab1 VALUES ("+strconv.Itoa(i)+", "+strconv.Itoa(i*10)+", "+strconv.FormatFloat(float64(i)+0.5, 'f', 2, 64)+")"); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	rows, err := ex.QueryAll(ctx, "SELECT pk FROM tab1 WHERE col1 > 5.5 OR (col0 IN (10,20,30)) AND col0 > 8")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) == 0 {
		t.Errorf("expected >0 rows, got 0")
	}
	_ = eng
}

// REQ000725: Multi-table implicit cross join with 4+ DT.Tables.
func TestREQ000725_ImplicitCrossJoin(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t29", []string{"a29", "x29"})
	ex.RegisterTable("t31", []string{"a31", "x31"})
	ex.RegisterTable("t51", []string{"a51", "x51"})
	ex.RegisterTable("t55", []string{"a55", "x55"})

	ctx := context.Background()
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
	got3, err := ex.QueryAll(ctx, "SELECT x29, x31, x51 FROM t51, t29, t31 WHERE a29=a51 AND a31=a51")
	if err != nil {
		t.Logf("3-table query err: %v", err)
	} else {
		t.Logf("3-table got %d rows", len(got3))
	}
	got4, err := ex.QueryAll(ctx, "SELECT x29, x31, x51, x55 FROM t51, t29, t31, t55 WHERE a51=1 AND a29=1 AND a29=a51 AND a55=a31")
	if err != nil {
		t.Logf("4-table err: %v", err)
	} else {
		t.Logf("4-table got %d rows", len(got4))
	}
	got4b, err := ex.QueryAll(ctx, "SELECT x29, x31, x51, x55 FROM t51, t29, t31, t55 WHERE a29=a51 AND a55=a31")
	if err != nil {
		t.Logf("4b err: %v", err)
	} else {
		t.Logf("4b got %d rows", len(got4b))
	}
	got4x, err := ex.QueryAll(ctx, "SELECT x29, x31, x51, x55 FROM t51, t29, t31, t55")
	if err != nil {
		t.Logf("4x err: %v", err)
	} else {
		t.Logf("4x got %d rows", len(got4x))
	}
	got4c, err := ex.QueryAll(ctx, "SELECT x29, x31, x51, x55 FROM t51 JOIN t29 ON a29=a51 JOIN t31 ON 1=1 JOIN t55 ON a55=a31")
	if err != nil {
		t.Logf("4c err: %v", err)
	} else {
		t.Logf("4c got %d rows", len(got4c))
	}
	got3b, err := ex.QueryAll(ctx, "SELECT x29 FROM t29, t31, t51 WHERE a29=a51")
	if err != nil {
		t.Logf("3b err: %v", err)
	} else {
		t.Logf("3b got %d rows", len(got3b))
	}
}

// REQ000736: NULLS FIRST/LAST ordering.
func TestREQ000736_ParserCheck(t *testing.T) {
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
	planner := NewPlanner()
	planner.RegisterTable("t", []DT.ColInfo{{Name: "v", Typ: 1}}, "")
	plann, err := planner.Plan(stmt)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if plann == nil || plann.Root == nil {
		t.Fatalf("nil plan")
	}
	found := false
	var walk func(DT.Operator)
	walk = func(op DT.Operator) {
		if s, ok := op.(*OP.Sort); ok {
			found = true
			t.Logf("OP.Sort has %d keys, NullsOrder[0]=%d", len(s.Keys()), s.Keys()[0].NullsOrder)
		}
		if c, ok := op.(interface{ Child() DT.Operator }); ok {
			c2 := c.Child()
			if c2 != nil {
				walk(c2)
			}
		}
	}
	walk(plann.Root)
	if !found {
		t.Fatal("OP.Sort operator not found in plan")
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

	rows, err := ex.QueryAll(ctx, "EXPLAIN SELECT v FROM t ORDER BY v ASC NULLS LAST")
	if err != nil {
		t.Fatalf("EXPLAIN: %v", err)
	}
	for _, r := range rows {
		t.Logf("EXPLAIN: %v", r.Data)
	}

	rows, err = ex.QueryAll(ctx, "SELECT v FROM t ORDER BY v ASC")
	if err != nil {
		t.Fatalf("ORDER BY ASC: %v", err)
	}
	t.Logf("ASC order: %v", rows)

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