package VL

import (
	"context"

	"github.com/cyw0ng95/razordata/internal/TXN/MV"
	"github.com/cyw0ng95/razordata/internal/TXN/SN"
)

var globalSlotManager = newSlotManager()
var globalMV = MV.NewMV()

type Tx interface {
	Get(ctx context.Context, key []byte) ([]byte, error)
	Insert(ctx context.Context, key, value []byte) error
	Delete(ctx context.Context, key []byte) error
	Commit(ctx context.Context) error
	Abort(ctx context.Context) error
}

type tx struct {
	slot     *transactionSlot
	readView *SN.ReadView
	arena    *MV.Arena
}

func (t *tx) Get(ctx context.Context, key []byte) ([]byte, error) {
	chain := globalMV.GetVersionChain(key)
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

	node := globalMV.FindVisible(key, t.slot.beginTS)
	if node == nil {
		return nil, nil
	}
	if node.Deleted() {
		return nil, nil
	}
	return node.Value(), nil
}

func (t *tx) Insert(ctx context.Context, key, value []byte) error {
	node := MV.NewVersionNode(t.slot.txnID, t.slot.beginTS, key, value, false)
	if !globalMV.Insert(key, node) {
		return ErrInsertFailed
	}
	t.slot.writeSet = append(t.slot.writeSet, KeyRange{Start: key, End: nil})
	return nil
}

func (t *tx) Delete(ctx context.Context, key []byte) error {
	node := MV.NewVersionNode(t.slot.txnID, t.slot.beginTS, key, nil, true)
	if !globalMV.Insert(key, node) {
		return ErrDeleteFailed
	}
	t.slot.writeSet = append(t.slot.writeSet, KeyRange{Start: key, End: nil})
	return nil
}

func (t *tx) Commit(ctx context.Context) error {
	if !globalSlotManager.Validate(t.slot) {
		return t.Abort(ctx)
	}

	commitTS := NextTS()

	for _, kr := range t.slot.writeSet {
		chain := globalMV.GetVersionChain(kr.Start)
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
	globalSlotManager.ReleaseSlot(t.slot)
	return nil
}

func (t *tx) Abort(ctx context.Context) error {
	t.slot.status.Store(int32(SlotAborted))
	globalSlotManager.ReleaseSlot(t.slot)
	return nil
}

func Begin(ctx context.Context) (Tx, error) {
	slot := globalSlotManager.AllocateSlot()
	if slot == nil {
		return nil, ErrNoSlotsAvailable
	}

	slot.txnID = NextTS()
	slot.beginTS = slot.txnID
	slot.status.Store(int32(SlotActive))

	arena := MV.GetArena()
	readView := SN.NewReadView(globalMV, slot.beginTS)

	return &tx{
		slot:     slot,
		readView: readView,
		arena:    arena,
	}, nil
}
