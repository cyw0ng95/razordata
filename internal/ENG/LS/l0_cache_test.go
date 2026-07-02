package ls

import (
	"bytes"
	"runtime"
	"sync"
	"testing"
)

func TestL0Cache_PutGet(t *testing.T) {
	c := newL0Cache(4)
	data := []byte("hello-block")
	c.Put(1, data)

	got, ok := c.Get(1)
	if !ok {
		t.Fatal("expected hit for blockID 1")
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("data mismatch: got %q want %q", got, data)
	}
}

func TestL0Cache_MissReturnsFalse(t *testing.T) {
	c := newL0Cache(4)
	got, ok := c.Get(999)
	if ok {
		t.Fatal("expected miss")
	}
	if got != nil {
		t.Fatalf("expected nil data on miss, got %v", got)
	}
}

func TestL0Cache_GetPromotesLRU(t *testing.T) {
	// Need per-shard capacity >= 2 to exercise intra-shard LRU promotion.
	tmp := newL0Cache(1)
	capacity := int(tmp.NShard()) * 2
	c := newL0Cache(capacity)
	perShard := c.shards[0].cap
	if perShard < 2 {
		t.Skipf("per-shard capacity %d < 2", perShard)
	}
	nshard := c.NShard()
	b1, b2, b3 := uint64(0), uint64(nshard), uint64(2*nshard) // all shard 0
c.Put(b1, []byte("a"))
	c.Put(b2, []byte("b"))
	if got, _ := c.Get(b1); !bytes.Equal(got, []byte("a")) {
		t.Fatalf("unexpected get(%d): %q", b1, got)
	}
	c.Put(b3, []byte("c"))

	if _, ok := c.Get(b2); ok {
		t.Fatal("expected b2 to be evicted after Get(b1) promoted b1 to front")
	}
	if got, ok := c.Get(b1); !ok || !bytes.Equal(got, []byte("a")) {
		t.Fatalf("expected b1 to survive: ok=%v got=%q", ok, got)
	}
	if got, ok := c.Get(b3); !ok || !bytes.Equal(got, []byte("c")) {
		t.Fatalf("expected b3 to be present: ok=%v got=%q", ok, got)
	}
}

func TestL0Cache_PutRefreshesExisting(t *testing.T) {
	c := newL0Cache(4)
	c.Put(7, []byte("first"))
	c.Put(7, []byte("second"))

	got, ok := c.Get(7)
	if !ok {
		t.Fatal("expected hit after refresh")
	}
	if !bytes.Equal(got, []byte("second")) {
		t.Fatalf("expected refreshed data, got %q", got)
	}
	if c.Size() != 1 {
		t.Fatalf("expected size 1 after refresh, got %d", c.Size())
	}
}

func TestL0Cache_EvictsLRUAtCapacity(t *testing.T) {
	tmp := newL0Cache(1)
	capacity := int(tmp.NShard()) * 2 // per-shard cap = 2
	c := newL0Cache(capacity)
	nshard := c.NShard()
	b1, b2, b3 := uint64(0), uint64(nshard), uint64(2*nshard) // all shard 0
c.Put(b1, []byte("a"))
	c.Put(b2, []byte("b"))
	c.Put(b3, []byte("c"))

	if _, ok := c.Get(b1); ok {
		t.Fatal("expected b1 to be evicted (LRU)")
	}
	if _, ok := c.Get(b2); !ok {
		t.Fatal("b2 should still be present")
	}
	if _, ok := c.Get(b3); !ok {
		t.Fatal("b3 should still be present")
	}
	if got := c.Size(); got != 2 {
		t.Fatalf("expected size 2, got %d", got)
	}
}

func TestL0Cache_ZeroCapacityDefaultsToOne(t *testing.T) {
	c := newL0Cache(0)
	n := uint64(c.NShard())
c.Put(0*n, []byte("a"))
	c.Put(1*n, []byte("b"))

	if _, ok := c.Get(0*n); ok {
		t.Fatal("block 0*n should have been evicted at capacity 1")
	}
	if _, ok := c.Get(1*n); !ok {
		t.Fatal("block 1*n should be present")
	}
	if got := c.Size(); got != 1 {
		t.Fatalf("expected size 1, got %d", got)
	}
}

