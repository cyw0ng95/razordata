package VL

import (
	"sync"
	"testing"
)

func TestKeyRangeOverlap(t *testing.T) {
	cases := []struct {
		a, b     KeyRange
		expected bool
	}{
		{KeyRange{Start: []byte("a"), End: []byte("c")}, KeyRange{Start: []byte("b"), End: []byte("d")}, true},
		{KeyRange{Start: []byte("a"), End: []byte("b")}, KeyRange{Start: []byte("c"), End: []byte("d")}, false},
		{KeyRange{Start: nil, End: nil}, KeyRange{Start: []byte("b"), End: nil}, true},
		{KeyRange{Start: []byte("a"), End: []byte("c")}, KeyRange{Start: []byte("x"), End: []byte("z")}, false},
	}

	for _, c := range cases {
		result := RangesOverlap(c.a, c.b)
		if result != c.expected {
			t.Errorf("RangesOverlap(%v, %v) = %v, want %v", c.a, c.b, result, c.expected)
		}
	}
}

func TestKeyRangesOverlap(t *testing.T) {
	a := []KeyRange{{Start: []byte("a"), End: []byte("c")}}
	b := []KeyRange{{Start: []byte("b"), End: []byte("d")}}

	if !KeyRangesOverlap(a, b) {
		t.Error("expected overlap")
	}

	b2 := []KeyRange{{Start: []byte("x"), End: []byte("z")}}
	if KeyRangesOverlap(a, b2) {
		t.Error("expected no overlap")
	}
}

func TestNewSlotManager(t *testing.T) {
	sm := newSlotManager()

	if sm.NumFreeSlots() != MaxConcurrentTXNs {
		t.Errorf("expected %d free slots, got %d", MaxConcurrentTXNs, sm.NumFreeSlots())
	}
}

func TestAllocateSlot(t *testing.T) {
	sm := newSlotManager()

	slot := sm.AllocateSlot()
	if slot == nil {
		t.Fatal("expected slot, got nil")
	}

	if sm.NumFreeSlots() != MaxConcurrentTXNs-1 {
		t.Errorf("expected %d free slots, got %d", MaxConcurrentTXNs-1, sm.NumFreeSlots())
	}

	if slot.status.Load() != int32(SlotActive) {
		t.Errorf("expected status %d, got %d", SlotActive, slot.status.Load())
	}
}

func TestAllocateAllSlots(t *testing.T) {
	sm := newSlotManager()

	for i := 0; i < MaxConcurrentTXNs; i++ {
		slot := sm.AllocateSlot()
		if slot == nil {
			t.Fatalf("expected slot %d, got nil", i)
		}
	}

	if sm.NumFreeSlots() != 0 {
		t.Errorf("expected 0 free slots, got %d", sm.NumFreeSlots())
	}

	slot := sm.AllocateSlot()
	if slot != nil {
		t.Error("expected nil when no slots available")
	}
}

func TestReleaseSlot(t *testing.T) {
	sm := newSlotManager()

	slot := sm.AllocateSlot()
	idx := slot.index

	sm.ReleaseSlot(slot)

	if sm.NumFreeSlots() != MaxConcurrentTXNs {
		t.Errorf("expected %d free slots, got %d", MaxConcurrentTXNs, sm.NumFreeSlots())
	}

	slot2 := sm.AllocateSlot()
	if slot2.index != idx {
		t.Error("expected same slot to be reused")
	}
}

func TestSlotReuse(t *testing.T) {
	sm := newSlotManager()

	slot1 := sm.AllocateSlot()
	slot2 := sm.AllocateSlot()

	sm.ReleaseSlot(slot1)
	sm.ReleaseSlot(slot2)

	slot3 := sm.AllocateSlot()
	slot4 := sm.AllocateSlot()

	if slot3.index == slot4.index {
		t.Error("released slots should be reused")
	}
}

func TestConcurrentAllocateRelease(t *testing.T) {
	sm := newSlotManager()

	var wg sync.WaitGroup
	goroutines := 100
	iterations := 10

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				slot := sm.AllocateSlot()
				if slot != nil {
					sm.ReleaseSlot(slot)
				}
			}
		}()
	}

	wg.Wait()

	if sm.NumFreeSlots() != MaxConcurrentTXNs {
		t.Errorf("expected %d free slots after concurrent ops, got %d", MaxConcurrentTXNs, sm.NumFreeSlots())
	}
}

func TestSlotStatus(t *testing.T) {
	sm := newSlotManager()

	slot := sm.AllocateSlot()
	idx := slot.index

	if sm.GetSlotStatus(idx) != SlotActive {
		t.Errorf("expected status %d, got %d", SlotActive, sm.GetSlotStatus(idx))
	}

	slot.status.Store(int32(SlotCommitted))
	if sm.GetSlotStatus(idx) != SlotCommitted {
		t.Errorf("expected status %d, got %d", SlotCommitted, sm.GetSlotStatus(idx))
	}

	slot.status.Store(int32(SlotAborted))
	if sm.GetSlotStatus(idx) != SlotAborted {
		t.Errorf("expected status %d, got %d", SlotAborted, sm.GetSlotStatus(idx))
	}
}

func TestSlotWriteSet(t *testing.T) {
	sm := newSlotManager()

	slot := sm.AllocateSlot()
	idx := slot.index

	slot.writeSet = []KeyRange{
		{Start: []byte("a"), End: []byte("c")},
		{Start: []byte("x"), End: []byte("z")},
	}

	ws := sm.GetSlotWriteSet(idx)
	if len(ws) != 2 {
		t.Errorf("expected 2 key ranges, got %d", len(ws))
	}
}

