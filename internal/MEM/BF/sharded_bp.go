package bf

import (
	"sync"
	"sync/atomic"
)

// shardedBufferPool splits the global buffer pool into N shards,
// each with its own sub-map, clock hand, and eviction logic.
// This eliminates global mutex contention under concurrent reads.
// REQ000539.
type shardedBufferPool struct {
	shards []*bufferShard
	n      int
	// totalUsed tracks the total number of used slots across all shards
	totalUsed atomic.Int64
}

// bufferShard is one shard of the sharded buffer pool.
type bufferShard struct {
	slots map[uint64]*bufferSlot
	mu    sync.RWMutex
	// hand is the clock sweep hand for this shard
	hand atomic.Uint64
	// capacity is the max number of slots in this shard
	capacity int64
	// used is the number of occupied slots in this shard
	used atomic.Int64
}

// newShardedBufferPool creates a new sharded buffer pool with N shards.
// N should be a power of 2 for efficient modulo via bit masking.
// Default is 32 shards if n <= 0.
func newShardedBufferPool(n int) *shardedBufferPool {
	if n <= 0 {
		n = 32
	}
	// Round up to next power of 2
	n = nextPowerOf2(n)

	shards := make([]*bufferShard, n)
	for i := 0; i < n; i++ {
		shards[i] = &bufferShard{
			slots:    make(map[uint64]*bufferSlot),
			capacity: 0, // unlimited per-shard capacity; global capacity enforced at sbp level
		}
	}

	return &shardedBufferPool{
		shards: shards,
		n:      n,
	}
}

// shardFor computes the shard index for a given blockID.
func (sbp *shardedBufferPool) shardFor(blockID uint64) int {
	return int(blockID & uint64(sbp.n-1))
}

// GetOrInsert returns the slot for the given blockID, creating it if absent.
// Returns (slot, created) where created is true if the slot was newly created.
func (sbp *shardedBufferPool) GetOrInsert(blockID uint64) (*bufferSlot, bool) {
	idx := sbp.shardFor(blockID)
	shard := sbp.shards[idx]

	// Fast path: R-lock lookup
	shard.mu.RLock()
	slot, ok := shard.slots[blockID]
	if ok {
		shard.mu.RUnlock()
		return slot, false
	}
	shard.mu.RUnlock()

	// Slow path: create new slot
	shard.mu.Lock()
	defer shard.mu.Unlock()

	// Double-check after acquiring write lock
	slot, ok = shard.slots[blockID]
	if ok {
		return slot, false
	}

	slot = &bufferSlot{
		blockID: blockID,
	}
	shard.slots[blockID] = slot
	shard.used.Add(1)
	sbp.totalUsed.Add(1)

	return slot, true
}

// GetSlot returns the slot for the given blockID, or nil if not found.
func (sbp *shardedBufferPool) GetSlot(blockID uint64) *bufferSlot {
	idx := sbp.shardFor(blockID)
	shard := sbp.shards[idx]

	shard.mu.RLock()
	defer shard.mu.RUnlock()
	return shard.slots[blockID]
}

// Delete removes the slot for the given blockID.
func (sbp *shardedBufferPool) Delete(blockID uint64) {
	idx := sbp.shardFor(blockID)
	shard := sbp.shards[idx]

	shard.mu.Lock()
	defer shard.mu.Unlock()

	if slot, ok := shard.slots[blockID]; ok {
		delete(shard.slots, blockID)
		shard.used.Add(-1)
		sbp.totalUsed.Add(-1)
		_ = slot // slot is removed, caller may want to clean up
	}
}

// EachShardHand returns the clock hand value for each shard.
func (sbp *shardedBufferPool) EachShardHand() []uint64 {
	hands := make([]uint64, sbp.n)
	for i, shard := range sbp.shards {
		hands[i] = shard.hand.Load()
	}
	return hands
}

