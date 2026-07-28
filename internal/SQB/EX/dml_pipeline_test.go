//go:build !slt_corpus_full

package EX

import (
	"context"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
)

func TestDMLPipeline_InsertUpdateDelete(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	DT.RegisterTableSchema("t1", []string{"id", "name"})
	DT.RegisterTableSchema("t2", []string{"id", "val"})

	e := NewExecutor()
	e.EnablePipelinePath()
	defer e.DisablePipelinePath()

	ctx := context.Background()

	// INSERT via pipeline
	res, err := e.Exec(ctx, "INSERT INTO t1 VALUES (1, 'alice'), (2, 'bob')")
	if err != nil {
		t.Fatalf("INSERT: %v", err)
	}
	if res.RowsAffected != 2 {
		t.Errorf("INSERT: expected 2, got %d", res.RowsAffected)
	}

	// UPDATE via pipeline
	res, err = e.Exec(ctx, "UPDATE t1 SET name = 'charlie' WHERE id = 1")
	if err != nil {
		t.Fatalf("UPDATE: %v", err)
	}
	if res.RowsAffected != 1 {
		t.Errorf("UPDATE: expected 1, got %d", res.RowsAffected)
	}

	// DELETE via pipeline
	res, err = e.Exec(ctx, "DELETE FROM t1 WHERE id = 2")
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	if res.RowsAffected != 1 {
		t.Errorf("DELETE: expected 1, got %d", res.RowsAffected)
	}

	// Verify final state
	rows, err := e.QueryAll(ctx, "SELECT id, name FROM t1 ORDER BY id")
	if err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Data[0].I64 != 1 || rows[0].Data[1].S != "charlie" {
		t.Errorf("unexpected row: %v", rows[0])
	}
}

func TestDMLPipeline_InsertReturning(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	DT.RegisterTableSchema("t1", []string{"id", "name"})

	e := NewExecutor()
	e.EnablePipelinePath()
	defer e.DisablePipelinePath()

	ctx := context.Background()

	res, err := e.Exec(ctx, "INSERT INTO t1 VALUES (1, 'alice') RETURNING id")
	if err != nil {
		t.Fatalf("INSERT RETURNING: %v", err)
	}
	if res.RowsAffected != 1 {
		t.Errorf("INSERT RETURNING: expected 1, got %d", res.RowsAffected)
	}
}