func TestL0Cache_NegativeCapacityDefaultsToOne(t *testing.T) {
	c := newL0Cache(-5)
	n := uint64(c.NShard())
	c.Put(0*n, []byte("a"))
	c.Put(1*n, []byte("b")) // same shard 0, should evict "a"

	if _, ok := c.Get(0*n); ok {
		t.Fatal("block 0*n should have been evicted at clamped capacity 1")
	}
	if got := c.Size(); got != 1 {
		t.Fatalf("expected size 1, got %d", got)
	}
}

func TestL0Cache_EmptySize(t *testing.T) {
	c := newL0Cache(4)
	if got := c.Size(); got != 0 {
		t.Fatalf("expected empty cache size 0, got %d", got)
	}
}

func TestL0Cache_PutNilData(t *testing.T) {
	c := newL0Cache(4)
	c.Put(42, nil)

	got, ok := c.Get(42)
	if !ok {
		t.Fatal("expected hit even with nil data")
	}
	if got != nil {
		t.Fatalf("expected nil data to round-trip, got %v", got)
	}
}

func TestL0Cache_LargeBlocks(t *testing.T) {
	c := newL0Cache(2)
	big := make([]byte, 64*1024)
	for i := range big {
		big[i] = byte(i % 251)
	}
	c.Put(1, big)
	c.Put(2, big)

	got, ok := c.Get(1)
	if !ok || !bytes.Equal(got, big) {
		t.Fatalf("large block round-trip failed: ok=%v len=%d", ok, len(got))
	}
}

func TestL0Cache_ConcurrentAccess(t *testing.T) {
	const goroutines = 16
	const opsPerGoroutine = 200
	const capacity = 64
	c := newL0Cache(capacity)

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				id := uint64(g*opsPerGoroutine + i)
				c.Put(id, []byte{byte(g), byte(i)})
				c.Get(id)
			}
		}(g)
	}
	wg.Wait()

	if got := c.Size(); got > capacity {
		t.Fatalf("size %d exceeds capacity %d", got, capacity)
	}
}

func TestL0Cache_ConcurrentSameKey(t *testing.T) {
	const goroutines = 16
	const opsPerGoroutine = 100
	c := newL0Cache(8)

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				c.Put(42, []byte{byte(g), byte(i)})
				c.Get(42)
			}
		}(g)
	}
	wg.Wait()

	if got := c.Size(); got != 1 {
		t.Fatalf("expected size 1 after concurrent same-key writes, got %d", got)
	}
}

func TestL0Cache_GetAfterEvictionReturnsMiss(t *testing.T) {
	c := newL0Cache(1)
	n := uint64(c.NShard())
	c.Put(0*n, []byte("a"))
	c.Put(1*n, []byte("b"))

	if _, ok := c.Get(0*n); ok {
		t.Fatal("expected miss on evicted blockID 0*n")
	}
	if _, ok := c.Get(1*n); !ok {
		t.Fatal("expected hit on most-recent blockID 1*n")
	}
}

func TestL0Cache_ShardsMatchGOMAXPROCS(t *testing.T) {
	c := newL0Cache(64)
	n := runtime.GOMAXPROCS(0)
	if int(c.NShard()) != n {
		t.Fatalf("expected %d shards, got %d", n, c.NShard())
	}
}

func TestL0Cache_PerShardCapacity(t *testing.T) {
	total := 100
	c := newL0Cache(total)
	n := c.NShard()
	perShard := total / int(n)
	if perShard == 0 {
		perShard = 1
	}
	if c.shards[0].cap != perShard {
		t.Fatalf("expected per-shard cap %d, got %d", perShard, c.shards[0].cap)
	}
}
