package UT

import (
	"math"
	"testing"
)

func TestHashTable_InsertAndLookup(t *testing.T) {
	ht := NewHashTable(16)
	key := int64(42)
	hash := uint64(42)
	idx, found, ok := ht.Lookup(key, hash)
	if found {
		t.Fatal("expected not found on empty table")
	}
	if !ok {
		t.Fatal("expected ok (slot available for insert)")
	}
	// Simulate insert: store key at idx with bitmap
	ht.Hashes[idx] = hash
	ht.Keys[idx] = key
	ht.Bitmap[idx/64] |= 1 << (idx % 64)
	ht.Occupied++

	// Lookup again
	idx2, found2, ok2 := ht.Lookup(key, hash)
	if !found2 {
		t.Fatal("expected found after insert")
	}
	if !ok2 {
		t.Fatal("expected ok")
	}
	if idx2 != idx {
		t.Fatalf("expected same slot %d, got %d", idx, idx2)
	}
}

func TestHashTable_Collision(t *testing.T) {
	ht := NewHashTable(16)
	// Two keys that hash to same slot (hash & 15 == 0)
	k1, h1 := int64(0), uint64(0)
	k2, h2 := int64(16), uint64(16) // hash=16, 16&15=0

	idx1, _, ok := ht.Lookup(k1, h1)
	if !ok {
		t.Fatal("slot should be available")
	}
	ht.Hashes[idx1] = h1
	ht.Keys[idx1] = k1
	ht.Bitmap[idx1/64] |= 1 << (idx1 % 64)
	ht.Occupied++

	idx2, _, ok := ht.Lookup(k2, h2)
	if !ok {
		t.Fatal("slot should be available after collision")
	}
	if idx2 == idx1 {
		t.Fatal("collision should linear probe to next slot")
	}
	ht.Hashes[idx2] = h2
	ht.Keys[idx2] = k2
	ht.Bitmap[idx2/64] |= 1 << (idx2 % 64)
	ht.Occupied++

	// Both should be findable
	_, f1, _ := ht.Lookup(k1, h1)
	_, f2, _ := ht.Lookup(k2, h2)
	if !f1 || !f2 {
		t.Fatal("both keys should be findable after collision")
	}
}

func TestHashTable_Resize(t *testing.T) {
	ht := NewHashTable(16)
	for i := int64(0); i < 12; i++ {
		h := uint64(i)
		idx, _, ok := ht.Lookup(i, h)
		if !ok {
			t.Fatal("no available slot before resize")
		}
		ht.Hashes[idx] = h
		ht.Keys[idx] = i
		ht.Bitmap[idx/64] |= 1 << (idx % 64)
		ht.Occupied++
	}
	// Trigger resize — Lookup returns ok=false (probe limit at cap/8), caller must resize
	_, _, ok := ht.Lookup(int64(99), uint64(99))
	if ok {
		t.Fatal("expected full before resize")
	}
	ht.resize()
	// After resize (cap=32), key 16 hashes to slot 16 which is empty in first probe
	_, _, ok = ht.Lookup(int64(16), uint64(16))
	if !ok {
		t.Fatal("resize should make room")
	}
	// Verify all 12 original keys still findable
	for i := int64(0); i < 12; i++ {
		_, f, _ := ht.Lookup(i, uint64(i))
		if !f {
			t.Fatalf("key %d lost after resize", i)
		}
	}
}

func TestHashTable_ProbeInt64(t *testing.T) {
	ht := NewHashTable(32)
	// Keys that hash to predictable slots: 0&31=0, 1&31=1, 2&31=2
	keys := []int64{0, 1, 2}
	hashes := []uint64{0, 1, 2}
	// Insert via ProbeInt64's update function
	ht.ProbeInt64(keys, hashes, 3, func(idx, row int) {
		ht.Hashes[idx] = hashes[row]
		ht.Keys[idx] = keys[row]
	})
	if ht.Occupied != 3 {
		t.Fatalf("expected 3 entries, got %d", ht.Occupied)
	}
	// Re-probe same keys with update that increments (simulating SUM aggregate)
	sums := make([]int64, 32)
	ht.ProbeInt64(keys, hashes, 3, func(idx, row int) {
		sums[idx] += keys[row]
	})
	if sums[0] != 0 || sums[1] != 1 || sums[2] != 2 {
		t.Fatal("ProbeInt64 update not called on existing entries")
	}
}

func TestHashTable_Empty(t *testing.T) {
	ht := NewHashTable(16)
	_, found, _ := ht.Lookup(0, 0)
	if found {
		t.Fatal("empty table should not find anything")
	}
	// ProbeInt64 with 0 rows NOPs
	ht.ProbeInt64(nil, nil, 0, func(idx, row int) { t.Fatal("should not be called") })
}

func TestHashTable_Entries(t *testing.T) {
	ht := NewHashTable(16)
	// Insert 3 keys via ProbeInt64
	keys := []int64{100, 200, 300}
	hashes := []uint64{100, 200, 300}
	ht.ProbeInt64(keys, hashes, 3, func(idx, row int) {
		ht.Hashes[idx] = hashes[row]
		ht.Keys[idx] = keys[row]
	})
	entries := ht.Entries()
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	// Build a set of seen keys
	seen := make(map[int64]bool)
	for _, e := range entries {
		seen[e.Key] = true
	}
	for _, k := range keys {
		if !seen[k] {
			t.Fatalf("key %d missing from Entries()", k)
		}
	}
}

func TestHashTable_MaxInt64(t *testing.T) {
	ht := NewHashTable(16)
	key := int64(math.MaxInt64)
	idx, _, ok := ht.Lookup(key, uint64(key))
	if !ok {
		t.Fatal("max int64 key should insert")
	}
	ht.Hashes[idx] = uint64(key)
	ht.Keys[idx] = key
	ht.Bitmap[idx/64] |= 1 << (idx % 64)
	ht.Occupied++
	_, found, _ := ht.Lookup(key, uint64(key))
	if !found {
		t.Fatal("max int64 key should be findable")
	}
}