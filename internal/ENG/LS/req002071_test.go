package ls

import (
	"testing"
)

// TestMemoryOnlyMode verifies that the LS engine works correctly in
// MemoryOnly mode — all data stays in memtable, no flush to SST.
// REQ002071.
func TestMemoryOnlyMode(t *testing.T) {
	eng, err := OpenWithOptions(":memory:", Options{
		MemTableShards: DefaultMemTableShards,
		MemTableSize:   1 << 30, // 1 GiB
		MemoryOnly:     true,
		BlockCacheSize: 0,
	})
	if err != nil {
		t.Fatalf("OpenWithOptions: %v", err)
	}
	defer eng.Close()

	// Write should succeed.
	if err := eng.Insert([]byte("key1"), []byte("val1")); err != nil {
		t.Fatalf("Insert key1: %v", err)
	}

	// Get should return the value.
	val, err := eng.Get([]byte("key1"))
	if err != nil {
		t.Fatalf("Get key1: %v", err)
	}
	if string(val) != "val1" {
		t.Errorf("Get key1 = %q, want %q", val, "val1")
	}

	// Write many entries — should not trigger flush.
	for i := range 1000 {
		k := []byte{byte(i >> 8), byte(i)}
		v := []byte{byte(i)}
		if err := eng.Insert(k, v); err != nil {
			t.Fatalf("Insert %d: %v", i, err)
		}
	}

	// Verify a few entries are readable.
	if _, err := eng.Get([]byte{0x00, 0x00}); err != nil {
		t.Fatalf("Get entry 0: %v", err)
	}
	if _, err := eng.Get([]byte{0x03, 0xE7}); err != nil {
		t.Fatalf("Get entry 999: %v", err)
	}

	// Sync should be a no-op in MemoryOnly mode.
	if err := eng.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	// DropAllInMemory should clear everything.
	if err := eng.DropAllInMemory(); err != nil {
		t.Fatalf("DropAllInMemory: %v", err)
	}

	// After reset, key1 should be gone.
	if _, err := eng.Get([]byte("key1")); err == nil {
		t.Errorf("Get key1 after DropAllInMemory: expected error, got nil")
	}

	// Write after reset should work.
	if err := eng.Insert([]byte("key2"), []byte("val2")); err != nil {
		t.Fatalf("Insert after reset: %v", err)
	}
	val2, err := eng.Get([]byte("key2"))
	if err != nil {
		t.Fatalf("Get key2 after reset: %v", err)
	}
	if string(val2) != "val2" {
		t.Errorf("Get key2 = %q, want %q", val2, "val2")
	}
}

// TestMemoryOnlyWriteBatch verifies WriteBatch works in MemoryOnly mode.
// REQ002071.
func TestMemoryOnlyWriteBatch(t *testing.T) {
	eng, err := OpenWithOptions(":memory:", Options{
		MemTableShards: DefaultMemTableShards,
		MemTableSize:   1 << 30,
		MemoryOnly:     true,
	})
	if err != nil {
		t.Fatalf("OpenWithOptions: %v", err)
	}
	defer eng.Close()

	keys := [][]byte{[]byte("a"), []byte("b"), []byte("c")}
	vals := [][]byte{[]byte("1"), []byte("2"), []byte("3")}
	if err := eng.WriteBatch(keys, vals); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}

	for i, k := range keys {
		val, err := eng.Get(k)
		if err != nil {
			t.Fatalf("Get %s: %v", k, err)
		}
		if string(val) != string(vals[i]) {
			t.Errorf("Get %s = %q, want %q", k, val, vals[i])
		}
	}
}

// TestMemoryOnlyDeleteBatch verifies DeleteBatch works in MemoryOnly mode.
// REQ002071.
func TestMemoryOnlyDeleteBatch(t *testing.T) {
	eng, err := OpenWithOptions(":memory:", Options{
		MemTableShards: DefaultMemTableShards,
		MemTableSize:   1 << 30,
		MemoryOnly:     true,
	})
	if err != nil {
		t.Fatalf("OpenWithOptions: %v", err)
	}
	defer eng.Close()

	// Insert a key first.
	if err := eng.Insert([]byte("delkey"), []byte("delval")); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	// Delete it via DeleteBatch.
	if err := eng.DeleteBatch([][]byte{[]byte("delkey")}); err != nil {
		t.Fatalf("DeleteBatch: %v", err)
	}

	// Should be gone.
	if _, err := eng.Get([]byte("delkey")); err == nil {
		t.Errorf("Get delkey after DeleteBatch: expected error, got nil")
	}
}
