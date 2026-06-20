package ls

import (
	"bytes"
	"testing"
)

func TestPageCache_PutGet(t *testing.T) {
	c := NewPageCache(DefaultPageCacheSize)
	data := make([]byte, PageSize)
	for i := range data {
		data[i] = byte(i % 256)
	}
	c.Put(1, 0, data)
	got, ok := c.Get(1, 0)
	if !ok {
		t.Fatal("expected cache hit")
	}
	if !bytes.Equal(got, data) {
		t.Fatal("data mismatch")
	}
}

func TestPageCache_Miss(t *testing.T) {
	c := NewPageCache(DefaultPageCacheSize)
	_, ok := c.Get(99, 0)
	if ok {
		t.Fatal("expected cache miss")
	}
}

func TestPageCache_Eviction(t *testing.T) {
	c := NewPageCache(PageSize * 2) // only 2 slots
	d1 := bytes.Repeat([]byte{1}, PageSize)
	d2 := bytes.Repeat([]byte{2}, PageSize)
	d3 := bytes.Repeat([]byte{3}, PageSize)
	c.Put(1, 0, d1)
	c.Put(2, 0, d2)
	c.Put(3, 0, d3) // should evict one
	if c.Len() > 2 {
		t.Fatalf("expected at most 2 entries, got %d", c.Len())
	}
}

func TestPageCache_Invalidate(t *testing.T) {
	c := NewPageCache(DefaultPageCacheSize)
	c.Put(1, 0, bytes.Repeat([]byte{1}, PageSize))
	c.Put(1, PageSize, bytes.Repeat([]byte{2}, PageSize))
	c.Put(2, 0, bytes.Repeat([]byte{3}, PageSize))
	c.Invalidate(1)
	if c.Len() != 1 {
		t.Fatalf("expected 1 entry after invalidate, got %d", c.Len())
	}
	_, ok := c.Get(1, 0)
	if ok {
		t.Fatal("expected miss after invalidate")
	}
	_, ok = c.Get(2, 0)
	if !ok {
		t.Fatal("expected hit for unaffected file")
	}
}

func TestPageCache_Update(t *testing.T) {
	c := NewPageCache(DefaultPageCacheSize)
	d1 := bytes.Repeat([]byte{1}, PageSize)
	d2 := bytes.Repeat([]byte{2}, PageSize)
	c.Put(1, 0, d1)
	c.Put(1, 0, d2) // update same key
	got, ok := c.Get(1, 0)
	if !ok {
		t.Fatal("expected cache hit")
	}
	if !bytes.Equal(got, d2) {
		t.Fatal("expected updated data")
	}
	if c.Len() != 1 {
		t.Fatalf("expected 1 entry, got %d", c.Len())
	}
}

func TestPageCache_EmptyData(t *testing.T) {
	c := NewPageCache(DefaultPageCacheSize)
	c.Put(1, 0, nil) // should be no-op
	if c.Len() != 0 {
		t.Fatal("expected 0 entries")
	}
}

func TestPageCache_Concurrent(t *testing.T) {
	c := NewPageCache(DefaultPageCacheSize)
	done := make(chan struct{})
	for g := 0; g < 4; g++ {
		go func(id uint64) {
			defer func() { done <- struct{}{} }()
			data := bytes.Repeat([]byte{byte(id)}, PageSize)
			for i := 0; i < 100; i++ {
				c.Put(id, uint32(i*PageSize), data)
				c.Get(id, uint32(i*PageSize))
			}
		}(uint64(g))
	}
	for i := 0; i < 4; i++ {
		<-done
	}
}
