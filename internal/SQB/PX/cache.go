package PX

import (
	"sync"

	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// cacheEntry tracks a single cached item across the text, memo,
// and stmts sub-maps. It is used for unified LRU eviction.
type cacheEntry struct {
	sql     string // exact SQL text (key for text + stmts maps)
	memoKey string // parameterized key (key for memo map)
}

// PipelineCache unifies the current three separate caches
// (stmtCache, planCache, textPlanCache) into a single structure
// with consistent LRU eviction. When an entry is evicted, it is
// removed from all three sub-maps simultaneously.
//
// PipelineCache is thread-safe. It is designed to be shared across
// Executor clones via pointer, matching the current pattern for
// stmtCache and planCache.
type PipelineCache struct {
	mu      sync.Mutex
	maxSize int

	// Exact SQL text → PipelineSpec
	text map[string]*PipelineSpec
	// Parameterized memo key → PipelineSpec
	memo map[string]*PipelineSpec
	// Exact SQL text → parsed AST (for parse skip on cache miss)
	stmts map[string]PS.Stmt

	// Unified LRU: ordered list of cache entries for eviction.
	// Oldest entries are at the front (index 0).
	lru []*cacheEntry
}

// NewPipelineCache creates a cache with the given maximum number
// of entries. Each entry covers all three sub-maps (text, memo, stmts).
func NewPipelineCache(maxSize int) *PipelineCache {
	if maxSize <= 0 {
		maxSize = 256
	}
	return &PipelineCache{
		maxSize: maxSize,
		text:    make(map[string]*PipelineSpec, maxSize),
		memo:    make(map[string]*PipelineSpec, maxSize),
		stmts:   make(map[string]PS.Stmt, maxSize),
		lru:     make([]*cacheEntry, 0, maxSize),
	}
}

// GetByText returns the PipelineSpec and parsed statement for the
// given exact SQL text. Returns nil if not found.
func (c *PipelineCache) GetByText(sql string) (*PipelineSpec, PS.Stmt) {
	c.mu.Lock()
	defer c.mu.Unlock()

	spec := c.text[sql]
	if spec == nil {
		return nil, nil
	}
	stmt := c.stmts[sql]
	c.touchEntry(sql)
	return spec, stmt
}

// GetByMemo returns the PipelineSpec for the given parameterized
// memo key. Returns nil if not found.
func (c *PipelineCache) GetByMemo(key string) *PipelineSpec {
	c.mu.Lock()
	defer c.mu.Unlock()

	spec := c.memo[key]
	if spec != nil {
		// Move to most-recent position (find by memoKey)
		for i, e := range c.lru {
			if e.memoKey == key {
				c.lru = append(c.lru[:i], c.lru[i+1:]...)
				c.lru = append(c.lru, e)
				break
			}
		}
	}
	return spec
}

// Put inserts a PipelineSpec and its parsed statement into all
// applicable sub-maps. If the spec has a non-empty SQLText, it is
// stored in the text and stmts maps. If it has a non-empty MemoKey,
// it is stored in the memo map.
func (c *PipelineCache) Put(spec *PipelineSpec, stmt PS.Stmt) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry := &cacheEntry{
		sql:     spec.SQLText,
		memoKey: spec.MemoKey,
	}

	if spec.SQLText != "" {
		c.text[spec.SQLText] = spec
		if stmt != nil {
			c.stmts[spec.SQLText] = stmt
		}
	}
	if spec.MemoKey != "" {
		c.memo[spec.MemoKey] = spec
	}

	c.lru = append(c.lru, entry)
	c.evict()
}

// Clear removes all entries from the cache.
func (c *PipelineCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.text = make(map[string]*PipelineSpec, c.maxSize)
	c.memo = make(map[string]*PipelineSpec, c.maxSize)
	c.stmts = make(map[string]PS.Stmt, c.maxSize)
	c.lru = c.lru[:0]
}

// Len returns the number of entries in the cache.
func (c *PipelineCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.lru)
}

// touchEntry moves the entry with the given SQL text to the
// most-recent position in the LRU list. Must be called with
// c.mu held.
func (c *PipelineCache) touchEntry(sql string) {
	for i, e := range c.lru {
		if e.sql == sql {
			c.lru = append(c.lru[:i], c.lru[i+1:]...)
			c.lru = append(c.lru, e)
			return
		}
	}
}

// evict removes the oldest entries until the cache is within
// maxSize. Must be called with c.mu held.
func (c *PipelineCache) evict() {
	for len(c.lru) > c.maxSize {
		oldest := c.lru[0]
		c.lru = c.lru[1:]

		if oldest.sql != "" {
			delete(c.text, oldest.sql)
			delete(c.stmts, oldest.sql)
		}
		if oldest.memoKey != "" {
			delete(c.memo, oldest.memoKey)
		}
	}
}
