package VL

import (
	"context"
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
	lt.mu.RLock()
	if len(lt.locks) != 0 {
		t.Errorf("expected 0 locks, got %d", len(lt.locks))
	}
	lt.mu.RUnlock()
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

	lt.mu.RLock()
	if len(lt.locks) != 0 {
		t.Errorf("expected 0 locks, got %d", len(lt.locks))
	}
	lt.mu.RUnlock()
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
