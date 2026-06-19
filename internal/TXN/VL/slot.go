package VL

import (
	"sync/atomic"

	"github.com/cyw0ng95/razordata/internal/TXN/MV"
)

type KeyRange struct {
	Start []byte
	End   []byte
}

// ReadEntry records a key and the beginTS of the version we observed
// during a Get. The OCC validation uses this to detect read-write
// conflicts with concurrently committed transactions. REQ000307.
type ReadEntry struct {
	Key        []byte
	ObservedTS uint64
}

const MaxConcurrentTXNs = 1024

type SlotStatus int32

const (
	SlotInactive  SlotStatus = 0
	SlotActive    SlotStatus = 1
	SlotCommitted SlotStatus = 2
	SlotAborted   SlotStatus = 3
)

// slotNode is a single link in the Treiber stack used for the slot free
// list. REQ000555: lock-free recycling via CAS.
type slotNode struct {
	idx  int
	next *slotNode
}

// slotStack is a Treiber stack — a lock-free LIFO used to track free
// transaction slots. Push and Pop are CAS-based and wait-free under
// bounded contention. REQ000555.
type slotStack struct {
	head atomic.Pointer[slotNode]
}

// Push prepends idx to the stack in LIFO order.
func (s *slotStack) Push(idx int) {
	n := &slotNode{idx: idx}
	for {
		old := s.head.Load()
		n.next = old
		if s.head.CompareAndSwap(old, n) {
			return
		}
	}
}

// Pop returns the most recently pushed index, or -1 if empty.
func (s *slotStack) Pop() int {
	for {
		n := s.head.Load()
		if n == nil {
			return -1
		}
		if s.head.CompareAndSwap(n, n.next) {
			return n.idx
		}
	}
}

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
	readSet  []ReadEntry // REQ000307: keys read + observed version beginTS
	arena    *MV.Arena
	index    int
}

type slotManager struct {
	slots     [MaxConcurrentTXNs]transactionSlot
	freeStack slotStack
	freeCount atomic.Int64
}

func newSlotManager() *slotManager {
	sm := &slotManager{}
	for i := 0; i < MaxConcurrentTXNs; i++ {
		sm.slots[i].index = i
		sm.freeStack.Push(i)
	}
	sm.freeCount.Store(int64(MaxConcurrentTXNs))
	return sm
}

// AllocateSlot pops a free slot, resets its per-txn state, and pulls a
// fresh arena from the global pool (R16-18). The slot's arena is the
// only memory the tx needs that is not garbage-collected; by tying its
// acquisition to AllocateSlot and its release to ReleaseSlot we ensure
// arena reuse is bounded by slot churn, not by tx lifecycle correctness.
// REQ000555: the free-list is now a lock-free Treiber stack, so burst
// Begin no longer serializes on a mutex.
func (sm *slotManager) AllocateSlot() *transactionSlot {
	idx := sm.freeStack.Pop()
	if idx < 0 {
		return nil
	}
	sm.freeCount.Add(-1)

	slot := &sm.slots[idx]
	slot.txnID = 0
	slot.status.Store(int32(SlotActive))
	slot.beginTS = 0
	slot.commitTS = 0
	slot.writeSet = nil
	slot.readSet = nil
	slot.arena = MV.GetArena()

	return slot
}

// ReleaseSlot marks the slot inactive, zeroes its fields, returns the
// arena to the pool, and pushes the slot index back onto the free stack
// (R16-18). All arena lifecycle is owned here; tx.finalize no longer
// touches the arena directly. REQ000555: push is lock-free.
func (sm *slotManager) ReleaseSlot(slot *transactionSlot) {
	idx := slot.index

	slot.status.Store(int32(SlotInactive))
	slot.txnID = 0
	slot.beginTS = 0
	slot.commitTS = 0
	slot.writeSet = nil
	slot.readSet = nil
	if slot.arena != nil {
		MV.PutArena(slot.arena)
		slot.arena = nil
	}

	if idx >= 0 && idx < MaxConcurrentTXNs {
		sm.freeStack.Push(idx)
		sm.freeCount.Add(1)
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

// NumFreeSlots reports the current free count from the lock-free
// counter. The value is eventually consistent with concurrent
// Allocate/Release, which is sufficient for observability callers
// (Stats, cond, tests). REQ000555.
func (sm *slotManager) NumFreeSlots() int {
	return int(sm.freeCount.Load())
}

// NumActiveSlots reports Capacity minus the free count. See
// NumFreeSlots for consistency caveats. REQ000555.
func (sm *slotManager) NumActiveSlots() int {
	return MaxConcurrentTXNs - int(sm.freeCount.Load())
}

func (sm *slotManager) GetSlot(index int) *transactionSlot {
	return &sm.slots[index]
}

// ScanSlots walks every slot. With the lock-free stack this no longer
// needs the free-list mutex; the slot array itself is never mutated in
// place after construction.
func (sm *slotManager) ScanSlots(fn func(int, *transactionSlot) bool) {
	for i := 0; i < MaxConcurrentTXNs; i++ {
		slot := &sm.slots[i]
		if !fn(i, slot) {
			return
		}
	}
}
