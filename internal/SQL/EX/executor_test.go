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
