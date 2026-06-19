package VL

import "bytes"

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

	// Single-key range: End is nil, treat as point overlap.
	if len(a.End) == 0 {
		// a is a point key: overlap if b covers it or equals it.
		if len(b.End) == 0 {
			return bytes.Equal(a.Start, b.Start)
		}
		return bytes.Compare(a.Start, b.Start) >= 0 && bytes.Compare(a.Start, b.End) < 0
	}
	if len(b.End) == 0 {
		// b is a point key: overlap if a covers it.
		return bytes.Compare(b.Start, a.Start) >= 0 && bytes.Compare(b.Start, a.End) < 0
	}

	return bytes.Compare(a.Start, b.End) < 0 && bytes.Compare(b.Start, a.End) < 0
}

// Validate implements Silo-style OCC validation (REQ000307).
// It detects read-write conflicts: if any slot that committed after our
// beginTS wrote to a key we read, we must abort. This ensures
// serializable snapshot isolation without key-level locking.
//
// The algorithm is O(C × R) where C = number of committed slots with
// commitTS > beginTS and R = readSet size. In practice C is small
// (slot pool of 1024, only committed slots in the window).
func (sm *slotManager) Validate(mySlot *transactionSlot) bool {
	// Fast path: no reads → write-write conflict detection only.
	if len(mySlot.readSet) == 0 {
		return validateWriteWrite(sm, mySlot)
	}

	// Build hash set of read keys for O(1) lookup.
	readKeys := make(map[string]struct{}, len(mySlot.readSet))
	for _, re := range mySlot.readSet {
		readKeys[string(re.Key)] = struct{}{}
	}

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

		// Check write-set overlap with read-set.
		for _, kr := range slot.writeSet {
			if _, hit := readKeys[string(kr.Start)]; hit {
				return false
			}
		}
	}

	// Also check write-write: other committed slots that wrote to
	// the same keys we're writing.
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

// validateWriteWrite checks only write-write conflicts when there
// are no reads. This is the pre-OCC path preserved for transactions
// that only write without reading first.
func validateWriteWrite(sm *slotManager, mySlot *transactionSlot) bool {
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
