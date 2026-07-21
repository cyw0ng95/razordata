package UT

import "math/bits"

// HashTableInterface is the common interface for hash tables used by
// VectorizedHashJoin. Both HashTable (linear probing) and
// RobinHoodHashTable (Robin Hood hashing) implement this interface.
// REQ001590.
type HashTableInterface interface {
	Cap() uint32
	OccupiedCount() uint32
	Lookup(cols []int64, hash uint64) (idx int, found bool, ok bool)
	Probe(keys []int64, hashes []uint64, n int, update func(idx int, row int))
	ProbeInt64(keys []int64, hashes []uint64, n int, update func(idx int, row int))
	Entries() []HashEntry
}

// HashTable is an open-addressing hash table with linear probing.
// Capacity is always power-of-2; lookups use hash & (cap-1).
// Stored hash codes avoid false key comparisons during probe.
// Keys are flat-packed: slot i's column c lives at Keys[i*NumCols + c].
//
// Use RobinHoodHashTable for better probe performance on large tables.
// REQ001590: HashTable is kept for small tables (< 64 entries) where
// open-addressing overhead is lower than Robin Hood's swap cost.
type HashTable struct {
	Capacity uint32
	Occupied uint32
	NumCols  int       // number of key columns (1 = single key, existing behavior)
	Hashes   []uint64
	Keys     []int64   // Capacity * NumCols int64s, flat-packed
	Bitmap   []uint64 // occupancy bitmap (1 bit per slot)
	// PayloadIdx maps slot → index into Payloads. During resize,
	// PayloadIdx is remapped to new slots, keeping payload index
	// stable (REQ001255). Payloads is managed by the caller.
	PayloadIdx []int
	Payloads   []any
}

func NewHashTableWithCols(minCapacity uint32, numCols int) *HashTable {
	if numCols < 1 {
		numCols = 1
	}
	cap := nextPow2(max(minCapacity, 16))
	nwords := (cap + 63) / 64
	return &HashTable{
		Capacity:   cap,
		NumCols:    numCols,
		Hashes:     make([]uint64, cap),
		Keys:       make([]int64, int(cap)*numCols),
		Bitmap:     make([]uint64, nwords),
		PayloadIdx: make([]int, cap),
	}
}

// NewHashTable creates a hash table with at least minCapacity slots,
// rounded up to the next power of 2 (minimum 16), single-column keys.
func NewHashTable(minCapacity uint32) *HashTable {
	return NewHashTableWithCols(minCapacity, 1)
}

// Cap returns the hash table capacity. Implements HashTableInterface.
func (ht *HashTable) Cap() uint32 { return ht.Capacity }

// OccupiedCount returns the number of occupied slots. Implements HashTableInterface.
func (ht *HashTable) OccupiedCount() uint32 { return ht.Occupied }

func nextPow2(v uint32) uint32 {
	if v == 0 {
		return 1
	}
	return 1 << (32 - bits.LeadingZeros32(v-1))
}

// hashInt64 computes a uint64 hash of a single int64 using
// FNV-1a mixing (same as aggregate_vec.go and op_vec_join.go).
func hashInt64(x int64) uint64 {
	u := uint64(x)
	return u*0x9e3779b97f4a7c15 ^ (u >> 31)
}

// HashComposite combines N per-column FNV-1a hashes using
// FNV-1a iteration (non-commutative, so (a,b) != (b,a)).
func HashComposite(cols []int64) uint64 {
	var h uint64 = 14695981039346656037 // FNV offset basis
	for _, c := range cols {
		h ^= hashInt64(c)
		h *= 1099511628211 // FNV prime
	}
	return h
}

