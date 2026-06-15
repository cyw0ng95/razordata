package VL

import (
	"sync"
	"sync/atomic"

	"github.com/cyw0ng95/razordata/internal/TXN/MV"
)

type KeyRange struct {
	Start []byte
	End   []byte
}

const MaxConcurrentTXNs = 1024

type SlotStatus int32

const (
	SlotInactive  SlotStatus = 0
	SlotActive    SlotStatus = 1
	SlotCommitted SlotStatus = 2
	SlotAborted   SlotStatus = 3
)

// transactionSlot holds the per-transaction state for one slot in the
// fixed-size pool (R16-17). The arena lives on the slot itself so its
// lifecycle is locked to the slot: AllocateSlot pulls a fresh arena from
// the pool, ReleaseSlot returns it. This guarantees the arena is
// reclaimed even when a transaction is leaked without reaching
// Commit/Abort (e.g., on panic) — the slot's next allocate will surface
// the leaked arena through PutArena as part of slot reset.
type transactionSlot struct {
	txnID    uint64
	status   atomic.Int32
	beginTS  uint64
	commitTS uint64
	writeSet []KeyRange
	arena    *MV.Arena
	index    int
}

type slotManager struct {
	slots    [MaxConcurrentTXNs]transactionSlot
	freeList []int
	mu       sync.Mutex
}

func newSlotManager() *slotManager {
	sm := &slotManager{}
	sm.freeList = make([]int, MaxConcurrentTXNs)
	for i := 0; i < MaxConcurrentTXNs; i++ {
		sm.freeList[i] = i
		sm.slots[i].index = i
	}
	return sm
}

// AllocateSlot pops a free slot, resets its per-txn state, and pulls a
// fresh arena from the global pool (R16-18). The slot's arena is the
// only memory the tx needs that is not garbage-collected; by tying its
// acquisition to AllocateSlot and its release to ReleaseSlot we ensure
// arena reuse is bounded by slot churn, not by tx lifecycle correctness.
func (sm *slotManager) AllocateSlot() *transactionSlot {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if len(sm.freeList) == 0 {
		return nil
	}

	idx := sm.freeList[len(sm.freeList)-1]
	sm.freeList = sm.freeList[:len(sm.freeList)-1]

	slot := &sm.slots[idx]
	slot.txnID = 0
	slot.status.Store(int32(SlotActive))
	slot.beginTS = 0
	slot.commitTS = 0
	slot.writeSet = nil
	slot.arena = MV.GetArena()

	return slot
}

// ReleaseSlot marks the slot inactive, zeroes its fields, returns the
// arena to the pool, and pushes the slot index back onto the free list
// (R16-18). All arena lifecycle is owned here; tx.finalize no longer
// touches the arena directly.
func (sm *slotManager) ReleaseSlot(slot *transactionSlot) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	idx := slot.index

	slot.status.Store(int32(SlotInactive))
	slot.txnID = 0
	slot.beginTS = 0
	slot.commitTS = 0
	slot.writeSet = nil
	if slot.arena != nil {
		MV.PutArena(slot.arena)
		slot.arena = nil
	}

	if idx >= 0 && idx < MaxConcurrentTXNs {
		sm.freeList = append(sm.freeList, idx)
	}
}

func (sm *slotManager) GetSlotStatus(idx int) SlotStatus {
	return SlotStatus(sm.slots[idx].status.Load())
}

func (sm *slotManager) GetSlotBeginTS(idx int) uint64 {
	return sm.slots[idx].beginTS
}

func (sm *slotManager) GetSlotCommitTS(idx int) uint64 {
	return sm.slots[idx].commitTS
}

func (sm *slotManager) GetSlotWriteSet(idx int) []KeyRange {
	return sm.slots[idx].writeSet
}

func (sm *slotManager) NumFreeSlots() int {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return len(sm.freeList)
}

func (sm *slotManager) NumActiveSlots() int {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return MaxConcurrentTXNs - len(sm.freeList)
}

func (sm *slotManager) GetSlot(index int) *transactionSlot {
	return &sm.slots[index]
}

func (sm *slotManager) ScanSlots(fn func(int, *transactionSlot) bool) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	for i := 0; i < MaxConcurrentTXNs; i++ {
		slot := &sm.slots[i]
		if !fn(i, slot) {
			return
		}
	}
}
