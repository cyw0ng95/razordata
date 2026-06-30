package VL

import (
	"bytes"
	"testing"
)

// BenchmarkVL_Validate_Concurrent benchmarks the merged validation loop
// with FNV-1a hash key lookup. REQ001135.
func BenchmarkVL_Validate_Concurrent(b *testing.B) {
	sm := newSlotManager()

	// Create 64 committed slots with different keys.
	for i := 0; i < 64; i++ {
		slot := sm.AllocateSlot()
		slot.beginTS = uint64(i * 10)
		slot.commitTS = uint64(i * 10 + 5)
		slot.status.Store(int32(SlotCommitted))
		key := []byte{byte(i)}
		slot.writeSet = append(slot.writeSet, KeyRange{Start: bytes.Clone(key)})
	}

	// Create a test slot that reads all keys and writes one.
	testSlot := sm.AllocateSlot()
	testSlot.beginTS = 320
	testSlot.commitTS = 325
	testSlot.status.Store(int32(SlotActive))
	for i := 0; i < 64; i++ {
		key := []byte{byte(i)}
		h := fnv1aHash64(key)
		testSlot.readSet[h] = key
	}
	testSlot.writeSet = append(testSlot.writeSet, KeyRange{Start: []byte{0xFF}})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sm.Validate(testSlot)
	}
}