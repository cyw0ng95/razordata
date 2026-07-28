//go:build !slt_corpus_full

package EX

import (
	"context"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
)

func TestDebugPipeline(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	DT.RegisterTableSchema("t1", []string{"id", "name"})

	e := NewExecutor()
	e.EnablePipelinePath()
	defer e.DisablePipelinePath()

	ctx := context.Background()

	res, err := e.Exec(ctx, "INSERT INTO t1 VALUES (1, 'alice')")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("RowsAffected = %d", res.RowsAffected)

	// Check if the row was actually inserted
	DT.TablesMu.Lock()
	rows := DT.Tables["t1"]
	DT.TablesMu.Unlock()
	t.Logf("t1 row count = %d", len(rows))
	if len(rows) != 1 {
		t.Errorf("expected 1 row, got %d", len(rows))
	}
}