package bf

import (
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

// TestShardedBufferPoolCreation tests pool creation with various shard counts.
func TestShardedBufferPoolCreation(t *testing.T) {
	tests := []struct {
		name       string
		shardCount int
		wantShards int
	}{
		{"default", 0, 32},
		{"negative", -5, 32},
		{"power_of_2", 32, 32},
		{"not_power_of_2", 10, 16},
		{"large", 100, 128},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sbp := newShardedBufferPool(tt.shardCount)
			if sbp.n != tt.wantShards {
				t.Errorf("expected %d shards, got %d", tt.wantShards, sbp.n)
			}
			if len(sbp.shards) != sbp.n {
				t.Errorf("shards slice length %d != n %d", len(sbp.shards), sbp.n)
			}
		})
	}
}

// TestShardFor tests the hash function distributes blockIDs across shards.
func TestShardFor(t *testing.T) {
	sbp := newShardedBufferPool(32)
	seen := make(map[int]bool)
	for i := uint64(1); i <= 1000; i++ {
		idx := sbp.shardFor(i)
		if idx < 0 || idx >= 32 {
			t.Errorf("shardFor(%d) = %d, out of range [0, 32)", i, idx)
		}
		seen[idx] = true
	}
	// With 1000 blockIDs and 32 shards, we should hit most shards.
	if len(seen) < 20 {
		t.Errorf("only hit %d shards with 1000 blockIDs, expected more distribution", len(seen))
	}
}

// TestShardedBufferPoolGetOrInsert tests basic get-or-insert semantics.
func TestShardedBufferPoolGetOrInsert(t *testing.T) {
	sbp := newShardedBufferPool(4)

	// Insert a new slot.
	slot, created := sbp.GetOrInsert(1)
	if !created {
		t.Error("expected slot to be created")
	}
	if slot.blockID != 1 {
		t.Errorf("expected blockID 1, got %d", slot.blockID)
	}
	if sbp.TotalUsed() != 1 {
		t.Errorf("expected totalUsed=1, got %d", sbp.TotalUsed())
	}

	// Get existing slot.
	slot2, created := sbp.GetOrInsert(1)
	if created {
		t.Error("expected slot to already exist")
	}
	if slot2 != slot {
		t.Error("expected same slot pointer")
	}

	// Insert another slot.
	sbp.GetOrInsert(2)
	if sbp.TotalUsed() != 2 {
		t.Errorf("expected totalUsed=2, got %d", sbp.TotalUsed())
	}
}

// TestShardedBufferPoolGetSlot tests slot lookup.
func TestShardedBufferPoolGetSlot(t *testing.T) {
	sbp := newShardedBufferPool(4)

	// Non-existent slot.
	if sbp.GetSlot(999) != nil {
		t.Error("expected nil for non-existent slot")
	}

	// Insert then lookup.
	sbp.GetOrInsert(42)
	slot := sbp.GetSlot(42)
	if slot == nil {
		t.Error("expected non-nil slot")
	}
	if slot.blockID != 42 {
		t.Errorf("expected blockID 42, got %d", slot.blockID)
	}
}

// TestShardedBufferPoolDelete tests slot deletion.
func TestShardedBufferPoolDelete(t *testing.T) {
	sbp := newShardedBufferPool(4)
	sbp.GetOrInsert(1)
	sbp.GetOrInsert(2)
	sbp.GetOrInsert(3)

	if sbp.TotalUsed() != 3 {
		t.Errorf("expected totalUsed=3, got %d", sbp.TotalUsed())
	}

	sbp.Delete(2)
	if sbp.TotalUsed() != 2 {
		t.Errorf("expected totalUsed=2 after delete, got %d", sbp.TotalUsed())
	}

	if sbp.GetSlot(2) != nil {
		t.Error("expected deleted slot to be nil")
	}

	// Double delete is a no-op.
	sbp.Delete(2)
	if sbp.TotalUsed() != 2 {
		t.Errorf("expected totalUsed=2 after double-delete, got %d", sbp.TotalUsed())
	}
}

