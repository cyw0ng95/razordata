package VL

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// LockMode defines the type of lock.
type LockMode int32

const (
	LockModeShared    LockMode = iota // S-lock: for reads
	LockModeExclusive                 // X-lock: for writes
)

const lockTableShardCount = 64

// LockEntry represents a single lock on a key.
type LockEntry struct {
	key     []byte
	mode    LockMode
	holders map[uint64]LockMode // txnID -> mode (S or X)
	waiters *waitQueue
}

// waitQueue is a FIFO queue of transactions waiting for a lock.
type waitQueue struct {
	mu    sync.Mutex
	cond  *sync.Cond
	heads map[uint64]*waitNode // txnID -> node
}

// waitNode represents a single waiter in the queue.
type waitNode struct {
	txnID   uint64
	mode    LockMode
	done    chan struct{}
	granted bool
}

// newWaitQueue creates a new wait queue.
func newWaitQueue() *waitQueue {
	wq := &waitQueue{heads: make(map[uint64]*waitNode)}
	wq.cond = sync.NewCond(&wq.mu)
	return wq
}

// enqueue adds a waiter to the queue and returns the node.
func (wq *waitQueue) enqueue(txnID uint64, mode LockMode) *waitNode {
	wq.mu.Lock()
	defer wq.mu.Unlock()

	node := &waitNode{
		txnID: txnID,
		mode:  mode,
		done:  make(chan struct{}),
	}
	wq.heads[txnID] = node
	return node
}

// dequeue removes a waiter from the queue.
func (wq *waitQueue) dequeue(txnID uint64) {
	wq.mu.Lock()
	defer wq.mu.Unlock()

	node, ok := wq.heads[txnID]
	if !ok {
		return
	}
	delete(wq.heads, txnID)
	select {
	case <-node.done:
	default:
		close(node.done)
	}
}

// grant grants the lock to a waiter.
func (wq *waitQueue) grant(txnID uint64) {
	wq.mu.Lock()
	defer wq.mu.Unlock()

	node, ok := wq.heads[txnID]
	if !ok {
		return
	}
	node.granted = true
	delete(wq.heads, txnID)
	close(node.done)
	wq.cond.Signal()
}

// lockTableShard is one shard of the sharded lock table.
type lockTableShard struct {
	mu    sync.RWMutex
	locks map[uint64]*LockEntry
}

// LockTable is the central lock manager with sharded locking.
type LockTable struct {
	shards  [lockTableShardCount]lockTableShard
	timeout time.Duration
	aborted atomic.Int64
}

// shardFor computes the shard index for a given key hash.
func (lt *LockTable) shardFor(h uint64) int {
	return int(h & (lockTableShardCount - 1))
}

// NewLockTable creates a new lock table with the given timeout.
func NewLockTable(timeout time.Duration) *LockTable {
	lt := &LockTable{
		timeout: timeout,
	}
	for i := range lockTableShardCount {
		lt.shards[i].locks = make(map[uint64]*LockEntry)
	}
	return lt
}

// lockShards locks the shards for the given hashes in ascending order.
func (lt *LockTable) lockShards(hashes []uint64) []int {
	indices := make([]int, len(hashes))
	for i, h := range hashes {
		indices[i] = lt.shardFor(h)
	}
	// Sort unique indices ascending
	for i := 0; i < len(indices); i++ {
		for j := i + 1; j < len(indices); j++ {
			if indices[j] < indices[i] {
				indices[i], indices[j] = indices[j], indices[i]
			}
		}
	}
	// Deduplicate
	prev := -1
	for _, idx := range indices {
		if idx != prev {
			lt.shards[idx].mu.Lock()
			prev = idx
		}
	}
	return indices
}

// unlockShards unlocks the shards locked by lockShards (reversed order).
func (lt *LockTable) unlockShards(indices []int) {
	prev := -1
	for i := len(indices) - 1; i >= 0; i-- {
		idx := indices[i]
		if idx != prev {
			lt.shards[idx].mu.Unlock()
			prev = idx
		}
	}
}

// Lock acquires a lock on the given key. Returns ErrDeadlockTimeout if the
// lock cannot be acquired within the timeout.
func (lt *LockTable) Lock(txnID uint64, key []byte, mode LockMode) error {
	h := fnv1aHash64(key)
	idx := lt.shardFor(h)
	shard := &lt.shards[idx]

	shard.mu.Lock()
	entry, ok := shard.locks[h]
	if !ok {
		entry = &LockEntry{
			key:     key,
			mode:    mode,
			holders: map[uint64]LockMode{txnID: mode},
			waiters: newWaitQueue(),
		}
		shard.locks[h] = entry
		shard.mu.Unlock()
		return nil
	}

	if _, held := entry.holders[txnID]; held {
		if mode == LockModeExclusive && entry.mode == LockModeShared {
			if len(entry.holders) == 1 {
				entry.mode = LockModeExclusive
				entry.holders[txnID] = LockModeExclusive
				shard.mu.Unlock()
				return nil
			}
		} else {
			shard.mu.Unlock()
			return nil
		}
	}

	if mode == LockModeShared && entry.mode == LockModeShared {
		entry.holders[txnID] = LockModeShared
		shard.mu.Unlock()
		return nil
	}

	node := entry.waiters.enqueue(txnID, mode)
	shard.mu.Unlock()

	select {
	case <-node.done:
		if node.granted {
			return nil
		}
		return ErrDeadlockTimeout
	case <-time.After(lt.timeout):
		entry.waiters.dequeue(txnID)
		lt.aborted.Add(1)
		return ErrDeadlockTimeout
	}
}

// Unlock releases all locks held by the given transaction.
func (lt *LockTable) Unlock(txnID uint64) {
	// Lock all shards in ascending order.
	for i := range lockTableShardCount {
		lt.shards[i].mu.Lock()
	}

	for i := range lockTableShardCount {
		shard := &lt.shards[i]
		for h, entry := range shard.locks {
			if _, held := entry.holders[txnID]; held {
				delete(entry.holders, txnID)

				if len(entry.holders) == 0 {
					if len(entry.waiters.heads) > 0 {
						for _, node := range entry.waiters.heads {
							entry.holders[node.txnID] = node.mode
							entry.mode = node.mode
							entry.waiters.grant(node.txnID)
							break
						}
					} else {
						delete(shard.locks, h)
					}
				} else if entry.mode == LockModeExclusive && len(entry.holders) > 0 {
					allShared := true
					for _, m := range entry.holders {
						if m == LockModeExclusive {
							allShared = false
							break
						}
					}
					if allShared {
						entry.mode = LockModeShared
					}
				}
			}
		}
	}

	// Unlock in reverse order.
	for i := lockTableShardCount - 1; i >= 0; i-- {
		lt.shards[i].mu.Unlock()
	}
}

// Stats returns lock table statistics.
func (lt *LockTable) Stats() LockStats {
	var total int64
	for i := range lockTableShardCount {
		lt.shards[i].mu.RLock()
		total += int64(len(lt.shards[i].locks))
		lt.shards[i].mu.RUnlock()
	}
	return LockStats{
		ActiveLocks: total,
		Aborted:     lt.aborted.Load(),
	}
}

// LockStats holds lock table statistics.
type LockStats struct {
	ActiveLocks int64 `json:"active_locks"`
	Aborted     int64 `json:"aborted"`
}

// ErrDeadlockTimeout is returned when a lock cannot be acquired within timeout.
var ErrDeadlockTimeout = errors.New("txn: lock acquisition timed out (deadlock detected)")
