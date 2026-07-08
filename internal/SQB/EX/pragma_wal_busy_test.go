package EX

import (
	"context"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
)

// TestPragma_WalAutocheckpoint_DefaultIs1000 verifies the default
// threshold is 1000 pages (SQLite compat). REQ001300.
func TestPragma_WalAutocheckpoint_DefaultIs1000(t *testing.T) {
	DT.SetWalAutocheckpoint(1000)
	defer DT.SetWalAutocheckpoint(1000)
	if DT.GetWalAutocheckpoint() != 1000 {
		t.Fatalf("default wal_autocheckpoint = %d, want 1000", DT.GetWalAutocheckpoint())
	}
}

// TestPragma_WalAutocheckpoint_ReadWriteRoundTrip verifies the
// PRAGMA read/write round-trip via Executor. REQ001300.
func TestPragma_WalAutocheckpoint_ReadWriteRoundTrip(t *testing.T) {
	DT.SetWalAutocheckpoint(1000)
	defer DT.SetWalAutocheckpoint(1000)

	e := NewExecutorWithEngine(nil)
	ctx := context.Background()

	// Read default.
	rows, err := e.QueryAll(ctx, "PRAGMA wal_autocheckpoint")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Data[0].I64 != 1000 {
		t.Fatalf("default wal_autocheckpoint = %d, want 1000", rows[0].Data[0].I64)
	}

	// Set 500.
	if _, err := e.Exec(ctx, "PRAGMA wal_autocheckpoint = 500"); err != nil {
		t.Fatal(err)
	}
	if DT.GetWalAutocheckpoint() != 500 {
		t.Fatalf("after set: %d, want 500", DT.GetWalAutocheckpoint())
	}

	// Read back.
	rows, err = e.QueryAll(ctx, "PRAGMA wal_autocheckpoint")
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Data[0].I64 != 500 {
		t.Fatalf("read back = %d, want 500", rows[0].Data[0].I64)
	}

	// Set 0 = disabled.
	if _, err := e.Exec(ctx, "PRAGMA wal_autocheckpoint = 0"); err != nil {
		t.Fatal(err)
	}
	if DT.GetWalAutocheckpoint() != 0 {
		t.Fatalf("after disable: %d, want 0", DT.GetWalAutocheckpoint())
	}
}

// TestPragma_BusyTimeout_DefaultIsZero verifies the default is 0
// (no wait). REQ001301.
func TestPragma_BusyTimeout_DefaultIsZero(t *testing.T) {
	DT.SetBusyTimeout(0)
	defer DT.SetBusyTimeout(0)
	if DT.GetBusyTimeout() != 0 {
		t.Fatalf("default busy_timeout = %d, want 0", DT.GetBusyTimeout())
	}
}

// TestPragma_BusyTimeout_ReadWriteRoundTrip verifies the PRAGMA
// read/write round-trip via Executor. REQ001301.
func TestPragma_BusyTimeout_ReadWriteRoundTrip(t *testing.T) {
	DT.SetBusyTimeout(0)
	defer DT.SetBusyTimeout(0)

	e := NewExecutorWithEngine(nil)
	ctx := context.Background()

	// Read default.
	rows, err := e.QueryAll(ctx, "PRAGMA busy_timeout")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Data[0].I64 != 0 {
		t.Fatalf("default busy_timeout = %d, want 0", rows[0].Data[0].I64)
	}

	// Set 1000ms.
	if _, err := e.Exec(ctx, "PRAGMA busy_timeout = 1000"); err != nil {
		t.Fatal(err)
	}
	if DT.GetBusyTimeout() != 1000 {
		t.Fatalf("after set: %d, want 1000", DT.GetBusyTimeout())
	}

	// Read back.
	rows, err = e.QueryAll(ctx, "PRAGMA busy_timeout")
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Data[0].I64 != 1000 {
		t.Fatalf("read back = %d, want 1000", rows[0].Data[0].I64)
	}
}

// TestPragma_BusyHandler_DefaultEmpty verifies the default handler
// name is empty. REQ001302.
func TestPragma_BusyHandler_DefaultEmpty(t *testing.T) {
	DT.SetBusyHandler("", nil)
	name, _ := DT.GetBusyHandler()
	if name != "" {
		t.Fatalf("default busy_handler name = %q, want empty", name)
	}
}

// TestPragma_BusyHandler_RegistersCallback verifies PRAGMA
// busy_handler = name registers the callback and the read PRAGMA
// returns the active name. REQ001302.
func TestPragma_BusyHandler_RegistersCallback(t *testing.T) {
	DT.SetBusyHandler("", nil)
	DT.UnregisterBusyHandler("my_handler")
	defer DT.SetBusyHandler("", nil)
	defer DT.UnregisterBusyHandler("my_handler")

	e := NewExecutorWithEngine(nil)
	ctx := context.Background()

	// Set a named handler via PRAGMA.
	if _, err := e.Exec(ctx, "PRAGMA busy_handler = my_handler"); err != nil {
		t.Fatal(err)
	}
	name, _ := DT.GetBusyHandler()
	if name != "my_handler" {
		t.Fatalf("after set: name = %q, want my_handler", name)
	}

	// Read back via PRAGMA.
	rows, err := e.QueryAll(ctx, "PRAGMA busy_handler")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Data[0].ToAny() != "my_handler" {
		t.Fatalf("read back = %v, want my_handler", rows[0].Data[0].ToAny())
	}
}

// TestPragma_BusyHandler_ReadAfterSetUnchanged verifies a read-only
// PRAGMA busy_handler (no = clause) does not clear the existing
// handler. REQ001302.
func TestPragma_BusyHandler_ReadAfterSetUnchanged(t *testing.T) {
	DT.SetBusyHandler("", nil)
	DT.UnregisterBusyHandler("read_after")
	defer DT.SetBusyHandler("", nil)
	defer DT.UnregisterBusyHandler("read_after")

	e := NewExecutorWithEngine(nil)
	ctx := context.Background()

	if _, err := e.Exec(ctx, "PRAGMA busy_handler = read_after"); err != nil {
		t.Fatal(err)
	}
	// Read PRAGMA (no value) — should be a no-op.
	if _, err := e.Exec(ctx, "PRAGMA busy_handler"); err != nil {
		t.Fatal(err)
	}
	name, _ := DT.GetBusyHandler()
	if name != "read_after" {
		t.Fatalf("after read: name = %q, want read_after (read must not clear)", name)
	}
}