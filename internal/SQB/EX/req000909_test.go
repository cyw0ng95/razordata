package EX

import (
	"context"
	"strings"
	"testing"
)

// REQ000909: CREATE VIRTUAL TABLE stub registration.
func TestCreateVirtualTable_StubRegistration(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	e := NewExecutor()
	defer e.Close()
	ctx := context.Background()

	// CREATE VIRTUAL TABLE should return a clear error, not panic.
	_, err := e.Exec(ctx, "CREATE VIRTUAL TABLE t1 USING fts5(content)")
	if err == nil {
		t.Fatal("expected error for virtual table, got nil")
	}
	if !strings.Contains(err.Error(), "virtual table module not supported") {
		t.Fatalf("unexpected error: %v", err)
	}

	// Parsing alone should work.
	_, err = e.QueryAll(ctx, "SELECT * FROM sqlite_master WHERE type='table'")
	if err != nil {
		t.Fatalf("sqlite_master query: %v", err)
	}
}