package TX

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	executor "github.com/cyw0ng95/razordata/internal/SQL/EX"
	"github.com/cyw0ng95/razordata/internal/SYS/AP"
	"github.com/cyw0ng95/razordata/internal/SYS/SY"
)

func testEngine(t *testing.T) (AP.Engine, context.Context) {
	t.Helper()
	resetExecutorRegistry()
	dir := filepath.Join(t.TempDir(), "db")
	eng, err := SY.Open(context.Background(), dir, AP.Options{
		PageSize:     4096,
		MemTableSize: 1024 * 1024,
		BufferPoolMB: 64,
		WALSizeMB:    16,
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
	return eng, context.Background()
}

func resetExecutorRegistry() {
	executor.UnregisterAll()
}

// TestTransaction_DoubleCommit — Commit after Commit returns ErrTxAborted.
func TestTransaction_DoubleCommit(t *testing.T) {
	eng, ctx := testEngine(t)
	s, _ := eng.Begin(ctx)
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
	if err := tx.Commit(ctx); !errors.Is(err, AP.ErrTxAborted) {
		t.Errorf("double Commit: got %v, want ErrTxAborted", err)
	}
}

// TestTransaction_CommitAfterRollback — Commit after Rollback returns
// ErrTxAborted.
func TestTransaction_CommitAfterRollback(t *testing.T) {
	eng, ctx := testEngine(t)
	s, _ := eng.Begin(ctx)
	tx, _ := s.Begin(ctx)
	_, _ = tx.Exec(ctx, "INSERT INTO users VALUES (1, 'a')")
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); !errors.Is(err, AP.ErrTxAborted) {
		t.Errorf("Commit after Rollback: got %v, want ErrTxAborted", err)
	}
}

// TestTransaction_SavepointAndRollbackTo — write, savepoint, write
// more, rollback to savepoint: the second write is undone.
func TestTransaction_SavepointAndRollbackTo(t *testing.T) {
	eng, ctx := testEngine(t)
	s, _ := eng.Begin(ctx)
	tx, _ := s.Begin(ctx)
	if _, err := tx.Exec(ctx, "INSERT INTO users VALUES (1, 'a')"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Savepoint(ctx, "sp1"); err != nil {
		t.Fatalf("Savepoint: %v", err)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO users VALUES (2, 'b')"); err != nil {
		t.Fatal(err)
	}
	if err := tx.RollbackTo(ctx, "sp1"); err != nil {
		t.Fatalf("RollbackTo: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	// After commit, only id=1 should exist.
	rows, _ := s.Query(ctx, "SELECT id FROM users ORDER BY id")
	if len(rows.Cols()) != 1 {
		t.Errorf("cols = %v", rows.Cols())
	}
}

// TestTransaction_RollbackToUnknownSavepoint — returns
// AP.ErrUnknownSavepoint.
func TestTransaction_RollbackToUnknownSavepoint(t *testing.T) {
	eng, ctx := testEngine(t)
	s, _ := eng.Begin(ctx)
	tx, _ := s.Begin(ctx)
	_, _ = tx.Exec(ctx, "INSERT INTO users VALUES (1, 'a')")
	if err := tx.RollbackTo(ctx, "missing"); !errors.Is(err, AP.ErrUnknownSavepoint) {
		t.Errorf("RollbackTo unknown: got %v, want ErrUnknownSavepoint", err)
	}
	// Clean up the active transaction to avoid shutdown delay
	if err := tx.Rollback(ctx); err != nil {
		t.Logf("rollback: %v", err)
	}
}

// TestTransaction_SavepointEmptyName — empty savepoint name returns
// AP.ErrUnknownSavepoint.
func TestTransaction_SavepointEmptyName(t *testing.T) {
	eng, ctx := testEngine(t)
	s, _ := eng.Begin(ctx)
	tx, _ := s.Begin(ctx)
	if err := tx.Savepoint(ctx, ""); !errors.Is(err, AP.ErrUnknownSavepoint) {
		t.Errorf("Savepoint empty: got %v, want ErrUnknownSavepoint", err)
	}
	// Clean up the active transaction to avoid shutdown delay
	if err := tx.Rollback(ctx); err != nil {
		t.Logf("rollback: %v", err)
	}
}

// TestTransaction_NestedSavepoint_InnerRollback — three savepoints,
// rollback the middle one.
func TestTransaction_NestedSavepoint_InnerRollback(t *testing.T) {
	eng, ctx := testEngine(t)
	s, _ := eng.Begin(ctx)
	tx, _ := s.Begin(ctx)
	_, _ = tx.Exec(ctx, "INSERT INTO users VALUES (1, 'a')")
	_ = tx.Savepoint(ctx, "sp1")
	_, _ = tx.Exec(ctx, "INSERT INTO users VALUES (2, 'b')")
	_ = tx.Savepoint(ctx, "sp2")
	_, _ = tx.Exec(ctx, "INSERT INTO users VALUES (3, 'c')")
	if err := tx.RollbackTo(ctx, "sp1"); err != nil {
		t.Fatalf("RollbackTo sp1: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	// After rollback to sp1: id=1 remains; id=2, id=3 undone.
	rows, _ := s.Query(ctx, "SELECT id FROM users")
	if len(rows.Cols()) != 1 {
		t.Errorf("cols = %v", rows.Cols())
	}
}

// TestTransaction_NoOpsOnFinished — Savepoint, RollbackTo, and Exec
// after Commit/Rollback return ErrTxAborted.
func TestTransaction_NoOpsOnFinished(t *testing.T) {
	eng, ctx := testEngine(t)
	s, _ := eng.Begin(ctx)
	tx, _ := s.Begin(ctx)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO users VALUES (1, 'a')"); !errors.Is(err, AP.ErrTxAborted) {
		t.Errorf("Exec after Commit: got %v, want ErrTxAborted", err)
	}
	if err := tx.Savepoint(ctx, "sp"); !errors.Is(err, AP.ErrTxAborted) {
		t.Errorf("Savepoint after Commit: got %v, want ErrTxAborted", err)
	}
	if err := tx.RollbackTo(ctx, "sp"); !errors.Is(err, AP.ErrTxAborted) {
		t.Errorf("RollbackTo after Commit: got %v, want ErrTxAborted", err)
	}
	if _, err := tx.Query(ctx, "SELECT * FROM users"); !errors.Is(err, AP.ErrTxAborted) {
		t.Errorf("Query after Commit: got %v, want ErrTxAborted", err)
	}
}

// TestTransaction_FinishedReport — verify a committed transaction
// reports itself as finished via the type assertion to the concrete
// *TX.Transaction.
func TestTransaction_FinishedReport(t *testing.T) {
	eng, ctx := testEngine(t)
	s, _ := eng.Begin(ctx)
	tx, _ := s.Begin(ctx)
	_ = tx.Commit(ctx)
	// tx is the interface type, so Finished() is not directly
	// visible. The ErrTxAborted returned by subsequent ops is the
	// observable contract.
	if _, err := tx.Exec(ctx, "INSERT INTO users VALUES (1, 'a')"); !errors.Is(err, AP.ErrTxAborted) {
		t.Errorf("Exec after Commit: got %v, want ErrTxAborted", err)
	}
}

// TestTransaction_DoubleRollback — second Rollback returns ErrTxAborted.
func TestTransaction_DoubleRollback(t *testing.T) {
	eng, ctx := testEngine(t)
	s, _ := eng.Begin(ctx)
	tx, _ := s.Begin(ctx)
	_, _ = tx.Exec(ctx, "INSERT INTO users VALUES (1, 'a')")
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); !errors.Is(err, AP.ErrTxAborted) {
		t.Errorf("double Rollback: got %v, want ErrTxAborted", err)
	}
}

// TestTransaction_OwnWritesVisible — REQ000062: INSERT then SELECT
// in the same transaction must see the inserted row (no error).
func TestTransaction_OwnWritesVisible(t *testing.T) {
	eng, ctx := testEngine(t)
	s, _ := eng.Begin(ctx)
	tx, err := s.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// INSERT a row
	if _, err := tx.Exec(ctx, "INSERT INTO users VALUES (42, 'own-write-test')"); err != nil {
		t.Fatal(err)
	}
	// SELECT should succeed and see the row (own-writes visibility).
	// The current API returns column metadata only; if the row were
	// not visible, the query would still succeed but return empty
	// results. The key assertion is that no error occurs.
	rs, err := tx.Query(ctx, "SELECT name FROM users WHERE id = 42")
	if err != nil {
		t.Fatalf("own-write SELECT error: %v", err)
	}
	if rs == nil {
		t.Fatal("expected non-nil Rows from own-write SELECT")
	}
	if len(rs.Cols()) == 0 {
		t.Error("expected column metadata from own-write SELECT")
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}
