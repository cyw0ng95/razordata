package EX

import (
	"context"
	"testing"
)

// REQ853: CHANGES() / TOTAL_CHANGES().
func TestReq853_CHANGES(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ctx := context.Background()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"id", "val"})
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 'a')")
	rs, _ := ex.QueryAll(ctx, "SELECT CHANGES()")
	if rs[0].Data[0].ToAny() != int64(1) {
		t.Errorf("after INSERT CHANGES()=%v want 1", rs[0].Data[0].ToAny())
	}
	rs, _ = ex.QueryAll(ctx, "SELECT TOTAL_CHANGES()")
	if rs[0].Data[0].ToAny() != int64(1) {
		t.Errorf("TOTAL_CHANGES=%v want 1", rs[0].Data[0].ToAny())
	}
	ex.Exec(ctx, "UPDATE t SET val = 'b' WHERE id = 1")
	ex.Exec(ctx, "DELETE FROM t WHERE id = 1")
	rs, _ = ex.QueryAll(ctx, "SELECT TOTAL_CHANGES()")
	if rs[0].Data[0].ToAny() != int64(3) {
		t.Errorf("TOTAL_CHANGES after 3 DML=%v want 3", rs[0].Data[0].ToAny())
	}
}

// REQ932: Integer and float division.
func TestReq932_Division(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ctx := context.Background()
	ex := NewExecutor()
	ex.RegisterTable("tab0", []string{"col0", "col2"})
	ex.Exec(ctx, "INSERT INTO tab0 VALUES (1, 3)")
	ex.Exec(ctx, "INSERT INTO tab0 VALUES (4, 2)")
	rs, _ := ex.QueryAll(ctx, "SELECT col0 / col2 FROM tab0 WHERE col0 = 1")
	t.Logf("1/3 = %v", rs[0].Data[0].ToAny())
	if rs[0].Data[0].ToAny() != int64(0) {
		t.Errorf("1/3 = %v, want int64(0)", rs[0].Data[0].ToAny())
	}
	rs, _ = ex.QueryAll(ctx, "SELECT col0 / CAST(col2 AS REAL) FROM tab0 WHERE col0 = 1")
	t.Logf("1/3.0 = %v", rs[0].Data[0].ToAny())
	if rs[0].Data[0].ToAny() != float64(0.3333333333333333) {
		t.Errorf("1/3.0 = %v, want 0.333", rs[0].Data[0].ToAny())
	}
	ex2 := NewExecutor()
	ex2.RegisterTable("tab1", []string{"col0", "col2"})
	ex2.Exec(ctx, "INSERT INTO tab1 VALUES (1, 3)")
	ex2.Exec(ctx, "INSERT INTO tab1 VALUES (1, 3)")
	rs, _ = ex2.QueryAll(ctx, "SELECT col0 / col2 col0 FROM tab1 GROUP BY col2, col0")
	t.Logf("GROUP BY 1/3 = %v", rs[0].Data[0].ToAny())
	if rs[0].Data[0].ToAny() != int64(0) {
		t.Errorf("GROUP BY 1/3 = %v, want int64(0)", rs[0].Data[0].ToAny())
	}
	ex3 := NewExecutor()
	ex3.RegisterTable("t", []string{"x"})
	ex3.Exec(ctx, "INSERT INTO t VALUES (5)")
	_, err := ex3.QueryAll(ctx, "SELECT x / 0 FROM t")
	if err != nil {
		t.Logf("5/0 error: %v", err)
	} else {
		t.Logf("5/0: no error (returns NULL)")
	}
}

