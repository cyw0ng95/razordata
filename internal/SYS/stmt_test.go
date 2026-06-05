package SYS

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	executor "github.com/cyw0ng95/razordata/internal/SQL/EX"
	"github.com/cyw0ng95/razordata/internal/SYS/AP"
	"github.com/cyw0ng95/razordata/internal/SYS/ST"
)

// TestStmt_PrepareAndReuse — R16/R17: prepare a statement, execute it
// multiple times.
func TestStmt_PrepareAndReuse(t *testing.T) {
	executor.UnregisterAll()
	dir := filepath.Join(t.TempDir(), "db")
	eng, err := Open(context.Background(), dir, AP.Options{
		PageSize:     4096,
		MemTableSize: 1024 * 1024,
		BufferPoolMB: 16,
		WALSizeMB:    4,
		MaxLevel:     3,
		LogLevel:     8,
		LogFormat:    "text",
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	s, err := eng.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := s.Exec(context.Background(), "CREATE TABLE users (id INTEGER, name TEXT, PRIMARY KEY (id))"); err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close(context.Background()) })
	ctx := context.Background()

	stmt, err := ST.PrepareFromInterface(eng, "INSERT INTO users VALUES (1, 'a')")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	// Reuse the prepared statement for two executions. The first
	// succeeds; the second violates the unique PK and reports 0
	// rows affected, which exercises the not-error path.
	if _, err := stmt.Exec(ctx); err != nil {
		t.Fatalf("first exec: %v", err)
	}
	if res, err := stmt.Exec(ctx); err != nil {
		t.Errorf("second exec: %v", err)
	} else if res.RowsAffected > 1 {
		t.Errorf("unexpected RowsAffected = %d", res.RowsAffected)
	}
}

