package ls

import (
	"container/list"
	"runtime"
	"sync"
)

// l0Cache is a dedicated LRU for recently-read L0 SST blocks (REQ000540).
// Sharded by GOMAXPROCS to reduce contention under concurrent reads.
type l0Cache struct {
	shards []*l0CacheShard
	nshard uint32
	cap    int
}

type l0CacheShard struct {
	mu    sync.Mutex
	items map[uint64]*list.Element
	lru   *list.List
	cap   int
}

type cacheEntry struct {
	blockID uint64
	data    []byte
}

func newL0Cache(capacity int) *l0Cache {
	nshard := uint32(runtime.GOMAXPROCS(0))
	if nshard == 0 {
		nshard = 1
	}
	perShard := capacity / int(nshard)
	if perShard == 0 {
		perShard = 1
	}

	shards := make([]*l0CacheShard, nshard)
	for i := range shards {
		shards[i] = &l0CacheShard{
			items: make(map[uint64]*list.Element),
			lru:   list.New(),
			cap:   perShard,
		}
	}

	return &l0Cache{
		shards: shards,
		nshard: nshard,
		cap:    capacity,
	}
}

func (c *l0Cache) shard(blockID uint64) *l0CacheShard {
	return c.shards[blockID%uint64(c.nshard)]
}

func (c *l0Cache) Get(blockID uint64) (data []byte, hit bool) {
	s := c.shard(blockID)
	s.mu.Lock()
	defer s.mu.Unlock()

	elem, ok := s.items[blockID]
	if !ok {
		return nil, false
	}

	s.lru.MoveToFront(elem)
	return elem.Value.(*cacheEntry).data, true
}

func (c *l0Cache) Put(blockID uint64, data []byte) {
	s := c.shard(blockID)
	s.mu.Lock()
	defer s.mu.Unlock()

	elem, ok := s.items[blockID]
	if ok {
		entry := elem.Value.(*cacheEntry)
		entry.data = data
		s.lru.MoveToFront(elem)
		return
	}

	entry := &cacheEntry{blockID: blockID, data: data}
	s.items[blockID] = s.lru.PushFront(entry)
	for s.lru.Len() > s.cap {
		s.evictBack()
	}
}

func (c *l0Cache) Size() int {
	total := 0
	for _, s := range c.shards {
		s.mu.Lock()
		total += s.lru.Len()
		s.mu.Unlock()
	}
	return total
}

func (c *l0Cache) NShard() uint32 {
	return c.nshard
}

func (s *l0CacheShard) evictBack() {
	back := s.lru.Back()
	if back == nil {
		return
	}
	entry := back.Value.(*cacheEntry)
	s.lru.Remove(back)
	delete(s.items, entry.blockID)
}
