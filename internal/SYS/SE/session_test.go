package SE

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	executor "github.com/cyw0ng95/razordata/internal/SQB/EX"
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

// TestSession_ErrLockedOnDoubleBegin — R11: Begin while a transaction
// is already active returns AP.ErrLocked.
func TestSession_ErrLockedOnDoubleBegin(t *testing.T) {
	eng, ctx := testEngine(t)
	s, err := eng.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.Begin(ctx)
	if err != nil {
		t.Fatalf("first Begin: %v", err)
	}
	_, err = s.Begin(ctx)
	if !AP.IsKind(err, AP.KindLocked) {
		t.Errorf("second Begin: got %v, want ErrLocked", err)
	}
	// Clean up the active transaction to avoid shutdown delay
	if err := tx.Rollback(ctx); err != nil {
		t.Logf("rollback: %v", err)
	}
}

// TestSession_CommitRollbackWithoutTxn — R11: Commit/Rollback on a
// session without an active transaction returns ErrNoActiveTxn.
func TestSession_CommitRollbackWithoutTxn(t *testing.T) {
	eng, ctx := testEngine(t)
	s, err := eng.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Commit(ctx); !AP.IsKind(err, AP.KindConstraint) {
		t.Errorf("Commit without txn: got %v, want ErrNoActiveTxn", err)
	}
	if err := s.Rollback(ctx); !AP.IsKind(err, AP.KindConstraint) {
		t.Errorf("Rollback without txn: got %v, want ErrNoActiveTxn", err)
	}
}

// TestSession_StatsCountersIncrement — R14: QueryCount and ActiveTXN
// reflect the session's activity.
func TestSession_StatsCountersIncrement(t *testing.T) {
	eng, ctx := testEngine(t)
	s, _ := eng.Begin(ctx)
	before := s.Stats()
	if before.QueryCount != 0 {
		t.Errorf("initial QueryCount = %d, want 0", before.QueryCount)
	}
	if before.ActiveTXN {
		t.Errorf("initial ActiveTXN = true, want false")
	}
	if _, err := s.Exec(ctx, "INSERT INTO users VALUES (1, 'a')"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Query(ctx, "SELECT id, name FROM users"); err != nil {
		t.Fatal(err)
	}
	mid := s.Stats()
	if mid.QueryCount < 2 {
		t.Errorf("QueryCount after 2 ops = %d, want >= 2", mid.QueryCount)
	}
	tx, err := s.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	active := s.Stats()
	if !active.ActiveTXN {
		t.Errorf("ActiveTXN after Begin = false, want true")
	}
	if err := s.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	after := s.Stats()
	if after.ActiveTXN {
		t.Errorf("ActiveTXN after Rollback = true, want false")
	}
	_ = tx
}

// TestSession_Stats_StableID — R14: ID is set once and stable.
func TestSession_Stats_StableID(t *testing.T) {
	eng, ctx := testEngine(t)
	s1, _ := eng.Begin(ctx)
	s2, _ := eng.Begin(ctx)
	if s1.Stats().ID == s2.Stats().ID {
		t.Errorf("two sessions have the same ID: %d", s1.Stats().ID)
	}
	id1 := s1.Stats().ID
	if s1.Stats().ID != id1 {
		t.Errorf("ID changed across Stats() call")
	}
}

// TestSession_SetDeadline — R13: SetDeadline stores a time.
func TestSession_SetDeadline(t *testing.T) {
	eng, ctx := testEngine(t)
	s, _ := eng.Begin(ctx)
	deadline := time.Now().Add(30 * time.Millisecond)
	if err := s.SetDeadline(deadline); err != nil {
		t.Fatalf("SetDeadline: %v", err)
	}
	// Wait past the deadline; subsequent Exec/Query should fail.
	time.Sleep(50 * time.Millisecond)
	_, err := s.Exec(ctx, "INSERT INTO users VALUES (100, 'late')")
	if !AP.IsKind(err, AP.KindDeadlineExceeded) {
		t.Errorf("Exec after deadline: got %v, want ErrDeadlineExceeded", err)
	}
}

// TestSession_SetDeadline_Future — R13: future deadline does not
// affect operations.
func TestSession_SetDeadline_Future(t *testing.T) {
	eng, ctx := testEngine(t)
	s, _ := eng.Begin(ctx)
	deadline := time.Now().Add(1 * time.Hour)
	if err := s.SetDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(ctx, "INSERT INTO users VALUES (1, 'a')"); err != nil {
		t.Errorf("Exec with future deadline: %v", err)
	}
}

// TestSession_Begin_TxQuery_TxExec — exercises the session
// through both Query and Exec on the same transaction.
func TestSession_Begin_TxQuery_TxExec(t *testing.T) {
	eng, ctx := testEngine(t)
	s, _ := eng.Begin(ctx)
	if _, err := s.Exec(ctx, "INSERT INTO users VALUES (1, 'a')"); err != nil {
		t.Fatal(err)
	}
	tx, err := s.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO users VALUES (2, 'b')"); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.Query(ctx, "SELECT name FROM users")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows.Cols()) != 1 || rows.Cols()[0] != "name" {
		t.Errorf("tx Query cols = %v", rows.Cols())
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

// TestSession_MultipleSessions_Independent — two sessions are
// independent: one session's actions don't affect another's view of
// ActiveTXN.
func TestSession_MultipleSessions_Independent(t *testing.T) {
	eng, ctx := testEngine(t)
	s1, _ := eng.Begin(ctx)
	s2, _ := eng.Begin(ctx)
	tx1, err := s1.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_ = tx1
	if !s1.Stats().ActiveTXN {
		t.Error("s1 should be in txn")
	}
	if s2.Stats().ActiveTXN {
		t.Error("s2 should NOT be in txn")
	}
	// Clean up the active transaction to avoid shutdown delay
	if err := tx1.Rollback(ctx); err != nil {
		t.Logf("rollback: %v", err)
	}
}
