package EX

import (
	"context"
	"fmt"
	"testing"
)

func TestCompoundUnionAll_Streaming(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t1", []string{"x"})
	ex.RegisterTable("t2", []string{"x"})

	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t1 VALUES (1)")
	ex.Exec(ctx, "INSERT INTO t1 VALUES (2)")
	ex.Exec(ctx, "INSERT INTO t2 VALUES (3)")

	rs, err := ex.QueryAll(ctx, "SELECT x FROM t1 UNION ALL SELECT x FROM t2 ORDER BY x")
	if err != nil {
		t.Fatalf("UNION ALL: %v", err)
	}
	if len(rs) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rs))
	}
	for i, want := range []int64{1, 2, 3} {
		got := fmt.Sprint(rs[i].Data[0].ToAny())
		if got != fmt.Sprint(want) {
			t.Errorf("row %d = %v, want %d", i, got, want)
		}
	}
}

func TestCompoundExcept_Streaming(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t1", []string{"x"})
	ex.RegisterTable("t2", []string{"x"})

	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t1 VALUES (1)")
	ex.Exec(ctx, "INSERT INTO t1 VALUES (2)")
	ex.Exec(ctx, "INSERT INTO t1 VALUES (3)")
	ex.Exec(ctx, "INSERT INTO t2 VALUES (2)")

	rs, err := ex.QueryAll(ctx, "SELECT x FROM t1 EXCEPT SELECT x FROM t2 ORDER BY x")
	if err != nil {
		t.Fatalf("EXCEPT: %v", err)
	}
	if len(rs) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rs))
	}
	for i, want := range []int64{1, 3} {
		got := fmt.Sprint(rs[i].Data[0].ToAny())
		if got != fmt.Sprint(want) {
			t.Errorf("row %d = %v, want %d", i, got, want)
		}
	}
}

func TestCompoundIntersect_Streaming(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t1", []string{"x"})
	ex.RegisterTable("t2", []string{"x"})

	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t1 VALUES (1)")
	ex.Exec(ctx, "INSERT INTO t1 VALUES (2)")
	ex.Exec(ctx, "INSERT INTO t2 VALUES (2)")
	ex.Exec(ctx, "INSERT INTO t2 VALUES (3)")

	rs, err := ex.QueryAll(ctx, "SELECT x FROM t1 INTERSECT SELECT x FROM t2")
	if err != nil {
		t.Fatalf("INTERSECT: %v", err)
	}
	if len(rs) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rs))
	}
	got := fmt.Sprint(rs[0].Data[0].ToAny())
	if got != "2" {
		t.Errorf("got %v, want 2", got)
	}
}

func TestCompoundExcept_DeepChain(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t1", []string{"x"})
	ex.RegisterTable("t2", []string{"x"})
	ex.RegisterTable("t3", []string{"x"})

	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t1 VALUES (1)")
	ex.Exec(ctx, "INSERT INTO t1 VALUES (2)")
	ex.Exec(ctx, "INSERT INTO t1 VALUES (3)")
	ex.Exec(ctx, "INSERT INTO t2 VALUES (2)")
	ex.Exec(ctx, "INSERT INTO t3 VALUES (3)")

	rs, err := ex.QueryAll(ctx, "SELECT x FROM t1 EXCEPT SELECT x FROM t2 EXCEPT SELECT x FROM t3")
	if err != nil {
		t.Fatalf("EXCEPT chain: %v", err)
	}
	if len(rs) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rs))
	}
	got := fmt.Sprint(rs[0].Data[0].ToAny())
	if got != "1" {
		t.Errorf("got %v, want 1", got)
	}
}

func TestCompoundUnion_StillDedup(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t1", []string{"x"})
	ex.RegisterTable("t2", []string{"x"})

	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t1 VALUES (1)")
	ex.Exec(ctx, "INSERT INTO t1 VALUES (2)")
	ex.Exec(ctx, "INSERT INTO t2 VALUES (2)")
	ex.Exec(ctx, "INSERT INTO t2 VALUES (3)")

	rs, err := ex.QueryAll(ctx, "SELECT x FROM t1 UNION SELECT x FROM t2 ORDER BY x")
	if err != nil {
		t.Fatalf("UNION: %v", err)
	}
	if len(rs) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rs))
	}
	for i, want := range []int64{1, 2, 3} {
		got := fmt.Sprint(rs[i].Data[0].ToAny())
		if got != fmt.Sprint(want) {
			t.Errorf("row %d = %v, want %d", i, got, want)
		}
	}
}
