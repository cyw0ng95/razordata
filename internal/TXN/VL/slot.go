package VL

import (
	"sync"
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

type transactionSlot struct {
	txnID    uint64
	status   SlotStatus
	beginTS  uint64
	commitTS uint64
	writeSet []KeyRange
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
	slot.status = SlotActive
	slot.beginTS = 0
	slot.commitTS = 0
	slot.writeSet = nil

	return slot
}

func (sm *slotManager) ReleaseSlot(slot *transactionSlot) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	idx := slot.index

	slot.status = SlotInactive
	slot.txnID = 0
	slot.beginTS = 0
	slot.commitTS = 0
	slot.writeSet = nil

	if idx >= 0 && idx < MaxConcurrentTXNs {
		sm.freeList = append(sm.freeList, idx)
	}
}

func (sm *slotManager) GetSlotStatus(idx int) SlotStatus {
	return sm.slots[idx].status
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
