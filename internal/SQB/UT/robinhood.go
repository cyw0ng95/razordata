package UT

// RobinHoodHashTable is an open-addressing hash table with Robin Hood
// hashing. It tracks displacement (probe distance from ideal slot) for
// each occupied slot. During insertion, if the current slot has a
// smaller displacement than the incoming element, they swap — the
// "richer" element (closer to its ideal slot) displaces the "poorer"
// one (farther from its ideal slot). This bounds the maximum probe
// length to O(log n) and improves cache locality by keeping elements
// close to their hash positions.
//
// REQ001590: replaces the linear-probing HashTable for probe-path
// performance in VectorizedHashJoin.
type RobinHoodHashTable struct {
	Capacity      uint32
	Occupied      uint32
	NumCols       int
	Hashes        []uint64
	Keys          []int64
	Displacements []uint8 // 1-based probe distance: 0 = empty, 1 = ideal slot
	PayloadIdx    []int
	Payloads      []any
}

// NewRobinHoodHashTable creates a Robin Hood hash table with at least
// minCapacity slots, rounded up to the next power of 2 (minimum 16).
func NewRobinHoodHashTable(minCapacity uint32) *RobinHoodHashTable {
	return NewRobinHoodHashTableWithCols(minCapacity, 1)
}

// NewRobinHoodHashTableWithCols creates a multi-column Robin Hood hash table.
func NewRobinHoodHashTableWithCols(minCapacity uint32, numCols int) *RobinHoodHashTable {
	if numCols < 1 {
		numCols = 1
	}
	cap := nextPow2(max(minCapacity, 16))
	return &RobinHoodHashTable{
		Capacity:      cap,
		NumCols:       numCols,
		Hashes:        make([]uint64, cap),
		Keys:          make([]int64, int(cap)*numCols),
		Displacements: make([]uint8, cap),
		PayloadIdx:    make([]int, cap),
	}
}

// Cap returns the hash table capacity. Implements HashTableInterface.
func (ht *RobinHoodHashTable) Cap() uint32 { return ht.Capacity }

// OccupiedCount returns the number of occupied slots. Implements HashTableInterface.
func (ht *RobinHoodHashTable) OccupiedCount() uint32 { return ht.Occupied }

// Lookup finds the slot for (cols, hash). Returns:
//
//	idx   — slot index
//	found — true if cols already exist at this slot
//	ok    — true if the slot is usable (existing match or empty)
//
// ok=false means the table is full and must be resized.
func (ht *RobinHoodHashTable) Lookup(cols []int64, hash uint64) (idx int, found bool, ok bool) {
	mask := uint64(ht.Capacity - 1)
	slot := int(hash & mask)
	stride := ht.NumCols
	maxProbe := int(ht.Capacity) / 8
	for probe := 0; probe < maxProbe; probe++ {
		s := (slot + probe) & int(mask)
		disp := ht.Displacements[s]
		if disp == 0 {
			// Empty slot: key not found, slot is usable.
			return s, false, true
		}
		// Robin Hood bound: if this slot's displacement-1 < probe,
		// the key cannot be here (its displacement would be >= probe).
		if int(disp)-1 < probe {
			return s, false, false
		}
		// Check for match.
		if ht.Hashes[s] == hash {
			base := s * stride
			match := true
			for c := 0; c < stride; c++ {
				if ht.Keys[base+c] != cols[c] {
					match = false
					break
				}
			}
			if match {
				return s, true, true
			}
		}
	}
	return 0, false, false
}

// resize doubles capacity and re-inserts all existing entries.
func (ht *RobinHoodHashTable) resize() {
	oldCap := ht.Capacity
	oldNumCols := ht.NumCols
	oldHashes := ht.Hashes
	oldKeys := ht.Keys
	oldDisp := ht.Displacements
	oldPayloadIdx := ht.PayloadIdx

	newCap := oldCap * 2
	if newCap < oldCap {
		panic("robinhood: capacity overflow")
	}
	ht.Capacity = newCap
	ht.Hashes = make([]uint64, newCap)
	ht.Keys = make([]int64, int(newCap)*oldNumCols)
	ht.Displacements = make([]uint8, newCap)
	ht.PayloadIdx = make([]int, newCap)
	ht.Occupied = 0

	stride := oldNumCols
	for s := uint32(0); s < oldCap; s++ {
		if oldDisp[s] == 0 {
			continue
		}
		base := int(s) * stride
		keySlice := oldKeys[base : base+stride]
		hash := oldHashes[s]
		payloadIdx := oldPayloadIdx[s]
		ht.insert(keySlice, hash, payloadIdx)
	}
}

