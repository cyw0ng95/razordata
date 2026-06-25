package EX

import (
	"context"
	"testing"
)

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
