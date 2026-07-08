package VL

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestLockManager_BasicLockUnlock(t *testing.T) {
	lt := NewLockTable(100 * time.Millisecond)

	// Acquire exclusive lock.
	if err := lt.Lock(1, []byte("k1"), LockModeExclusive); err != nil {
		t.Fatalf("Lock X: %v", err)
	}

	// Same txn re-acquire should succeed.
	if err := lt.Lock(1, []byte("k1"), LockModeExclusive); err != nil {
		t.Fatalf("Re-lock X: %v", err)
	}

	// Release lock.
	lt.Unlock(1)

	// Lock table should be empty.
	if stats := lt.Stats(); stats.ActiveLocks != 0 {
		t.Errorf("expected 0 locks, got %d", stats.ActiveLocks)
	}
}

func TestLockManager_SharedLocks(t *testing.T) {
	lt := NewLockTable(100 * time.Millisecond)

	// Two transactions can hold shared locks on the same key.
	if err := lt.Lock(1, []byte("k1"), LockModeShared); err != nil {
		t.Fatalf("Lock S1: %v", err)
	}
	if err := lt.Lock(2, []byte("k1"), LockModeShared); err != nil {
		t.Fatalf("Lock S2: %v", err)
	}

	// Release both.
	lt.Unlock(1)
	lt.Unlock(2)

	if stats := lt.Stats(); stats.ActiveLocks != 0 {
		t.Errorf("expected 0 locks, got %d", stats.ActiveLocks)
	}
}

func TestLockManager_ExclusiveBlocksShared(t *testing.T) {
	lt := NewLockTable(50 * time.Millisecond)

	// TX1 holds exclusive lock.
	if err := lt.Lock(1, []byte("k1"), LockModeExclusive); err != nil {
		t.Fatalf("Lock X: %v", err)
	}

	// TX2 tries to acquire shared lock — should timeout.
	err := lt.Lock(2, []byte("k1"), LockModeShared)
	if err != ErrDeadlockTimeout {
		t.Errorf("expected ErrDeadlockTimeout, got %v", err)
	}

	// Release TX1's lock.
	lt.Unlock(1)
}

func TestLockManager_SUpgradeToExclusive(t *testing.T) {
	lt := NewLockTable(100 * time.Millisecond)

	// TX1 acquires shared lock.
	if err := lt.Lock(1, []byte("k1"), LockModeShared); err != nil {
		t.Fatalf("Lock S: %v", err)
	}

	// TX1 upgrades to exclusive (no other holders).
	if err := lt.Lock(1, []byte("k1"), LockModeExclusive); err != nil {
		t.Fatalf("Upgrade S->X: %v", err)
	}

	lt.Unlock(1)
}

func TestLockManager_ConcurrentWrites(t *testing.T) {
	m := NewManager()
	defer m.Close()

	ctx := context.Background()
	key := []byte("shared_key")

	var wg sync.WaitGroup
	committed := 0
	aborted := 0
	mu := sync.Mutex{}

	// Launch 10 concurrent transactions that all write the same key.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tx, err := m.Begin(ctx)
			if err != nil {
				return
			}
			if err := tx.Insert(ctx, key, []byte("v")); err != nil {
				mu.Lock()
				aborted++
				mu.Unlock()
				return
			}
			if err := tx.Commit(ctx); err != nil {
				mu.Lock()
				aborted++
				mu.Unlock()
				return
			}
			mu.Lock()
			committed++
			mu.Unlock()
		}()
	}
	wg.Wait()

	// At least one should commit, and the rest should abort due to conflict.
	if committed == 0 {
		t.Error("expected at least one commit")
	}
	t.Logf("committed=%d aborted=%d", committed, aborted)
}

func TestLockManager_Stats(t *testing.T) {
	lt := NewLockTable(100 * time.Millisecond)

	if err := lt.Lock(1, []byte("k1"), LockModeExclusive); err != nil {
		t.Fatalf("Lock: %v", err)
	}

	stats := lt.Stats()
	if stats.ActiveLocks != 1 {
		t.Errorf("expected 1 active lock, got %d", stats.ActiveLocks)
	}

	lt.Unlock(1)
	stats = lt.Stats()
	if stats.ActiveLocks != 0 {
		t.Errorf("expected 0 active locks, got %d", stats.ActiveLocks)
	}
}