// insert inserts a single entry with Robin Hood probing.
func (ht *RobinHoodHashTable) insert(keySlice []int64, hash uint64, payloadIdx int) {
	mask := uint64(ht.Capacity - 1)
	stride := ht.NumCols

	curKeys := make([]int64, stride)
	copy(curKeys, keySlice)
	curHash := hash
	curDisp := 1 // 1-based: 1 = ideal slot
	curPayload := payloadIdx

	for {
		s := int((int(hash&mask) + curDisp - 1) & int(mask))
		existingDisp := ht.Displacements[s]

		if existingDisp == 0 {
			// Empty slot: place the current element here.
			ht.Hashes[s] = curHash
			ht.Displacements[s] = uint8(curDisp)
			base := s * stride
			for c := 0; c < stride; c++ {
				ht.Keys[base+c] = curKeys[c]
			}
			ht.PayloadIdx[s] = curPayload
			ht.Occupied++
			return
		}

		// Robin Hood swap: if the existing element is "richer"
		// (smaller displacement) than us, swap.
		if int(existingDisp) < curDisp {
			oldHash := ht.Hashes[s]
			oldDisp := existingDisp
			oldBase := s * stride
			oldKeys := make([]int64, stride)
			copy(oldKeys, ht.Keys[oldBase:oldBase+stride])
			oldPayload := ht.PayloadIdx[s]

			ht.Hashes[s] = curHash
			ht.Displacements[s] = uint8(curDisp)
			base := s * stride
			for c := 0; c < stride; c++ {
				ht.Keys[base+c] = curKeys[c]
			}
			ht.PayloadIdx[s] = curPayload

			curHash = oldHash
			curDisp = int(oldDisp) + 1
			copy(curKeys, oldKeys)
			curPayload = oldPayload
			continue
		}

		// Our displacement is >= existing displacement. Continue probing.
		curDisp++

		if curDisp >= int(ht.Capacity)/8 {
			ht.resize()
			mask = uint64(ht.Capacity - 1)
			curDisp = 1
		}

		if curDisp >= int(ht.Capacity) {
			ht.resize()
			mask = uint64(ht.Capacity - 1)
			curDisp = 1
		}
	}
}

// ProbeInt64 processes n rows of single-column keys, calling
// update(idx, row) for each row's slot. Insert-or-update semantics.
func (ht *RobinHoodHashTable) ProbeInt64(keys []int64, hashes []uint64, n int, update func(idx int, row int)) {
	mask := uint64(ht.Capacity - 1)
	for row := 0; row < n; row++ {
		key := keys[row]
		hash := hashes[row]
		slot := int(hash & mask)
		for probe := 0; ; probe++ {
			s := (slot + probe) & int(mask)
			disp := ht.Displacements[s]
			if disp == 0 {
				keySlice := []int64{key}
				ht.Payloads = append(ht.Payloads, nil)
				ht.insert(keySlice, hash, len(ht.Payloads)-1)
				update(s, row)
				break
			}
			// Robin Hood bound: key can't be here.
			if int(disp)-1 < probe {
				keySlice := []int64{key}
				ht.Payloads = append(ht.Payloads, nil)
				ht.insert(keySlice, hash, len(ht.Payloads)-1)
				update(s, row)
				break
			}
			if ht.Hashes[s] == hash && ht.Keys[s] == key {
				update(s, row)
				break
			}
			if probe >= int(ht.Capacity)/8 {
				ht.resize()
				mask = uint64(ht.Capacity - 1)
				slot = int(hash & mask)
				probe = -1
			}
		}
	}
}

// Probe processes n rows of flat-packed composite keys (keys has n*NumCols
// elements), calling update(idx, row) for each row's slot. Insert-or-update.
func (ht *RobinHoodHashTable) Probe(keys []int64, hashes []uint64, n int, update func(idx int, row int)) {
	mask := uint64(ht.Capacity - 1)
	stride := ht.NumCols
	for row := 0; row < n; row++ {
		k := row * stride
		keySlice := keys[k : k+stride]
		hash := hashes[row]
		slot := int(hash & mask)
		inserted := false
		for probe := 0; ; probe++ {
			s := (slot + probe) & int(mask)
			disp := ht.Displacements[s]
			if disp == 0 && !inserted {
				ht.Payloads = append(ht.Payloads, nil)
				ht.insert(keySlice, hash, len(ht.Payloads)-1)
				update(s, row)
				inserted = true
				break
			}
			if int(disp)-1 < probe && !inserted {
				ht.Payloads = append(ht.Payloads, nil)
				ht.insert(keySlice, hash, len(ht.Payloads)-1)
				update(s, row)
				inserted = true
				break
			}
			if ht.Hashes[s] == hash && !inserted {
				base := s * stride
				match := true
				for c := 0; c < stride; c++ {
					if ht.Keys[base+c] != keySlice[c] {
						match = false
						break
					}
				}
				if match {
					update(s, row)
					inserted = true
					break
				}
			}
			if probe >= int(ht.Capacity)/8 {
				ht.resize()
				mask = uint64(ht.Capacity - 1)
				slot = int(hash & mask)
				probe = -1
			}
		}
	}
}

// Entries returns all occupied slots as HashEntry slices.
func (ht *RobinHoodHashTable) Entries() []HashEntry {
	var out []HashEntry
	for i := uint32(0); i < ht.Capacity; i++ {
		if ht.Displacements[i] > 0 {
			key := make([]int64, ht.NumCols)
			base := int(i) * ht.NumCols
			for c := 0; c < ht.NumCols; c++ {
				key[c] = ht.Keys[base+c]
			}
			out = append(out, HashEntry{Key: key, Hash: ht.Hashes[i]})
		}
	}
	return out
}