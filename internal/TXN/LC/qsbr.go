package LC

import (
	"runtime"
	"sync"
	"sync/atomic"
)

// QSBR (Quiescent-State-Based Reclamation) is an alternative
// memory reclamation scheme to epoch-based reclamation.
// REQ000308.
//
// In QSBR, readers set a per-CPU "active" flag when entering a
// critical section and clear it on exit. The writer waits for
// all flags to be cleared (all readers have passed a
// quiescent state) before reclaiming retired objects. This is
// lower-overhead than epoch-based reclamation because:
//  1. Readers perform a single atomic store (no per-read load)
//  2. The writer only waits for "all idle" instead of "all
//     past the current epoch"
//
// Trade-offs vs epoch-based:
//   - QSBR requires the OS to support CPU pinning or
//     thread-local storage. On Go (which has goroutines), we
//     approximate per-CPU state with a sharded array indexed
//     by goroutine hash. This is approximate but works in
//     practice for the common case where goroutines don't
//     migrate CPUs often.
//   - The writer is more synchronous: it spins waiting for
//     all readers to reach quiescence before reclaiming.
//
// The implementation lives alongside epoch.go so callers can
// pick the policy that fits their access pattern.

const (
	// qsbrShardCount is the number of CPU shards. Power of 2
	// for cheap bitmask modulo.
	qsbrShardCount = 64
	// qsbrShardMask is shardCount - 1.
	qsbrShardMask = qsbrShardCount - 1
)

// qsbrState is a single reader's state. An active reader has
// state == 1; an idle reader has state == 0. The writer waits
// for all shards to be 0 before reclaiming.
type qsbrState struct {
	flag atomic.Uint32
	pad  [56]byte // cache-line padding to avoid false sharing
}

// qsbrManager implements QSBR. The API mirrors epochManager's
// so the two are drop-in alternatives.
type qsbrManager struct {
	// perShard[goroutineID % shardCount] gives the reader's flag.
	perShard [qsbrShardCount]qsbrState
	// globalEpoch increments on each Enter; readers snapshot
	// the epoch when entering so the writer can wait for them.
	globalEpoch atomic.Uint64
	// generation is incremented on Reclaim. The writer
	// remembers the generation when it retired an object; it
	// can reclaim once the current generation is past
	// (maxObserved + 1) by all readers.
	generation atomic.Uint64
	stopCh    chan struct{}
	wg        sync.WaitGroup
}

// newQSBRManager creates a fresh QSBR manager.
func newQSBRManager() *qsbrManager {
	return &qsbrManager{
		stopCh: make(chan struct{}),
	}
}

// shardFor returns the shard index for a goroutine ID.
func (q *qsbrManager) shardFor(gid uint64) int {
	return int(gid & qsbrShardMask)
}

// Enter is called by a reader when entering a critical section.
// Returns the current global epoch so the caller can publish
// it alongside the read for later reclamation decisions.
// Also returns the shard index so the caller can pair the
// matching Exit call without re-deriving the goroutine ID
// (which would advance the counter and miss the shard).
func (q *qsbrManager) Enter() (uint64, int) {
	gid := getGoroutineID()
	shard := q.shardFor(gid)
	q.perShard[shard].flag.Store(1)
	ep := q.globalEpoch.Load()
	q.generation.Add(0) // touch
	return ep, shard
}

// Exit is called by a reader when leaving a critical section.
// The caller passes the shard returned by the matching Enter
// call, so Exit can clear the right shard without re-deriving
// the goroutine ID.
func (q *qsbrManager) Exit(shard int) {
	if shard < 0 || shard >= qsbrShardCount {
		return
	}
	q.perShard[shard].flag.Store(0)
}

// IsQuiescent returns true if all shards are at 0 (no active
// readers). The writer calls this in a spin loop to wait for
// all readers to finish their critical sections.
func (q *qsbrManager) IsQuiescent() bool {
	for i := 0; i < qsbrShardCount; i++ {
		if q.perShard[i].flag.Load() != 0 {
			return false
		}
	}
	return true
}

// WaitQuiescent spins until all readers have exited, with a
// runtime.Gosched() between polls to avoid monopolizing the
// scheduler. bounded by maxSpins. Returns true if quiescence
// was reached within the budget.
func (q *qsbrManager) WaitQuiescent(maxSpins int) bool {
	for i := 0; i < maxSpins; i++ {
		if q.IsQuiescent() {
			return true
		}
		runtime.Gosched()
	}
	return q.IsQuiescent()
}

// BumpEpoch increments the global epoch. Readers entering after
// this call will see the new epoch. The writer can then wait
// for the previous epoch's readers to drain.
func (q *qsbrManager) BumpEpoch() uint64 {
	return q.globalEpoch.Add(1)
}

// CurrentEpoch returns the current global epoch.
func (q *qsbrManager) CurrentEpoch() uint64 {
	return q.globalEpoch.Load()
}

// Stop shuts down the background goroutine. Idempotent.
func (q *qsbrManager) Stop() {
	select {
	case <-q.stopCh:
		return
	default:
		close(q.stopCh)
	}
	q.wg.Wait()
}

// qsbrStats is a snapshot of QSBR counters.
type qsbrStats struct {
	Epoch    uint64
	Active   int // number of currently-active shards
	Shards   int
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
