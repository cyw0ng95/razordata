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

// LockTable is the central lock manager.
type LockTable struct {
	mu      sync.RWMutex
	locks   map[uint64]*LockEntry // FNV-1a hash -> LockEntry
	timeout time.Duration
	aborted atomic.Int64 // count of timeout-aborted transactions
}

// NewLockTable creates a new lock table with the given timeout.
func NewLockTable(timeout time.Duration) *LockTable {
	return &LockTable{
		locks:   make(map[uint64]*LockEntry),
		timeout: timeout,
	}
}

// Lock acquires a lock on the given key. Returns ErrDeadlockTimeout if the
// lock cannot be acquired within the timeout.
func (lt *LockTable) Lock(txnID uint64, key []byte, mode LockMode) error {
	h := fnv1aHash64(key)

	lt.mu.Lock()
	entry, ok := lt.locks[h]
	if !ok {
		// No existing lock — create and grant immediately.
		entry = &LockEntry{
			key:     key,
			mode:    mode,
			holders: map[uint64]LockMode{txnID: mode},
			waiters: newWaitQueue(),
		}
		lt.locks[h] = entry
		lt.mu.Unlock()
		return nil
	}

	// Lock exists — check compatibility.
	if _, held := entry.holders[txnID]; held {
		// Same transaction already holds a lock.
		if mode == LockModeExclusive && entry.mode == LockModeShared {
			// Upgrade S->X: check no other holders.
			if len(entry.holders) == 1 {
				entry.mode = LockModeExclusive
				entry.holders[txnID] = LockModeExclusive
				lt.mu.Unlock()
				return nil
			}
			// Other holders exist — need to wait for upgrade.
		} else {
			lt.mu.Unlock()
			return nil
		}
	}

	// Check compatibility with existing holders.
	if mode == LockModeShared && entry.mode == LockModeShared {
		// S-lock compatible with existing S-locks.
		entry.holders[txnID] = LockModeShared
		lt.mu.Unlock()
		return nil
	}

	// Incompatible — enqueue waiter.
	node := entry.waiters.enqueue(txnID, mode)
	lt.mu.Unlock()

	// Wait with timeout.
	select {
	case <-node.done:
		if node.granted {
			return nil
		}
		return ErrDeadlockTimeout
	case <-time.After(lt.timeout):
		// Timeout — remove from queue and abort.
		entry.waiters.dequeue(txnID)
		lt.aborted.Add(1)
		return ErrDeadlockTimeout
	}
}

// Unlock releases all locks held by the given transaction.
func (lt *LockTable) Unlock(txnID uint64) {
	lt.mu.Lock()
	defer lt.mu.Unlock()

	for h, entry := range lt.locks {
		if _, held := entry.holders[txnID]; held {
			delete(entry.holders, txnID)

			if len(entry.holders) == 0 {
				// No more holders — grant to next waiter if any.
				if len(entry.waiters.heads) > 0 {
					for _, node := range entry.waiters.heads {
						entry.holders[node.txnID] = node.mode
						entry.mode = node.mode
						entry.waiters.grant(node.txnID)
						break
					}
				} else {
					delete(lt.locks, h)
				}
			} else if entry.mode == LockModeExclusive && len(entry.holders) > 0 {
				// Check if all remaining holders are S-locks.
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

// Stats returns lock table statistics.
func (lt *LockTable) Stats() LockStats {
	lt.mu.RLock()
	defer lt.mu.RUnlock()
	return LockStats{
		ActiveLocks: int64(len(lt.locks)),
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
