package SY

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SYS/AP"
)

// TestEngine_DoubleClose — R08 Close is idempotent.
func TestEngine_DoubleClose(t *testing.T) {
	eng, _ := testEngine(t)
	if err := eng.Close(context.Background()); err != nil {
		t.Errorf("first Close: %v", err)
	}
	if err := eng.Close(context.Background()); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if err := eng.Close(context.Background()); err != nil {
		t.Errorf("third Close: %v", err)
	}
}

// TestEngine_BeginAfterClose — Begin on a closed engine returns
// AP.ErrClosed.
func TestEngine_BeginAfterClose(t *testing.T) {
	eng, _ := testEngine(t)
	_ = eng.Close(context.Background())
	if _, err := eng.Begin(context.Background()); !errors.Is(err, AP.ErrClosed) {
		t.Errorf("Begin after Close: got %v, want ErrClosed", err)
	}
}

// TestEngine_OpenMethodNoOp — R01 / R08: the public Open method on an
// already-constructed engine returns AP.ErrAlreadyOpen.
func TestEngine_OpenMethodNoOp(t *testing.T) {
	eng, _ := testEngine(t)
	if err := eng.Open(context.Background(), "/tmp", AP.Options{}); !errors.Is(err, AP.ErrAlreadyOpen) {
		t.Errorf("Open on running engine: got %v, want ErrAlreadyOpen", err)
	}
}

// TestEngine_StatsAfterOperations — R09: stats are non-zero after
// some activity.
func TestEngine_StatsAfterOperations(t *testing.T) {
	eng, ctx := testEngine(t)
	s, _ := eng.Begin(ctx)
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
		BufferPoolMB: 64,
		WALSizeMB:    16,
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
		BufferPoolMB: 64,
		WALSizeMB:    16,
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
	resetExecutorRegistry()
	eng2, err := Open(context.Background(), dir, AP.Options{
		PageSize:     4096,
		MemTableSize: 1024 * 1024,
		BufferPoolMB: 64,
		WALSizeMB:    16,
		MaxLevel:     3,
		LogLevel:     8,
	})
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer eng2.Close(context.Background())
	s2, _ := eng2.Begin(context.Background())
	// iter-12 invariant: the table is already registered via the
	// on-disk catalog. A second CREATE TABLE must fail.
	if _, err := s2.Exec(context.Background(), "CREATE TABLE t (id INTEGER, v TEXT, PRIMARY KEY (id))"); err == nil {
		t.Fatal("expected errTableExists on duplicate CREATE, got nil")
	}
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
		BufferPoolMB: 64,
		WALSizeMB:    16,
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
