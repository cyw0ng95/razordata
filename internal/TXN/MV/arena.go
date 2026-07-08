package MV

import (
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	nm "github.com/cyw0ng95/razordata/internal/ENG/NM"
)

const (
	youngSize          = 16 * 1024 // 16 KB
	oldSize            = 1 << 20   // 1 MB
	promotionThreshold = youngSize / 2
)

var (
	reclaimMu   sync.Mutex
	pendingOlds [][]byte
)

// Arena is a per-transaction bump allocator for VersionNode storage (REQ000064).
type Arena struct {
	// young is the current young generation. Allocations go here
	// first.
	young     []byte
	youngOff  atomic.Int64
	old       []byte
	oldOff    atomic.Int64
	promoted  atomic.Bool
	initOldMu sync.Mutex
}

// NewArena constructs a fresh, non-pooled Arena.
func NewArena() *Arena {
	return &Arena{
		young: make([]byte, youngSize),
	}
}

var newArena = NewArena

// Alloc allocates n bytes from the arena. Returns nil if exhausted.
func (a *Arena) Alloc(n int) []byte {
	if a.promoted.Load() {
		if off := a.tryAllocOld(n); off >= 0 {
			return a.old[off : off+int64(n)]
		}
		return nil
	}
	if off := a.tryAllocYoung(n); off >= 0 {
		return a.young[off : off+int64(n)]
	}
	a.promote()
	if off := a.tryAllocOld(n); off >= 0 {
		return a.old[off : off+int64(n)]
	}
	return nil
}

func (a *Arena) tryAllocYoung(n int) int64 {
	for {
		old := a.youngOff.Load()
		new := old + int64(n)
		if new > youngSize {
			return -1
		}
		if a.youngOff.CompareAndSwap(old, new) {
			return old
		}
	}
}

func (a *Arena) tryAllocOld(n int) int64 {
	for {
		old := a.oldOff.Load()
		new := old + int64(n)
		if new > oldSize {
			return -1
		}
		if a.oldOff.CompareAndSwap(old, new) {
			return old
		}
	}
}

func (a *Arena) promote() {
	a.initOldMu.Lock()
	if a.old == nil {
		a.old = make([]byte, oldSize)
		a.oldOff.Store(0)
	}
	a.initOldMu.Unlock()

	// Loop: incrementally promote deltas. We CAS youngOff to 0 to seal;
	// if it fails a concurrent tryAllocYoung bumped it, we loop back
	// and promote the new delta. This closes the TOCTOU window between
	// the youngOff load and the Store(0) (REQ000588).
	var promotedYoung int64
	var casFails int
	for {
		currentYoungOff := a.youngOff.Load()
		if currentYoungOff == 0 {
			a.promoted.Store(true)
			return
		}
		youngDelta := currentYoungOff - promotedYoung
		oldOff := a.oldOff.Load()
		new := oldOff + youngDelta
		if youngDelta == 0 {
			if a.youngOff.CompareAndSwap(currentYoungOff, 0) {
				a.promoted.Store(true)
				return
			}
			casFails++
			if casFails >= 16 {
				runtime.Gosched()
				time.Sleep(1 * time.Microsecond)
			} else if casFails >= 4 {
				runtime.Gosched()
			}
			continue
		}
		if new > oldSize {
			a.promoted.Store(true)
			return
		}
		if !a.oldOff.CompareAndSwap(oldOff, new) {
			casFails++
			if casFails >= 16 {
				runtime.Gosched()
				time.Sleep(1 * time.Microsecond)
			} else if casFails >= 4 {
				runtime.Gosched()
			}
			continue
		}
		copy(a.old[oldOff:new], a.young[promotedYoung:currentYoungOff])
		promotedYoung = currentYoungOff
		casFails = 0
	}
}

// Remaining returns the total available bytes across both young and old
// generations.
func (a *Arena) Remaining() int64 {
	if a.promoted.Load() {
		return oldSize - a.oldOff.Load()
	}
	return int64(youngSize-a.youngOff.Load()) + (oldSize - a.oldOff.Load())
}

// YoungRemaining returns the available bytes in the young generation.
func (a *Arena) YoungRemaining() int64 {
	return int64(youngSize - a.youngOff.Load())
}

// OldRemaining returns the available bytes in the old generation.
func (a *Arena) OldRemaining() int64 {
	return oldSize - a.oldOff.Load()
}

// Size returns the total capacity of the arena in bytes (young + old).
func (a *Arena) Size() int64 {
	return int64(youngSize + oldSize)
}

// YoungSize returns the capacity of the young generation in bytes.
func (a *Arena) YoungSize() int64 {
	return int64(youngSize)
}

// OldSize returns the capacity of the old generation in bytes.
func (a *Arena) OldSize() int64 {
	return int64(oldSize)
}

// numaArenaPool holds per-NUMA-node arena pools to eliminate
// contention on a single global pool under high transaction
// throughput on multi-socket hosts (REQ000547).
type numaArenaPool struct {
	pools []sync.Pool
}

var numaPool = newNumaArenaPool()

func newNumaArenaPool() *numaArenaPool {
	nodeCount := nm.NodeCount()
	if nodeCount < 1 {
		nodeCount = 1
	}
	p := &numaArenaPool{
		pools: make([]sync.Pool, nodeCount),
	}
	for i := range p.pools {
		p.pools[i].New = func() any {
			return NewArena()
		}
	}
	return p
}

// AcquireArena acquires an Arena from the per-NUMA-node pool.
// Uses first-touch policy: the arena is allocated on the NUMA
// node of the calling goroutine (REQ000547).
func AcquireArena() *Arena {
	node := nm.CurrentNode()
	if node < 0 || node >= len(numaPool.pools) {
		node = 0
	}
	return numaPool.pools[node].Get().(*Arena)
}

// PutArena returns an Arena to its originating NUMA-node pool (REQ000547).
func PutArena(a *Arena) {
	a.youngOff.Store(0)
	if a.promoted.Load() && a.old != nil {
		reclaimMu.Lock()
		pendingOlds = append(pendingOlds, a.old)
		reclaimMu.Unlock()
		a.old = nil
	}
	a.oldOff.Store(0)
	a.promoted.Store(false)
	node := nm.CurrentNode()
	if node < 0 || node >= len(numaPool.pools) {
		node = 0
	}
	numaPool.pools[node].Put(a)
}

// ReclaimOldGenerations drains the pending old-generation buffer list (REQ000305).
func ReclaimOldGenerations() {
	reclaimMu.Lock()
	for i := range pendingOlds {
		pendingOlds[i] = nil
	}
	pendingOlds = pendingOlds[:0]
	reclaimMu.Unlock()
}
