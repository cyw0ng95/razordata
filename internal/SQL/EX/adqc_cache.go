package EX

import (
	"context"
	"sync"
)

// SpecializedPlan holds a compiled plan and its measured performance.
type SpecializedPlan struct {
	Fn             func(ctx context.Context, batch *Batch, params []any) (*Batch, error)
	OpType         string
	PlanHash       string
	SchemaVersion  uint64
	MeasuredCostNs int64
}

// AdqcCache is an LRU cache for specialized query plans.
// Composite key: planHash + schemaVersion so ALTER TABLE
// automatically invalidates the cache.
type AdqcCache struct {
	mu    sync.RWMutex
	items map[string]*SpecializedPlan
	order []string
	limit int
}

// NewAdqcCache creates a bounded LRU cache.
func NewAdqcCache(limit int) *AdqcCache {
	if limit <= 0 {
		limit = 256
	}
	return &AdqcCache{
		items: make(map[string]*SpecializedPlan),
		order: make([]string, 0, limit),
		limit: limit,
	}
}

// cacheKey builds the composite key from plan hash and schema version.
func cacheKey(planHash string, schemaVersion uint64) string {
	if schemaVersion == 0 {
		return planHash
	}
	return planHash + "@" + string(rune(schemaVersion))
}

// Get returns a cached specialized plan. Returns nil on miss.
func (c *AdqcCache) Get(planHash string, schemaVersion uint64) *SpecializedPlan {
	key := cacheKey(planHash, schemaVersion)
	c.mu.RLock()
	p, ok := c.items[key]
	c.mu.RUnlock()
	if !ok {
		return nil
	}
	c.mu.Lock()
	c.promote(key)
	c.mu.Unlock()
	return p
}

// Put stores a specialized plan in the cache.
func (c *AdqcCache) Put(planHash string, schemaVersion uint64, plan *SpecializedPlan) {
	key := cacheKey(planHash, schemaVersion)
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.items[key]; ok {
		c.items[key] = plan
		c.promote(key)
		return
	}

	if len(c.items) >= c.limit {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.items, oldest)
	}

	c.items[key] = plan
	c.order = append(c.order, key)
}

// Invalidate removes all entries containing the given table prefix.
func (c *AdqcCache) Invalidate(schemaVersion uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	suffix := "@" + string(rune(schemaVersion))
	var kept []string
	for _, key := range c.order {
		if len(key) >= len(suffix) && key[len(key)-len(suffix):] == suffix {
			delete(c.items, key)
		} else {
			kept = append(kept, key)
		}
	}
	c.order = kept
}

// Len returns the number of cached plans.
func (c *AdqcCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.items)
}

// promote moves a key to the most-recently-used position.
func (c *AdqcCache) promote(key string) {
	idx := -1
	for i, k := range c.order {
		if k == key {
			idx = i
			break
		}
	}
	if idx < 0 {
		return
	}
	c.order = append(c.order[:idx], c.order[idx+1:]...)
	c.order = append(c.order, key)
}
