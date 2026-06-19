package LC

import (
	"runtime"
	"sync"
	"sync/atomic"
)

const (
	qsbrShardCount = 64
	qsbrShardMask  = qsbrShardCount - 1
)

type qsbrState struct {
	flag atomic.Uint32
	pad  [56]byte // cache-line padding to avoid false sharing
}

type qsbrManager struct {
	perShard    [qsbrShardCount]qsbrState
	globalEpoch atomic.Uint64
	generation  atomic.Uint64
	stopCh      chan struct{}
	wg          sync.WaitGroup
}

func newQSBRManager() *qsbrManager {
	return &qsbrManager{
		stopCh: make(chan struct{}),
	}
}

func (q *qsbrManager) shardFor(gid uint64) int {
	return int(gid & qsbrShardMask)
}

// Enter is called by a reader when entering a critical section.
func (q *qsbrManager) Enter() (uint64, int) {
	gid := getGoroutineID()
	shard := q.shardFor(gid)
	// Load globalEpoch BEFORE setting the flag to prevent
	// Store-Load reordering on weakly-ordered architectures
	// (ARM/POWER). This ensures the epoch is visible before
	// the flag signals critical section entry (REQ000597).
	ep := q.globalEpoch.Load()
	q.perShard[shard].flag.Store(1)
	q.generation.Add(0) // touch
	return ep, shard
}

// Exit is called by a reader when leaving a critical section.
func (q *qsbrManager) Exit(shard int) {
	if shard < 0 || shard >= qsbrShardCount {
		return
	}
	q.perShard[shard].flag.Store(0)
}

// IsQuiescent returns true if all shards are at 0.
func (q *qsbrManager) IsQuiescent() bool {
	for i := 0; i < qsbrShardCount; i++ {
		if q.perShard[i].flag.Load() != 0 {
			return false
		}
	}
	return true
}

// WaitQuiescent spins until all readers have exited.
func (q *qsbrManager) WaitQuiescent(maxSpins int) bool {
	for i := 0; i < maxSpins; i++ {
		if q.IsQuiescent() {
			return true
		}
		runtime.Gosched()
	}
	return q.IsQuiescent()
}

// BumpEpoch increments the global epoch.
func (q *qsbrManager) BumpEpoch() uint64 {
	return q.globalEpoch.Add(1)
}

func (q *qsbrManager) CurrentEpoch() uint64 {
	return q.globalEpoch.Load()
}

func (q *qsbrManager) Stop() {
	select {
	case <-q.stopCh:
		return
	default:
		close(q.stopCh)
	}
	q.wg.Wait()
}

type qsbrStats struct {
	Epoch  uint64
	Active int // number of currently-active shards
	Shards int
}

func (q *qsbrManager) Stats() qsbrStats {
	active := 0
	for i := 0; i < qsbrShardCount; i++ {
		if q.perShard[i].flag.Load() != 0 {
			active++
		}
	}
	return qsbrStats{
		Epoch:  q.globalEpoch.Load(),
		Active: active,
		Shards: qsbrShardCount,
	}
}
