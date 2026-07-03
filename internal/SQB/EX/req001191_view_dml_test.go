package EX

import (
	"context"
	"strings"
	"testing"
)

// REQ001191: DML on views must be rejected. Views are read-only
// in SQLite unless they have INSTEAD OF triggers.
func TestReq001191_ViewDMLRejected(t *testing.T) {
	ResetForTest(t)
	ctx := context.Background()
	ex := NewExecutor()

	mustExec(t, ex, ctx, "CREATE TABLE t1 (x INTEGER, y TEXT)")
	mustExec(t, ex, ctx, "INSERT INTO t1 VALUES (1, 'a'), (2, 'b')")
	mustExec(t, ex, ctx, "CREATE VIEW view1 AS SELECT x, y FROM t1")

	// DELETE on view should fail.
	_, err := ex.Exec(ctx, "DELETE FROM view1 WHERE x > 0")
	if err == nil {
		t.Fatal("DELETE on view should fail, got nil error")
	}
	if !strings.Contains(err.Error(), "cannot modify view") {
		t.Errorf("DELETE error should mention 'cannot modify view', got: %v", err)
	}

	// UPDATE on view should fail.
	_, err = ex.Exec(ctx, "UPDATE view1 SET x = 2")
	if err == nil {
		t.Fatal("UPDATE on view should fail, got nil error")
	}
	if !strings.Contains(err.Error(), "cannot modify view") {
		t.Errorf("UPDATE error should mention 'cannot modify view', got: %v", err)
	}

	// INSERT on view should fail.
	_, err = ex.Exec(ctx, "INSERT INTO view1 VALUES (3, 'c')")
	if err == nil {
		t.Fatal("INSERT on view should fail, got nil error")
	}
	if !strings.Contains(err.Error(), "cannot modify view") {
		t.Errorf("INSERT error should mention 'cannot modify view', got: %v", err)
	}

	// SELECT on view should still work.
	rows, err := ex.QueryAll(ctx, "SELECT * FROM view1")
	if err != nil {
		t.Fatalf("SELECT on view should succeed: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("expected 2 rows from view, got %d", len(rows))
	}
}
