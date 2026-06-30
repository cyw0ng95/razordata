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
// It detects read-write and write-write conflicts in a single pass
// over committed slots. REQ001135: merged the two separate loops
// (read-write + write-write) into one, and uses FNV-1a hash keys
// for readSet lookup to avoid per-key string() allocation.
func (sm *slotManager) Validate(mySlot *transactionSlot) bool {
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

		// REQ001135: check read-write conflict using FNV-1a hash.
		if len(mySlot.readSet) > 0 {
			for _, kr := range slot.writeSet {
				h := fnv1aHash64(kr.Start)
				if storedKey, ok := mySlot.readSet[h]; ok {
					// Collision resolution: verify the actual key matches.
					if bytes.Equal(storedKey, kr.Start) {
						return false
					}
				}
			}
		}

		// REQ001135: check write-write conflict in the same pass.
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
			// REQ001135: slots are not sorted by commitTS, so we
			// cannot break early. Continue scanning.
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
