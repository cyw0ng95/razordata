package EX

import (
	"context"
	"fmt"
	"testing"
)

func TestCTE_Basic(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"v"})

	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (10)")
	ex.Exec(ctx, "INSERT INTO t VALUES (20)")
	ex.Exec(ctx, "INSERT INTO t VALUES (30)")

	rs, err := ex.QueryAll(ctx,
		"WITH cte AS (SELECT v FROM t WHERE v > 15) SELECT v FROM cte ORDER BY v")
	if err != nil {
		t.Fatalf("CTE basic: %v", err)
	}
	if len(rs) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rs))
	}
	for i, want := range []int64{20, 30} {
		got := fmt.Sprint(rs[i].Data[0].ToAny())
		if got != fmt.Sprint(want) {
			t.Errorf("row %d = %v, want %d", i, got, want)
		}
	}
}

func TestCTE_WithAggregate(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"g", "v"})

	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES ('a', 10)")
	ex.Exec(ctx, "INSERT INTO t VALUES ('a', 20)")
	ex.Exec(ctx, "INSERT INTO t VALUES ('b', 30)")

	rs, err := ex.QueryAll(ctx,
		"WITH sums AS (SELECT g, SUM(v) AS total FROM t GROUP BY g) SELECT g, total FROM sums ORDER BY g")
	if err != nil {
		t.Fatalf("CTE aggregate: %v", err)
	}
	if len(rs) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rs))
	}
}

func TestCTE_Multiple(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"v"})

	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (10)")
	ex.Exec(ctx, "INSERT INTO t VALUES (20)")
	ex.Exec(ctx, "INSERT INTO t VALUES (30)")

	rs, err := ex.QueryAll(ctx,
		"WITH a AS (SELECT v FROM t WHERE v < 20), b AS (SELECT v FROM t WHERE v > 15) SELECT v FROM a UNION ALL SELECT v FROM b ORDER BY v")
	if err != nil {
		t.Fatalf("CTE multiple: %v", err)
	}
	if len(rs) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rs))
	}
}

func TestCTE_WithValues(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()

	ctx := context.Background()
	rs, err := ex.QueryAll(ctx, `
		WITH cte AS (SELECT 10 AS x UNION ALL SELECT 20 UNION ALL SELECT 30)
		SELECT * FROM cte ORDER BY x
	`)
	if err != nil {
		t.Fatalf("CTE with values: %v", err)
	}
	if len(rs) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rs))
	}
	for i, want := range []int64{10, 20, 30} {
		got := fmt.Sprint(rs[i].Data[0].ToAny())
		if got != fmt.Sprint(want) {
			t.Errorf("row %d = %v, want %d", i, got, want)
		}
	}
}
