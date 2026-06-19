package ls

import (
	"sync/atomic"
)

// iteratorAdapter wraps *Iterator to satisfy RangeIter interface.
type iteratorAdapter struct {
	it *Iterator
}

func (a *iteratorAdapter) Next() bool    { return a.it.Next() }
func (a *iteratorAdapter) Key() []byte   { return a.it.Key() }
func (a *iteratorAdapter) Value() []byte { return a.it.Value() }
func (a *iteratorAdapter) Err() error    { return nil }
func (a *iteratorAdapter) Close() error {
	// *Iterator has no Close method; no-op
	return nil
}

// shardedMemtable splits the active memtable into N sharded skiplists
// keyed by hash(key) % N. Each shard is an independent memtable with
// its own skiplist, size counter, and flush state. This eliminates
// global CAS contention on a single head pointer under high write
// concurrency. REQ000537.
type shardedMemtable struct {
	shards_ []*memtable
	hash   shardHash
	n      int
	// totalSize tracks the combined size of all shards for flush decisions
	totalSize atomic.Int64
	// maxSize is the total size threshold that triggers a flush
	maxSize int64
	// frozen tracks which shards are frozen (immutable)
	frozen []atomic.Bool
}

// shardHash computes the shard index for a given key.
// The default implementation uses FNV-1a hash with modulo.
type shardHash func(key []byte) int

// newShardedMemtable creates a new sharded memtable with N shards.
// N should be a power of 2 for efficient modulo via bit masking.
// If n <= 0, defaults to 1 (single-shard mode, equivalent to legacy memtable).
func newShardedMemtable(maxSize int64, n int) *shardedMemtable {
	if n <= 0 {
		n = 1
	}
	// Round up to next power of 2
	n = nextPowerOf2(n)

	shards := make([]*memtable, n)
	frozen := make([]atomic.Bool, n)
	for i := 0; i < n; i++ {
		shards[i] = newMemtable(maxSize / int64(n))
	}

	sm := &shardedMemtable{
		shards_:  shards,
		n:       n,
		maxSize: maxSize,
		frozen:  frozen,
	}
	sm.hash = sm.fnv1aHash
	return sm
}

// fnv1aHash computes the FNV-1a hash of the key and returns shard index.
func (sm *shardedMemtable) fnv1aHash(key []byte) int {
	var hash uint64 = fnv1aOffsetBasis
	for _, b := range key {
		hash ^= uint64(b)
		hash *= fnv1aPrime
	}
	return int(hash & uint64(sm.n-1)) // fast modulo for power-of-2
}

// shard returns the shard index for a given key.
func (sm *shardedMemtable) shard(key []byte) int {
	return sm.hash(key)
}

// Insert inserts a key-value pair into the appropriate shard.
func (sm *shardedMemtable) Insert(key, value []byte) {
	idx := sm.shard(key)
	shard := sm.shards_[idx]
	shard.Insert(key, value)
	sm.totalSize.Add(int64(len(key) + len(value)))
}

// Get looks up a key in the appropriate shard.
func (sm *shardedMemtable) Get(key []byte) ([]byte, bool) {
	idx := sm.shard(key)
	return sm.shards_[idx].Get(key)
}

// Size returns the total size of all shards.
func (sm *shardedMemtable) Size() int64 {
	return sm.totalSize.Load()
}

// Len returns the total number of entries across all shards.
func (sm *shardedMemtable) Len() int64 {
	var total int64
	for _, shard := range sm.shards_ {
		total += shard.Len()
	}
	return total
}

// ShouldFlush reports whether the total size exceeds the threshold.
func (sm *shardedMemtable) ShouldFlush() bool {
	return sm.totalSize.Load() >= sm.maxSize
}

// Freeze freezes all shards, making them immutable.
func (sm *shardedMemtable) Freeze() {
	for i := range sm.shards_ {
		sm.shards_[i].Freeze()
		sm.frozen[i].Store(true)
	}
}

// IsFrozen reports whether all shards are frozen.
func (sm *shardedMemtable) IsFrozen() bool {
	for i := range sm.shards_ {
		if !sm.frozen[i].Load() {
			return false
		}
	}
	return true
}

// IncRef increments the reference count on all shards.
func (sm *shardedMemtable) IncRef() {
	for _, shard := range sm.shards_ {
		shard.IncRef()
	}
}

// DecRef decrements the reference count on all shards.
func (sm *shardedMemtable) DecRef() {
	for _, shard := range sm.shards_ {
		shard.DecRef()
	}
}

// RefCount returns the maximum reference count across all shards.
func (sm *shardedMemtable) RefCount() int64 {
	var max int64
	for _, shard := range sm.shards_ {
		if ref := shard.RefCount(); ref > max {
			max = ref
		}
	}
	return max
}

// Iterator returns an iterator over all non-frozen shards.
// The iterator merges results from each active shard.
func (sm *shardedMemtable) Iterator() RangeIter {
	active := make([]*memtable, 0, len(sm.shards_))
	for _, shard := range sm.shards_ {
		if !shard.IsFrozen() {
			active = append(active, shard)
		}
	}
	return newShardedIter(active)
}

// shards returns all shards (frozen and active).
func (sm *shardedMemtable) shards() []*memtable {
	return sm.shards_
}

// nextPowerOf2 returns the smallest power of 2 >= n.
func nextPowerOf2(n int) int {
	if n <= 1 {
		return 1
	}
	p := 1
	for p < n {
		p <<= 1
	}
	return p
}

// FNV-1a constants
const (
	fnv1aOffsetBasis uint64 = 14695981039346656037
	fnv1aPrime       uint64 = 1099511628211
)

// shardedIter iterates over multiple shards, merging results in order.
// It does not guarantee global sorted order across shards (each shard
// is sorted independently), but provides a union of all active shard entries.
type shardedIter struct {
	shards []*memtable
	its    []RangeIter
	pos    int
	done   bool
	err    error
}

func newShardedIter(shards []*memtable) RangeIter {
	its := make([]RangeIter, 0, len(shards))
	for _, s := range shards {
		its = append(its, &iteratorAdapter{it: s.Iterator()})
	}
	return &shardedIter{
		shards: shards,
		its:    its,
		pos:    -1,
	}
}

// Next advances to the next entry across any shard.
// Returns false when all shards are exhausted.
// NOTE: Does not guarantee sorted order - just unions all shards.
func (si *shardedIter) Next() bool {
	if si.done {
		return false
	}

	// Find the next available iterator
	for {
		if si.pos >= 0 && si.pos < len(si.its) {
			if si.its[si.pos].Next() {
				return true
			}
		}
		// Move to next shard
		si.pos++
		if si.pos >= len(si.its) {
			si.done = true
			return false
		}
		// Check if this shard has entries
		if si.its[si.pos].Next() {
			si.pos-- // back up so current position is valid
			return true
		}
	}
}

// Key returns the key at the current position.
func (si *shardedIter) Key() []byte {
	if si.pos >= 0 && si.pos < len(si.its) {
		return si.its[si.pos].Key()
	}
	return nil
}

// Value returns the value at the current position.
func (si *shardedIter) Value() []byte {
	if si.pos >= 0 && si.pos < len(si.its) {
		return si.its[si.pos].Value()
	}
	return nil
}

// Err returns any error encountered.
func (si *shardedIter) Err() error {
	return si.err
}

// Close releases resources held by the iterator.
func (si *shardedIter) Close() error {
	for _, it := range si.its {
		it.Close()
	}
	return nil
}