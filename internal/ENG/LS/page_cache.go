package ls

import (
	"runtime"
	"sync"
	"sync/atomic"
)

const (
	DefaultPageCacheSize = 256 * 1024 * 1024
	PageSize             = 4 * 1024
)

type pageKey struct {
	fileID uint64
	offset uint32
}

type pageEntry struct {
	key     pageKey
	data    []byte
	ref     atomic.Int32
	visited atomic.Int32
	valid   atomic.Bool
}

type pageCacheShard struct {
	mu      sync.RWMutex
	slots   []*pageEntry
	index   map[pageKey]*pageEntry
	cap     int
	size    int
	hand    int
	bufPool sync.Pool
}

type PageCache struct {
	shards []*pageCacheShard
	nshard uint32
}

func NewPageCache(capacityBytes int) *PageCache {
	if capacityBytes < PageSize {
		capacityBytes = PageSize
	}
	n := runtime.GOMAXPROCS(0)
	if n < 1 {
		n = 1
	}
	capPerShard := (capacityBytes / PageSize) / n
	if capPerShard < 1 {
		capPerShard = 1
	}
	shards := make([]*pageCacheShard, n)
	for i := range shards {
		s := &pageCacheShard{
			cap: capPerShard,
		}
		s.slots = make([]*pageEntry, s.cap)
		s.index = make(map[pageKey]*pageEntry, s.cap)
		s.bufPool = sync.Pool{
			New: func() any {
				buf := make([]byte, PageSize)
				return &buf
			},
		}
		for j := range s.slots {
			s.slots[j] = &pageEntry{}
		}
		shards[i] = s
	}
	return &PageCache{
		shards: shards,
		nshard: uint32(n),
	}
}

func (c *PageCache) shardIndex(key pageKey) int {
	return int((key.fileID*31 + uint64(key.offset)) % uint64(c.nshard))
}

func (c *PageCache) Get(fileID uint64, offset uint32) ([]byte, bool) {
	key := pageKey{fileID: fileID, offset: offset}
	s := c.shards[c.shardIndex(key)]
	s.mu.RLock()
	defer s.mu.RUnlock()
	if slot, ok := s.index[key]; ok {
		if slot.valid.Load() {
			slot.visited.Store(1)
			return slot.data, true
		}
	}
	return nil, false
}

func (c *PageCache) Put(fileID uint64, offset uint32, data []byte) {
	if len(data) == 0 {
		return
	}
	key := pageKey{fileID: fileID, offset: offset}
	s := c.shards[c.shardIndex(key)]

	s.mu.Lock()
	defer s.mu.Unlock()

	if slot, ok := s.index[key]; ok {
		copy(slot.data, data)
		slot.visited.Store(1)
		return
	}

	slot := s.evict()
	if slot.valid.Swap(false) {
		delete(s.index, slot.key)
		s.size--
	}
	slot.key = key
	if slot.data == nil {
		bufPtr := s.bufPool.Get().(*[]byte)
		slot.data = *bufPtr
	}
	n := copy(slot.data, data)
	for i := n; i < len(slot.data); i++ {
		slot.data[i] = 0
	}
	slot.visited.Store(1)
	slot.ref.Store(0)
	slot.valid.Store(true)
	s.index[key] = slot
	s.size++
}

func (s *pageCacheShard) evict() *pageEntry {
	for i := range s.cap {
		if !s.slots[i].valid.Load() {
			return s.slots[i]
		}
	}
	for {
		slot := s.slots[s.hand]
		s.hand = (s.hand + 1) % s.cap
		if slot.ref.Load() > 0 {
			continue
		}
		if slot.visited.CompareAndSwap(1, 0) {
			continue
		}
		return slot
	}
}

func (c *PageCache) Invalidate(fileID uint64) {
	for _, s := range c.shards {
		s.mu.Lock()
		for _, slot := range s.slots {
			if slot.valid.Load() && slot.key.fileID == fileID {
				delete(s.index, slot.key)
				slot.valid.Store(false)
				s.size--
			}
		}
		s.mu.Unlock()
	}
}

func (c *PageCache) Len() int {
	n := 0
	for _, s := range c.shards {
		s.mu.RLock()
		n += s.size
		s.mu.RUnlock()
	}
	return n
}
