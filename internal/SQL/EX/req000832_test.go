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

// TestCoalesceWithAggregate verifies REQ000832: aggregate inside
// COALESCE must be evaluated, not treated as NULL.
// SELECT COALESCE(NULL, MIN(ALL -30) * 45 * 77) FROM t should
// return -103950 (= -30 * 45 * 77), not NULL.
func TestCoalesceWithAggregate(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"x"})

	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (-30)")

	rs, err := ex.QueryAll(ctx, `SELECT COALESCE(NULL, MIN(x) * 10) FROM t`)
	if err != nil {
		t.Fatalf("COALESCE+MIN: %v", err)
	}
	if len(rs) != 1 {
		t.Fatalf("got %d rows, want 1", len(rs))
	}
	got := rs[0].Data[0].ToAny()
	// MIN(-30) = -30, * 10 = -300
	if got != int64(-300) {
		t.Errorf("COALESCE(NULL, MIN(x)*10) = %v, want -300", got)
	}
}

// TestCoalesceWithAggregateSum verifies REQ000832: COALESCE with
// SUM inside.
func TestCoalesceWithAggregateSum(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"x"})

	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (10)")
	ex.Exec(ctx, "INSERT INTO t VALUES (20)")
	ex.Exec(ctx, "INSERT INTO t VALUES (30)")

	rs, err := ex.QueryAll(ctx, `SELECT COALESCE(NULL, SUM(x)) FROM t`)
	if err != nil {
		t.Fatalf("COALESCE+SUM: %v", err)
	}
	if len(rs) != 1 {
		t.Fatalf("got %d rows, want 1", len(rs))
	}
	got := rs[0].Data[0].ToAny()
	if got != int64(60) {
		t.Errorf("COALESCE(NULL, SUM(x)) = %v, want 60", got)
	}
}

// TestCoalesceWithAggregateFirstNonNull verifies that COALESCE
// returns the first non-null argument even when it contains an aggregate.
func TestCoalesceWithAggregateFirstNonNull(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"x"})

	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (5)")

	rs, err := ex.QueryAll(ctx, `SELECT COALESCE(MAX(x), 99) FROM t`)
	if err != nil {
		t.Fatalf("COALESCE+MAX: %v", err)
	}
	if len(rs) != 1 {
		t.Fatalf("got %d rows, want 1", len(rs))
	}
	got := rs[0].Data[0].ToAny()
	// MAX(5) = 5, which is non-null, so COALESCE returns 5 (not 99)
	if got != int64(5) {
		t.Errorf("COALESCE(MAX(x), 99) = %v, want 5", got)
	}
}
