package VL

import (
	"context"
	"errors"
	"sync"
	"testing"

	walwr "github.com/cyw0ng95/razordata/internal/WAL/WR"
)

// mockWALWriter captures records passed to Append/Sync for testing.
type mockWALWriter struct {
	mu        sync.Mutex
	appended  []walwr.WriteBatch
	appends   int
	syncs     int
	appendErr error
	syncErr   error
}

func (m *mockWALWriter) Append(batch *walwr.WriteBatch) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.appended = append(m.appended, *batch)
	m.appends++
	if m.appendErr != nil {
		return 0, m.appendErr
	}
	return uint64(m.appends), nil
}

func (m *mockWALWriter) Sync() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.syncs++
	return m.syncErr
}

func (m *mockWALWriter) snapshot() (appends, syncs int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.appends, m.syncs
}

// TestCommit_WritesWALRecord verifies that Commit emits an RTCommit
// record when a WAL writer is attached. REQ000171.
func TestCommit_WritesWALRecord(t *testing.T) {
	m := NewManager()
	defer m.Close()

	wal := &mockWALWriter{}
	txn, err := m.Begin(context.Background())
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	txn.(*tx).WithWAL(wal)
	if err := txn.Insert(context.Background(), []byte("k1"), []byte("v1")); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	// Capture txnID before Commit (slot is released after Commit, txnID reset)
	wantTxnID := txn.(*tx).slot.txnID
	if err := txn.Commit(context.Background()); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	appends, syncs := wal.snapshot()
	if appends != 1 {
		t.Errorf("expected 1 Append call, got %d", appends)
	}
	if syncs != 1 {
		t.Errorf("expected 1 Sync call, got %d", syncs)
	}
	if len(wal.appended) != 1 {
		t.Fatalf("expected 1 batch, got %d", len(wal.appended))
	}
	recs := wal.appended[0].Recs
	if len(recs) != 1 {
		t.Fatalf("expected 1 record in batch, got %d", len(recs))
	}
	if recs[0].Type != walwr.RTCommit {
		t.Errorf("expected RTCommit, got type %d", recs[0].Type)
	}
	decoded, err := DecodeCommitRecord(recs[0].Value)
	if err != nil {
		t.Fatalf("DecodeCommitRecord: %v", err)
	}
	if decoded.TxnID != wantTxnID {
		t.Errorf("TxnID mismatch: got %d, want %d", decoded.TxnID, wantTxnID)
	}
	if decoded.CommitTS == 0 {
		t.Errorf("CommitTS should be non-zero, got 0")
	}
	if decoded.KeyCount != 1 {
		t.Errorf("KeyCount: got %d, want 1", decoded.KeyCount)
	}
	if string(decoded.Keys[0]) != "k1" {
		t.Errorf("Key[0]: got %q, want k1", decoded.Keys[0])
	}
}

// TestCommit_NoWAL_NilWriter verifies Commit works without WAL.
func TestCommit_NoWAL_NilWriter(t *testing.T) {
	m := NewManager()
	defer m.Close()

	txn, err := m.Begin(context.Background())
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := txn.Insert(context.Background(), []byte("k"), []byte("v")); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := txn.Commit(context.Background()); err != nil {
		t.Errorf("Commit with nil WAL should succeed, got: %v", err)
	}
}

// TestCommit_WALAppendError verifies that Commit returns Append error.
func TestCommit_WALAppendError(t *testing.T) {
	m := NewManager()
	defer m.Close()

	wal := &mockWALWriter{appendErr: errors.New("disk full")}
	txn, err := m.Begin(context.Background())
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	txn.(*tx).WithWAL(wal)
	if err := txn.Insert(context.Background(), []byte("k"), []byte("v")); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := txn.Commit(context.Background()); err == nil {
		t.Error("expected Commit to return error")
	}
	_, syncs := wal.snapshot()
	if syncs != 0 {
		t.Errorf("expected 0 Sync calls after Append error, got %d", syncs)
	}
}

// TestCommit_WALSyncError verifies Sync error propagates.
func TestCommit_WALSyncError(t *testing.T) {
	m := NewManager()
	defer m.Close()

	wal := &mockWALWriter{syncErr: errors.New("fsync failed")}
	txn, err := m.Begin(context.Background())
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	txn.(*tx).WithWAL(wal)
	if err := txn.Insert(context.Background(), []byte("k"), []byte("v")); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := txn.Commit(context.Background()); err == nil {
		t.Error("expected Commit to return error")
	}
}
