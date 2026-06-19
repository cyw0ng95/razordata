package PL

import (
	"fmt"
	"sync"
	"testing"
)

type fakePlan struct {
	id int
}

func TestPredicateCache_PutGet(t *testing.T) {
	c := NewPredicateCache(4)
	p := &fakePlan{id: 1}
	c.Put("k1", p)

	got, ok := c.Get("k1")
	if !ok {
		t.Fatal("expected hit for k1")
	}
	fp, ok := got.(*fakePlan)
	if !ok || fp.id != 1 {
		t.Fatalf("expected plan with id=1, got %T %+v", got, got)
	}
}

func TestPredicateCache_MissReturnsFalse(t *testing.T) {
	c := NewPredicateCache(4)
	got, ok := c.Get("nope")
	if ok {
		t.Fatal("expected miss")
	}
	if got != nil {
		t.Fatalf("expected nil plan on miss, got %v", got)
	}
}

func TestPredicateCache_PutRefreshesExisting(t *testing.T) {
	c := NewPredicateCache(4)
	c.Put("k", &fakePlan{id: 1})
	c.Put("k", &fakePlan{id: 2})

	got, ok := c.Get("k")
	if !ok {
		t.Fatal("expected hit after refresh")
	}
	fp := got.(*fakePlan)
	if fp.id != 2 {
		t.Fatalf("expected refreshed plan id=2, got %d", fp.id)
	}
	if got := c.Size(); got != 1 {
		t.Fatalf("expected size 1 after refresh, got %d", got)
	}
}

func TestPredicateCache_GetPromotesLRU(t *testing.T) {
	c := NewPredicateCache(2)
	c.Put("a", &fakePlan{id: 1})
	c.Put("b", &fakePlan{id: 2})
	if _, ok := c.Get("a"); !ok {
		t.Fatal("expected hit on a")
	}

	c.Put("c", &fakePlan{id: 3})

	if _, ok := c.Get("b"); ok {
		t.Fatal("expected b to be evicted after Get(a) promoted a")
	}
	if _, ok := c.Get("a"); !ok {
		t.Fatal("expected a to survive")
	}
	if _, ok := c.Get("c"); !ok {
		t.Fatal("expected c to be present")
	}
}

func TestPredicateCache_EvictsLRUAtCapacity(t *testing.T) {
	c := NewPredicateCache(2)
	c.Put("a", &fakePlan{id: 1})
	c.Put("b", &fakePlan{id: 2})
	c.Put("c", &fakePlan{id: 3})

	if _, ok := c.Get("a"); ok {
		t.Fatal("expected a to be evicted (LRU)")
	}
	if _, ok := c.Get("b"); !ok {
		t.Fatal("b should still be present")
	}
	if _, ok := c.Get("c"); !ok {
		t.Fatal("c should still be present")
	}
	if got := c.Size(); got != 2 {
		t.Fatalf("expected size 2, got %d", got)
	}
}

func TestPredicateCache_ZeroCapacityDefaultsToOne(t *testing.T) {
	c := NewPredicateCache(0)
	c.Put("a", &fakePlan{id: 1})
	c.Put("b", &fakePlan{id: 2})

	if _, ok := c.Get("a"); ok {
		t.Fatal("a should have been evicted at clamped capacity 1")
	}
	if got := c.Size(); got != 1 {
		t.Fatalf("expected size 1, got %d", got)
	}
}

func TestPredicateCache_NegativeCapacityDefaultsToOne(t *testing.T) {
	c := NewPredicateCache(-10)
	c.Put("a", &fakePlan{id: 1})
	c.Put("b", &fakePlan{id: 2})

	if _, ok := c.Get("a"); ok {
		t.Fatal("a should have been evicted at clamped capacity 1")
	}
	if got := c.Size(); got != 1 {
		t.Fatalf("expected size 1, got %d", got)
	}
}

func TestPredicateCache_EmptySize(t *testing.T) {
	c := NewPredicateCache(4)
	if got := c.Size(); got != 0 {
		t.Fatalf("expected empty size 0, got %d", got)
	}
}

func TestPredicateCache_PutNilPlan(t *testing.T) {
	c := NewPredicateCache(4)
	c.Put("nilkey", nil)

	got, ok := c.Get("nilkey")
	if !ok {
		t.Fatal("expected hit even with nil plan")
	}
	if got != nil {
		t.Fatalf("expected nil plan to round-trip, got %v", got)
	}
}

func TestPredicateCache_DistinguishesKeys(t *testing.T) {
	c := NewPredicateCache(8)
	for i := 0; i < 5; i++ {
		key := fmt.Sprintf("k%d", i)
		c.Put(key, &fakePlan{id: i})
	}
	for i := 0; i < 5; i++ {
		key := fmt.Sprintf("k%d", i)
		got, ok := c.Get(key)
		if !ok {
			t.Fatalf("expected hit on %s", key)
		}
		if got.(*fakePlan).id != i {
			t.Fatalf("expected id=%d on %s, got %d", i, key, got.(*fakePlan).id)
		}
	}
}

func TestPredicateCache_ConcurrentAccess(t *testing.T) {
	const goroutines = 16
	const opsPerGoroutine = 200
	const capacity = 64
	c := NewPredicateCache(capacity)

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				key := fmt.Sprintf("g%d-i%d", g, i)
				c.Put(key, &fakePlan{id: g*1000 + i})
				c.Get(key)
			}
		}(g)
	}
	wg.Wait()

	if got := c.Size(); got > capacity {
		t.Fatalf("size %d exceeds capacity %d", got, capacity)
	}
}

func TestPredicateCache_ConcurrentSameKey(t *testing.T) {
	const goroutines = 16
	const opsPerGoroutine = 100
	c := NewPredicateCache(8)

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				c.Put("hot", &fakePlan{id: g*1000 + i})
				c.Get("hot")
			}
		}(g)
	}
	wg.Wait()

	if got := c.Size(); got != 1 {
		t.Fatalf("expected size 1 after concurrent same-key writes, got %d", got)
	}
}

func TestPredicateCache_GetAfterEvictionReturnsMiss(t *testing.T) {
	c := NewPredicateCache(1)
	c.Put("a", &fakePlan{id: 1})
	c.Put("b", &fakePlan{id: 2})

	if _, ok := c.Get("a"); ok {
		t.Fatal("expected miss on evicted key")
	}
	if _, ok := c.Get("b"); !ok {
		t.Fatal("expected hit on most-recent key")
	}
}