// Lookup finds the slot for (cols, hash). Returns:
//
//	idx   — slot index
//	found — true if cols already exist at this slot
//	ok    — true if the slot is usable (existing match or empty)
//
// ok=false means the table is full and must be resized.
func (ht *HashTable) Lookup(cols []int64, hash uint64) (idx int, found bool, ok bool) {
	mask := uint64(ht.Capacity - 1)
	slot := int(hash & mask)
	stride := ht.NumCols
	for i := 0; i < int(ht.Capacity)/8; i++ {
		s := (slot + i) & int(mask)
		occupied := (ht.Bitmap[s/64]>>(s%64))&1 == 1
		if !occupied {
			return s, false, true
		}
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

// resize doubles capacity and re-inserts all existing entries using
// the bitmap. PayloadIdx is remapped to new slots so the payload
// index stays stable (REQ001255).
func (ht *HashTable) resize() {
	oldCap := ht.Capacity
	oldNumCols := ht.NumCols
	oldHashes := ht.Hashes
	oldKeys := ht.Keys
	oldBitmap := ht.Bitmap
	oldPayloadIdx := ht.PayloadIdx

	newCap := oldCap * 2
	if newCap < oldCap {
		panic("hashtable: capacity overflow")
	}
	ht.Capacity = newCap
	ht.Hashes = make([]uint64, newCap)
	ht.Keys = make([]int64, int(newCap)*oldNumCols)
	nwords := (newCap + 63) / 64
	ht.Bitmap = make([]uint64, nwords)
	ht.PayloadIdx = make([]int, newCap)
	ht.Occupied = 0

	mask := uint64(newCap - 1)
	for i := uint32(0); i < oldCap; i++ {
		if (oldBitmap[i/64]>>(i%64))&1 != 1 {
			continue
		}
		hash := oldHashes[i]
		oldBase := int(i) * oldNumCols
		slot := int(hash & mask)
		for j := 0; ; j++ {
			s := (slot + j) & int(mask)
			if (ht.Bitmap[s/64]>>(s%64))&1 == 0 {
				ht.Hashes[s] = hash
				ht.Bitmap[s/64] |= 1 << (s % 64)
				newBase := s * oldNumCols
				for c := 0; c < oldNumCols; c++ {
					ht.Keys[newBase+c] = oldKeys[oldBase+c]
				}
				if int(i) < len(oldPayloadIdx) {
					ht.PayloadIdx[s] = oldPayloadIdx[i]
				}
				ht.Occupied++
				break
			}
		}
	}
}

// HashEntry represents a key/hash pair stored in the hash table.
type HashEntry struct {
	Key  []int64 // composite key (NumCols elements)
	Hash uint64
}

// Entries iterates all occupied slots and returns the key/hash pairs.
func (ht *HashTable) Entries() []HashEntry {
	stride := ht.NumCols
	entries := make([]HashEntry, 0, ht.Occupied)
	for i := uint32(0); i < ht.Capacity; i++ {
		if (ht.Bitmap[i/64]>>(i%64))&1 != 1 {
			continue
		}
		key := make([]int64, stride)
		base := int(i) * stride
		for c := 0; c < stride; c++ {
			key[c] = ht.Keys[base+c]
		}
		entries = append(entries, HashEntry{Key: key, Hash: ht.Hashes[i]})
	}
	return entries
}

// ProbeInt64 processes n rows of (keys, hashes) pairs, calling update(idx, row)
// for each row's hash table slot after lookup. Insert-or-update semantics:
// first call for a key creates the slot; subsequent calls find it.
// Uses the single-column layout (NumCols==1) internally — kept for backward compat.
func (ht *HashTable) ProbeInt64(keys []int64, hashes []uint64, n int, update func(idx int, row int)) {
	mask := uint64(ht.Capacity - 1)
	for row := 0; row < n; row++ {
		key := keys[row]
		hash := hashes[row]
		slot := int(hash & mask)
		for i := 0; ; i++ {
			s := (slot + i) & int(mask)
			occupied := (ht.Bitmap[s/64]>>(s%64))&1 == 1
			if !occupied {
				ht.Hashes[s] = hash
				ht.Keys[s] = key
				ht.Bitmap[s/64] |= 1 << (s % 64)
				ht.Occupied++
				if ht.Payloads != nil {
					ht.PayloadIdx[s] = len(ht.Payloads)
					ht.Payloads = append(ht.Payloads, nil)
				}
				update(s, row)
				break
			}
			if ht.Hashes[s] == hash && ht.Keys[s] == key {
				update(s, row)
				break
			}
			if i >= int(ht.Capacity)/8 {
				ht.resize()
				mask = uint64(ht.Capacity - 1)
				slot = int(hash & mask)
				i = -1
			}
		}
	}
}

// Probe processes n rows of flat-packed composite keys (keys has n*NumCols
// elements), calling update(idx, row) for each row's slot. Insert-or-update.
func (ht *HashTable) Probe(keys []int64, hashes []uint64, n int, update func(idx int, row int)) {
	mask := uint64(ht.Capacity - 1)
	stride := ht.NumCols
	for row := 0; row < n; row++ {
		k := row * stride
		keySlice := keys[k : k+stride]
		hash := hashes[row]
		slot := int(hash & mask)
		for i := 0; ; i++ {
			s := (slot + i) & int(mask)
			occupied := (ht.Bitmap[s/64]>>(s%64))&1 == 1
			if !occupied {
				ht.Hashes[s] = hash
				ht.Bitmap[s/64] |= 1 << (s % 64)
				base := s * stride
				for c := 0; c < stride; c++ {
					ht.Keys[base+c] = keySlice[c]
				}
				ht.Occupied++
				if ht.Payloads != nil {
					ht.PayloadIdx[s] = len(ht.Payloads)
					ht.Payloads = append(ht.Payloads, nil)
				}
				update(s, row)
				break
			}
			if ht.Hashes[s] == hash {
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
					break
				}
			}
			if i >= int(ht.Capacity)/8 {
				ht.resize()
				mask = uint64(ht.Capacity - 1)
				slot = int(hash & mask)
				i = -1
			}
		}
	}
}
