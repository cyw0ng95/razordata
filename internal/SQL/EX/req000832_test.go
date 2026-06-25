package EX

import (
	"context"
	"fmt"
	"testing"
)

func TestCaseNotNullGeNull(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"x"})

	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1)")

	rs, err := ex.QueryAll(ctx, `SELECT NOT(NOT -79 >= NULL) FROM t`)
	if err != nil {
		t.Fatalf("NOT(NOT-79>=NULL): %v", err)
	}
	if len(rs) > 0 {
		got := rs[0].Data[0].ToAny()
		if got != nil {
			t.Errorf("got %v, want nil", got)
		}
	}
}

func TestCaseWhenNullCondition(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"x"})

	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1)")

	rs, err := ex.QueryAll(ctx, `SELECT CASE WHEN NULL THEN 'a' ELSE 'b' END FROM t`)
	if err != nil {
		t.Fatalf("CASE WHEN NULL: %v", err)
	}
	if len(rs) > 0 {
		got := fmt.Sprint(rs[0].Data[0].ToAny())
		if got != "b" {
			t.Errorf("got %s, want b", got)
		}
	}
}

func TestCoalesceWithNullCase(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"x"})

	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1)")

	rs, err := ex.QueryAll(ctx, `SELECT COALESCE(
		(CASE WHEN NULL THEN 1 END),
		99
	) FROM t`)
	if err != nil {
		t.Fatalf("COALESCE+NULL CASE: %v", err)
	}
	if len(rs) > 0 {
		got := fmt.Sprint(rs[0].Data[0].ToAny())
		if got != "99" {
			t.Errorf("got %s, want 99", got)
		}
	}
}

func TestCoalesceWithNonNullCase(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"x"})

	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1)")

	rs, err := ex.QueryAll(ctx, `SELECT COALESCE(
		(CASE WHEN 1=1 THEN 42 END),
		99
	) FROM t`)
	if err != nil {
		t.Fatalf("COALESCE+non-null CASE: %v", err)
	}
	if len(rs) > 0 {
		got := fmt.Sprint(rs[0].Data[0].ToAny())
		if got != "42" {
			t.Errorf("got %s, want 42", got)
		}
	}
}
