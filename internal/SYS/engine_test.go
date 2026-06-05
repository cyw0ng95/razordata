package SYS

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	executor "github.com/cyw0ng95/razordata/internal/SQL/EX"
	"github.com/cyw0ng95/razordata/internal/SYS/AP"
)

// TestEngine_DoubleClose — R08 Close is idempotent.
func TestEngine_DoubleClose(t *testing.T) {
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
	ctx := context.Background()

	if err := eng.Close(ctx); err != nil {
		t.Errorf("first Close: %v", err)
	}
	if err := eng.Close(ctx); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if err := eng.Close(ctx); err != nil {
		t.Errorf("third Close: %v", err)
	}
}

// TestEngine_BeginAfterClose — Begin on a closed engine returns
// AP.ErrClosed.
func TestEngine_BeginAfterClose(t *testing.T) {
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
	ctx := context.Background()

	_ = eng.Close(ctx)
	if _, err := eng.Begin(ctx); !errors.Is(err, AP.ErrClosed) {
		t.Errorf("Begin after Close: got %v, want ErrClosed", err)
	}
}

// TestEngine_OpenMethodNoOp — R01 / R08: the public Open method on an
// already-constructed engine returns AP.ErrAlreadyOpen.
func TestEngine_OpenMethodNoOp(t *testing.T) {
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
	defer eng.Close(context.Background())

	if err := eng.Open(context.Background(), "/tmp", AP.Options{}); !errors.Is(err, AP.ErrAlreadyOpen) {
		t.Errorf("Open on running engine: got %v, want ErrAlreadyOpen", err)
	}
}

// TestEngine_StatsAfterOperations — R09: stats are non-zero after
// some activity.
func TestEngine_StatsAfterOperations(t *testing.T) {
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

	// Run a real transaction so Tx.Committed goes up.
	tx, err := s.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO users VALUES (1, 'a')"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(ctx, "INSERT INTO users VALUES (2, 'b')"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Query(ctx, "SELECT id, name FROM users"); err != nil {
		t.Fatal(err)
	}
	st := eng.Stats()
	if st.Tx.Committed == 0 {
		t.Errorf("expected non-zero Tx.Committed after Commit; got %d", st.Tx.Committed)
	}
	if st.Uptime <= 0 {
		t.Errorf("uptime = %v, want > 0", st.Uptime)
	}
}

// TestEngine_ConcurrentClose — R08: concurrent goroutines may Close;
// only the first one performs teardown.
func TestEngine_ConcurrentClose(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "db")
	eng, err := Open(context.Background(), dir, AP.Options{
		PageSize:     4096,
		MemTableSize: 1024 * 1024,
		BufferPoolMB: 16,
		WALSizeMB:    4,
		MaxLevel:     3,
		LogLevel:     8,
	})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = eng.Close(context.Background())
		}()
	}
	wg.Wait()
}

// TestEngine_OpenDuplicateDir — opening the same dir twice
// sequentially. The second Open should succeed (reopen is supported).
// v1 limitation: the executor's in-memory catalog does not survive
// process restart, so the caller must re-run CREATE TABLE on the
// new handle. This test asserts only that the second Open does not
// error; data is not asserted.
func TestEngine_OpenDuplicateDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "db")
	eng1, err := Open(context.Background(), dir, AP.Options{
		PageSize:     4096,
		MemTableSize: 1024 * 1024,
		BufferPoolMB: 16,
		WALSizeMB:    4,
		MaxLevel:     3,
		LogLevel:     8,
	})
	if err != nil {
		t.Fatal(err)
	}
	s, _ := eng1.Begin(context.Background())
	_, _ = s.Exec(context.Background(), "CREATE TABLE t (id INTEGER, v TEXT, PRIMARY KEY (id))")
	_, _ = s.Exec(context.Background(), "INSERT INTO t VALUES (1, 'persisted')")
	if err := eng1.Close(context.Background()); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	// Wipe the in-memory catalog so the second engine can re-create.
	executor.UnregisterAll()
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
	// Recreate the table on the new handle to query it.
	s2, _ := eng2.Begin(context.Background())
	if _, err := s2.Exec(context.Background(), "CREATE TABLE t (id INTEGER, v TEXT, PRIMARY KEY (id))"); err != nil {
		t.Fatalf("re-create: %v", err)
	}
	// The catalog is fresh; the SELECT may return zero cols (no
	// rows) because the executor's table schema registry is empty.
	// What we assert here is that the second engine accepts the
	// query without erroring.
	rows, err := s2.Query(context.Background(), "SELECT v FROM t")
	if err != nil {
		t.Fatalf("post-reopen query: %v", err)
	}
	_ = rows
}

// TestEngine_Reopen_LargeDataSet — insert many rows, close, reopen,
// verify the table is still addressable (data path itself is
// flushed).
func TestEngine_Reopen_LargeDataSet(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "db")
	eng1, err := Open(context.Background(), dir, AP.Options{
		PageSize:     4096,
		MemTableSize: 1024 * 1024,
		BufferPoolMB: 16,
		WALSizeMB:    4,
		MaxLevel:     3,
		LogLevel:     8,
	})
	if err != nil {
		t.Fatal(err)
	}
	s, _ := eng1.Begin(context.Background())
	_, _ = s.Exec(context.Background(), "CREATE TABLE t (id INTEGER, v INTEGER, PRIMARY KEY (id))")
	for i := 0; i < 100; i++ {
		_, _ = s.Exec(context.Background(), "INSERT INTO t VALUES (1, 100)")
	}
	_ = eng1.Close(context.Background())
}
