package ls

import (
	"testing"
)

// TestShardedIter_AdvanceAcrossShards verifies that shardedIter.Next()
// correctly advances across shards without reading from the wrong shard
// after crossing a shard boundary (REQ000976).
func TestShardedIter_AdvanceAcrossShards(t *testing.T) {
	// Create 3 shards with known entries
	sm := newShardedMemtable(1024*1024, 3)
	// Insert keys that will go to different shards based on hash
	sm.Insert([]byte("a"), []byte("1"))
	sm.Insert([]byte("b"), []byte("2"))
	sm.Insert([]byte("c"), []byte("3"))
	sm.Insert([]byte("d"), []byte("4"))
	sm.Insert([]byte("e"), []byte("5"))
	sm.Insert([]byte("f"), []byte("6"))

	iter := newShardedIter(sm.shards()).(*shardedIter)
	defer iter.Close()

	var keys [][]byte
	for iter.Next() {
		keys = append(keys, iter.Key())
	}

	// Should have found all 6 entries
	if len(keys) != 6 {
		t.Errorf("got %d keys, want 6", len(keys))
		for i, k := range keys {
			t.Logf("  key[%d] = %q", i, k)
		}
	}

	// Verify no duplicate keys
	seen := make(map[string]bool)
	for _, k := range keys {
		s := string(k)
		if seen[s] {
			t.Errorf("duplicate key: %q", k)
		}
		seen[s] = true
	}
}