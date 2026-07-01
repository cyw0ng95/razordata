package VL

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestNewManager_BasicBeginCommit(t *testing.T) {
	m := NewManager()
	defer m.Close()

	tx, err := m.Begin(context.Background())
	if err != nil {
		t.Fatalf("Begin failed: %v", err)
	}

	if err := tx.Insert(context.Background(), []byte("k"), []byte("v")); err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	if err := tx.Commit(context.Background()); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	stats := m.Stats()
	if stats.Committed != 1 {
		t.Errorf("expected Committed=1, got %d", stats.Committed)
	}
	if stats.Active != 0 {
		t.Errorf("expected Active=0 after commit, got %d", stats.Active)
	}
}

func TestNewManager_Isolation(t *testing.T) {
	// Two managers must have independent state.
	m1 := NewManager()
	m2 := NewManager()
	defer m1.Close()
	defer m2.Close()

	tx1, err := m1.Begin(context.Background())
	if err != nil {
		t.Fatalf("m1.Begin: %v", err)
	}
	if err := tx1.Insert(context.Background(), []byte("shared-key"), []byte("m1-value")); err != nil {
		t.Fatalf("tx1.Insert: %v", err)
	}
	if err := tx1.Commit(context.Background()); err != nil {
		t.Fatalf("tx1.Commit: %v", err)
	}

	tx2, err := m2.Begin(context.Background())
	if err != nil {
		t.Fatalf("m2.Begin: %v", err)
	}
	defer tx2.Abort(context.Background())

	// m2 should not see m1's writes (separate MV instance).
	got, _ := tx2.Get(context.Background(), []byte("shared-key"))
	if got != nil {
		t.Errorf("expected nil from isolated manager, got %q", got)
	}
}

func TestManager_AbortCounter(t *testing.T) {
	m := NewManager()
	defer m.Close()

	tx, err := m.Begin(context.Background())
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := tx.Abort(context.Background()); err != nil {
		t.Fatalf("Abort: %v", err)
	}

	stats := m.Stats()
	if stats.Aborted != 1 {
		t.Errorf("expected Aborted=1, got %d", stats.Aborted)
	}
	if stats.Committed != 0 {
		t.Errorf("expected Committed=0, got %d", stats.Committed)
	}
}

