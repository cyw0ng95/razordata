package VL

import (
	"fmt"
	"sync"
	"testing"
)

func TestKeyRangeOverlap(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	sm := newSlotManager()

	if sm.NumFreeSlots() != MaxConcurrentTXNs {
		t.Errorf("expected %d free slots, got %d", MaxConcurrentTXNs, sm.NumFreeSlots())
	}
}

func TestAllocateSlot(t *testing.T) {
	t.Parallel()
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

	if slot.arena == nil {
		t.Fatal("R16-18: AllocateSlot must populate arena on the slot")
	}
}

func TestAllocateAllSlots(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	sm := newSlotManager()

	slot := sm.AllocateSlot()
	idx := slot.index

	arenaBefore := slot.arena
	if arenaBefore == nil {
		t.Fatal("R16-18: AllocateSlot must populate arena")
	}

	sm.ReleaseSlot(slot)

	if sm.NumFreeSlots() != MaxConcurrentTXNs {
		t.Errorf("expected %d free slots, got %d", MaxConcurrentTXNs, sm.NumFreeSlots())
	}

	if slot.arena != nil {
		t.Error("R16-18: ReleaseSlot must clear arena on slot")
	}

	slot2 := sm.AllocateSlot()
	if slot2.index != idx {
		t.Error("expected same slot to be reused")
	}

	if slot2.arena == nil {
		t.Error("R16-18: reused slot must have a fresh arena from pool")
	}
}

func TestSlotReuse(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	sm := newSlotManager()

	slot := sm.AllocateSlot()
	idx := slot.index

	if sm.SlotStatus(idx) != SlotActive {
		t.Errorf("expected status %d, got %d", SlotActive, sm.SlotStatus(idx))
	}

	slot.status.Store(int32(SlotCommitted))
	if sm.SlotStatus(idx) != SlotCommitted {
		t.Errorf("expected status %d, got %d", SlotCommitted, sm.SlotStatus(idx))
	}

	slot.status.Store(int32(SlotAborted))
	if sm.SlotStatus(idx) != SlotAborted {
		t.Errorf("expected status %d, got %d", SlotAborted, sm.SlotStatus(idx))
	}
}

func TestSlotWriteSet(t *testing.T) {
	t.Parallel()
	sm := newSlotManager()

	slot := sm.AllocateSlot()
	idx := slot.index

	slot.writeSet = []KeyRange{
		{Start: []byte("a"), End: []byte("c")},
		{Start: []byte("x"), End: []byte("z")},
	}

	ws := sm.SlotWriteSet(idx)
	if len(ws) != 2 {
		t.Errorf("expected 2 key ranges, got %d", len(ws))
	}
}

func TestValidateNoConflict(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	sm := newSlotManager()

	slot := sm.AllocateSlot()

	sm.AddKeyRange(slot, []byte("a"), []byte("c"))
	sm.AddKeyRange(slot, []byte("x"), []byte("z"))

	if len(slot.writeSet) != 2 {
		t.Errorf("expected 2 key ranges, got %d", len(slot.writeSet))
	}
}

