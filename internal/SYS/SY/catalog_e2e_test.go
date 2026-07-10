package SY

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	"github.com/cyw0ng95/razordata/internal/SYS/AP"
)

// execOnEngine runs a DDL/DML statement directly on the engine's
// executor, bypassing the session layer (which requires SE/TX
// registration). Used by catalog e2e tests to avoid import cycles.
func execOnEngine(e *Engine, ctx context.Context, sql string, args ...any) error {
	_, err := e.exe.Exec(ctx, sql, args...)
	return err
}

// queryOnEngine runs a SELECT statement directly on the engine's
// executor and drains it (no rows returned to caller; just
// succeeds/fails).
func queryOnEngine(e *Engine, ctx context.Context, sql string, args ...any) error {
	stream, err := e.exe.QueryStream(ctx, sql, args...)
	if err != nil {
		return err
	}
	defer stream.Close()
	// Drain the stream to ensure it executes.
	for {
		_, err := stream.Next()
		if err != nil {
			break
		}
	}
	return nil
}

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
	if err := execOnEngine(eng1, context.Background(),
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
	// Duplicate CREATE must fail (catalog already has the table).
	err = execOnEngine(eng2, context.Background(),
		"CREATE TABLE r12_t (id INTEGER, name TEXT, PRIMARY KEY (id))")
	if err == nil {
		t.Fatal("expected errTableExists on duplicate CREATE, got nil")
	}
	// SELECT against the original table must succeed.
	if err := queryOnEngine(eng2, context.Background(), "SELECT name FROM r12_t"); err != nil {
		t.Fatalf("post-restart SELECT: %v", err)
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
	if err := execOnEngine(eng1, context.Background(),
		"CREATE TABLE r12_drop (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatalf("CREATE: %v", err)
	}
	if err := execOnEngine(eng1, context.Background(), "DROP TABLE r12_drop"); err != nil {
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
	if err := execOnEngine(eng2, context.Background(),
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
	names := []string{"r12_a", "r12_b", "r12_c", "r12_d"}
	for _, n := range names {
		if err := execOnEngine(eng1, context.Background(),
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
	for _, n := range names {
		if err := queryOnEngine(eng2, context.Background(), "SELECT v FROM "+n); err != nil {
			t.Fatalf("post-restart SELECT %s: %v", n, err)
		}
	}
}

// TestR12_Catalog_StatsWireAfterAnalyze — REQ001318 sanity:
// ColumnStats are present in the catalog right after ANALYZE and
// survive close+reopen.
func TestR12_Catalog_StatsWireAfterAnalyze(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "db")
	ctx := context.Background()

	eng1, err := Open(ctx, dir, AP.Options{
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

	if err := execOnEngine(eng1, ctx,
		"CREATE TABLE st_wire2 (id INTEGER, v INTEGER, PRIMARY KEY (id))"); err != nil {
		t.Fatalf("CREATE: %v", err)
	}
	for i := int64(1); i <= 10; i++ {
		if err := execOnEngine(eng1, ctx,
			"INSERT INTO st_wire2 VALUES (?, ?)", i, i*10); err != nil {
			t.Fatalf("INSERT %d: %v", i, err)
		}
	}
	if err := execOnEngine(eng1, ctx, "ANALYZE st_wire2"); err != nil {
		t.Fatalf("ANALYZE: %v", err)
	}

	// Stats must be in the on-disk catalog after ANALYZE.
	idStats := eng1.catalog.ColumnStatsByName("st_wire2", "id")
	if idStats == nil {
		t.Fatal("expected st_wire2.id ColumnStats in catalog after ANALYZE")
	}
	if idStats.RowCount != 10 {
		t.Errorf("st_wire2.id RowCount = %d, want 10", idStats.RowCount)
	}
	if err := eng1.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	resetExecutorRegistry()

	eng2, err := Open(ctx, dir, AP.Options{
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
	defer eng2.Close(ctx)

	// Stats must survive restart in the catalog.
	entries2 := eng2.catalog.List()
	var found *ls.CatalogEntry
	for _, e := range entries2 {
		if e.Name == "st_wire2" {
			found = e
			break
		}
	}
	if found == nil {
		t.Fatal("st_wire2 not found in catalog after restart")
	}
	if len(found.ColumnStats) == 0 {
		t.Fatal("REQ001318: ColumnStats lost after restart — catalog persisted zero stats entries")
	}
	for _, se := range found.ColumnStats {
		t.Logf("surviving stat: col=%s ndv=%d rows=%d min=%x max=%x",
			se.Column, se.Stats.DistinctCount, se.Stats.RowCount,
			se.Stats.MinValue, se.Stats.MaxValue)
	}
	idStats2 := eng2.catalog.ColumnStatsByName("st_wire2", "id")
	if idStats2 == nil {
		t.Fatal("REQ001318: column stats lost after restart — catalog may not persist Stats blob")
	}
	if idStats2.RowCount != 10 {
		t.Errorf("st_wire2.id RowCount after restart = %d, want 10", idStats2.RowCount)
	}

	// Verify query works through the executor (implicitly uses wired StatsCatalog).
	if err := queryOnEngine(eng2, ctx, "SELECT v FROM st_wire2 WHERE id = 5"); err != nil {
		t.Fatalf("SELECT after restart: %v", err)
	}
}

// TestR12_Catalog_StatsWiredOnOpen — REQ001318: after ANALYZE,
// stats survive restart in the on-disk catalog and the planner's
// StatsCatalog is wired at engine open so queries can use them.
func TestR12_Catalog_StatsWiredOnOpen(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "db")
	ctx := context.Background()

	eng1, err := Open(ctx, dir, AP.Options{
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

	if err := execOnEngine(eng1, ctx,
		"CREATE TABLE st_wire (id INTEGER, v INTEGER, PRIMARY KEY (id))"); err != nil {
		t.Fatalf("CREATE: %v", err)
	}
	for i := int64(1); i <= 10; i++ {
		if err := execOnEngine(eng1, ctx,
			"INSERT INTO st_wire VALUES (?, ?)", i, i*10); err != nil {
			t.Fatalf("INSERT %d: %v", i, err)
		}
	}
	if err := execOnEngine(eng1, ctx, "ANALYZE st_wire"); err != nil {
		t.Fatalf("ANALYZE: %v", err)
	}

	// Stats must be in the on-disk catalog after ANALYZE.
	idStats := eng1.catalog.ColumnStatsByName("st_wire", "id")
	if idStats == nil {
		t.Fatal("expected st_wire.id ColumnStats in catalog after ANALYZE")
	}
	if idStats.RowCount != 10 {
		t.Errorf("st_wire.id RowCount = %d, want 10", idStats.RowCount)
	}
	vStats := eng1.catalog.ColumnStatsByName("st_wire", "v")
	if vStats == nil {
		t.Fatal("expected st_wire.v ColumnStats in catalog after ANALYZE")
	}

	if err := eng1.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	resetExecutorRegistry()

	eng2, err := Open(ctx, dir, AP.Options{
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
	defer eng2.Close(ctx)

	// Stats must survive restart in the catalog.
	idStats2 := eng2.catalog.ColumnStatsByName("st_wire", "id")
	if idStats2 == nil {
		t.Fatal("expected st_wire.id ColumnStats after restart (REQ001318)")
	}
	if idStats2.RowCount != 10 {
		t.Errorf("st_wire.id RowCount after restart = %d, want 10", idStats2.RowCount)
	}

	// Verify the executor can query — implicitly uses wired StatsCatalog.
	if err := queryOnEngine(eng2, ctx, "SELECT v FROM st_wire WHERE id = 5"); err != nil {
		t.Fatalf("SELECT after restart: %v", err)
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
	if err := os.WriteFile(catPath, []byte("not a valid catalog file"), 0o644); err != nil {
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
	if !strings.Contains(err.Error(), "catalog") {
		t.Fatalf("expected catalog error, got %v", err)
	}
}
