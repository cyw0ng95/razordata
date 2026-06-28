package EX

import (
	"context"
	"strconv"
	"testing"
)

// REQ001059: NULL IN (subquery) should return NULL (UNKNOWN) per SQL
// three-valued logic when the subquery is non-empty, regardless of
// whether it contains NULLs. Only empty subquery returns FALSE.
// This overrides the previous REQ000721 behavior which returned
// FALSE for NULL IN a subquery with no NULLs — that conflated
// "all comparisons are UNKNOWN" with "no NULLs in RHS".
func TestREQ000721_NullInSubqueryNoNulls(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t1", []string{"x"})

	ctx := context.Background()
	for _, v := range []int{1, 2, 3} {
		if _, err := ex.Exec(ctx, "INSERT INTO t1 VALUES ("+strconv.Itoa(v)+")"); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	rows, err := ex.QueryAll(ctx, "SELECT null IN (SELECT * FROM t1)")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	// SQL standard: NULL IN (1,2,3) = NULL (UNKNOWN).
	// Each comparison NULL=1/NULL=2/NULL=3 returns UNKNOWN.
	// Since no comparison returns TRUE, the ANY quantifier
	// yields UNKNOWN (NULL), not FALSE.
	if !rows[0].Data[0].IsNull() {
		t.Errorf("expected NULL (UNKNOWN), got %v (%T)", rows[0].Data[0], rows[0].Data[0])
	}
}

func TestREQ000721_NullInSubqueryWithNulls(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t1", []string{"x"})

	ctx := context.Background()
	for _, v := range []any{1, nil, 3} {
		var sql string
		if v == nil {
			sql = "INSERT INTO t1 VALUES (NULL)"
		} else {
			sql = "INSERT INTO t1 VALUES (" + strconv.Itoa(v.(int)) + ")"
		}
		if _, err := ex.Exec(ctx, sql); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	rows, err := ex.QueryAll(ctx, "SELECT null IN (SELECT * FROM t1)")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	// SQL semantics: NULL IN (1, NULL, 3) = NULL (unknown).
	if rows[0].Data[0].IsNull() == false {
		t.Errorf("expected NULL (unknown), got %v (%T)", rows[0].Data[0], rows[0].Data[0])
	}
}