// TestStmt_CloseIdempotent — R16: Close is idempotent.
func TestStmt_CloseIdempotent(t *testing.T) {
	executor.UnregisterAll()
	dir := filepath.Join(t.TempDir(), "db")
	eng, err := Open(context.Background(), dir, AP.Options{
		PageSize:     4096,
		MemTableSize: 1024 * 1024,
		BufferPoolMB: 16,
		WALSizeMB:    4,
		MaxLevel:     3,
		LogLevel:     8,
		LogFormat:    "text",
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	s, err := eng.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := s.Exec(context.Background(), "CREATE TABLE users (id INTEGER, name TEXT, PRIMARY KEY (id))"); err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close(context.Background()) })

	stmt, err := ST.PrepareFromInterface(eng, "SELECT 1")
	if err != nil {
		t.Fatal(err)
	}
	if err := stmt.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	if err := stmt.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

// TestStmt_ExecAfterClose — R16: executing a closed statement
// returns AP.ErrClosed.
func TestStmt_ExecAfterClose(t *testing.T) {
	executor.UnregisterAll()
	dir := filepath.Join(t.TempDir(), "db")
	eng, err := Open(context.Background(), dir, AP.Options{
		PageSize:     4096,
		MemTableSize: 1024 * 1024,
		BufferPoolMB: 16,
		WALSizeMB:    4,
		MaxLevel:     3,
		LogLevel:     8,
		LogFormat:    "text",
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	s, err := eng.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := s.Exec(context.Background(), "CREATE TABLE users (id INTEGER, name TEXT, PRIMARY KEY (id))"); err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close(context.Background()) })
	ctx := context.Background()

	stmt, err := ST.PrepareFromInterface(eng, "SELECT 1")
	if err != nil {
		t.Fatal(err)
	}
	_ = stmt.Close()
	if _, err := stmt.Exec(ctx); !errors.Is(err, AP.ErrClosed) {
		t.Errorf("Exec after Close: got %v, want ErrClosed", err)
	}
	if _, err := stmt.Query(ctx); !errors.Is(err, AP.ErrClosed) {
		t.Errorf("Query after Close: got %v, want ErrClosed", err)
	}
}

// TestStmt_PrepareEmptySQL — R16: empty SQL returns AP.ErrSyntax.
func TestStmt_PrepareEmptySQL(t *testing.T) {
	executor.UnregisterAll()
	dir := filepath.Join(t.TempDir(), "db")
	eng, err := Open(context.Background(), dir, AP.Options{
		PageSize:     4096,
		MemTableSize: 1024 * 1024,
		BufferPoolMB: 16,
		WALSizeMB:    4,
		MaxLevel:     3,
		LogLevel:     8,
		LogFormat:    "text",
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	s, err := eng.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := s.Exec(context.Background(), "CREATE TABLE users (id INTEGER, name TEXT, PRIMARY KEY (id))"); err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close(context.Background()) })

	_, err = ST.PrepareFromInterface(eng, "")
	if !errors.Is(err, AP.ErrSyntax) {
		t.Errorf("empty SQL: got %v, want ErrSyntax", err)
	}
}

// TestStmt_PrepareNilEngine — AP.ErrNotOpen.
func TestStmt_PrepareNilEngine(t *testing.T) {
	_, err := ST.PrepareFromInterface(nil, "SELECT 1")
	if !errors.Is(err, AP.ErrNotOpen) {
		t.Errorf("nil engine: got %v, want ErrNotOpen", err)
	}
}

// TestStmt_QueryReturnsCols — R17: prepare + Query returns the
// expected column metadata.
func TestStmt_QueryReturnsCols(t *testing.T) {
	executor.UnregisterAll()
	dir := filepath.Join(t.TempDir(), "db")
	eng, err := Open(context.Background(), dir, AP.Options{
		PageSize:     4096,
		MemTableSize: 1024 * 1024,
		BufferPoolMB: 16,
		WALSizeMB:    4,
		MaxLevel:     3,
		LogLevel:     8,
		LogFormat:    "text",
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	s, err := eng.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := s.Exec(context.Background(), "CREATE TABLE users (id INTEGER, name TEXT, PRIMARY KEY (id))"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.Exec(context.Background(), "INSERT INTO users VALUES (1, 'a')"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close(context.Background()) })
	ctx := context.Background()

	stmt, err := ST.PrepareFromInterface(eng, "SELECT id, name FROM users")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	rows, err := stmt.Query(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows.Cols) != 2 || rows.Cols[0] != "id" || rows.Cols[1] != "name" {
		t.Errorf("cols = %v, want [id name]", rows.Cols)
	}
}

// TestStmt_ExecReturnsRowsAffected — R16: Exec returns the result's
// RowsAffected.
func TestStmt_ExecReturnsRowsAffected(t *testing.T) {
	executor.UnregisterAll()
	dir := filepath.Join(t.TempDir(), "db")
	eng, err := Open(context.Background(), dir, AP.Options{
		PageSize:     4096,
		MemTableSize: 1024 * 1024,
		BufferPoolMB: 16,
		WALSizeMB:    4,
		MaxLevel:     3,
		LogLevel:     8,
		LogFormat:    "text",
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	s, err := eng.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := s.Exec(context.Background(), "CREATE TABLE users (id INTEGER, name TEXT, PRIMARY KEY (id))"); err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close(context.Background()) })
	ctx := context.Background()

	stmt, err := ST.PrepareFromInterface(eng, "INSERT INTO users VALUES (1, 'a')")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	res, err := stmt.Exec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.RowsAffected != 1 {
		t.Errorf("RowsAffected = %d, want 1", res.RowsAffected)
	}
}

// TestStmt_SQLAccessor — R17: the SQL text is preserved.
func TestStmt_SQLAccessor(t *testing.T) {
	executor.UnregisterAll()
	dir := filepath.Join(t.TempDir(), "db")
	eng, err := Open(context.Background(), dir, AP.Options{
		PageSize:     4096,
		MemTableSize: 1024 * 1024,
		BufferPoolMB: 16,
		WALSizeMB:    4,
		MaxLevel:     3,
		LogLevel:     8,
		LogFormat:    "text",
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	s, err := eng.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := s.Exec(context.Background(), "CREATE TABLE users (id INTEGER, name TEXT, PRIMARY KEY (id))"); err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close(context.Background()) })

	stmt, err := ST.PrepareFromInterface(eng, "SELECT 1")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	if stmt.SQL() != "SELECT 1" {
		t.Errorf("SQL() = %q, want %q", stmt.SQL(), "SELECT 1")
	}
}
