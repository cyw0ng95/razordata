package VL

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/cyw0ng95/razordata/internal/TXN/MV"
	"github.com/cyw0ng95/razordata/internal/TXN/SN"
	walwr "github.com/cyw0ng95/razordata/internal/WAL/WR"
)

// WALWriter is the minimal interface a transaction Commit needs
// to persist a record. The concrete *walwr.writer implements it.
type WALWriter interface {
	Append(batch *walwr.WriteBatch) (uint64, error)
	Sync() error
}

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

// CommitPhase tracks the 6-phase commit protocol state (REQ000147).
// Each phase is a one-way progression; the protocol cannot go
// backwards. Reading a phase externally is safe (atomic).
type CommitPhase int32

const (
	// PhaseBegin is the initial state. Reads/writes allowed.
	PhaseBegin CommitPhase = iota
	// PhaseRead is set when the first Get/Iter is called. The
	// read view is fixed for the rest of the transaction.
	PhaseRead
	// PhaseWrite is set on the first Insert/Delete. The write
	// set is captured.
	PhaseWrite
	// PhasePreCommit is entered by Commit() after validation
	// passes. No further writes allowed.
	PhasePreCommit
	// PhaseCommit is entered after the WAL record is durably
	// persisted. Version chains are marked committed.
	PhaseCommit
	// PhasePostCommit is the terminal state. Slot released.
	PhasePostCommit
	// PhaseAborted is the alternative terminal state for the
	// Abort flow.
	PhaseAborted
)

type tx struct {
	sm       *slotManager
	mv       *MV.MV
	manager  *Manager
	slot     *transactionSlot
	readView *SN.ReadView
	// arena lives on t.slot (R16-17..19); the slot manager owns its
	// lifecycle. Reads/writes go through t.slot.arena. The per-txn
	// mutex on t.mu serializes access so concurrent Insert/Delete on
	// the same transaction cannot race on arena.Alloc.
	mu       sync.Mutex
	finished bool
	// wal is the optional WAL writer. When non-nil, Commit emits
	// an RTCommit record via EncodeCommitRecord and calls Sync for
	// durability. REQ000171. nil WAL skips the write (test mode).
	wal WALWriter
	// phase tracks the 6-phase commit protocol progress (REQ000147).
	// Set atomically so observers (debug, metrics) can read it
	// without acquiring t.mu.
	phase atomic.Int32
}

// Phase returns the current commit phase (REQ000147).
func (t *tx) Phase() CommitPhase {
	return CommitPhase(t.phase.Load())
}

// setPhase atomically advances the phase.
func (t *tx) setPhase(p CommitPhase) {
	t.phase.Store(int32(p))
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
	// PhaseRead: first read fixes the read view (REQ000147).
	if t.Phase() == PhaseBegin {
		t.setPhase(PhaseRead)
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
	// PhaseWrite: first write enters the write phase (REQ000147).
	t.setPhase(PhaseWrite)
	node := MV.NewVersionNode(t.slot.arena, t.slot.txnID, t.slot.beginTS, key, value, false)
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
	// PhaseWrite: first write enters the write phase (REQ000147).
	t.setPhase(PhaseWrite)
	node := MV.NewVersionNode(t.slot.arena, t.slot.txnID, t.slot.beginTS, key, nil, true)
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
	// PhasePreCommit: validate before committing (REQ000147).
	t.setPhase(PhasePreCommit)
	if !t.sm.Validate(t.slot) {
		t.setPhase(PhaseAborted)
		t.finalize(SlotAborted)
		if t.manager != nil {
			t.manager.recordAbort()
		}
		return ErrWriteConflict
	}

	commitTS := NextTS()

	// PhaseCommit: mark version chains committed.
	t.setPhase(PhaseCommit)
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

	// Persist WAL record for durability (REQ000171). nil WAL
	// skips the write (test mode, no WAL configured).
	if t.wal != nil {
		keys := make([][]byte, 0, len(t.slot.writeSet))
		for _, kr := range t.slot.writeSet {
			keys = append(keys, kr.Start)
		}
		rec := EncodeCommitRecord(t.slot.txnID, commitTS, keys)
		batch := &walwr.WriteBatch{Recs: []walwr.LogRecord{{Type: walwr.RTCommit, Value: rec}}}
		if _, err := t.wal.Append(batch); err != nil {
			t.setPhase(PhaseAborted)
			return err
		}
		if err := t.wal.Sync(); err != nil {
			t.setPhase(PhaseAborted)
			return err
		}
	}

	// PhasePostCommit: terminal state. Slot released.
	t.setPhase(PhasePostCommit)
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
	// PhaseAborted: terminal state for the Abort flow (REQ000147).
	t.setPhase(PhaseAborted)
	t.finalize(SlotAborted)
	if t.manager != nil {
		t.manager.recordAbort()
	}
	return nil
}

// finalize marks the slot's terminal state and releases the slot back
// to the pool. The slot manager owns the arena lifecycle; ReleaseSlot
// returns the arena to the global pool. Must be called with t.mu held.
func (t *tx) finalize(status SlotStatus) {
	t.slot.status.Store(int32(status))
	t.sm.ReleaseSlot(t.slot)
	t.finished = true
}

// Begin allocates a transaction on the default (global) manager. Preserved
// for backward compatibility with code written before TxnManager existed.
// New code should call Manager.Begin on an explicit manager instance.
func Begin(ctx context.Context) (Tx, error) {
	return defaultManager.Begin(ctx)
}

// WithWAL attaches a WAL writer to the transaction. The Commit
// path will emit an RTCommit record and call Sync. Passing nil
// disables WAL emission (test mode). REQ000171.
func (t *tx) WithWAL(w WALWriter) Tx {
	t.wal = w
	return t
}