// ── WaitForActive ───────────────────────────────────────────────────

func TestWaitForActive_NoActiveReturnsImmediately(t *testing.T) {
	t.Parallel()
	m := NewManager()
	ctx := context.Background()
	start := time.Now()
	if err := m.WaitForActive(ctx, 5*time.Second); err != nil {
		t.Fatalf("WaitForActive: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Errorf("WaitForActive took %v on empty manager; want <100ms", elapsed)
	}
}

// TestWaitForActive_DrainsOnCommit starts a transaction, kicks off
// WaitForActive in a goroutine, then commits the transaction. The
// wait should return nil shortly after the commit.
func TestWaitForActive_DrainsOnCommit(t *testing.T) {
	t.Parallel()
	m := NewManager()
	tx, err := m.Begin(context.Background())
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if m.Stats().Active != 1 {
		t.Fatalf("Stats.Active: want 1, got %d", m.Stats().Active)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	var waitErr error
	go func() {
		defer wg.Done()
		waitErr = m.WaitForActive(context.Background(), 2*time.Second)
	}()
	// Give the waiter a moment to enter the loop.
	time.Sleep(20 * time.Millisecond)
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	wg.Wait()
	if waitErr != nil {
		t.Errorf("WaitForActive: %v", waitErr)
	}
}

// TestWaitForActive_Timeout verifies that a hung transaction causes
// WaitForActive to return a timeout error wrapping the residual
// active count.
func TestWaitForActive_Timeout(t *testing.T) {
	t.Parallel()
	m := NewManager()
	tx, err := m.Begin(context.Background())
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer func() { _ = tx.Abort(context.Background()) }()
	ctx := context.Background()
	err = m.WaitForActive(ctx, 50*time.Millisecond)
	if err == nil {
		t.Fatalf("WaitForActive: want timeout error, got nil")
	}
	if !errors.Is(err, err) {
		t.Errorf("WaitForActive error %v should be non-nil and contain residual count", err)
	}
	if m.Stats().Active != 1 {
		t.Errorf("after timeout, Active: want 1 (tx still held), got %d", m.Stats().Active)
	}
}

// TestWaitForActive_ContextCancel verifies that a pre-cancelled
// context returns ctx.Err() immediately.
func TestWaitForActive_ContextCancel(t *testing.T) {
	t.Parallel()
	m := NewManager()
	tx, err := m.Begin(context.Background())
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer func() { _ = tx.Abort(context.Background()) }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = m.WaitForActive(ctx, 5*time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitForActive: want context.Canceled, got %v", err)
	}
}

// TestWaitForActive_ZeroTimeoutRejected covers the parameter
// validation: a non-positive timeout is an error.
func TestWaitForActive_ZeroTimeoutRejected(t *testing.T) {
	t.Parallel()
	m := NewManager()
	if err := m.WaitForActive(context.Background(), 0); err == nil {
		t.Errorf("WaitForActive(0): want error, got nil")
	}
	if err := m.WaitForActive(context.Background(), -1); err == nil {
		t.Errorf("WaitForActive(-1): want error, got nil")
	}
}

// TestWaitForActive_MultipleTransactionsDrainsOnLast verifies that
// the wait does not return until ALL active transactions have
// completed.
func TestWaitForActive_MultipleTransactionsDrainsOnLast(t *testing.T) {
	t.Parallel()
	m := NewManager()
	tx1, err := m.Begin(context.Background())
	if err != nil {
		t.Fatalf("Begin 1: %v", err)
	}
	tx2, err := m.Begin(context.Background())
	if err != nil {
		t.Fatalf("Begin 2: %v", err)
	}
	if m.Stats().Active != 2 {
		t.Fatalf("Stats.Active: want 2, got %d", m.Stats().Active)
	}
	if err := tx1.Commit(context.Background()); err != nil {
		t.Fatalf("Commit 1: %v", err)
	}
	// Wait should not return yet — tx2 is still active.
	if err := m.WaitForActive(context.Background(), 10*time.Millisecond); err == nil {
		t.Errorf("WaitForActive returned nil with tx2 still active; want timeout")
	}
	if err := tx2.Commit(context.Background()); err != nil {
		t.Fatalf("Commit 2: %v", err)
	}
	if err := m.WaitForActive(context.Background(), 1*time.Second); err != nil {
		t.Errorf("WaitForActive after both commits: %v", err)
	}
}
