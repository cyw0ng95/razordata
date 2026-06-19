package ls

import (
	"container/list"
	"sync"
)

// l0Cache is a dedicated LRU for recently-read L0 SST blocks (REQ000540).
type l0Cache struct {
	mu       sync.Mutex
	capacity int
	items    map[uint64]*list.Element
	lru      *list.List
}

type cacheEntry struct {
	blockID uint64
	data    []byte
}

// newL0Cache creates an L0 block cache with the given capacity.
func newL0Cache(capacity int) *l0Cache {
	if capacity <= 0 {
		capacity = 1
	}
	return &l0Cache{
		capacity: capacity,
		items:    make(map[uint64]*list.Element, capacity),
		lru:      list.New(),
	}
}

// Get returns the cached block for blockID.
func (c *l0Cache) Get(blockID uint64) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	elem, ok := c.items[blockID]
	if !ok {
		return nil, false
	}
	c.lru.MoveToFront(elem)
	return elem.Value.(*cacheEntry).data, true
}

// Put inserts or refreshes a block in the cache.
func (c *l0Cache) Put(blockID uint64, data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if elem, ok := c.items[blockID]; ok {
		entry := elem.Value.(*cacheEntry)
		entry.data = data
		c.lru.MoveToFront(elem)
		return
	}
	entry := &cacheEntry{blockID: blockID, data: data}
	elem := c.lru.PushFront(entry)
	c.items[blockID] = elem
	for c.lru.Len() > c.capacity {
		c.evictBack()
	}
}

func (c *l0Cache) Size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

func (c *l0Cache) evictBack() {
	elem := c.lru.Back()
	if elem == nil {
		return
	}
	entry := elem.Value.(*cacheEntry)
	c.lru.Remove(elem)
	delete(c.items, entry.blockID)
}
