package EX

import (
	"context"
	"testing"
)

// REQ000534 — VIEW WHERE clause not applied on SELECT
func TestReq534_ViewWhereMerge(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ctx := context.Background()

	ex.RegisterTable("t1", []string{"x"})
	ex.Exec(ctx, "INSERT INTO t1 VALUES (1)")
	ex.Exec(ctx, "INSERT INTO t1 VALUES (2)")
	ex.Exec(ctx, "INSERT INTO t1 VALUES (3)")

	// Create view: x > 1
	_, err := ex.Exec(ctx, "CREATE VIEW view1 AS SELECT x FROM t1 WHERE x > 1")
	if err != nil {
		t.Fatalf("CREATE VIEW: %v", err)
	}

	// Query view with outer WHERE
	rows, err := ex.QueryAll(ctx, "SELECT x FROM view1 WHERE x > 2")
	if err != nil {
		t.Fatalf("SELECT from view: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1 (WHERE combined: view->x>1 AND outer->x>2)", len(rows))
	}
	if rows[0].Data[0] != int64(3) {
		t.Errorf("got x=%v, want 3", rows[0].Data[0])
	}

	// View without outer WHERE
	rows, err = ex.QueryAll(ctx, "SELECT x FROM view1")
	if err != nil {
		t.Fatalf("SELECT from view (no outer WHERE): %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2 (view WHERE x>1)", len(rows))
	}

	// View with outer WHERE that eliminates all rows
	rows, err = ex.QueryAll(ctx, "SELECT x FROM view1 WHERE x > 10")
	if err != nil {
		t.Fatalf("SELECT from view (no match): %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("got %d rows, want 0", len(rows))
	}
}

// REQ000477 — INSERT RETURNING
func TestReq477_InsertReturning(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ctx := context.Background()

	ex.RegisterTable("t1", []string{"a", "b"})

	// INSERT RETURNING *
	res, err := ex.Exec(ctx, "INSERT INTO t1 VALUES (1, 10) RETURNING *")
	if err != nil {
		t.Fatalf("INSERT RETURNING *: %v", err)
	}
	if res.RowsAffected != 1 {
		t.Errorf("RowsAffected: got %d, want 1", res.RowsAffected)
	}
	// Verify the row was inserted
	rows, err := ex.QueryAll(ctx, "SELECT a, b FROM t1 WHERE a = 1")
	if err != nil {
		t.Fatalf("SELECT after INSERT RETURNING: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}

	// INSERT RETURNING specific column
	res, err = ex.Exec(ctx, "INSERT INTO t1 VALUES (2, 20) RETURNING a")
	if err != nil {
		t.Fatalf("INSERT RETURNING col: %v", err)
	}
	if res.RowsAffected != 1 {
		t.Errorf("RowsAffected: got %d, want 1", res.RowsAffected)
	}

	// INSERT RETURNING with multiple rows
	res, err = ex.Exec(ctx, "INSERT INTO t1 VALUES (3, 30), (4, 40) RETURNING a")
	if err != nil {
		t.Fatalf("INSERT multi RETURNING: %v", err)
	}
	if res.RowsAffected != 2 {
		t.Errorf("RowsAffected: got %d, want 2", res.RowsAffected)
	}
}

// REQ000504 — DELETE wrong row count
func TestReq504_DeleteRowCount(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ctx := context.Background()

	ex.RegisterTable("t1", []string{"a", "b"})
	ex.Exec(ctx, "INSERT INTO t1 VALUES (1, 10)")
	ex.Exec(ctx, "INSERT INTO t1 VALUES (2, 20)")
	ex.Exec(ctx, "INSERT INTO t1 VALUES (3, 30)")

	// DELETE with WHERE that matches some rows
	res, err := ex.Exec(ctx, "DELETE FROM t1 WHERE a > 1")
	if err != nil {
		t.Fatalf("DELETE WHERE: %v", err)
	}
	if res.RowsAffected != 2 {
		t.Errorf("RowsAffected: got %d, want 2", res.RowsAffected)
	}

	// Verify remaining row
	rows, err := ex.QueryAll(ctx, "SELECT a FROM t1 ORDER BY a")
	if err != nil {
		t.Fatalf("SELECT after DELETE: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].Data[0] != int64(1) {
		t.Errorf("got a=%v, want 1", rows[0].Data[0])
	}

	// DELETE without WHERE (all remaining rows)
	res, err = ex.Exec(ctx, "DELETE FROM t1")
	if err != nil {
		t.Fatalf("DELETE all: %v", err)
	}
	if res.RowsAffected != 1 {
		t.Errorf("RowsAffected: got %d, want 1", res.RowsAffected)
	}

	// DELETE with WHERE that matches no rows
	ex.Exec(ctx, "INSERT INTO t1 VALUES (4, 40)")
	res, err = ex.Exec(ctx, "DELETE FROM t1 WHERE a > 100")
	if err != nil {
		t.Fatalf("DELETE no match: %v", err)
	}
	if res.RowsAffected != 0 {
		t.Errorf("RowsAffected: got %d, want 0", res.RowsAffected)
	}
}

// REQ000475 — DELETE with ORDER BY / LIMIT
func TestReq475_DeleteOrderByLimit(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ctx := context.Background()

	ex.RegisterTable("t1", []string{"a", "b"})
	ex.Exec(ctx, "INSERT INTO t1 VALUES (1, 10)")
	ex.Exec(ctx, "INSERT INTO t1 VALUES (2, 20)")
	ex.Exec(ctx, "INSERT INTO t1 VALUES (3, 30)")

	// DELETE with LIMIT
	res, err := ex.Exec(ctx, "DELETE FROM t1 LIMIT 2")
	if err != nil {
		t.Fatalf("DELETE LIMIT: %v", err)
	}
	if res.RowsAffected != 2 {
		t.Errorf("RowsAffected: got %d, want 2", res.RowsAffected)
	}

	// Verify remaining row
	rows, err := ex.QueryAll(ctx, "SELECT a FROM t1 ORDER BY a")
	if err != nil {
		t.Fatalf("SELECT after DELETE LIMIT: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1 (DELETE LIMIT 2 left 1)", len(rows))
	}

	// Clean up and test DELETE ORDER BY LIMIT
	ex.Exec(ctx, "DELETE FROM t1")
	ex.Exec(ctx, "INSERT INTO t1 VALUES (1, 10)")
	ex.Exec(ctx, "INSERT INTO t1 VALUES (2, 20)")
	ex.Exec(ctx, "INSERT INTO t1 VALUES (3, 30)")

	res, err = ex.Exec(ctx, "DELETE FROM t1 ORDER BY a DESC LIMIT 1")
	if err != nil {
		t.Fatalf("DELETE ORDER BY LIMIT: %v", err)
	}
	if res.RowsAffected != 1 {
		t.Errorf("RowsAffected: got %d, want 1", res.RowsAffected)
	}

	// Should have deleted a=3 (highest)
	rows, err = ex.QueryAll(ctx, "SELECT a FROM t1 ORDER BY a")
	if err != nil {
		t.Fatalf("SELECT after DELETE ORDER BY: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if rows[1].Data[0] != int64(2) {
		t.Errorf("got highest a=%v, want 2 (DESC LIMIT 1 deleted a=3)", rows[1].Data[0])
	}
}