// EvictFromShard runs the clock sweep eviction on the given shard.
// Returns the number of slots evicted.
func (sbp *shardedBufferPool) EvictFromShard(shardIdx int, maxEvict int) int {
	if shardIdx < 0 || shardIdx >= sbp.n {
		return 0
	}
	shard := sbp.shards[shardIdx]

	evicted := 0
	for evicted < maxEvict && shard.used.Load() > 0 {
		// Advance clock hand
		hand := shard.hand.Add(1)
		hand = hand % uint64(sbp.n) // wrap around

		// Find a slot to evict (simplified: evict any slot with refKey < hand)
		shard.mu.Lock()
		toEvict := make([]*bufferSlot, 0, 1)
		for _, slot := range shard.slots {
			if slot.refKey.Load() < hand {
				toEvict = append(toEvict, slot)
				break // evict one at a time
			}
		}
		shard.mu.Unlock()

		if len(toEvict) == 0 {
			break // no eligible slots
		}

		// Evict the slot (remove from map)
		slot := toEvict[0]
		sbp.Delete(slot.blockID)
		evicted++
	}

	return evicted
}

// TotalUsed returns the total number of occupied slots across all shards.
func (sbp *shardedBufferPool) TotalUsed() int64 {
	return sbp.totalUsed.Load()
}

// TotalCapacity returns the total capacity (sum of all shard capacities).
func (sbp *shardedBufferPool) TotalCapacity() int64 {
	var total int64
	for _, shard := range sbp.shards {
		total += shard.capacity
	}
	return total
}

// ForEachShard calls f for each shard.
func (sbp *shardedBufferPool) ForEachShard(f func(shardIdx int, shard *bufferShard)) {
	for i, shard := range sbp.shards {
		f(i, shard)
	}
}

// evictOne attempts to evict one slot from the buffer pool.
// It first tries the given shard (shard, idx), then searches other shards if needed.
// Returns (evicted, data) where evicted is true if a slot was evicted,
// and data is the evicted slot's data buffer (caller's responsibility to return to pool).
func (sbp *shardedBufferPool) evictOne(pass int, shard *bufferShard, idx int) ([]byte, bool) {
	// REQ000161: first pass — evict slots whose refKey is
	// older than (hand - clockInterval). These are LRU
	// candidates by the clock-sweep design.
	hand := shard.hand.Add(1)
	for bid, s := range shard.slots {
		if s.refKey.Load() < hand-uint64(clockInterval) {
			if s.pinCount.Load() == 0 {
				delete(shard.slots, bid)
				sbp.totalUsed.Add(-1)
				return s.data, true
			}
		}
	}

	// REQ000161: second pass — find the slot with the lowest
	// refKey (least-recently-used) among unpinned slots in this shard.
	var victimID uint64
	var victimRefKey uint64 = ^uint64(0) // max uint64
	var found bool
	for bid, s := range shard.slots {
		if s.pinCount.Load() == 0 {
			rk := s.refKey.Load()
			if rk < victimRefKey {
				victimRefKey = rk
				victimID = bid
				found = true
			}
		}
	}
	if found {
		victim := shard.slots[victimID]
		delete(shard.slots, victimID)
		sbp.totalUsed.Add(-1)
		return victim.data, true
	}

	// This shard has no eligible victims. Search other shards.
	for otherIdx := 0; otherIdx < sbp.n; otherIdx++ {
		if otherIdx == idx {
			continue
		}
		other := sbp.shards[otherIdx]
		other.mu.Lock()
		// First pass on other shard.
		otherHand := other.hand.Add(1)
		for bid, s := range other.slots {
			if s.refKey.Load() < otherHand-uint64(clockInterval) {
				if s.pinCount.Load() == 0 {
					delete(other.slots, bid)
					sbp.totalUsed.Add(-1)
					other.mu.Unlock()
					return s.data, true
				}
			}
		}
		// Second pass on other shard.
		var oVictimID uint64
		var oVictimRefKey uint64 = ^uint64(0)
		var oFound bool
		for bid, s := range other.slots {
			if s.pinCount.Load() == 0 {
				rk := s.refKey.Load()
				if rk < oVictimRefKey {
					oVictimRefKey = rk
					oVictimID = bid
					oFound = true
				}
			}
		}
		if oFound {
			oVictim := other.slots[oVictimID]
			delete(other.slots, oVictimID)
			sbp.totalUsed.Add(-1)
			other.mu.Unlock()
			return oVictim.data, true
		}
		other.mu.Unlock()
	}

	return nil, false
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