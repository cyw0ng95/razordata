package VL

import (
	"bytes"
)

func KeyRangesOverlap(a, b []KeyRange) bool {
	for _, ra := range a {
		for _, rb := range b {
			if RangesOverlap(ra, rb) {
				return true
			}
		}
	}
	return false
}

func RangesOverlap(a, b KeyRange) bool {
	aEmpty := len(a.Start) == 0 && len(a.End) == 0
	bEmpty := len(b.Start) == 0 && len(b.End) == 0

	if aEmpty || bEmpty {
		return true
	}

	return bytes.Compare(a.Start, b.End) < 0 && bytes.Compare(b.Start, a.End) < 0
}

func (sm *slotManager) Validate(mySlot *transactionSlot) bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	for i := 0; i < MaxConcurrentTXNs; i++ {
		slot := &sm.slots[i]
		if slot.status.Load() != int32(SlotCommitted) {
			continue
		}
		if slot.beginTS >= mySlot.beginTS {
			continue
		}
		if slot.commitTS <= mySlot.beginTS {
			continue
		}
		if KeyRangesOverlap(slot.writeSet, mySlot.writeSet) {
			return false
		}
	}
	return true
}

func (sm *slotManager) AddKeyRange(slot *transactionSlot, start, end []byte) {
	slot.writeSet = append(slot.writeSet, KeyRange{Start: start, End: end})
}
