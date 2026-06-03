package EX

import (
	"context"
	"reflect"
	"testing"
)

func TestExecutorCRUD(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("users", []string{"id", "name", "age"})

	ctx := context.Background()
	if _, err := ex.Exec(ctx, "INSERT INTO users VALUES (1, 'alice', 30)"); err != nil {
		t.Fatalf("insert 1: %v", err)
	}
	if _, err := ex.Exec(ctx, "INSERT INTO users VALUES (2, 'bob', 25)"); err != nil {
		t.Fatalf("insert 2: %v", err)
	}
	if _, err := ex.Exec(ctx, "INSERT INTO users VALUES (3, 'carol', 40)"); err != nil {
		t.Fatalf("insert 3: %v", err)
	}

	rows, err := ex.QueryAll(ctx, "SELECT * FROM users")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}

	res, err := ex.Exec(ctx, "UPDATE users SET age = 31 WHERE name = 'alice'")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if res.RowsAffected != 1 {
		t.Errorf("expected 1 row affected, got %d", res.RowsAffected)
	}

	res, err = ex.Exec(ctx, "DELETE FROM users WHERE age > 35")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if res.RowsAffected != 1 {
		t.Errorf("expected 1 row deleted, got %d", res.RowsAffected)
	}

	rows, err = ex.QueryAll(ctx, "SELECT * FROM users")
	if err != nil {
		t.Fatalf("query after: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("expected 2 rows remaining, got %d", len(rows))
	}
}

func TestExecutorQueryCols(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"a", "b"})
	ctx := context.Background()
	if _, err := ex.Exec(ctx, "INSERT INTO t VALUES (1, 'x')"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	rs, err := ex.Query(ctx, "SELECT a, b FROM t")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if !reflect.DeepEqual(rs.Cols, []string{"a", "b"}) {
		t.Errorf("expected cols [a b], got %v", rs.Cols)
	}
}

func TestExecutorInSubquery(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("users", []string{"id", "name", "age"})
	ex.RegisterTable("orders", []string{"user_id"})
	ctx := context.Background()
	for _, v := range []string{
		"INSERT INTO users VALUES (1, 'alice', 30)",
		"INSERT INTO users VALUES (2, 'bob', 25)",
		"INSERT INTO users VALUES (3, 'carol', 40)",
		"INSERT INTO orders VALUES (1)",
		"INSERT INTO orders VALUES (3)",
	} {
		if _, err := ex.Exec(ctx, v); err != nil {
			t.Fatalf("%s: %v", v, err)
		}
	}
	rows, err := ex.QueryAll(ctx, "SELECT name FROM users WHERE id IN (SELECT user_id FROM orders)")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	got := map[string]bool{}
	for _, r := range rows {
		got[r.Data[0].(string)] = true
	}
	if !got["alice"] || !got["carol"] || got["bob"] {
		t.Errorf("unexpected results: %v", got)
	}
}

func TestExecutorDistinct(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"category", "value"})
	ctx := context.Background()
	for _, v := range []string{
		"INSERT INTO t VALUES ('a', 1)",
		"INSERT INTO t VALUES ('a', 2)",
		"INSERT INTO t VALUES ('a', 1)",
		"INSERT INTO t VALUES ('b', 10)",
		"INSERT INTO t VALUES ('b', 10)",
		"INSERT INTO t VALUES ('c', 100)",
	} {
		if _, err := ex.Exec(ctx, v); err != nil {
			t.Fatalf("%s: %v", v, err)
		}
	}
	rows, err := ex.QueryAll(ctx, "SELECT DISTINCT category FROM t")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 distinct, got %d", len(rows))
	}
	rows, err = ex.QueryAll(ctx, "SELECT DISTINCT category, value FROM t")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 4 {
		t.Fatalf("expected 4 distinct (a,1), (a,2), (b,10), (c,100), got %d", len(rows))
	}
}

func TestExecutorDistinctWithWhere(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"x"})
	ctx := context.Background()
	for _, v := range []string{
		"INSERT INTO t VALUES (1)",
		"INSERT INTO t VALUES (2)",
		"INSERT INTO t VALUES (2)",
		"INSERT INTO t VALUES (3)",
		"INSERT INTO t VALUES (3)",
		"INSERT INTO t VALUES (3)",
	} {
		ex.Exec(ctx, v)
	}
	rows, _ := ex.QueryAll(ctx, "SELECT DISTINCT x FROM t WHERE x > 1")
	if len(rows) != 2 {
		t.Errorf("expected 2 distinct (2, 3), got %d", len(rows))
	}
}

func TestExecutorExists(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"a"})
	ex.RegisterTable("has_orders", []string{"id"})
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2)")
	ex.Exec(ctx, "INSERT INTO has_orders VALUES (10)")
	rows, err := ex.QueryAll(ctx, "SELECT a FROM t WHERE EXISTS (SELECT 1 FROM has_orders)")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("expected 2 (EXISTS matches every row), got %d", len(rows))
	}
}

func TestExecutorExistsFalse(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("users", []string{"id", "name"})
	ex.RegisterTable("orders", []string{"user_id"})
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO users VALUES (1, 'alice')")
	ex.Exec(ctx, "INSERT INTO users VALUES (2, 'bob')")
	rows, _ := ex.QueryAll(ctx, "SELECT name FROM users WHERE EXISTS (SELECT 1 FROM orders)")
	if len(rows) != 0 {
		t.Errorf("expected 0 rows (no orders), got %d", len(rows))
	}
}

func TestExecutorScalarSubquery(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"x"})
	ex.RegisterTable("counters", []string{"n"})
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2)")
	ex.Exec(ctx, "INSERT INTO t VALUES (3)")
	ex.Exec(ctx, "INSERT INTO counters VALUES (10)")
	rows, err := ex.QueryAll(ctx, "SELECT x, (SELECT n FROM counters) AS c FROM t")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	for _, r := range rows {
		if r.Data[1] != int64(10) {
			t.Errorf("expected c=10, got %v", r.Data[1])
		}
	}
}

func TestExecutorScalarSubqueryEmpty(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"x"})
	ex.RegisterTable("counters", []string{"n"})
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1)")
	rows, _ := ex.QueryAll(ctx, "SELECT x, (SELECT n FROM counters) AS c FROM t")
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Data[1] != nil {
		t.Errorf("expected nil c, got %v", rows[0].Data[1])
	}
}
