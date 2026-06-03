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
