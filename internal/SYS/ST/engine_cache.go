// Package ST — engine-level prepared statement cache (REQ000548).
//
// Caches parsed + planned statements keyed by SQL text. Entries are
// reference-counted so callers can hand out a cached stmt without
// worrying about eviction. When the count hits zero, the entry is
// removable by the LRU policy.
//
// This is distinct from the per-Session Stmt (which is one stmt per
// session) and from planMemo (which is keyed by AST hash and still
// requires parse). Here we cache the fully prepared *Stmt (parse +
// plan) and short-circuit Session.Query to skip both stages.
package ST

import (
	"container/list"
	"sync"
	"sync/atomic"
)

// StmtCache is an engine-level prepared-statement cache.
type StmtCache struct {
	mu       sync.Mutex
	entries  map[string]*cacheEntry
	lru      *list.List
	maxSize  int
	hitCount atomic.Int64
	missCount atomic.Int64
}

type cacheEntry struct {
	sql      string
	stmt     *Stmt
	refCount atomic.Int64
	lruElem  *list.Element
}

// NewStmtCache creates a cache with the given max size (LRU
// eviction). maxSize must be > 0; default fallback is 256.
func NewStmtCache(maxSize int) *StmtCache {
	if maxSize <= 0 {
		maxSize = 256
	}
	return &StmtCache{
		entries: make(map[string]*cacheEntry, maxSize),
		lru:     list.New(),
		maxSize: maxSize,
	}
}

// Get looks up a cached stmt and increments its ref count. Returns
// (stmt, true) on hit; (nil, false) on miss. The caller must
// Release the stmt when done.
func (c *StmtCache) Get(sql string) (*Stmt, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[sql]
	if !ok {
		c.missCount.Add(1)
		return nil, false
	}
	e.refCount.Add(1)
	c.lru.MoveToFront(e.lruElem)
	c.hitCount.Add(1)
	return e.stmt, true
}

// Put inserts a stmt into the cache. If the cache is at capacity,
// evicts the least-recently-used entry with refcount==0. If all
// entries are pinned, Put still inserts (caller will pay memory
// cost).
func (c *StmtCache) Put(sql string, stmt *Stmt) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[sql]; ok {
		e.stmt = stmt
		e.lruElem = e.lruElem // keep position
		c.lru.MoveToFront(e.lruElem)
		return
	}
	e := &cacheEntry{sql: sql, stmt: stmt}
	e.refCount.Store(1) // caller is the first reference
	e.lruElem = c.lru.PushFront(e)
	c.entries[sql] = e
	// Evict if over capacity.
	for c.lru.Len() > c.maxSize {
		c.evictLRU()
	}
}

// Release decrements the entry's ref count.
func (c *StmtCache) Release(sql string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[sql]; ok {
		e.refCount.Add(-1)
	}
}

// Clear removes all entries.
func (c *StmtCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]*cacheEntry, c.maxSize)
	c.lru = list.New()
}

// Size returns the number of entries currently cached.
func (c *StmtCache) Size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// Stats returns (hits, misses).
func (c *StmtCache) Stats() (int64, int64) {
	return c.hitCount.Load(), c.missCount.Load()
}

// evictLRU removes the LRU entry with refCount==0. Caller must hold mu.
func (c *StmtCache) evictLRU() {
	for elem := c.lru.Back(); elem != nil; elem = elem.Prev() {
		e := elem.Value.(*cacheEntry)
		if e.refCount.Load() == 0 {
			c.lru.Remove(elem)
			delete(c.entries, e.sql)
			return
		}
	}
}
