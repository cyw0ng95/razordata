package EX

import (
	"context"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
)

// TestForeignKeys_DefaultOff verifies REQ001307: default FK state.
func TestForeignKeys_DefaultOff(t *testing.T) {
	e := NewExecutorWithEngine(nil)
	ctx := context.Background()

	// Default should be ON (true).
	rows, err := e.QueryAll(ctx, "PRAGMA foreign_keys")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Data[0].I64 != 1 {
		t.Fatalf("expected foreign_keys=1 (default ON), got %v", rows)
	}

	// Toggle OFF.
	if _, err := e.Exec(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		t.Fatal(err)
	}
	if DT.IsForeignKeysEnabled() {
		t.Fatal("expected FK disabled after PRAGMA")
	}
}

// TestForeignKeys_ToggleOn verifies REQ001307: toggle back ON.
func TestForeignKeys_ToggleOn(t *testing.T) {
	e := NewExecutorWithEngine(nil)
	ctx := context.Background()

	// Toggle ON.
	if _, err := e.Exec(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		t.Fatal(err)
	}
	if !DT.IsForeignKeysEnabled() {
		t.Fatal("expected FK enabled after PRAGMA ON")
	}

	// Toggle OFF.
	if _, err := e.Exec(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		t.Fatal(err)
	}
	if DT.IsForeignKeysEnabled() {
		t.Fatal("expected FK disabled after PRAGMA OFF")
	}
}

// TestForeignKeys_InsideTxn_NoOp verifies REQ001307: PRAGMA foreign_keys
// is a no-op inside a transaction — returns current value but does not
// change the state.
func TestForeignKeys_InsideTxn_NoOp(t *testing.T) {
	e := NewExecutorWithEngine(nil)
	ctx := context.Background()

	// Ensure FK is ON.
	if _, err := e.Exec(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		t.Fatal(err)
	}

	// Simulate a transaction by setting a TxWriter.
	e.SetTxWriter(&noopTxWriter{})
	defer e.ClearTxWriter() // Restore state after test.

	// Attempt to turn FK OFF inside transaction — should be no-op.
	_, err := e.Exec(ctx, "PRAGMA foreign_keys = OFF")
	if err != nil {
		t.Fatal(err)
	}

	// FK should still be ON.
	if !DT.IsForeignKeysEnabled() {
		t.Fatal("expected FK still ON after no-op inside txn")
	}

	// Read should return current value.
	rows, err := e.QueryAll(ctx, "PRAGMA foreign_keys")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Data[0].I64 != 1 {
		t.Fatalf("expected foreign_keys=1, got %v", rows)
	}
}

// noopTxWriter is a dummy TxWriter for simulating transaction context.
type noopTxWriter struct{}

func (n *noopTxWriter) RecordWrite(key []byte, newValue []byte) {}
func (n *noopTxWriter) RecordInMemoryTable(table string, snapshot []DT.Row) {}
