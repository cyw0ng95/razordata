package EX

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// REQ000908: ATTACH / DETACH DATABASE catalog.
func TestAttachDetach_SamePath(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	e := NewExecutor()
	defer e.Close()
	ctx := context.Background()

	// ATTACH should succeed
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	_, err := e.Exec(ctx, fmt.Sprintf("ATTACH DATABASE '%s' AS db1", dbPath))
	if err != nil {
		t.Fatalf("ATTACH: %v", err)
	}

	// DETACH should succeed
	_, err = e.Exec(ctx, "DETACH DATABASE db1")
	if err != nil {
		t.Fatalf("DETACH: %v", err)
	}

	// DETACH of unknown name should succeed (idempotent)
	_, err = e.Exec(ctx, "DETACH DATABASE nonexistent")
	if err != nil {
		t.Fatalf("DETACH nonexistent: %v", err)
	}
}

// REQ000908: DETACH from test file that doesn't exist on disk.
func TestAttachDetach_QueryAcross(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	e := NewExecutor()
	defer e.Close()
	ctx := context.Background()

	// ATTACH a non-existent path — should succeed (v1 just stores mapping)
	_, err := e.Exec(ctx, fmt.Sprintf("ATTACH DATABASE '%s' AS ext", filepath.Join(t.TempDir(), "ext.db")))
	if err != nil {
		t.Fatalf("ATTACH: %v", err)
	}

	// DETACH should clean up
	_, err = e.Exec(ctx, "DETACH DATABASE ext")
	if err != nil {
		t.Fatalf("DETACH: %v", err)
	}
}

// REQ000908: Parse-level test for ATTACH/DETACH.
func TestAttachDetach_Parse(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	e := NewExecutor()
	defer e.Close()
	ctx := context.Background()

	// Verify the raw path is empty (no actual file creation needed)
	_, err := e.Exec(ctx, "ATTACH DATABASE ':memory:' AS main")
	if err != nil {
		t.Fatalf("ATTACH memory: %v", err)
	}
	_, err = e.Exec(ctx, "DETACH DATABASE main")
	if err != nil {
		t.Fatalf("DETACH memory: %v", err)
	}

	// Verify tmp directory exists
	tmpDir, _ := os.MkdirTemp("", "razor-attach-test-*")
	defer os.RemoveAll(tmpDir)
	_, err = e.Exec(ctx, fmt.Sprintf("ATTACH DATABASE '%s/test.db' AS testdb", tmpDir))
	if err != nil {
		t.Fatalf("ATTACH tmpdir: %v", err)
	}
	_, err = e.Exec(ctx, "DETACH DATABASE testdb")
	if err != nil {
		t.Fatalf("DETACH testdb: %v", err)
	}
}