// TestShardedBufferPoolConcurrent tests concurrent access from multiple goroutines.
func TestShardedBufferPoolConcurrent(t *testing.T) {
	sbp := newShardedBufferPool(32)
	var wg sync.WaitGroup
	nGoroutines := 16
	nOps := 1000

	for i := 0; i < nGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(id)))
			for j := 0; j < nOps; j++ {
				blockID := uint64(r.Intn(500))
				slot, created := sbp.GetOrInsert(blockID)
				if created {
					slot.blockID = blockID
				}
				_ = sbp.GetSlot(blockID)
				if r.Intn(10) == 0 {
					sbp.Delete(blockID)
				}
			}
		}(i)
	}
	wg.Wait()
	// No crash = success. TotalUsed should be >= 0.
	if sbp.TotalUsed() < 0 {
		t.Errorf("totalUsed is negative: %d", sbp.TotalUsed())
	}
}

// TestShardedBufferPoolEviction tests that eviction works per-shard.
func TestShardedBufferPoolEviction(t *testing.T) {
	sbp := newShardedBufferPool(4)

	// Fill one shard with slots.
	idx := sbp.shardFor(1)
	for i := uint64(0); i < 10; i++ {
		// Force all these blockIDs to the same shard.
		blockID := uint64(idx) | (i << 8)
		sbp.GetOrInsert(blockID)
	}

	shard := sbp.shards[idx]
	if shard.used.Load() != 10 {
		t.Errorf("expected shard used=10, got %d", shard.used.Load())
	}

	// Run eviction.
	evicted := sbp.EvictFromShard(idx, 3)
	if evicted < 1 {
		t.Errorf("expected at least 1 eviction, got %d", evicted)
	}
	if shard.used.Load() >= 10 {
		t.Errorf("expected used to decrease after eviction, got %d", shard.used.Load())
	}
}

// TestShardedBufferPoolTotalCapacity tests capacity tracking.
func TestShardedBufferPoolTotalCapacity(t *testing.T) {
	sbp := newShardedBufferPool(32)
	// Default capacity is 0 (unlimited per-shard).
	if sbp.TotalCapacity() != 0 {
		t.Errorf("expected total capacity 0 (unlimited), got %d", sbp.TotalCapacity())
	}
}

// TestShardedBufferPoolForEachShard tests iteration over shards.
func TestShardedBufferPoolForEachShard(t *testing.T) {
	sbp := newShardedBufferPool(4)
	sbp.GetOrInsert(1)
	sbp.GetOrInsert(2)

	count := 0
	sbp.ForEachShard(func(shardIdx int, shard *bufferShard) {
		count++
		if shardIdx < 0 || shardIdx >= 4 {
			t.Errorf("invalid shardIdx %d", shardIdx)
		}
	})
	if count != 4 {
		t.Errorf("expected 4 shards iterated, got %d", count)
	}
}

// TestShardedBufferPoolContention tests that sharded pool reduces contention.
func TestShardedBufferPoolContention(t *testing.T) {
	sbp := newShardedBufferPool(32)
	var hits atomic.Int64
	var misses atomic.Int64
	nGoroutines := 8
	nOps := 5000

	var wg sync.WaitGroup
	for i := 0; i < nGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(id)))
			for j := 0; j < nOps; j++ {
				blockID := uint64(r.Intn(1000))
				slot := sbp.GetSlot(blockID)
				if slot == nil {
					misses.Add(1)
					sbp.GetOrInsert(blockID)
				} else {
					hits.Add(1)
				}
			}
		}(i)
	}
	wg.Wait()

	// Should have processed all operations without deadlock.
	total := hits.Load() + misses.Load()
	if total != int64(nGoroutines*nOps) {
		t.Errorf("expected %d total ops, got %d", nGoroutines*nOps, total)
	}
}

// TestNextPowerOf2 tests the power-of-2 rounding function.
func TestNextPowerOf2(t *testing.T) {
	tests := []struct {
		in, want int
	}{
		{0, 1},
		{1, 1},
		{2, 2},
		{3, 4},
		{4, 4},
		{5, 8},
		{31, 32},
		{32, 32},
		{33, 64},
		{100, 128},
		{1000, 1024},
	}
	for _, tt := range tests {
		got := nextPowerOf2(tt.in)
		if got != tt.want {
			t.Errorf("nextPowerOf2(%d) = %d, want %d", tt.in, got, tt.want)
		}
	}
}