// REQ934: Negated full query patterns.
func TestReq934_NegatedFullQuery(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ctx := context.Background()
	ex := NewExecutor()
	ex.RegisterTable("tab0", []string{"col0", "col1", "col2"})
	ex.Exec(ctx, "INSERT INTO tab0 VALUES (1, 10, 100)")
	ex.Exec(ctx, "INSERT INTO tab0 VALUES (2, 20, 200)")
	ex.Exec(ctx, "INSERT INTO tab0 VALUES (3, 30, 300)")
	tests := []string{
		"SELECT col0 FROM tab0 WHERE -col2 IN (-100)",
		"SELECT col0 FROM tab0 WHERE -col2 IN (-col0 * 100)",
		"SELECT col0 FROM tab0 WHERE -col0 NOT IN (-1, -2)",
		"SELECT col0 FROM tab0 WHERE NOT -col2 NOT IN (-col0)",
		"SELECT col0 FROM tab0 WHERE NOT -+col2 NOT IN (-col0)",
	}
	for _, sql := range tests {
		_, err := ex.QueryAll(ctx, sql)
		if err != nil {
			t.Errorf("FAIL %s: %v", sql, err)
		} else {
			t.Logf("OK   %s", sql)
		}
	}
}

// REQ936: COUNT(DISTINCT) with unary operators.
func TestReq936_CountDistinctUnary(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ctx := context.Background()
	ex := NewExecutor()
	ex.RegisterTable("tab1", []string{"col0", "col1", "col2"})
	ex.Exec(ctx, "INSERT INTO tab1 VALUES (1, 10, 5)")
	ex.Exec(ctx, "INSERT INTO tab1 VALUES (2, 20, 15)")
	ex.Exec(ctx, "INSERT INTO tab1 VALUES (3, 30, 25)")
	rs, _ := ex.QueryAll(ctx, "SELECT +55 * COUNT(DISTINCT +-col2) AS col2 FROM tab1")
	got := rs[0].Data[0].ToAny()
	if got != int64(165) {
		t.Errorf("got %v, want 165", got)
	}
}

func TestReq937_ChainedUnaryDivision(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ctx := context.Background()
	ex := NewExecutor()
	ex.RegisterTable("tab2", []string{"col0", "col1"})
	ex.Exec(ctx, "INSERT INTO tab2 VALUES (4, 20)")
	rs, _ := ex.QueryAll(ctx, "SELECT col1 / (++col0) + +col1 AS col1 FROM tab2")
	got := rs[0].Data[0].ToAny()
	if got != int64(25) {
		t.Errorf("got %v, want 25", got)
	}
	rs, _ = ex.QueryAll(ctx, "SELECT (++col0) FROM tab2")
	if rs[0].Data[0].ToAny() != int64(4) {
		t.Errorf("(++col0) = %v, want 4", rs[0].Data[0].ToAny())
	}
}

// REQ944: Chained DISTINCT aggregates with unary chain.
func TestChain_DistinctAggregates_UnaryChain(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ctx := context.Background()
	ex := NewExecutor()
	ex.RegisterTable("tab2", []string{"col0", "col1", "col2"})
	ex.Exec(ctx, "INSERT INTO tab2 VALUES (64, 77, 40)")
	ex.Exec(ctx, "INSERT INTO tab2 VALUES (75, 67, 58)")
	ex.Exec(ctx, "INSERT INTO tab2 VALUES (46, 51, 23)")
	rs, err := ex.QueryAll(ctx, "SELECT - COUNT(DISTINCT + col0) - + + SUM(DISTINCT - ( - 11 ) ) + 32 FROM tab2 AS cor0")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rs) != 1 {
		t.Fatalf("got %d rows, want 1", len(rs))
	}
	got, ok := rs[0].Data[0].ToAny().(int64)
	if !ok {
		t.Fatalf("result type %T, want int64", rs[0].Data[0].ToAny())
	}
	if got != 18 {
		t.Errorf("got %d, want 18", got)
	}
	rs, err = ex.QueryAll(ctx, "SELECT - COUNT ( DISTINCT + col0 ) - + + SUM ( DISTINCT - ( - 11 ) ) + 32 FROM tab2 AS cor0")
	if err != nil {
		t.Fatalf("query (loose): %v", err)
	}
	got, ok = rs[0].Data[0].ToAny().(int64)
	if !ok {
		t.Fatalf("result type %T, want int64", rs[0].Data[0].ToAny())
	}
	if got != 18 {
		t.Errorf("loose: got %d, want 18", got)
	}
}