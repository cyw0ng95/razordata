package ls

import (
	"bytes"
	"fmt"
	"sync"
	"testing"
)

func TestBlockCache_GetPut(t *testing.T) {
	c := NewBlockCache(4)

	// Put two entries
	c.Put("sst-1.razor:0", []byte("block0"))
	c.Put("sst-1.razor:1", []byte("block1"))

	// Get hit
	data, ok := c.Get("sst-1.razor:0")
	if !ok {
		t.Fatal("expected hit")
	}
	if !bytes.Equal(data, []byte("block0")) {
		t.Fatalf("got %q, want %q", data, "block0")
	}

	// Get miss
	_, ok = c.Get("sst-1.razor:99")
	if ok {
		t.Fatal("expected miss")
	}
}

func TestBlockCache_LRUEviction(t *testing.T) {
	c := NewBlockCache(2)

	c.Put("a:0", []byte("A"))
	c.Put("b:0", []byte("B"))

	// Access "a" to make it recently used
	c.Get("a:0")

	// Insert "c" should evict "b" (LRU)
	c.Put("c:0", []byte("C"))

	_, ok := c.Get("b:0")
	if ok {
		t.Fatal("b:0 should have been evicted")
	}

	data, ok := c.Get("a:0")
	if !ok || !bytes.Equal(data, []byte("A")) {
		t.Fatal("a:0 should survive")
	}

	data, ok = c.Get("c:0")
	if !ok || !bytes.Equal(data, []byte("C")) {
		t.Fatal("c:0 should be present")
	}
}

func TestBlockCache_EvictByPath(t *testing.T) {
	c := NewBlockCache(10)
	c.Put("sst-a.razor:0", []byte("A0"))
	c.Put("sst-a.razor:1", []byte("A1"))
	c.Put("sst-b.razor:0", []byte("B0"))

	c.Evict("sst-a.razor")

	_, ok := c.Get("sst-a.razor:0")
	if ok {
		t.Fatal("sst-a.razor:0 should be evicted")
	}
	_, ok = c.Get("sst-a.razor:1")
	if ok {
		t.Fatal("sst-a.razor:1 should be evicted")
	}
	data, ok := c.Get("sst-b.razor:0")
	if !ok || !bytes.Equal(data, []byte("B0")) {
		t.Fatal("sst-b.razor:0 should survive")
	}
}

func TestBlockCache_Concurrent(t *testing.T) {
	c := NewBlockCache(64)
	var wg sync.WaitGroup
	for g := 0; g < 50; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				key := fmt.Sprintf("sst-%d.razor:%d", id, i)
				c.Put(key, []byte(key))
				c.Get(key)
			}
		}(g)
	}
	wg.Wait()
}

func BenchmarkBlockCache_GetPut(b *testing.B) {
	c := NewBlockCache(1024)
	val := make([]byte, 4096)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("bench.razor:%d", i%1024)
		c.Put(key, val)
		c.Get(key)
	}
}

func TestBlockCache_CapacityZero(t *testing.T) {
	c := NewBlockCache(0)

	c.Put("a:0", []byte("A"))
	_, ok := c.Get("a:0")
	if ok {
		t.Fatal("capacity=0 should be disabled; Get must miss")
	}
	if c.Len() != 0 {
		t.Fatalf("expected Len=0, got %d", c.Len())
	}
}
