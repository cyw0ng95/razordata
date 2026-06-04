package VL

import (
	"context"
	"sync/atomic"

	"github.com/cyw0ng95/razordata/internal/TXN/MV"
	"github.com/cyw0ng95/razordata/internal/TXN/SN"
)

// TxnStats is a point-in-time snapshot of a TxnManager's resource usage.
// All fields are advisory; values can change immediately after being read.
type TxnStats struct {
	Capacity     int   `json:"capacity"`
	Active       int   `json:"active"`
	Committed    int64 `json:"committed"`
	Aborted      int64 `json:"aborted"`
	FreeSlots    int   `json:"free_slots"`
	CurrentTS    int64 `json:"current_ts"`
	CurrentEpoch int64 `json:"current_epoch"`
}

// TxnManager is the entry point for transaction creation and observability.
// It owns a slot pool and an MV instance, and is goroutine-safe.
type TxnManager interface {
	Begin(ctx context.Context) (Tx, error)
	Stats() TxnStats
	Close() error
}

// Manager is the default TxnManager implementation. It composes a slot
// pool, an MV instance, and a timestamp source.
//
// A Manager is goroutine-safe; all methods may be called concurrently.
type Manager struct {
	sm        *slotManager
	mv        *MV.MV
	committed atomic.Int64
	aborted   atomic.Int64
	closed    atomic.Bool
}

// NewManager constructs a Manager with its own private slot pool and MV
// instance. Transactions created by this manager do not share state with
// the package-level global state, which makes this constructor ideal for
// tests and for embedding the manager in larger subsystems (e.g., SYS/AP).
func NewManager() *Manager {
	return &Manager{
		sm: newSlotManager(),
		mv: MV.NewMV(),
	}
}

// NewManagerShared constructs a Manager that wraps an existing slot pool
// and MV. This is the form used internally by the package-level Begin
// function to preserve backward compatibility with code that relied on
// the global slot manager.
func NewManagerShared(sm *slotManager, mv *MV.MV) *Manager {
	return &Manager{
		sm: sm,
		mv: mv,
	}
}

// Begin allocates a transaction slot from this manager's pool, assigns a
// begin timestamp, and returns a Tx bound to this manager. The returned
// tx's Commit/Abort release the slot back to *this* manager's pool, not
// the global pool.
//
// Returns ErrNoSlotsAvailable if the pool is exhausted.
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

	arena := MV.GetArena()
	readView := SN.NewReadView(m.mv, slot.beginTS)

	return &tx{
		sm:       m.sm,
		mv:       m.mv,
		manager:  m,
		slot:     slot,
		readView: readView,
		arena:    arena,
	}, nil
}

// Stats returns a snapshot of the manager's current state.
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

// Close marks the manager as closed. After Close, Begin returns
// ErrManagerClosed. Outstanding transactions are not interrupted; they
// continue to operate against the manager's private state.
//
// Close is idempotent and safe to call from multiple goroutines.
func (m *Manager) Close() error {
	m.closed.Store(true)
	return nil
}

// recordCommit is called by tx.Commit on success. Atomic accounting only.
func (m *Manager) recordCommit() {
	m.committed.Add(1)
}

// recordAbort is called by tx.Abort. Atomic accounting only.
func (m *Manager) recordAbort() {
	m.aborted.Add(1)
}
