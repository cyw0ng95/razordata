package PL

import (
	"container/list"
	"sync"
)

// REQ000550: per-Stmt predicate cache.
// When a prepared statement is executed repeatedly with the same
// parameter tuple (e.g. a hot SELECT inside a loop), the resolved
// plan does not depend on the parameter values and can be reused.
// This cache keys on a caller-built string (typically the
// sorted-and-joined tuple of parameter encodings) and stores the
// already-resolved plan object.
// The cache is scoped to a single Stmt instance — it is not a
// session- or engine-level structure. Each Stmt owns its own
// PredicateCache so eviction in one statement does not affect
// another. The plan value is stored as interface{} so this cache
// has no compile-time dependency on the concrete plan type and can
// ship ahead of planner integration.
type PredicateCache struct {
	mu      sync.Mutex
	entries map[string]*list.Element
	lru     *list.List
	maxSize int
}

type predEntry struct {
	key  string
	plan interface{}
}

// NewPredicateCache creates a per-Stmt plan cache with the given
// maxSize. A non-positive maxSize is clamped to 1 so the cache is
// always usable.
func NewPredicateCache(maxSize int) *PredicateCache {
	if maxSize <= 0 {
		maxSize = 1
	}
	return &PredicateCache{
		entries: make(map[string]*list.Element, maxSize),
		lru:     list.New(),
		maxSize: maxSize,
	}
}

// Get returns the cached plan for key and promotes the entry to
// the front of the LRU. On miss the returned plan is nil and ok is
// false.
func (c *PredicateCache) Get(key string) (interface{}, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	elem, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	c.lru.MoveToFront(elem)
	return elem.Value.(*predEntry).plan, true
}

// Put inserts or refreshes a (key, plan) entry. If the key is
// already present the plan is replaced and the entry is promoted.
// If the cache is at capacity the least-recently-used entry is
// evicted. Put does not copy the plan value — the caller transfers
// ownership.
func (c *PredicateCache) Put(key string, plan interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if elem, ok := c.entries[key]; ok {
		entry := elem.Value.(*predEntry)
		entry.plan = plan
		c.lru.MoveToFront(elem)
		return
	}
	entry := &predEntry{key: key, plan: plan}
	elem := c.lru.PushFront(entry)
	c.entries[key] = elem
	for c.lru.Len() > c.maxSize {
		c.evictBack()
	}
}

// Size returns the number of plans currently cached.
func (c *PredicateCache) Size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// evictBack removes the least-recently-used entry. Caller must hold mu.
func (c *PredicateCache) evictBack() {
	elem := c.lru.Back()
	if elem == nil {
		return
	}
	entry := elem.Value.(*predEntry)
	c.lru.Remove(elem)
	delete(c.entries, entry.key)
}