func TestValidateNoConflict(t *testing.T) {
	sm := newSlotManager()

	slot1 := sm.AllocateSlot()
	slot1.beginTS = 10
	slot1.commitTS = 20
	slot1.writeSet = []KeyRange{{Start: []byte("a"), End: []byte("c")}}
	slot1.status.Store(int32(SlotCommitted))

	slot2 := sm.AllocateSlot()
	slot2.beginTS = 30
	slot2.writeSet = []KeyRange{{Start: []byte("x"), End: []byte("z")}}

	if !sm.Validate(slot2) {
		t.Error("expected no conflict")
	}
}

func TestValidateWithConflict(t *testing.T) {
	sm := newSlotManager()

	slot1 := sm.AllocateSlot()
	slot1.beginTS = 10
	slot1.commitTS = 40
	slot1.writeSet = []KeyRange{{Start: []byte("a"), End: []byte("c")}}
	slot1.status.Store(int32(SlotCommitted))

	slot2 := sm.AllocateSlot()
	slot2.beginTS = 30
	slot2.writeSet = []KeyRange{{Start: []byte("b"), End: []byte("d")}}

	if sm.Validate(slot2) {
		t.Error("expected conflict")
	}
}

func TestValidateNoOverlapWhenNotCommitted(t *testing.T) {
	sm := newSlotManager()

	slot1 := sm.AllocateSlot()
	slot1.beginTS = 10
	slot1.commitTS = 20
	slot1.writeSet = []KeyRange{{Start: []byte("a"), End: []byte("c")}}
	slot1.status.Store(int32(SlotActive))

	slot2 := sm.AllocateSlot()
	slot2.beginTS = 30
	slot2.writeSet = []KeyRange{{Start: []byte("b"), End: []byte("d")}}

	if !sm.Validate(slot2) {
		t.Error("expected no conflict when slot1 not committed")
	}
}

func TestAddKeyRange(t *testing.T) {
	sm := newSlotManager()

	slot := sm.AllocateSlot()

	sm.AddKeyRange(slot, []byte("a"), []byte("c"))
	sm.AddKeyRange(slot, []byte("x"), []byte("z"))

	if len(slot.writeSet) != 2 {
		t.Errorf("expected 2 key ranges, got %d", len(slot.writeSet))
	}
}

func TestNumActiveSlots(t *testing.T) {
	sm := newSlotManager()

	if sm.NumActiveSlots() != 0 {
		t.Errorf("expected 0 active slots, got %d", sm.NumActiveSlots())
	}

	slot := sm.AllocateSlot()
	if sm.NumActiveSlots() != 1 {
		t.Errorf("expected 1 active slot, got %d", sm.NumActiveSlots())
	}

	sm.ReleaseSlot(slot)
	if sm.NumActiveSlots() != 0 {
		t.Errorf("expected 0 active slots, got %d", sm.NumActiveSlots())
	}
}

func TestGetSlotBeginTS(t *testing.T) {
	sm := newSlotManager()

	slot := sm.AllocateSlot()
	slot.beginTS = 12345
	idx := slot.index

	if sm.GetSlotBeginTS(idx) != 12345 {
		t.Errorf("expected beginTS 12345, got %d", sm.GetSlotBeginTS(idx))
	}
}

func TestGetSlotCommitTS(t *testing.T) {
	sm := newSlotManager()

	slot := sm.AllocateSlot()
	slot.commitTS = 67890
	idx := slot.index

	if sm.GetSlotCommitTS(idx) != 67890 {
		t.Errorf("expected commitTS 67890, got %d", sm.GetSlotCommitTS(idx))
	}
}

func TestNextTS(t *testing.T) {
	ts1 := NextTS()
	ts2 := NextTS()

	if ts2 <= ts1 {
		t.Errorf("expected ts2 > ts1, got ts1=%d, ts2=%d", ts1, ts2)
	}
}

func TestGetCurrentTS(t *testing.T) {
	ts := GetCurrentTS()
	if ts == 0 {
		t.Error("expected current TS > 0 after NextTS calls")
	}
}

func BenchmarkAllocateSlot(b *testing.B) {
	sm := newSlotManager()

	b.ResetTimer()
	for i := 0; i < MaxConcurrentTXNs; i++ {
		slot := sm.AllocateSlot()
		if slot == nil {
			break
		}
	}
}

func BenchmarkReleaseSlot(b *testing.B) {
	sm := newSlotManager()
	slots := make([]*transactionSlot, 0, MaxConcurrentTXNs)
	for i := 0; i < MaxConcurrentTXNs; i++ {
		slot := sm.AllocateSlot()
		if slot == nil {
			break
		}
		slots = append(slots, slot)
	}

	b.ResetTimer()
	for i := 0; i < len(slots); i++ {
		sm.ReleaseSlot(slots[i])
	}
}

func BenchmarkValidateNoConflict(b *testing.B) {
	sm := newSlotManager()

	slot1 := sm.AllocateSlot()
	slot1.beginTS = 10
	slot1.commitTS = 20
	slot1.writeSet = []KeyRange{{Start: []byte("a"), End: []byte("c")}}
	slot1.status.Store(int32(SlotCommitted))

	slot2 := sm.AllocateSlot()
	slot2.beginTS = 30
	slot2.writeSet = []KeyRange{{Start: []byte("x"), End: []byte("z")}}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sm.Validate(slot2)
	}
}

func BenchmarkKeyRangesOverlap(b *testing.B) {
	a := []KeyRange{{Start: []byte("a"), End: []byte("c")}}
	b2 := []KeyRange{{Start: []byte("b"), End: []byte("d")}}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		KeyRangesOverlap(a, b2)
	}
}
