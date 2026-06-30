package VL

import (
	"context"
	"testing"
)

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
