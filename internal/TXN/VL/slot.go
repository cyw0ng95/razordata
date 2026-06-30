package VL

import (
	"sync"
	"sync/atomic"

	"github.com/cyw0ng95/razordata/internal/TXN/MV"
)

// FNV-1a 64-bit constants for readSet key hashing (REQ001135).
const (
	fnv1aOffset64 = 14695981039346656037
	fnv1aPrime64  = 1099511628211
)

// fnv1aHash64 computes the FNV-1a 64-bit hash of data.
func fnv1aHash64(data []byte) uint64 {
	var hash uint64 = fnv1aOffset64
	for _, b := range data {
		hash ^= uint64(b)
		hash *= fnv1aPrime64
	}
	return hash
}

type KeyRange struct {
	Start []byte
	End   []byte
}

// ReadEntry records a key read and observed version TS (REQ000307).
type ReadEntry struct {
	Key        []byte
	ObservedTS uint64
}

const MaxConcurrentTXNs = 1024

var readSetPool = sync.Pool{
	New: func() any {
		return make(map[uint64][]byte)
	},
}

type SlotStatus int32

const (
	SlotInactive  SlotStatus = 0
	SlotActive    SlotStatus = 1
	SlotCommitted SlotStatus = 2
	SlotAborted   SlotStatus = 3
)

// slotNode is a single link in the Treiber stack free list (REQ000555).
type slotNode struct {
	idx  int
	next *slotNode
}

// slotStack is a lock-free LIFO for free transaction slots (REQ000555).
type slotStack struct {
	head atomic.Pointer[slotNode]
}

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

// transactionSlot holds per-transaction state for one slot in the pool.
type transactionSlot struct {
	txnID    uint64
	status   atomic.Int32
	beginTS  uint64
	commitTS uint64
	writeSet []KeyRange
	// REQ001135: readSet uses FNV-1a hash keys (uint64) instead of
	// string keys to avoid per-read string() allocation. The value
	// is the original key bytes for collision resolution during
	// validation.
	readSet map[uint64][]byte
	arena   *MV.Arena
	index   int
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

// AllocateSlot pops a free slot, resets its state, and pulls a fresh arena (REQ000555).
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
	slot.readSet = readSetPool.Get().(map[uint64][]byte)
	slot.arena = MV.AcquireArena()

	return slot
}

// ReleaseSlot marks the slot inactive, returns the arena, and pushes back to free stack (REQ000555).
func (sm *slotManager) ReleaseSlot(slot *transactionSlot) {
	idx := slot.index

	slot.status.Store(int32(SlotInactive))
	slot.txnID = 0
	slot.beginTS = 0
	slot.commitTS = 0
	slot.writeSet = nil
	if slot.readSet != nil {
		clear(slot.readSet)
		readSetPool.Put(slot.readSet)
		slot.readSet = nil
	}
	if slot.arena != nil {
		MV.PutArena(slot.arena)
		slot.arena = nil
	}

	if idx >= 0 && idx < MaxConcurrentTXNs {
		sm.freeStack.Push(idx)
		sm.freeCount.Add(1)
	}
}

func (sm *slotManager) SlotStatus(idx int) SlotStatus {
	return SlotStatus(sm.slots[idx].status.Load())
}

func (sm *slotManager) SlotBeginTS(idx int) uint64 {
	return sm.slots[idx].beginTS
}

func (sm *slotManager) SlotCommitTS(idx int) uint64 {
	return sm.slots[idx].commitTS
}

func (sm *slotManager) SlotWriteSet(idx int) []KeyRange {
	return sm.slots[idx].writeSet
}

// NumFreeSlots reports the current free count (REQ000555).
func (sm *slotManager) NumFreeSlots() int {
	return int(sm.freeCount.Load())
}

// NumActiveSlots reports Capacity minus the free count (REQ000555).
func (sm *slotManager) NumActiveSlots() int {
	return MaxConcurrentTXNs - int(sm.freeCount.Load())
}

func (sm *slotManager) Slot(index int) *transactionSlot {
	return &sm.slots[index]
}

// ScanSlots walks every slot.
func (sm *slotManager) ScanSlots(fn func(int, *transactionSlot) bool) {
	for i := 0; i < MaxConcurrentTXNs; i++ {
		slot := &sm.slots[i]
		if !fn(i, slot) {
			return
		}
	}
}
