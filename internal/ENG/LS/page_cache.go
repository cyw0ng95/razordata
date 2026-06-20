package ls

import (
	"sync"
	"sync/atomic"
)

const (
	// DefaultPageCacheSize is the default page cache capacity (256 MB).
	DefaultPageCacheSize = 256 * 1024 * 1024
	// PageSize is the size of each cached page (4 KB).
	PageSize = 4 * 1024
)

// pageKey uniquely identifies a block within an SST file.
type pageKey struct {
	fileID uint64
	offset uint32
}

// pageEntry is a single slot in the page cache.
type pageEntry struct {
	key     pageKey
	data    []byte
	ref     atomic.Int32
	visited atomic.Int32 // clock-sweep hand
	valid   atomic.Bool
}

// PageCache is a fixed-size block-level cache for SST file pages.
// It uses clock-sweep eviction and sync.Pool for page buffers
// (REQ000571).
type PageCache struct {
	mu      sync.RWMutex
	slots   []*pageEntry
	cap     int // max number of pages
	size    int // current number of valid pages
	hand    int // clock-sweep hand position
	bufPool sync.Pool
}

// NewPageCache creates a page cache with the given capacity in bytes.
// The capacity is rounded down to a multiple of PageSize.
func NewPageCache(capacityBytes int) *PageCache {
	if capacityBytes < PageSize {
		capacityBytes = PageSize
	}
	cap := capacityBytes / PageSize
	c := &PageCache{
		slots: make([]*pageEntry, cap),
		cap:   cap,
	}
	c.bufPool = sync.Pool{
		New: func() any {
			buf := make([]byte, PageSize)
			return &buf
		},
	}
	for i := range c.slots {
		c.slots[i] = &pageEntry{}
	}
	return c
}

// Get retrieves a cached page for the given fileID and offset.
// Returns the page data and true if found, nil and false otherwise.
func (c *PageCache) Get(fileID uint64, offset uint32) ([]byte, bool) {
	key := pageKey{fileID: fileID, offset: offset}
	c.mu.RLock()
	defer c.mu.RUnlock()

	for _, slot := range c.slots {
		if slot.valid.Load() && slot.key == key {
			slot.visited.Store(1)
			slot.ref.Add(1)
			data := slot.data
			slot.ref.Add(-1)
			return data, true
		}
	}
	return nil, false
}

// Put inserts a page into the cache. If the cache is full, evicts
// a page using clock-sweep. The data is copied into a pooled buffer.
func (c *PageCache) Put(fileID uint64, offset uint32, data []byte) {
	if len(data) == 0 {
		return
	}
	key := pageKey{fileID: fileID, offset: offset}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Check if already cached.
	for _, slot := range c.slots {
		if slot.valid.Load() && slot.key == key {
			// Already cached; update data.
			copy(slot.data, data)
			slot.visited.Store(1)
			return
		}
	}

	// Find a free slot or evict.
	slot := c.evict()
	slot.key = key
	if slot.data == nil {
		bufPtr := c.bufPool.Get().(*[]byte)
		slot.data = *bufPtr
	}
	n := copy(slot.data, data)
	// Zero-pad if data is shorter than PageSize.
	for i := n; i < len(slot.data); i++ {
		slot.data[i] = 0
	}
	slot.visited.Store(1)
	slot.ref.Store(0)
	slot.valid.Store(true)
	c.size++
}

// evict returns a free or evicted slot using clock-sweep.
func (c *PageCache) evict() *pageEntry {
	// First pass: look for invalid (free) slot.
	for i := 0; i < c.cap; i++ {
		if !c.slots[i].valid.Load() {
			return c.slots[i]
		}
	}
	// Clock-sweep eviction.
	for {
		slot := c.slots[c.hand]
		c.hand = (c.hand + 1) % c.cap
		if slot.ref.Load() > 0 {
			continue
		}
		if slot.visited.CompareAndSwap(1, 0) {
			continue
		}
		// Evict this slot.
		slot.valid.Store(false)
		c.size--
		return slot
	}
}

// Invalidate removes all cached pages for the given fileID.
// Called when an SST file is compacted or deleted.
func (c *PageCache) Invalidate(fileID uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, slot := range c.slots {
		if slot.valid.Load() && slot.key.fileID == fileID {
			slot.valid.Store(false)
			c.size--
		}
	}
}

// Len returns the number of valid cached pages.
func (c *PageCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.size
}
