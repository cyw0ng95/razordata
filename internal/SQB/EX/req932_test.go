package EX

import (
	"context"
	"testing"
)

func TestReq932_Division(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	ctx := context.Background()
	ex := NewExecutor()
	ex.RegisterTable("tab0", []string{"col0", "col2"})
	ex.Exec(ctx, "INSERT INTO tab0 VALUES (1, 3)")
	ex.Exec(ctx, "INSERT INTO tab0 VALUES (4, 2)")

	// Integer division: 1/3 = 0
	rs, _ := ex.QueryAll(ctx, "SELECT col0 / col2 FROM tab0 WHERE col0 = 1")
	t.Logf("1/3 = %v", rs[0].Data[0].ToAny())
	if rs[0].Data[0].ToAny() != int64(0) {
		t.Errorf("1/3 = %v, want int64(0)", rs[0].Data[0].ToAny())
	}

	// Float division: 1/3.0 = 0.333
	rs, _ = ex.QueryAll(ctx, "SELECT col0 / CAST(col2 AS REAL) FROM tab0 WHERE col0 = 1")
	t.Logf("1/3.0 = %v", rs[0].Data[0].ToAny())
	if rs[0].Data[0].ToAny() != float64(0.3333333333333333) {
		t.Errorf("1/3.0 = %v, want 0.333", rs[0].Data[0].ToAny())
	}

	// Integer division with GROUP BY
	ex2 := NewExecutor()
	ex2.RegisterTable("tab1", []string{"col0", "col2"})
	ex2.Exec(ctx, "INSERT INTO tab1 VALUES (1, 3)")
	ex2.Exec(ctx, "INSERT INTO tab1 VALUES (1, 3)")
	rs, _ = ex2.QueryAll(ctx, "SELECT col0 / col2 col0 FROM tab1 GROUP BY col2, col0")
	t.Logf("GROUP BY 1/3 = %v", rs[0].Data[0].ToAny())
	if rs[0].Data[0].ToAny() != int64(0) {
		t.Errorf("GROUP BY 1/3 = %v, want int64(0)", rs[0].Data[0].ToAny())
	}

	// LiteFS path: DIV operator
	rs, _ = ex.QueryAll(ctx, "SELECT col0 DIV col2 FROM tab0 WHERE col0 = 1")
	t.Logf("1 DIV 3 = %v", rs[0].Data[0].ToAny())

	// Mixed: int / float
	rs, _ = ex.QueryAll(ctx, "SELECT col0 / 3.0 FROM tab0 WHERE col0 = 1")
	t.Logf("1/3.0 (lit) = %v", rs[0].Data[0].ToAny())

	// Division by zero
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
