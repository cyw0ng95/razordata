package VL

import (
	"testing"
	"time"
	"unsafe"
)

func TestStartGC(t *testing.T) {
	StartGC()
	defer StopGC()
}

func TestStopGC(t *testing.T) {
	StartGC()
	StopGC()
}

func TestCurrentEpoch(t *testing.T) {
	StartGC()
	defer StopGC()

	epoch1 := CurrentEpoch()
	// Ticker interval is 100 ms; sleep just past one tick so at
	// least one increment is observable.
	time.Sleep(110 * time.Millisecond)
	epoch2 := CurrentEpoch()

	if epoch2 <= epoch1 {
		t.Errorf("expected epoch2 > epoch1, got %d >= %d", epoch2, epoch1)
	}
}

func TestAdvanceEpoch(t *testing.T) {
	StartGC()
	defer StopGC()

	epoch1 := CurrentEpoch()
	AdvanceEpoch()
	epoch2 := CurrentEpoch()

	if epoch2 != epoch1+1 {
		t.Errorf("expected epoch2 = epoch1+1, got %d vs %d", epoch2, epoch1+1)
	}
}

func TestReclaimVersionNodesEmpty(t *testing.T) {
	StartGC()
	defer StopGC()

	ReclaimVersionNodes(nil)
	ReclaimVersionNodes([]unsafe.Pointer{})
}

func TestRegisterGCThread(t *testing.T) {
	StartGC()
	defer StopGC()

	RegisterGCThread(123)
	UnregisterGCThread(123)
}

func TestSlot(t *testing.T) {
	sm := newSlotManager()
	slot := sm.AllocateSlot()

	got := sm.Slot(slot.index)
	if got != slot {
		t.Error("expected same slot")
	}
}

func TestScanSlots(t *testing.T) {
	sm := newSlotManager()
	sm.AllocateSlot()
	sm.AllocateSlot()

	count := 0
	sm.ScanSlots(func(i int, slot *transactionSlot) bool {
		if slot.status.Load() == int32(SlotActive) {
			count++
		}
		return true
	})

	if count != 2 {
		t.Errorf("expected 2 active slots, got %d", count)
	}
}
