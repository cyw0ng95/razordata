package VL

import (
	"bytes"
	"context"
	"sync"
	"sync/atomic"

	EC "github.com/cyw0ng95/razordata/internal/LOG/EC"
	"github.com/cyw0ng95/razordata/internal/TXN/MV"
	"github.com/cyw0ng95/razordata/internal/TXN/SN"
	walwr "github.com/cyw0ng95/razordata/internal/WAL/WR"
)

// WALWriter is the minimal interface for WAL persistence.
type WALWriter interface {
	Append(batch *walwr.WriteBatch) (uint64, error)
	Sync() error
}

var globalSlotManager = newSlotManager()
var globalMV = MV.NewMV()

var defaultManager = NewManagerShared(globalSlotManager, globalMV)

func init() {
	// Disable lock table for global manager to avoid test interference.
	defaultManager.lt = nil
}

type Tx interface {
	Get(ctx context.Context, key []byte) ([]byte, error)
	Insert(ctx context.Context, key, value []byte) error
	Delete(ctx context.Context, key []byte) error
	Commit(ctx context.Context) error
	Abort(ctx context.Context) error
	// Savepoint creates a named savepoint for partial rollback (REQ000995).
	Savepoint(name string) error
	// RollbackTo rolls back to a named savepoint, undoing writes and reads
	// made after the savepoint (REQ000995).
	RollbackTo(name string) error
}

// CommitPhase tracks the 6-phase commit protocol state (REQ000147).
type CommitPhase int32

const (
	PhaseBegin CommitPhase = iota
	PhaseRead
	PhaseWrite
	PhasePreCommit
	PhaseCommit
	PhasePostCommit
	PhaseAborted
)

type tx struct {
	sm       *slotManager
	mv       *MV.MV
	manager  *Manager
	slot     *transactionSlot
	readView *SN.ReadView
	mu       sync.Mutex
	finished bool
	wal      WALWriter
	phase    atomic.Int32 // REQ000147: 6-phase commit protocol
	// REQ000995: savepoint stack for partial rollback.
	savepoints map[string]*savepoint
}

// savepoint holds a snapshot of the transaction state at a point in time.
type savepoint struct {
	writeSetLen int
	readSet     map[uint64][]byte
}

func (t *tx) Phase() CommitPhase {
	return CommitPhase(t.phase.Load())
}

func (t *tx) setPhase(p CommitPhase) {
	t.phase.Store(int32(p))
}

func (t *tx) Get(ctx context.Context, key []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.finished {
		return nil, ErrTxFinished
	}
	if t.Phase() == PhaseBegin {
		t.setPhase(PhaseRead)
	}
	// REQ000994: acquire shared lock on read.
	if t.manager != nil && t.manager.lt != nil {
		if err := t.manager.lt.Lock(t.slot.txnID, key, LockModeShared); err != nil {
			return nil, err
		}
	}
	chain := t.mv.VersionChain(key)
	if chain != nil {
		for node := chain.Head(); node != nil; node = node.Next() {
			if node.TxnID() == t.slot.txnID && node.BeginTS() == t.slot.beginTS {
				// REQ000995: skip own writes that were rolled back (not in writeSet).
				inWriteSet := false
				for _, kr := range t.slot.writeSet {
					if bytes.Equal(kr.Start, key) {
						inWriteSet = true
						break
					}
				}
				if !inWriteSet {
					continue
				}
				t.trackRead(key, node.BeginTS())
				if node.Deleted() {
					return nil, nil
				}
				return node.Value(), nil
			}
		}
	}

	node := t.mv.FindVisible(key, t.slot.beginTS)
	if node == nil {
		t.trackRead(key, 0)
		return nil, nil
	}
	t.trackRead(key, node.BeginTS())
	if node.Deleted() {
		return nil, nil
	}
	return node.Value(), nil
}

