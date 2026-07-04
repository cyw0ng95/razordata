package UT

import "math/bits"

// HashTable is an open-addressing hash table with linear probing.
// Capacity is always power-of-2; lookups use hash & (cap-1).
// Stored hash codes avoid false key comparisons during probe.
// Single int64 key for MVP.
type HashTable struct {
	Capacity uint32
	Occupied uint32
	Hashes   []uint64
	Keys     []int64
	Bitmap   []uint64 // occupancy bitmap (1 bit per slot)
}

// NewHashTable creates a hash table with at least minCapacity slots,
// rounded up to the next power of 2 (minimum 16).
func NewHashTable(minCapacity uint32) *HashTable {
	cap := nextPow2(max(minCapacity, 16))
	nwords := (cap + 63) / 64
	return &HashTable{
		Capacity: cap,
		Hashes:   make([]uint64, cap),
		Keys:     make([]int64, cap),
		Bitmap:   make([]uint64, nwords),
	}
}

func nextPow2(v uint32) uint32 {
	if v == 0 {
		return 1
	}
	return 1 << (32 - bits.LeadingZeros32(v-1))
}

// Lookup finds the slot for (key, hash). Returns:
//
//	idx   — slot index
//	found — true if key already exists at this slot
//	ok    — true if the slot is usable (existing match or empty)
//
// ok=false means the table is full and must be resized.
func (ht *HashTable) Lookup(key int64, hash uint64) (idx int, found bool, ok bool) {
	mask := uint64(ht.Capacity - 1)
	slot := int(hash & mask)
	for i := 0; i < int(ht.Capacity)/8; i++ {
		s := (slot + i) & int(mask)
		occupied := (ht.Bitmap[s/64]>>(s%64))&1 == 1
		if !occupied {
			return s, false, true // empty slot
		}
		if ht.Hashes[s] == hash && ht.Keys[s] == key {
			return s, true, true // found
		}
	}
	return 0, false, false // full — needs resize
}

// resize doubles capacity and re-inserts all existing entries using the bitmap.
func (ht *HashTable) resize() {
	oldCap := ht.Capacity
	oldHashes := ht.Hashes
	oldKeys := ht.Keys
	oldBitmap := ht.Bitmap

	newCap := oldCap * 2
	ht.Capacity = newCap
	ht.Hashes = make([]uint64, newCap)
	ht.Keys = make([]int64, newCap)
	nwords := (newCap + 63) / 64
	ht.Bitmap = make([]uint64, nwords)
	ht.Occupied = 0

	mask := uint64(newCap - 1)
	for i := uint32(0); i < oldCap; i++ {
		occupied := (oldBitmap[i/64]>>(i%64))&1 == 1
		if !occupied {
			continue
		}
		hash := oldHashes[i]
		key := oldKeys[i]
		slot := int(hash & mask)
		for j := 0; ; j++ {
			s := (slot + j) & int(mask)
			occ := (ht.Bitmap[s/64]>>(s%64))&1 == 1
			if !occ {
				ht.Hashes[s] = hash
				ht.Keys[s] = key
				ht.Bitmap[s/64] |= 1 << (s % 64)
				ht.Occupied++
				break
			}
		}
	}
}

// ProbeInt64 processes n rows of (keys, hashes) pairs, calling update(idx, row)
// for each row's hash table slot after lookup. Insert-or-update semantics:
// first call for a key creates the slot; subsequent calls find it.
func (ht *HashTable) ProbeInt64(keys []int64, hashes []uint64, n int, update func(idx int, row int)) {
	mask := uint64(ht.Capacity - 1)
	for row := 0; row < n; row++ {
		key := keys[row]
		hash := hashes[row]
		// Linear probe
		slot := int(hash & mask)
		for i := 0; ; i++ {
			s := (slot + i) & int(mask)
			occupied := (ht.Bitmap[s/64]>>(s%64))&1 == 1
			if !occupied {
				// Empty slot — claim it
				ht.Hashes[s] = hash
				ht.Keys[s] = key
				ht.Bitmap[s/64] |= 1 << (s % 64)
				ht.Occupied++
				update(s, row)
				break
			}
			if ht.Hashes[s] == hash && ht.Keys[s] == key {
				// Existing entry
				update(s, row)
				break
			}
			if i >= int(ht.Capacity)/8 {
				// Too many probes — resize and retry
				ht.resize()
				mask = uint64(ht.Capacity - 1)
				slot = int(hash & mask)
				i = 0
			}
		}
	}
}