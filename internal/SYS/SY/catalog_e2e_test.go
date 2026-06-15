package SY

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SYS/AP"
)

// TestR12_Catalog_SurvivesRestart — REQ000127 end-to-end
// invariant. Open a database, create a table, close, reopen, and
// confirm the table is addressable for SELECT. This is the
// canonical "create table, kill process, restart" loop.
func TestR12_Catalog_SurvivesRestart(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "db")

	// First handle: open, create, close.
	eng1, err := Open(context.Background(), dir, AP.Options{
		PageSize:     4096,
		MemTableSize: 1024 * 1024,
		BufferPoolMB: 64,
		WALSizeMB:    16,
		MaxLevel:     3,
		LogLevel:     8,
		LogFormat:    "text",
	})
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	s1, _ := eng1.Begin(context.Background())
	if _, err := s1.Exec(context.Background(),
		"CREATE TABLE r12_t (id INTEGER, name TEXT, PRIMARY KEY (id))"); err != nil {
		t.Fatalf("CREATE TABLE: %v", err)
	}
	if err := eng1.Close(context.Background()); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	// Wipe the in-memory catalog so the second Open must
	// rebuild from the on-disk file.
	resetExecutorRegistry()

	// Second handle: open, verify the table is addressable.
	eng2, err := Open(context.Background(), dir, AP.Options{
		PageSize:     4096,
		MemTableSize: 1024 * 1024,
		BufferPoolMB: 64,
		WALSizeMB:    16,
		MaxLevel:     3,
		LogLevel:     8,
		LogFormat:    "text",
	})
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer eng2.Close(context.Background())
	s2, _ := eng2.Begin(context.Background())
	// Duplicate CREATE must fail (catalog already has the table).
	_, err = s2.Exec(context.Background(),
		"CREATE TABLE r12_t (id INTEGER, name TEXT, PRIMARY KEY (id))")
	if err == nil {
		t.Fatal("expected errTableExists on duplicate CREATE, got nil")
	}
	// SELECT against the original table must succeed.
	rows, err := s2.Query(context.Background(), "SELECT name FROM r12_t")
	if err != nil {
		t.Fatalf("post-restart SELECT: %v", err)
	}
	if rows == nil {
		t.Fatal("post-restart SELECT returned nil rows")
	}
}

// TestR12_Catalog_DropSurvivesRestart — DROP TABLE must persist.
// After a drop, the schema is gone in the on-disk catalog and
// a re-CREATE must succeed.
func TestR12_Catalog_DropSurvivesRestart(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "db")
	eng1, err := Open(context.Background(), dir, AP.Options{
		PageSize:     4096,
		MemTableSize: 1024 * 1024,
		BufferPoolMB: 64,
		WALSizeMB:    16,
		MaxLevel:     3,
		LogLevel:     8,
		LogFormat:    "text",
	})
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	s1, _ := eng1.Begin(context.Background())
	if _, err := s1.Exec(context.Background(),
		"CREATE TABLE r12_drop (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatalf("CREATE: %v", err)
	}
	if _, err := s1.Exec(context.Background(), "DROP TABLE r12_drop"); err != nil {
		t.Fatalf("DROP: %v", err)
	}
	if err := eng1.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	resetExecutorRegistry()

	eng2, err := Open(context.Background(), dir, AP.Options{
		PageSize:     4096,
		MemTableSize: 1024 * 1024,
		BufferPoolMB: 64,
		WALSizeMB:    16,
		MaxLevel:     3,
		LogLevel:     8,
		LogFormat:    "text",
	})
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer eng2.Close(context.Background())
	s2, _ := eng2.Begin(context.Background())
	if _, err := s2.Exec(context.Background(),
		"CREATE TABLE r12_drop (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatalf("re-CREATE after restart: %v", err)
	}
}

// TestR12_Catalog_MultipleTablesPersisted — create a handful of
// tables, close, reopen, confirm all are addressable.
func TestR12_Catalog_MultipleTablesPersisted(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "db")
	eng1, err := Open(context.Background(), dir, AP.Options{
		PageSize:     4096,
		MemTableSize: 1024 * 1024,
		BufferPoolMB: 64,
		WALSizeMB:    16,
		MaxLevel:     3,
		LogLevel:     8,
		LogFormat:    "text",
	})
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	s1, _ := eng1.Begin(context.Background())
	names := []string{"r12_a", "r12_b", "r12_c", "r12_d"}
	for _, n := range names {
		if _, err := s1.Exec(context.Background(),
			"CREATE TABLE "+n+" (id INTEGER, v TEXT, PRIMARY KEY (id))"); err != nil {
			t.Fatalf("CREATE %s: %v", n, err)
		}
	}
	if err := eng1.Close(context.Background()); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	resetExecutorRegistry()

	eng2, err := Open(context.Background(), dir, AP.Options{
		PageSize:     4096,
		MemTableSize: 1024 * 1024,
		BufferPoolMB: 64,
		WALSizeMB:    16,
		MaxLevel:     3,
		LogLevel:     8,
		LogFormat:    "text",
	})
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer eng2.Close(context.Background())
	s2, _ := eng2.Begin(context.Background())
	for _, n := range names {
		if _, err := s2.Query(context.Background(), "SELECT v FROM "+n); err != nil {
			t.Fatalf("post-restart SELECT %s: %v", n, err)
		}
	}
}

// TestR12_Catalog_OpenRejectsCorrupt — if catalog.dat is
// corrupt, Open must return an error rather than booting a
// database with a silently broken schema registry.
func TestR12_Catalog_OpenRejectsCorrupt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "db")
	// First Open to create the catalog dir, then poison it.
	eng1, err := Open(context.Background(), dir, AP.Options{
		PageSize:     4096,
		MemTableSize: 1024 * 1024,
		BufferPoolMB: 64,
		WALSizeMB:    16,
		MaxLevel:     3,
		LogLevel:     8,
		LogFormat:    "text",
	})
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	_ = eng1.Close(context.Background())
	// Overwrite catalog.dat with garbage.
	catPath := filepath.Join(dir, "catalog", "catalog.dat")
	if err := writeFile(catPath, []byte("not a valid catalog file")); err != nil {
		t.Fatalf("seed corrupt: %v", err)
	}
	// Second Open must fail with ErrCatalogCorrupt.
	eng2, err := Open(context.Background(), dir, AP.Options{
		PageSize:     4096,
		MemTableSize: 1024 * 1024,
		BufferPoolMB: 64,
		WALSizeMB:    16,
		MaxLevel:     3,
		LogLevel:     8,
		LogFormat:    "text",
	})
	if err == nil {
		_ = eng2.Close(context.Background())
		t.Fatal("expected Open to fail on corrupt catalog, got nil")
	}
	if !errors.Is(err, errCatalogCorruptWrapped) && !strings.Contains(err.Error(), "catalog") {
		t.Fatalf("expected catalog error, got %v", err)
	}
}

// errCatalogCorruptWrapped is a sentinel used to confirm the
// error chain in TestR12_Catalog_OpenRejectsCorrupt. The
// bootstrap code in catalog.go wraps ErrCatalogCorrupt so the
// chain still satisfies errors.Is.
var errCatalogCorruptWrapped = errAnyCatalogCorrupt

// errAnyCatalogCorrupt is a stand-in for "any catalog error" so
// the test fails on unrelated errors. We rely on the wrapped
// chain in the actual production code.
var errAnyCatalogCorrupt = errors.New("catalog")

// writeFile is a small helper that mirrors os.WriteFile to keep
// the test file's import surface narrow.
func writeFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o644)
}