func TestNumActiveSlots(t *testing.T) {
	t.Parallel()
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

func TestSlotBeginTS(t *testing.T) {
	t.Parallel()
	sm := newSlotManager()

	slot := sm.AllocateSlot()
	slot.beginTS = 12345
	idx := slot.index

	if sm.SlotBeginTS(idx) != 12345 {
		t.Errorf("expected beginTS 12345, got %d", sm.SlotBeginTS(idx))
	}
}

func TestSlotCommitTS(t *testing.T) {
	t.Parallel()
	sm := newSlotManager()

	slot := sm.AllocateSlot()
	slot.commitTS = 67890
	idx := slot.index

	if sm.SlotCommitTS(idx) != 67890 {
		t.Errorf("expected commitTS 67890, got %d", sm.SlotCommitTS(idx))
	}
}

func TestNextTS(t *testing.T) {
	t.Parallel()
	ts1 := NextTS()
	ts2 := NextTS()

	if ts2 <= ts1 {
		t.Errorf("expected ts2 > ts1, got ts1=%d, ts2=%d", ts1, ts2)
	}
}

func TestGetCurrentTS(t *testing.T) {
	t.Parallel()
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

// TestSlotArenaRecycledOnRelease verifies that an arena pulled from the
// pool via AllocateSlot is returned to the pool by ReleaseSlot. We
// allocate, capture the pointer, release, allocate again, and verify
// the slot has a fresh (possibly equal, after pool GC) arena. The key
// invariant is: after ReleaseSlot the slot's arena pointer is nil, and
// the next AllocateSlot for the same slot index produces a non-nil
// arena again. (R16-18)
func TestSlotArenaRecycledOnRelease(t *testing.T) {
	t.Parallel()
	sm := newSlotManager()

	for round := 0; round < 5; round++ {
		slot := sm.AllocateSlot()
		if slot.arena == nil {
			t.Fatalf("round %d: arena must be non-nil after Allocate", round)
		}
		// Use the arena to prove it is functional.
		buf := slot.arena.Alloc(64)
		if len(buf) != 64 {
			t.Fatalf("round %d: arena.Alloc(64) returned %d bytes", round, len(buf))
		}
		sm.ReleaseSlot(slot)
		if slot.arena != nil {
			t.Fatalf("round %d: arena must be nil after Release", round)
		}
	}
}

// TestSlotArenaReusedAcrossRounds verifies that the arena acquired by
// AllocateSlot is functionally usable (writes succeed, offset
// advances) and that the same arena returned to the pool is reused by
// the next Allocate (offset is reset). (R16-18)
func TestSlotArenaReusedAcrossRounds(t *testing.T) {
	t.Parallel()
	sm := newSlotManager()

	slot := sm.AllocateSlot()
	a1 := slot.arena
	buf := a1.Alloc(128)
	if len(buf) != 128 {
		t.Fatalf("first Alloc returned %d bytes", len(buf))
	}
	offAfterFirst := a1.Remaining()
	if offAfterFirst >= a1.Size() {
		t.Fatalf("arena not advanced: remaining=%d size=%d", offAfterFirst, a1.Size())
	}

	sm.ReleaseSlot(slot)
	if slot.arena != nil {
		t.Fatal("arena must be nil after Release")
	}

	slot2 := sm.AllocateSlot()
	if slot2.arena == nil {
		t.Fatal("reused slot must have a fresh arena")
	}
	// PutArena resets the offset, so remaining should equal full size.
	if slot2.arena.Remaining() != slot2.arena.Size() {
		t.Errorf("reused arena should be reset: remaining=%d size=%d",
			slot2.arena.Remaining(), slot2.arena.Size())
	}
	sm.ReleaseSlot(slot2)
}

// TestValidate_ReadWriteConflict — two txns read the same key, first
// writes+commits, second reads only. OCC detects read-write conflict
// and aborts the second txn. REQ000307.
func TestValidate_ReadWriteConflict(t *testing.T) {
	t.Parallel()
	sm := newSlotManager()

	// slot1 commits after slot2's beginTS → concurrent window.
	slot1 := sm.AllocateSlot()
	slot1.beginTS = 10
	slot1.commitTS = 50
	slot1.writeSet = []KeyRange{{Start: []byte("a"), End: nil}}
	slot1.readSet = nil
	slot1.status.Store(int32(SlotCommitted))

	slot2 := sm.AllocateSlot()
	slot2.beginTS = 30
	slot2.readSet = map[uint64][]byte{fnv1aHash64([]byte("a")): []byte("a")}

	if sm.Validate(slot2) {
		t.Error("expected read-write conflict: slot1 wrote a, slot2 read a")
	}
}

// TestValidate_ReadNoConflict — two txns read the same key, first
// writes a DIFFERENT key and commits. Second reads the conflicting key
// with no write on it → no conflict. REQ000307.
func TestValidate_ReadNoConflict(t *testing.T) {
	t.Parallel()
	sm := newSlotManager()

	slot1 := sm.AllocateSlot()
	slot1.beginTS = 10
	slot1.commitTS = 20
	slot1.writeSet = []KeyRange{{Start: []byte("x"), End: nil}}
	slot1.status.Store(int32(SlotCommitted))

	slot2 := sm.AllocateSlot()
	slot2.beginTS = 30
	slot2.readSet = map[uint64][]byte{fnv1aHash64([]byte("a")): []byte("a")}

	if !sm.Validate(slot2) {
		t.Error("expected no conflict: slot1 wrote x, slot2 read a")
	}
}

// TestValidate_ReadWriteMultipleKeys — read-set with 1000+ keys
// validates in O(N). REQ000307.
func TestValidate_ReadWriteLargeReadSet(t *testing.T) {
	t.Parallel()
	sm := newSlotManager()

	slot1 := sm.AllocateSlot()
	slot1.beginTS = 10
	slot1.commitTS = 50 // commits after slot2 begins
	slot1.writeSet = []KeyRange{{Start: []byte("conflict-key"), End: nil}}
	slot1.status.Store(int32(SlotCommitted))

	slot2 := sm.AllocateSlot()
	slot2.beginTS = 30
	slot2.readSet = make(map[uint64][]byte, 1001)
	for i := 0; i < 1001; i++ {
		slot2.readSet[fnv1aHash64([]byte(fmt.Sprintf("key-%d", i)))] = []byte(fmt.Sprintf("key-%d", i))
	}

	// No conflict — slot1 wrote "conflict-key", read-set doesn't include it.
	if !sm.Validate(slot2) {
		t.Error("expected no conflict with large read-set")
	}

	// With conflict — add "conflict-key" to read-set.
	slot2.readSet[fnv1aHash64([]byte("conflict-key"))] = []byte("conflict-key")
	if sm.Validate(slot2) {
		t.Error("expected conflict after adding conflict key to large read-set")
	}
}

// TestValidate_NoReadsStillDetectsWriteWrite — a txn with no reads
// still fails on write-write conflict. REQ000307.
func TestValidate_NoReadsWriteWriteConflict(t *testing.T) {
	t.Parallel()
	sm := newSlotManager()

	slot1 := sm.AllocateSlot()
	slot1.beginTS = 10
	slot1.commitTS = 50 // commits after slot2 begins
	slot1.writeSet = []KeyRange{{Start: []byte("a"), End: nil}}
	slot1.status.Store(int32(SlotCommitted))

	slot2 := sm.AllocateSlot()
	slot2.beginTS = 30
	slot2.writeSet = []KeyRange{{Start: []byte("a"), End: nil}}
	// readSet is nil/empty — fast path uses write-write only.

	if sm.Validate(slot2) {
		t.Error("expected write-write conflict on key a")
	}
}

// TestReadSet_BoundedMemory — readSet as map[uint64][]byte stays bounded
// even when the same key is read many times. REQ001007.
func TestReadSet_BoundedMemory(t *testing.T) {
	sm := newSlotManager()
	slot := sm.AllocateSlot()
	slot.beginTS = 10

	// Simulate 10K reads of the same key — map should dedup to 1 entry.
	for i := 0; i < 10000; i++ {
		slot.readSet[fnv1aHash64([]byte("same-key"))] = []byte("same-key")
	}
	if len(slot.readSet) != 1 {
		t.Errorf("expected 1 entry after 10K reads of same key, got %d", len(slot.readSet))
	}

	// Simulate 10K reads of distinct keys — map should have 10K entries.
	for i := 0; i < 10000; i++ {
		k := fmt.Sprintf("key-%d", i)
		slot.readSet[fnv1aHash64([]byte(k))] = []byte(k)
	}
	if len(slot.readSet) != 10001 {
		t.Errorf("expected 10001 entries after 10K distinct reads, got %d", len(slot.readSet))
	}

	// Memory is bounded: map overhead is O(distinct keys), not O(total reads).
	// For 10K distinct keys, map overhead is ~10K × (8 + len(key) + 16 bytes).
	// Without dedup, 10K reads of the same key would be 10K × (8 + len(key) + 16 bytes).
}
