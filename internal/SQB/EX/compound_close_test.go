package EX

import (
	"context"
	"fmt"
	"testing"
)

// TestCompoundOp_CloseResetsState verifies that OP.CompoundOp.Close() resets
// all internal state so that the same operator can be reused after close.
// This is critical for memo-cached plan trees where the same OP.CompoundOp
// instance is returned for repeated executions of the same query.
// REQ001058.
func TestCompoundOp_CloseResetsState(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t1", []string{"x"})
	ex.RegisterTable("t2", []string{"x"})

	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t1 VALUES (1)")
	ex.Exec(ctx, "INSERT INTO t1 VALUES (2)")
	ex.Exec(ctx, "INSERT INTO t2 VALUES (3)")
	ex.Exec(ctx, "INSERT INTO t2 VALUES (4)")

	// Execute the same UNION ALL query twice through the executor.
	// The memo should cache the plan, so the second execution reuses
	// the same OP.CompoundOp instance. Close() must reset state so the
	// second execution returns rows instead of 0.
	sql := "SELECT x FROM t1 UNION ALL SELECT x FROM t2 ORDER BY x"

	for iter := 0; iter < 3; iter++ {
		rs, err := ex.QueryAll(ctx, sql)
		if err != nil {
			t.Fatalf("iteration %d: %v", iter, err)
		}
		if len(rs) != 4 {
			t.Fatalf("iteration %d: expected 4 rows, got %d", iter, len(rs))
		}
		for i, want := range []int64{1, 2, 3, 4} {
			got := fmt.Sprint(rs[i].Data[0].ToAny())
			if got != fmt.Sprint(want) {
				t.Errorf("iteration %d row %d = %v, want %d", iter, i, got, want)
			}
		}
	}
}

// TestCompoundOp_CloseResetsState_Except verifies the fix for EXCEPT
// streaming path (rightDrained, emittedKeys, rightKeys).
func TestCompoundOp_CloseResetsState_Except(t *testing.T) {
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

	sql := "SELECT x FROM t1 EXCEPT SELECT x FROM t2 ORDER BY x"

	for iter := 0; iter < 3; iter++ {
		rs, err := ex.QueryAll(ctx, sql)
		if err != nil {
			t.Fatalf("iteration %d: %v", iter, err)
		}
		if len(rs) != 2 {
			t.Fatalf("iteration %d: expected 2 rows, got %d", iter, len(rs))
		}
		for i, want := range []int64{1, 3} {
			got := fmt.Sprint(rs[i].Data[0].ToAny())
			if got != fmt.Sprint(want) {
				_ = got
				t.Errorf("iteration %d row %d = %v, want %d", iter, i, got, want)
			}
		}
	}
}

// TestCompoundOp_CloseResetsState_Intersect verifies the fix for INTERSECT
// streaming path.
func TestCompoundOp_CloseResetsState_Intersect(t *testing.T) {
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

	sql := "SELECT x FROM t1 INTERSECT SELECT x FROM t2"

	for iter := 0; iter < 3; iter++ {
		rs, err := ex.QueryAll(ctx, sql)
		if err != nil {
			t.Fatalf("iteration %d: %v", iter, err)
		}
		if len(rs) != 1 {
			t.Fatalf("iteration %d: expected 1 row, got %d", iter, len(rs))
		}
		got := fmt.Sprint(rs[0].Data[0].ToAny())
		if got != "2" {
			t.Errorf("iteration %d: got %v, want 2", iter, got)
		}
	}
}

// TestCompoundOp_CloseResetsState_Union verifies the fix for UNION
// (materialization path with dedup).
func TestCompoundOp_CloseResetsState_Union(t *testing.T) {
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

	sql := "SELECT x FROM t1 UNION SELECT x FROM t2 ORDER BY x"

	for iter := 0; iter < 3; iter++ {
		rs, err := ex.QueryAll(ctx, sql)
		if err != nil {
			_ = err
			t.Fatalf("iteration %d: %v", iter, err)
		}
		if len(rs) != 3 {
			t.Fatalf("iteration %d: expected 3 rows, got %d", iter, len(rs))
		}
		for i, want := range []int64{1, 2, 3} {
			got := fmt.Sprint(rs[i].Data[0].ToAny())
			if got != fmt.Sprint(want) {
				t.Errorf("iteration %d row %d = %v, want %d", iter, i, got, want)
			}
		}
	}
}