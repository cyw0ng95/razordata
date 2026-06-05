package VL

import (
	"context"
	"sync"

	"github.com/cyw0ng95/razordata/internal/TXN/MV"
	"github.com/cyw0ng95/razordata/internal/TXN/SN"
)

var globalSlotManager = newSlotManager()
var globalMV = MV.NewMV()

// defaultManager backs the package-level Begin function for backward
// compatibility. New code should construct a Manager explicitly via
// NewManager.
var defaultManager = NewManagerShared(globalSlotManager, globalMV)

type Tx interface {
	Get(ctx context.Context, key []byte) ([]byte, error)
	Insert(ctx context.Context, key, value []byte) error
	Delete(ctx context.Context, key []byte) error
	Commit(ctx context.Context) error
	Abort(ctx context.Context) error
}

type tx struct {
	sm       *slotManager
	mv       *MV.MV
	manager  *Manager
	slot     *transactionSlot
	readView *SN.ReadView
	arena    *MV.Arena
	// mu serializes the per-txn write set and arena against concurrent
	// goroutines that might call Insert/Delete on the same transaction.
	// The arena is per-txn and not safe for concurrent use; without this
	// lock, two goroutines could race on t.arena.Alloc.
	mu       sync.Mutex
	finished bool
}

func (t *tx) Get(ctx context.Context, key []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	// Once the tx is finished, its arena has been released back to
	// the pool and the slot reused. Walking the MV chain would read
	// recycled memory; refuse the operation explicitly so callers get
	// a defined error rather than undefined behaviour.
	if t.finished {
		return nil, ErrTxFinished
	}
	chain := t.mv.GetVersionChain(key)
	if chain != nil {
		for node := chain.GetHead(); node != nil; node = node.Next() {
			if node.TxnID() == t.slot.txnID && node.BeginTS() == t.slot.beginTS {
				if node.Deleted() {
					return nil, nil
				}
				return node.Value(), nil
			}
		}
	}

	node := t.mv.FindVisible(key, t.slot.beginTS)
	if node == nil {
		return nil, nil
	}
	if node.Deleted() {
		return nil, nil
	}
	return node.Value(), nil
}

func (t *tx) Insert(ctx context.Context, key, value []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.finished {
		return ErrTxFinished
	}
	node := MV.NewVersionNode(t.arena, t.slot.txnID, t.slot.beginTS, key, value, false)
	if !t.mv.Insert(key, node) {
		return ErrInsertFailed
	}
	t.slot.writeSet = append(t.slot.writeSet, KeyRange{Start: key, End: nil})
	return nil
}

func (t *tx) Delete(ctx context.Context, key []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.finished {
		return ErrTxFinished
	}
	node := MV.NewVersionNode(t.arena, t.slot.txnID, t.slot.beginTS, key, nil, true)
	if !t.mv.Insert(key, node) {
		return ErrDeleteFailed
	}
	t.slot.writeSet = append(t.slot.writeSet, KeyRange{Start: key, End: nil})
	return nil
}

func (t *tx) Commit(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.finished {
		return ErrTxFinished
	}
	if !t.sm.Validate(t.slot) {
		t.finalize(SlotAborted)
		if t.manager != nil {
			t.manager.recordAbort()
		}
		return ErrWriteConflict
	}

	commitTS := NextTS()

	for _, kr := range t.slot.writeSet {
		chain := t.mv.GetVersionChain(kr.Start)
		if chain == nil {
			continue
		}
		for node := chain.GetHead(); node != nil; node = node.Next() {
			if node.TxnID() == t.slot.txnID {
				node.Commit(commitTS)
				break
			}
		}
	}

	t.finalize(SlotCommitted)
	if t.manager != nil {
		t.manager.recordCommit()
	}
	return nil
}

func (t *tx) Abort(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.finished {
		// Abort is idempotent: a second call after the first is a no-op
		// and does NOT increment the abort counter or re-release the slot.
		// (The pre-existing code re-released the slot, which was a latent
		// bug; see the iter-05 audit notes.)
		return nil
	}
	t.finalize(SlotAborted)
	if t.manager != nil {
		t.manager.recordAbort()
	}
	return nil
}

// finalize marks the slot's terminal state, releases the slot back to
// the pool, and returns the per-txn arena to the global pool. Must be
// called with t.mu held.
func (t *tx) finalize(status SlotStatus) {
	t.slot.status.Store(int32(status))
	t.sm.ReleaseSlot(t.slot)
	if t.arena != nil {
		MV.PutArena(t.arena)
		t.arena = nil
	}
	t.finished = true
}

// Begin allocates a transaction on the default (global) manager. Preserved
// for backward compatibility with code written before TxnManager existed.
// New code should call Manager.Begin on an explicit manager instance.
func Begin(ctx context.Context) (Tx, error) {
	return defaultManager.Begin(ctx)
}
