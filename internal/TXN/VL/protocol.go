package VL

import (
	"context"
	"sync"
	"sync/atomic"

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

type Tx interface {
	Get(ctx context.Context, key []byte) ([]byte, error)
	Insert(ctx context.Context, key, value []byte) error
	Delete(ctx context.Context, key []byte) error
	Commit(ctx context.Context) error
	Abort(ctx context.Context) error
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
	chain := t.mv.VersionChain(key)
	if chain != nil {
		for node := chain.Head(); node != nil; node = node.Next() {
			if node.TxnID() == t.slot.txnID && node.BeginTS() == t.slot.beginTS {
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
			t.finalize(SlotAborted)
			if t.manager != nil {
				t.manager.recordAbort()
			}
			return err
		}
		if err := t.wal.Sync(); err != nil {
			t.setPhase(PhaseAborted)
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
		return nil
	}
	t.setPhase(PhasePreCommit) // REQ000617: persist RTRollback before marking aborted
	if t.wal != nil {
		batch := &walwr.WriteBatch{
			TxnID: t.slot.txnID,
			Recs:  []walwr.LogRecord{{Type: walwr.RTRollback}},
		}
		if _, err := t.wal.Append(batch); err != nil {
			t.setPhase(PhaseAborted)
			return err
		}
		if err := t.wal.Sync(); err != nil {
			t.setPhase(PhaseAborted)
			return err
		}
	}
	t.setPhase(PhaseAborted)
	t.finalize(SlotAborted)
	if t.manager != nil {
		t.manager.recordAbort()
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
