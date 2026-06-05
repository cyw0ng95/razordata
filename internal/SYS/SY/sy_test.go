package SY

import (
	"context"
	"path/filepath"
	"testing"

	executor "github.com/cyw0ng95/razordata/internal/SQL/EX"
	"github.com/cyw0ng95/razordata/internal/SYS/AP"
)

// TestEngine_ReopenPreservesCatalog verifies that CREATE TABLE
// statements survive a Close + Open cycle. After Open, the
// engine's catalog lists the table that was created in the
// previous session.
func TestEngine_ReopenPreservesCatalog(t *testing.T) {
	executor.UnregisterAll()
	dir := filepath.Join(t.TempDir(), "db")

	// First session: open, create a table, close.
	eng1, err := Open(context.Background(), dir, AP.Options{
		PageSize:     4096,
		MemTableSize: 1024 * 1024,
		BufferPoolMB: 16,
		WALSizeMB:    4,
		MaxLevel:     3,
		LogLevel:     8,
	})
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if _, err := eng1.Executor().Exec(context.Background(),
		"CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT)"); err != nil {
		t.Fatalf("CREATE TABLE: %v", err)
	}
	if err := eng1.Close(context.Background()); err != nil {
		t.Fatalf("first Close: %v", err)
	}

	// Second session: reopen the same dir, verify the table is
	// in the catalog.
	eng2, err := Open(context.Background(), dir, AP.Options{
		PageSize:     4096,
		MemTableSize: 1024 * 1024,
		BufferPoolMB: 16,
		WALSizeMB:    4,
		MaxLevel:     3,
		LogLevel:     8,
	})
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer eng2.Close(context.Background())
	// Query a SELECT against the table — this exercises the
	// catalog-populated path. An empty result is fine; we just
	// want to verify the table is reachable (no error about
	// "table not registered").
	rows, err := eng2.Executor().QueryAll(context.Background(),
		"SELECT name FROM t")
	if err != nil {
		t.Fatalf("SELECT on reopened table: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected 0 rows on empty table, got %d", len(rows))
	}
}