func TestManager_Close_Idempotent(t *testing.T) {
	m := NewManager()
	if err := m.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	if err := m.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestManager_BeginAfterClose(t *testing.T) {
	m := NewManager()
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	_, err := m.Begin(context.Background())
	if !errors.Is(err, ErrManagerClosed) {
		t.Errorf("expected ErrManagerClosed, got %v", err)
	}
}

func TestManager_BeginCancelledContext(t *testing.T) {
	m := NewManager()
	defer m.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := m.Begin(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestManager_BeginExhaustedSlots(t *testing.T) {
	m := NewManager()
	defer m.Close()

	// Drain the pool.
	held := make([]Tx, 0, MaxConcurrentTXNs)
	for i := 0; i < MaxConcurrentTXNs; i++ {
		tx, err := m.Begin(context.Background())
		if err != nil {
			t.Fatalf("Begin %d: %v", i, err)
		}
		held = append(held, tx)
	}

	_, err := m.Begin(context.Background())
	if !errors.Is(err, ErrNoSlotsAvailable) {
		t.Errorf("expected ErrNoSlotsAvailable, got %v", err)
	}

	// Release one slot; should be allocatable again.
	if err := held[0].Abort(context.Background()); err != nil {
		t.Fatalf("Abort: %v", err)
	}

	tx, err := m.Begin(context.Background())
	if err != nil {
		t.Fatalf("Begin after release: %v", err)
	}
	if err := tx.Abort(context.Background()); err != nil {
		t.Fatalf("Abort new tx: %v", err)
	}
}

func TestManager_Stats_Initial(t *testing.T) {
	m := NewManager()
	defer m.Close()

	s := m.Stats()
	if s.Capacity != MaxConcurrentTXNs {
		t.Errorf("Capacity: want %d, got %d", MaxConcurrentTXNs, s.Capacity)
	}
	if s.Active != 0 {
		t.Errorf("Active: want 0, got %d", s.Active)
	}
	if s.FreeSlots != MaxConcurrentTXNs {
		t.Errorf("FreeSlots: want %d, got %d", MaxConcurrentTXNs, s.FreeSlots)
	}
	if s.Committed != 0 || s.Aborted != 0 {
		t.Errorf("counters must start at 0, got committed=%d aborted=%d", s.Committed, s.Aborted)
	}
}

func TestManager_Stats_UnderLoad(t *testing.T) {
	m := NewManager()
	defer m.Close()

	const N = 50
	var (
		wg     sync.WaitGroup
		errsMu sync.Mutex
		errs   []error
	)
	recordErr := func(e error) {
		errsMu.Lock()
		errs = append(errs, e)
		errsMu.Unlock()
	}

	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			tx, err := m.Begin(context.Background())
			if err != nil {
				recordErr(err)
				return
			}
			// NOTE: Insert is omitted here on purpose — the MV
			// arena's thread-local path races when accessed from
			// many goroutines sharing one MV. That is a pre-
			// existing issue in the iter-04 MV layer, out of
			// scope for the manager audit. Begin/Commit/Abort
			// exercise the manager's atomic counters without
			// touching the arena.
			if i%2 == 0 {
				if err := tx.Commit(context.Background()); err != nil {
					recordErr(err)
				}
			} else {
				if err := tx.Abort(context.Background()); err != nil {
					recordErr(err)
				}
			}
		}(i)
	}
	wg.Wait()

	if len(errs) > 0 {
		t.Fatalf("goroutine errors: %v", errs)
	}

	s := m.Stats()
	if int(s.Committed+s.Aborted) != N {
		t.Errorf("expected Committed+Aborted=%d, got Committed=%d Aborted=%d", N, s.Committed, s.Aborted)
	}
	if s.Active != 0 {
		t.Errorf("expected Active=0 after wg.Wait, got %d", s.Active)
	}
}

func TestManager_TransactionCancelledMidWay(t *testing.T) {
	m := NewManager()
	defer m.Close()

	ctx, cancel := context.WithCancel(context.Background())
	tx, err := m.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	cancel()

	if err := tx.Insert(ctx, []byte("k"), []byte("v")); !errors.Is(err, context.Canceled) {
		t.Errorf("Insert on cancelled ctx: want context.Canceled, got %v", err)
	}
	if err := tx.Commit(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Commit on cancelled ctx: want context.Canceled, got %v", err)
	}
	// tx should still be allocatable for cleanup
	if err := tx.Abort(context.Background()); err != nil {
		t.Errorf("Abort after cancel: %v", err)
	}

	if got := m.Stats().Aborted; got != 1 {
		t.Errorf("Aborted counter: want 1, got %d", got)
	}
}

func TestManagerShared_GlobalCompat(t *testing.T) {
	// ManagerShared wraps the global slot pool — package-level Begin and
	// Manager.Begin on the shared instance should both work against the
	// same global slot pool (the package-level Begin and the shared
	// Manager both call into the same globalSlotManager).
	m := NewManagerShared(globalSlotManager, globalMV)
	defer m.Close()

	before := globalSlotManager.NumFreeSlots()
	txPkg, err := Begin(context.Background())
	if err != nil {
		t.Fatalf("package Begin: %v", err)
	}
	mid := globalSlotManager.NumFreeSlots()
	if mid != before-1 {
		t.Errorf("expected slot count to drop by 1 after pkg Begin, got before=%d mid=%d", before, mid)
	}

	if err := txPkg.Insert(context.Background(), []byte("k"), []byte("v")); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	// Same-transaction read works (read your own writes).
	if got, _ := txPkg.Get(context.Background(), []byte("k")); string(got) != "v" {
		t.Errorf("expected txPkg.Get == %q, got %q", "v", got)
	}

	if err := txPkg.Commit(context.Background()); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if got := globalSlotManager.NumFreeSlots(); got != before {
		t.Errorf("expected slot to be released, before=%d after=%d", before, got)
	}

	// Sanity: Manager.Begin on the shared instance also drains the global pool.
	txMgr, err := m.Begin(context.Background())
	if err != nil {
		t.Fatalf("Manager.Begin: %v", err)
	}
	defer txMgr.Abort(context.Background())

	if got := globalSlotManager.NumFreeSlots(); got != before-1 {
		t.Errorf("shared Manager.Begin did not drain global pool: before=%d after=%d", before, got)
	}
}

func TestManager_TxnManagerInterface(t *testing.T) {
	// Compile-time interface conformance check.
	var _ TxnManager = (*Manager)(nil)
}

// ── Savepoints ───────────────────────────────────────────────────────

func TestSavepoint_PartialRollback(t *testing.T) {
	m := NewManager()
	defer m.Close()

	ctx := context.Background()

	txn, err := m.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	// Insert k1.
	if err := txn.Insert(ctx, []byte("k1"), []byte("v1")); err != nil {
		t.Fatalf("Insert k1: %v", err)
	}

	// Create savepoint after k1.
	if err := txn.Savepoint("sp1"); err != nil {
		t.Fatalf("Savepoint sp1: %v", err)
	}

	// Insert k2.
	if err := txn.Insert(ctx, []byte("k2"), []byte("v2")); err != nil {
		t.Fatalf("Insert k2: %v", err)
	}

	// Delete k3 (create a tombstone).
	if err := txn.Delete(ctx, []byte("k3")); err != nil {
		t.Fatalf("Delete k3: %v", err)
	}

	// Rollback to sp1.
	if err := txn.RollbackTo("sp1"); err != nil {
		t.Fatalf("RollbackTo sp1: %v", err)
	}

	// Verify: k1 should still be visible (own write), k2 and k3 should be gone.
	val, err := txn.Get(ctx, []byte("k1"))
	if err != nil {
		t.Fatalf("Get k1 after rollback: %v", err)
	}
	if string(val) != "v1" {
		t.Errorf("expected k1=v1, got %s", val)
	}

	val, err = txn.Get(ctx, []byte("k2"))
	if err != nil {
		t.Fatalf("Get k2 after rollback: %v", err)
	}
	if val != nil {
		t.Errorf("expected k2=nil after rollback, got %s", val)
	}

	val, err = txn.Get(ctx, []byte("k3"))
	if err != nil {
		t.Fatalf("Get k3 after rollback: %v", err)
	}
	if val != nil {
		t.Errorf("expected k3=nil after rollback, got %s", val)
	}

	// Verify writeSet was correctly truncated: only k1 should be in writeSet.
	txi := txn.(*tx)
	if len(txi.slot.writeSet) != 1 {
		t.Errorf("expected writeSet len=1, got %d", len(txi.slot.writeSet))
	}
	if len(txi.slot.writeSet) > 0 && string(txi.slot.writeSet[0].Start) != "k1" {
		t.Errorf("expected writeSet[0]=k1, got %s", txi.slot.writeSet[0].Start)
	}

	// Commit the transaction.
	if err := txn.Commit(ctx); err != nil {
		t.Fatalf("Commit: %v", err)
	}
}

func TestSavepoint_NotFound(t *testing.T) {
	m := NewManager()
	defer m.Close()

	ctx := context.Background()
	txn, _ := m.Begin(ctx)

	err := txn.RollbackTo("nonexistent")
	if err != ErrSavepointNotFound {
		t.Errorf("expected ErrSavepointNotFound, got %v", err)
	}

	txn.Abort(ctx)
}

func TestSavepoint_RollbackPastSavepoint(t *testing.T) {
	m := NewManager()
	defer m.Close()

	ctx := context.Background()
	txn, _ := m.Begin(ctx)

	txn.Savepoint("sp1")
	txn.Insert(ctx, []byte("k1"), []byte("v1"))
	txn.Savepoint("sp2")
	txn.Insert(ctx, []byte("k2"), []byte("v2"))

	// Rollback to sp2, then try to rollback to sp1 (which is before sp2).
	txn.RollbackTo("sp2")
	err := txn.RollbackTo("sp1")
	if err != nil {
		t.Errorf("rollback to sp1 after sp2 should succeed: %v", err)
	}

	// Now writeSet is at sp1 (length 0). Rollback to sp1 again is a no-op.
	err = txn.RollbackTo("sp1")
	if err != nil {
		t.Errorf("re-rollback to same savepoint should be no-op: %v", err)
	}

	txn.Abort(ctx)
}
