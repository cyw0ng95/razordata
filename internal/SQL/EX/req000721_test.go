package EX

import (
	"context"
	"strconv"
	"testing"
)

// REQ000721: NULL IN (subquery) should return 0 (false) per SQL
// three-valued logic, not NULL, when the subquery contains no NULLs.
// If the subquery contains a NULL, the result is NULL (unknown).
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
	// SQL semantics: NULL IN (1,2,3) = 0 (false), not NULL.
	// SQLite returns 0 (int64) for the IN predicate.
	v := rows[0].Data[0]
	if v != int64(0) && v != false {
		t.Errorf("expected 0 or false, got %v (%T)", v, v)
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
	if rows[0].Data[0] != nil {
		t.Errorf("expected NULL (unknown), got %v (%T)", rows[0].Data[0], rows[0].Data[0])
	}
}
