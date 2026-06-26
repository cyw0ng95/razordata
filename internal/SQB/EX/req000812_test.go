package EX

import (
	"context"
	"fmt"
	"testing"
)

func TestChanges_Insert(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"id", "val"})

	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 10)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2, 20)")

	rs, err := ex.QueryAll(ctx, "SELECT CHANGES() FROM t LIMIT 1")
	if err != nil {
		t.Fatalf("CHANGES() after INSERT: %v", err)
	}
	if len(rs) == 0 {
		t.Fatal("expected result for CHANGES()")
	}
	got := fmt.Sprint(rs[0].Data[0].ToAny())
	if got != "1" {
		t.Errorf("CHANGES() after last INSERT = %s, want 1 (last INSERT affected 1 row)", got)
	}
}

func TestChanges_Update(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"id", "val"})

	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 10)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2, 20)")
	ex.Exec(ctx, "UPDATE t SET val = 99 WHERE id = 1")

	rs, err := ex.QueryAll(ctx, "SELECT CHANGES() FROM t LIMIT 1")
	if err != nil {
		t.Fatalf("CHANGES() after UPDATE: %v", err)
	}
	if len(rs) == 0 {
		t.Fatal("expected result for CHANGES()")
	}
	got := fmt.Sprint(rs[0].Data[0].ToAny())
	if got != "1" {
		t.Errorf("CHANGES() after UPDATE = %s, want 1", got)
	}
}

func TestChanges_Delete(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"id", "val"})

	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 10)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2, 20)")
	ex.Exec(ctx, "DELETE FROM t WHERE id = 1")

	rs, err := ex.QueryAll(ctx, "SELECT CHANGES() FROM t LIMIT 1")
	if err != nil {
		t.Fatalf("CHANGES() after DELETE: %v", err)
	}
	if len(rs) == 0 {
		t.Fatal("expected result for CHANGES()")
	}
	got := fmt.Sprint(rs[0].Data[0].ToAny())
	if got != "1" {
		t.Errorf("CHANGES() after DELETE = %s, want 1", got)
	}
}

func TestTotalChanges_Accumulate(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"id", "val"})

	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 10)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2, 20)")
	ex.Exec(ctx, "INSERT INTO t VALUES (3, 30)")
	ex.Exec(ctx, "UPDATE t SET val = 99 WHERE id = 2")
	ex.Exec(ctx, "DELETE FROM t WHERE id = 3")

	rs, err := ex.QueryAll(ctx, "SELECT TOTAL_CHANGES() FROM t LIMIT 1")
	if err != nil {
		t.Fatalf("TOTAL_CHANGES(): %v", err)
	}
	if len(rs) == 0 {
		t.Fatal("expected result for TOTAL_CHANGES()")
	}
	got := fmt.Sprint(rs[0].Data[0].ToAny())
	if got != "5" {
		t.Errorf("TOTAL_CHANGES() = %s, want 5 (3 inserts + 1 update + 1 delete)", got)
	}
}
