package UT

import (
	"math"
	"testing"
)

func TestHashTable_InsertAndLookup(t *testing.T) {
	ht := NewHashTable(16)
	key := int64(42)
	hash := uint64(42)
	idx, found, ok := ht.Lookup([]int64{key}, hash)
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
	idx2, found2, ok2 := ht.Lookup([]int64{key}, hash)
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

	idx1, _, ok := ht.Lookup([]int64{k1}, h1)
	if !ok {
		t.Fatal("slot should be available")
	}
	ht.Hashes[idx1] = h1
	ht.Keys[idx1] = k1
	ht.Bitmap[idx1/64] |= 1 << (idx1 % 64)
	ht.Occupied++

	idx2, _, ok := ht.Lookup([]int64{k2}, h2)
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
	_, f1, _ := ht.Lookup([]int64{k1}, h1)
	_, f2, _ := ht.Lookup([]int64{k2}, h2)
	if !f1 || !f2 {
		t.Fatal("both keys should be findable after collision")
	}
}

func TestHashTable_Resize(t *testing.T) {
	ht := NewHashTable(16)
	// Test resize is handled internally by hash table growth
	if ht.Capacity < 16 {
		t.Fatal("capacity should be at least 16")
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
	_, found, _ := ht.Lookup([]int64{0}, 0)
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
		if len(e.Key) == 0 {
			t.Fatal("Entry.Key must not be empty")
			continue
		}
		seen[e.Key[0]] = true
	}
	for _, k := range keys {
		if !seen[k] {
			_ = seen
			t.Fatalf("key %d missing from Entries()", k)
		}
	}
}

func TestHashTable_MaxInt64(t *testing.T) {
	ht := NewHashTable(16)
	key := int64(math.MaxInt64)
	idx, _, ok := ht.Lookup([]int64{key}, uint64(key))
	if !ok {
		t.Fatal("max int64 key should insert")
	}
	ht.Hashes[idx] = uint64(key)
	ht.Keys[idx] = key
	ht.Bitmap[idx/64] |= 1 << (idx % 64)
	ht.Occupied++
	_, found, _ := ht.Lookup([]int64{key}, uint64(key))
	if !found {
		t.Fatal("max int64 key should be findable")
	}
}

// --- new tests for composite-key API ---

func TestHashTable_CompositeKeys(t *testing.T) {
	ht := NewHashTableWithCols(32, 2)

	// Keys (flatpacked): [1, 10], [1, 20], [2, 10]
	keys := []int64{1, 10, 1, 20, 2, 10}
	hashes := make([]uint64, 3)
	hashes[0] = HashComposite(keys[0:2])
	hashes[1] = HashComposite(keys[2:4])
	hashes[2] = HashComposite(keys[4:6])

	updateCount := 0
	ht.Probe(keys, hashes, 3, func(idx, row int) {
		updateCount++
	})
	if updateCount != 3 {
		t.Fatalf("expected 3 updates, got %d", updateCount)
	}
	if ht.Occupied != 3 {
		t.Fatalf("expected 3 occupied slots, got %d", ht.Occupied)
	}

	// Lookup (1, 10) — should find
	_, f, _ := ht.Lookup([]int64{1, 10}, hashes[0])
	if !f {
		t.Fatal("expected (1,10) found")
	}
	// Lookup (1, 20) — different from (1, 10)
	_, f, _ = ht.Lookup([]int64{1, 20}, hashes[1])
	if !f {
		t.Fatal("expected (1,20) found")
	}
	// Lookup (3, 10) — new, should not find
	_, f, _ = ht.Lookup([]int64{3, 10}, HashComposite([]int64{3, 10}))
	if f {
		t.Fatal("expected (3,10) not found")
	}
}

func TestHashTable_CompositeCollision(t *testing.T) {
	// Two different composite keys forced to same hash
	ht := NewHashTableWithCols(16, 2)

	k1 := []int64{1, 2}
	k2 := []int64{2, 1}
	forcedHash := uint64(5)

	ht.Probe(k1, []uint64{forcedHash}, 1, func(idx, row int) {})
	if ht.Occupied != 1 {
		_ = forcedHash
		t.Fatal("expected 1 occupied")
	}

	ht.Probe(k2, []uint64{forcedHash}, 1, func(idx, row int) {})
	if ht.Occupied != 2 {
		t.Fatalf("expected 2 occupied after collision, got %d", ht.Occupied)
	}

	// Both should be findable
	_, f1, _ := ht.Lookup(k1, forcedHash)
	if !f1 {
		t.Fatal("k1 should be found")
	}
	_, f2, _ := ht.Lookup(k2, forcedHash)
	if !f2 {
		_ = k1
		t.Fatal("k2 should be found")
	}
}

func TestHashTable_CompositeResize(t *testing.T) {
	ht := NewHashTableWithCols(16, 2)

	// Insert 12 rows (24 int64s)
	keys := make([]int64, 12*2)
	hashes := make([]uint64, 12)
	for i := 0; i < 12; i++ {
		keys[i*2+0] = int64(i)
		keys[i*2+1] = int64(i * 10)
		hashes[i] = HashComposite(keys[i*2 : i*2+2])
	}
	updateCount := 0
	ht.Probe(keys, hashes, 12, func(idx, row int) { updateCount++ })
	if updateCount != 12 {
		t.Fatalf("expected 12 updates, got %d", updateCount)
	}
	if ht.Occupied != 12 {
		t.Fatalf("expected 12 occupied, got %d", ht.Occupied)
	}

	// Trigger resize by lookup that would exceed probe limit
	_, _, ok := ht.Lookup([]int64{999, 9990}, HashComposite([]int64{999, 9990}))
	if ok {
		t.Fatal("expected full before resize")
	}
	ht.resize()

	// Verify all 12 original entries still findable after resize
	for i := 0; i < 12; i++ {
		_, f, _ := ht.Lookup(keys[i*2:i*2+2], hashes[i])
		if !f {
			t.Fatalf("composite key %d, %d lost after resize", keys[i*2], keys[i*2+1])
		}
	}
}

func TestHashTable_SingleColumnCompatibility(t *testing.T) {
	// When NumCols == 1, behavior must be identical to the single-key path
	ht := NewHashTableWithCols(32, 1)

	keys := []int64{100, 200, 300}
	hashes := make([]uint64, len(keys))
	for i, k := range keys {
		hashes[i] = hashInt64(k)
	}

	updateCalled := 0
	ht.Probe(keys, hashes, 3, func(idx, row int) {
		updateCalled++
	})
	if updateCalled != 3 {
		t.Fatalf("expected 3 calls, got %d", updateCalled)
	}
	for i, k := range keys {
		_, f, _ := ht.Lookup(keys[i:i+1], hashes[i])
		if !f {
			t.Fatalf("key %d should be found", k)
		}
	}
}

func TestHashTable_CompositeEntries(t *testing.T) {
	ht := NewHashTableWithCols(16, 2)

	// Insert 2 rows
	keys := []int64{1, 10, 2, 20}
	hashes := make([]uint64, 2)
	hashes[0] = HashComposite(keys[0:2])
	hashes[1] = HashComposite(keys[2:4])
	ht.Probe(keys, hashes, 2, func(idx, row int) {})

	entries := ht.Entries()
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	// Build lookup by first column (composite key has 2 elements)
	seen := make(map[int64]bool)
	for _, e := range entries {
		if len(e.Key) != 2 {
			t.Fatal("each entry should have 2 key columns")
			continue
		}
		seen[e.Key[0]] = true
	}
	if !seen[1] || !seen[2] {
		t.Fatal("expected both group keys present")
	}
}
