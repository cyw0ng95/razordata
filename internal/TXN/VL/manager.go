package VL

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/cyw0ng95/razordata/internal/TXN/MV"
	"github.com/cyw0ng95/razordata/internal/TXN/SN"
)

// TxnStats is a point-in-time snapshot of a TxnManager's resource usage.
type TxnStats struct {
	Capacity     int   `json:"capacity"`
	Active       int   `json:"active"`
	Committed    int64 `json:"committed"`
	Aborted      int64 `json:"aborted"`
	FreeSlots    int   `json:"free_slots"`
	CurrentTS    int64 `json:"current_ts"`
	CurrentEpoch int64 `json:"current_epoch"`
}

// TxnManager is the entry point for transaction creation.
type TxnManager interface {
	Begin(ctx context.Context) (Tx, error)
	Stats() TxnStats
	Close() error
}

// Manager is the default TxnManager implementation.
type Manager struct {
	sm        *slotManager
	mv        *MV.MV
	lt        *LockTable
	committed atomic.Int64
	aborted   atomic.Int64
	closed    atomic.Bool
}

// NewManager constructs a Manager with its own private slot pool and MV.
func NewManager() *Manager {
	return &Manager{
		sm: newSlotManager(),
		mv: MV.NewMV(),
		lt: NewLockTable(5 * time.Second),
	}
}

// NewManagerShared constructs a Manager wrapping an existing slot pool and MV.
func NewManagerShared(sm *slotManager, mv *MV.MV) *Manager {
	return &Manager{
		sm: sm,
		mv: mv,
		lt: NewLockTable(5 * time.Second),
	}
}

// Begin allocates a transaction slot and returns a Tx.
func (m *Manager) Begin(ctx context.Context) (Tx, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if m.closed.Load() {
		return nil, ErrManagerClosed
	}

	slot := m.sm.AllocateSlot()
	if slot == nil {
		return nil, ErrNoSlotsAvailable
	}

	ts := NextTS()
	slot.txnID = ts
	slot.beginTS = ts
	slot.status.Store(int32(SlotActive))

	readView := SN.NewReadView(m.mv, slot.beginTS)

	return &tx{
		sm:       m.sm,
		mv:       m.mv,
		manager:  m,
		slot:     slot,
		readView: readView,
	}, nil
}

func (m *Manager) Stats() TxnStats {
	return TxnStats{
		Capacity:     MaxConcurrentTXNs,
		Active:       m.sm.NumActiveSlots(),
		FreeSlots:    m.sm.NumFreeSlots(),
		Committed:    m.committed.Load(),
		Aborted:      m.aborted.Load(),
		CurrentTS:    int64(GetCurrentTS()),
		CurrentEpoch: CurrentEpoch(),
	}
}

// Close marks the manager as closed.
func (m *Manager) Close() error {
	m.closed.Store(true)
	return nil
}

func (m *Manager) recordCommit() {
	m.committed.Add(1)
}

func (m *Manager) recordAbort() {
	m.aborted.Add(1)
}
