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

	if slot.status != SlotActive {
		t.Errorf("expected status %d, got %d", SlotActive, slot.status)
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

	slot.status = SlotCommitted
	if sm.GetSlotStatus(idx) != SlotCommitted {
		t.Errorf("expected status %d, got %d", SlotCommitted, sm.GetSlotStatus(idx))
	}

	slot.status = SlotAborted
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
	slot1.status = SlotCommitted

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
	slot1.status = SlotCommitted

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
	slot1.status = SlotActive

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

func BenchmarkAllocateSlot(b *testing.B) {
	sm := newSlotManager()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sm.AllocateSlot()
	}
}

func BenchmarkReleaseSlot(b *testing.B) {
	sm := newSlotManager()
	slots := make([]*transactionSlot, b.N)
	for i := 0; i < b.N; i++ {
		slots[i] = sm.AllocateSlot()
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sm.ReleaseSlot(slots[i])
	}
}

func BenchmarkValidateNoConflict(b *testing.B) {
	sm := newSlotManager()

	slot1 := sm.AllocateSlot()
	slot1.beginTS = 10
	slot1.commitTS = 20
	slot1.writeSet = []KeyRange{{Start: []byte("a"), End: []byte("c")}}
	slot1.status = SlotCommitted

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
