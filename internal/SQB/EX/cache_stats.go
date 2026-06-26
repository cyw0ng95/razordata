package EX

import (
	"fmt"
	"sync"
)

// CacheStats tracks plan cache hit/miss statistics.
// REQ000793: Plan cache analysis for EXPLAIN output.
type CacheStats struct {
	mu        sync.Mutex
	Hits      int64
	Misses    int64
	Evictions int64
	MaxSize   int
}

// NewCacheStats creates a new CacheStats tracker.
func NewCacheStats(maxSize int) *CacheStats {
	return &CacheStats{
		MaxSize: maxSize,
	}
}

// RecordHit records a cache hit.
func (cs *CacheStats) RecordHit() {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.Hits++
}

// RecordMiss records a cache miss.
func (cs *CacheStats) RecordMiss() {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.Misses++
}

// RecordEviction records a cache eviction.
func (cs *CacheStats) RecordEviction() {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.Evictions++
}

// GetHitRate returns the cache hit rate as a percentage.
func (cs *CacheStats) GetHitRate() float64 {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	total := cs.Hits + cs.Misses
	if total == 0 {
		return 0
	}
	return float64(cs.Hits) / float64(total) * 100
}

// GetSummary returns a human-readable summary.
func (cs *CacheStats) GetSummary() string {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	total := cs.Hits + cs.Misses
	rate := float64(0)
	if total > 0 {
		rate = float64(cs.Hits) / float64(total) * 100
	}

	return fmt.Sprintf("CacheStats: hits=%d misses=%d evictions=%d hit_rate=%.1f%% max_size=%d",
		cs.Hits, cs.Misses, cs.Evictions, rate, cs.MaxSize)
}

// StmtCacheEntry represents a cached prepared statement plan.
// REQ000793: StmtCache wiring for plan reuse.
type StmtCacheEntry struct {
	Plan     Operator
	SQL      string
	LastUsed int64 // unix nanos
	UseCount int64
}

// StmtCache is a simple LRU cache for prepared statement plans.
type StmtCache struct {
	mu        sync.Mutex
	entries   map[string]*StmtCacheEntry
	maxSize   int
	hits      int64
	misses    int64
	evictions int64
}

// NewStmtCache creates a new StmtCache with the given max size.
func NewStmtCache(maxSize int) *StmtCache {
	return &StmtCache{
		entries: make(map[string]*StmtCacheEntry),
		maxSize: maxSize,
	}
}

// Get retrieves a cached plan by SQL hash.
func (sc *StmtCache) Get(key string) *StmtCacheEntry {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	entry, ok := sc.entries[key]
	if !ok {
		sc.misses++
		return nil
	}
	sc.hits++
	entry.UseCount++
	return entry
}

// Put stores a plan in the cache.
func (sc *StmtCache) Put(key string, entry *StmtCacheEntry) {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	if len(sc.entries) >= sc.maxSize {
		// Simple eviction: remove the oldest entry (first one in map iteration)
		for k := range sc.entries {
			delete(sc.entries, k)
			sc.evictions++
			break
		}
	}
	sc.entries[key] = entry
}

// GetStats returns cache statistics.
func (sc *StmtCache) GetStats() *CacheStats {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	return &CacheStats{
		Hits:      sc.hits,
		Misses:    sc.misses,
		Evictions: sc.evictions,
		MaxSize:   sc.maxSize,
	}
}

// Clear clears the cache.
func (sc *StmtCache) Clear() {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.entries = make(map[string]*StmtCacheEntry)
}

// Size returns the current cache size.
func (sc *StmtCache) Size() int {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	return len(sc.entries)
}
