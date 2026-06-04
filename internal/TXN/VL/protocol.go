package VL

import (
	"context"

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
}

func (t *tx) Get(ctx context.Context, key []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
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
	node := MV.NewVersionNode(t.slot.txnID, t.slot.beginTS, key, value, false)
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
	node := MV.NewVersionNode(t.slot.txnID, t.slot.beginTS, key, nil, true)
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
	if !t.sm.Validate(t.slot) {
		return t.Abort(ctx)
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

	t.slot.commitTS = commitTS
	t.slot.status.Store(int32(SlotCommitted))
	t.sm.ReleaseSlot(t.slot)
	if t.manager != nil {
		t.manager.recordCommit()
	}
	return nil
}

func (t *tx) Abort(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	t.slot.status.Store(int32(SlotAborted))
	t.sm.ReleaseSlot(t.slot)
	if t.manager != nil {
		t.manager.recordAbort()
	}
	return nil
}

// Begin allocates a transaction on the default (global) manager. Preserved
// for backward compatibility with code written before TxnManager existed.
// New code should call Manager.Begin on an explicit manager instance.
func Begin(ctx context.Context) (Tx, error) {
	return defaultManager.Begin(ctx)
}
