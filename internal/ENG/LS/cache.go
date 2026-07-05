package ls

import (
	"container/list"
	"strings"
	"sync"
)

// lruBlockEntry is the value stored in the LRU list.
type lruBlockEntry struct {
	key  string
	data []byte
}

// BlockCache is an LRU cache for decompressed SST data blocks (REQ001242).
// Keys use the format "sstPath:blockIdx".
type BlockCache struct {
	mu       sync.Mutex
	entries  map[string]*list.Element
	lru      *list.List
	capacity int
}

// NewBlockCache creates a BlockCache with the given capacity (number of blocks).
// A capacity of 0 disables caching entirely.
func NewBlockCache(capacity int) *BlockCache {
	return &BlockCache{
		entries:  make(map[string]*list.Element),
		lru:      list.New(),
		capacity: capacity,
	}
}

// Get returns a copy of the cached data for key and true on hit,
// or nil/false on miss. MoveToFront updates LRU order.
func (c *BlockCache) Get(key string) ([]byte, bool) {
	if c.capacity == 0 {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	c.lru.MoveToFront(elem)
	entry := elem.Value.(*lruBlockEntry)
	out := make([]byte, len(entry.data))
	copy(out, entry.data)
	return out, true
}

// Put inserts or updates a block in the cache. If the cache is at capacity,
// the least-recently-used entry is evicted first.
func (c *BlockCache) Put(key string, data []byte) {
	if c.capacity == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.entries[key]; ok {
		c.lru.MoveToFront(elem)
		elem.Value.(*lruBlockEntry).data = data
		return
	}

	for c.lru.Len() >= c.capacity {
		c.evictBack()
	}

	entry := &lruBlockEntry{key: key, data: data}
	c.entries[key] = c.lru.PushFront(entry)
}

// Evict removes all cached entries whose key starts with pathPrefix + ":".
func (c *BlockCache) Evict(pathPrefix string) {
	if c.capacity == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	prefix := pathPrefix + ":"
	for key, elem := range c.entries {
		if strings.HasPrefix(key, prefix) {
			c.lru.Remove(elem)
			delete(c.entries, key)
		}
	}
}

// Len returns the number of entries in the cache.
func (c *BlockCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lru.Len()
}

func (c *BlockCache) evictBack() {
	back := c.lru.Back()
	if back == nil {
		return
	}
	entry := back.Value.(*lruBlockEntry)
	c.lru.Remove(back)
	delete(c.entries, entry.key)
}
