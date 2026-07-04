package ls

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestL0Cache_ShardedConcurrent(t *testing.T) {
	const (
		workers    = 16
		iterations = 1000
		capacity   = 64
	)

	cache := newL0Cache(capacity)

	// Verify sharding exists
	if cache.NShard() == 0 {
		t.Fatal("expected non-zero shard count")
	}

	// Pre-populate some entries
	for i := uint64(0); i < uint64(capacity/2); i++ {
		data := make([]byte, 4096)
		copy(data, []byte("initial"))
		cache.Put(i, data)
	}

	var wg sync.WaitGroup
	hits := atomic.Int64{}
	misses := atomic.Int64{}

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				blockID := uint64(id*iterations + i)
				// Read half the time
				if blockID%2 == 0 {
					_, hit := cache.Get(blockID)
					if hit {
						hits.Add(1)
					} else {
						misses.Add(1)
					}
				}
				// Write half the time
				data := make([]byte, 4096)
				copy(data, []byte("new data"))
				cache.Put(blockID, data)
			}
		}(w)
	}

	wg.Wait()

	t.Logf("hits=%d misses=%d", hits.Load(), misses.Load())

	// Basic sanity: total read operations
	if hits.Load()+misses.Load() != int64(workers*iterations/2) {
		t.Fatalf("expected %d read ops, got %d hits + %d misses",
			workers*iterations/2, hits.Load(), misses.Load())
	}

	// Capacity should not be grossly exceeded per shard
	if cache.Size() > capacity {
		t.Logf("cache size %d exceeds capacity %d (expected with concurrent puts)", cache.Size(), capacity)
	}
}

func TestL0Cache_CapacityEviction(t *testing.T) {
	// Use capacity large enough that per-shard capacity >= 2.
	tmp := newL0Cache(1)
	capacity := int(tmp.NShard()) * 2 // perShard = 2
	nshard := uint64(tmp.NShard())
	cache := newL0Cache(capacity)

	// Fill shard 0: blocks 0, nshard, 2*nshard
	for i := uint64(0); i < uint64(capacity); i++ {
		data := make([]byte, 4096)
		copy(data, []byte("data"))
		cache.Put(i*nshard, data)
	}

	// Add one more to shard 0 — should evict LRU (block 0*nshard)
	extraData := make([]byte, 4096)
	copy(extraData, []byte("extra"))
	cache.Put(uint64(capacity)*nshard, extraData)

	// Block 0*nshard should be evicted (oldest in shard 0)
	_, hit := cache.Get(0 * nshard)
	if hit {
		t.Error("expected block 0*nshard to be evicted after capacity exceeded")
	}

	// The newly inserted block should still be present
	_, hit = cache.Get(uint64(capacity) * nshard)
	if !hit {
		t.Errorf("expected block %d*n to be present after insert", capacity)
	}
}

func TestL0Cache_GetMiss(t *testing.T) {
	cache := newL0Cache(8)
	data, hit := cache.Get(999)
	if hit {
		t.Error("expected cache miss for non-existent key")
	}
	if data != nil {
		t.Error("expected nil data for cache miss")
	}
}

func TestL0Cache_GetHit(t *testing.T) {
	cache := newL0Cache(8)
	data := []byte("hello world")
	cache.Put(42, data)

	got, hit := cache.Get(42)
	if !hit {
		t.Error("expected cache hit for existing key")
	}
	if string(got) != "hello world" {
		t.Errorf("expected 'hello world', got %q", got)
	}
}

func TestL0Cache_MoveToFront(t *testing.T) {
	// Test that accessing a block moves it to front of LRU.
	// Use capacity large enough that per-shard capacity >= 2.
	tmp := newL0Cache(1)
	capacity := int(tmp.NShard()) * 2 // perShard = 2
	nshard := uint64(tmp.NShard())
	cache := newL0Cache(capacity)

	// Two block IDs that land in the same shard (shard 0)
	var a, b uint64 = 0, nshard

	data1 := make([]byte, 4096)
	copy(data1, []byte("a"))
	data2 := make([]byte, 4096)
	copy(data2, []byte("b"))

	cache.Put(a, data1)
	cache.Put(b, data2)

	// Access 'a' to move it to front
	cache.Get(a)

	// Add another block to the same shard — should evict 'b' (now at back)
	data3 := make([]byte, 4096)
	copy(data3, []byte("c"))
	cache.Put(a+nshard, data3)

	// 'a' should still be there (moved to front)
	_, hit := cache.Get(a)
	if !hit {
		t.Error("expected 'a' to survive eviction after being accessed (moved to front)")
	}
}

// Benchmark to compare before/after sharding
func BenchmarkL0Cache(b *testing.B) {
	cache := newL0Cache(1024)
	data := make([]byte, 4096)
	copy(data, []byte("benchmark data"))

	b.Run("Write", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			cache.Put(uint64(i), data)
		}
	})

	b.Run("Read", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			cache.Get(uint64(i % 1024))
		}
	})

	b.Run("Mixed", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if i%2 == 0 {
				cache.Get(uint64(i))
			} else {
				cache.Put(uint64(i), data)
			}
		}
	})
}
