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
	// REQ001574: pre-allocated indices buffer to avoid per-acquisition
	// allocation. Capacity equals max shard count (64) — the largest
	// possible number of unique shards a single key set can touch.
	indicesBuf []int
}

// shardFor computes the shard index for a given key hash.
func (lt *LockTable) shardFor(h uint64) int {
	return int(h & (lockTableShardCount - 1))
}

// NewLockTable creates a new lock table with the given timeout.
func NewLockTable(timeout time.Duration) *LockTable {
	lt := &LockTable{
		timeout: timeout,
		// REQ001574: pre-allocate indices buffer for lockShards.
		indicesBuf: make([]int, lockTableShardCount),
	}
	for i := range lockTableShardCount {
		lt.shards[i].locks = make(map[uint64]*LockEntry)
	}
	return lt
}

// lockShards locks the shards for the given hashes in ascending order.
func (lt *LockTable) lockShards(hashes []uint64) []int {
	// REQ001574: reuse pre-allocated buffer to avoid per-acquisition allocation.
	buf := lt.indicesBuf[:len(hashes)]
	for i, h := range hashes {
		buf[i] = lt.shardFor(h)
	}
	// Sort unique indices ascending
	for i := 0; i < len(buf); i++ {
		for j := i + 1; j < len(buf); j++ {
			if buf[j] < buf[i] {
				buf[i], buf[j] = buf[j], buf[i]
			}
		}
	}
	// Deduplicate
	prev := -1
	for _, idx := range buf {
		if idx != prev {
			lt.shards[idx].mu.Lock()
			prev = idx
		}
	}
	// Return a copy so the caller owns the returned slice.
	out := make([]int, 0, len(buf))
	for i, idx := range buf {
		if i == 0 || idx != buf[i-1] {
			out = append(out, idx)
		}
	}
	return out
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
	return lt.LockWithTimeout(txnID, key, mode, lt.timeout)
}

// LockWithTimeout is like Lock but uses the supplied timeout instead of
// the table-wide default. A non-positive timeout means "do not wait"
// and the call returns ErrDeadlockTimeout immediately if the lock
// cannot be granted. The timeout is honored per call, which lets
// callers honor busy_timeout / busy_handler (REQ001301 / REQ001302).
func (lt *LockTable) LockWithTimeout(txnID uint64, key []byte, mode LockMode, timeout time.Duration) error {
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

	if timeout <= 0 {
		entry.waiters.dequeue(txnID)
		lt.aborted.Add(1)
		return ErrDeadlockTimeout
	}

	select {
	case <-node.done:
		if node.granted {
			return nil
		}
		return ErrDeadlockTimeout
	case <-time.After(timeout):
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

// busyHandler is the package-level callback invoked when a lock
// cannot be acquired. It returns the duration to wait before the
// next retry and a boolean indicating whether to keep retrying.
// Setting it to nil disables the busy-handler path. REQ001302.
var busyHandler atomic.Pointer[func(attempt int) (time.Duration, bool)]

// SetBusyHandler installs (or clears, when fn is nil) the package-level
// busy handler. While set, Lock attempts invoke it on contention to
// decide how long to wait. REQ001302.
func SetBusyHandler(fn func(attempt int) (time.Duration, bool)) {
	if fn == nil {
		busyHandler.Store(nil)
		return
	}
	busyHandler.Store(&fn)
}

// getBusyHandler returns the installed busy handler or nil.
func getBusyHandler() func(attempt int) (time.Duration, bool) {
	if p := busyHandler.Load(); p != nil {
		return *p
	}
	return nil
}

// busyLock is the contention-aware entry point. It retries via the
// busy handler if one is registered, otherwise it uses the table-wide
// default timeout. The lock table is consulted on each attempt.
// REQ001301 / REQ001302.
func (lt *LockTable) busyLock(txnID uint64, key []byte, mode LockMode) error {
	h := getBusyHandler()
	if h == nil {
		return lt.LockWithTimeout(txnID, key, mode, lt.timeout)
	}
	const maxAttempts = 32
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		// Try a zero-timeout probe first; if it fails immediately,
		// consult the handler to decide wait vs. abort.
		err := lt.LockWithTimeout(txnID, key, mode, 0)
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrDeadlockTimeout) {
			return err
		}
		wait, retry := h(attempt)
		if !retry {
			return ErrDeadlockTimeout
		}
		if wait <= 0 {
			continue
		}
		// Sleep then retry. We cannot sleep and reacquire atomically
		// with the lock here; the caller will retry via the next loop
		// iteration. This is a faithful SQLite-style busy-handler
		// approximation that keeps the VL layer self-contained.
		time.Sleep(wait)
	}
	return ErrDeadlockTimeout
}
