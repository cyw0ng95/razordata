package ls

import (
	"container/list"
	"sync"
)

// REQ000540: dedicated LRU for recently-read L0 SST blocks.
//
// The L0 merge step in NewIterator walks every L0 file in order and
// seeks into each one. Without a block cache, every seek re-reads the
// block from disk (and every block re-fetches the index page). This
// cache sits between the SST reader and the OS page cache and pins
// recently-touched blocks in memory so a back-to-back merge of the
// same L0 set hits the cache instead of the disk.
//
// The cache is deliberately tiny and self-contained: it is not wired
// into the engine yet (REQ000540 ships the type only; integration
// lands in a follow-up). Block identity is the raw blockID the SST
// layer assigns (offset/crc pair), which is stable for the lifetime
// of the file.
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

// newL0Cache creates an L0 block cache with the given capacity
// (number of blocks). A capacity <= 0 is clamped to 1 so the cache
// is always usable but never silently accepts everything.
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

// Get returns the cached block for blockID. On hit the entry is
// promoted to the front of the LRU; on miss the returned bool is
// false and the slice is nil. The returned slice aliases the cache's
// backing buffer — callers that need to mutate must copy.
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

// Put inserts (or refreshes) a block. If the blockID is already
// present the data slice is replaced and the entry is promoted. If
// the cache is at capacity the least-recently-used entry is evicted.
// The cache does not copy the data slice — the caller transfers
// ownership.
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

// Size returns the number of blocks currently cached.
func (c *l0Cache) Size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

// evictBack removes the least-recently-used entry. Caller must hold mu.
func (c *l0Cache) evictBack() {
	elem := c.lru.Back()
	if elem == nil {
		return
	}
	entry := elem.Value.(*cacheEntry)
	c.lru.Remove(elem)
	delete(c.items, entry.blockID)
}