func (t *tx) trackRead(key []byte, observedTS uint64) {
	// REQ001135: store key bytes under FNV-1a hash to avoid
	// per-read string() allocation. On hash collision, the
	// existing entry is overwritten (conservative: we may
	// miss a conflict, but never false-positive).
	h := fnv1aHash64(key)
	t.slot.readSet[h] = key
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
	t.setPhase(PhaseWrite)
	// REQ000994: acquire exclusive lock on write.
	if t.manager != nil && t.manager.lt != nil {
		if err := t.manager.lt.Lock(t.slot.txnID, key, LockModeExclusive); err != nil {
			return err
		}
	}
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
	t.setPhase(PhaseWrite)
	// REQ000994: acquire exclusive lock on write.
	if t.manager != nil && t.manager.lt != nil {
		if err := t.manager.lt.Lock(t.slot.txnID, key, LockModeExclusive); err != nil {
			return err
		}
	}
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
	EC.BUG_ON(t.slot == nil, "tx.Commit: nil slot")
	EC.BUG_ON(t.slot.status.Load() != int32(SlotActive), "tx.Commit: commit protocol state machine violation — status %d != SlotActive", t.slot.status.Load())
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
	EC.BUG_ON(commitTS <= t.slot.beginTS, "tx.Commit: commit timestamp %d regression <= beginTS %d", commitTS, t.slot.beginTS)

	t.setPhase(PhaseCommit) // REQ000573: WAL write before chain commit
	if t.wal != nil {
		keys := make([][]byte, 0, len(t.slot.writeSet))
		for _, kr := range t.slot.writeSet {
			keys = append(keys, kr.Start)
		}
		rec := EncodeCommitRecord(t.slot.txnID, commitTS, keys)
		batch := &walwr.WriteBatch{Recs: []walwr.LogRecord{{Type: walwr.RTCommit, Value: rec}}}
		if _, err := t.wal.Append(batch); err != nil {
			t.setPhase(PhaseAborted)
			// REQ000994: release locks BEFORE finalize (which clears txnID).
			if t.manager != nil && t.manager.lt != nil {
				t.manager.lt.Unlock(t.slot.txnID)
			}
			t.finalize(SlotAborted)
			if t.manager != nil {
				t.manager.recordAbort()
			}
			return err
		}
		if err := t.wal.Sync(); err != nil {
			t.setPhase(PhaseAborted)
			// REQ000994: release locks BEFORE finalize (which clears txnID).
			if t.manager != nil && t.manager.lt != nil {
				t.manager.lt.Unlock(t.slot.txnID)
			}
			t.finalize(SlotAborted)
			if t.manager != nil {
				t.manager.recordAbort()
			}
			return err
		}
	}

	for _, kr := range t.slot.writeSet {
		chain := t.mv.VersionChain(kr.Start)
		if chain == nil {
			continue
		}
		for node := chain.Head(); node != nil; node = node.Next() {
			if node.TxnID() == t.slot.txnID {
				node.Commit(commitTS)
				break
			}
		}
	}

	t.setPhase(PhasePostCommit)
	// REQ000994: release locks BEFORE finalize (which clears txnID).
	if t.manager != nil && t.manager.lt != nil {
		t.manager.lt.Unlock(t.slot.txnID)
	}
	t.finalize(SlotCommitted)
	if t.manager != nil {
		// REQ000995: clear savepoints on commit.
		t.savepoints = nil
		t.manager.recordCommit(commitTS)
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
		return nil
	}
	t.setPhase(PhasePreCommit) // REQ000617: persist RTRollback before marking aborted
	// REQ000994: release locks before finalize (which clears txnID).
	if t.manager != nil && t.manager.lt != nil {
		t.manager.lt.Unlock(t.slot.txnID)
	}
	if t.wal != nil {
		batch := &walwr.WriteBatch{
			TxnID: t.slot.txnID,
			Recs:  []walwr.LogRecord{{Type: walwr.RTRollback}},
		}
		if _, err := t.wal.Append(batch); err != nil {
			// Locks already released above.
			t.setPhase(PhaseAborted)
			return err
		}
		if err := t.wal.Sync(); err != nil {
			// Locks already released above.
			t.setPhase(PhaseAborted)
			return err
		}
	}
	t.setPhase(PhaseAborted)
	t.finalize(SlotAborted)
	if t.manager != nil {
		// REQ000995: clear savepoints on abort.
		t.savepoints = nil
		t.manager.recordAbort()
	}
	return nil
}

// Savepoint creates a named savepoint for partial rollback (REQ000995).
func (t *tx) Savepoint(name string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.finished {
		return ErrTxFinished
	}
	if t.savepoints == nil {
		return ErrSavepointNotSupported
	}
	// Snapshot write-set length and readSet.
	readSetCopy := make(map[uint64][]byte, len(t.slot.readSet))
	for k, v := range t.slot.readSet {
		readSetCopy[k] = v
	}
	t.savepoints[name] = &savepoint{
		writeSetLen: len(t.slot.writeSet),
		readSet:     readSetCopy,
	}
	return nil
}

// RollbackTo rolls back to a named savepoint (REQ000995).
// It truncates the write-set and read-set to the saved state, and
// reverts the MVCC chain for writes made after the savepoint.
func (t *tx) RollbackTo(name string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.finished {
		return ErrTxFinished
	}
	sp, ok := t.savepoints[name]
	if !ok {
		return ErrSavepointNotFound
	}
	if len(t.slot.writeSet) < sp.writeSetLen {
		return ErrRollbackPastSavepoint
	}

	// Truncate write-set to saved length.
	removed := t.slot.writeSet[sp.writeSetLen:]
	t.slot.writeSet = t.slot.writeSet[:sp.writeSetLen]

	// Revert MVCC chain: mark version nodes for removed writes as uncommitted.
	for _, kr := range removed {
		chain := t.mv.VersionChain(kr.Start)
		if chain == nil {
			continue
		}
		for node := chain.Head(); node != nil; node = node.Next() {
			if node.TxnID() == t.slot.txnID && node.BeginTS() == t.slot.beginTS {
				node.Revert()
				break
			}
		}
	}

	// Restore read-set to saved state.
	if t.slot.readSet != nil {
		clear(t.slot.readSet)
	}
	for k, v := range sp.readSet {
		t.slot.readSet[k] = v
	}

	return nil
}

func (t *tx) finalize(status SlotStatus) {
	t.slot.status.Store(int32(status))
	t.sm.ReleaseSlot(t.slot)
	t.finished = true
}

// Begin allocates a transaction on the default (global) manager.
func Begin(ctx context.Context) (Tx, error) {
	return defaultManager.Begin(ctx)
}

// WithWAL attaches a WAL writer to the transaction (REQ000171).
func (t *tx) WithWAL(w WALWriter) Tx {
	t.wal = w
	return t
}
