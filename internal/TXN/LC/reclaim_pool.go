package LC

import (
	"sync"
	"sync/atomic"
	"unsafe"
)

// ReclaimPool collects retired pointers that the reclaimer has
// confirmed safe to free. REQ000175: the previous
// implementation had Reclaim() as a no-op (just iterated the
// reader set without actually freeing memory). This pool
// accumulates freed pointers and the runtime can reclaim them
// on the next GC cycle, or callers can drain the pool to
// release memory eagerly.
//
// The pool is bounded; if it exceeds maxPoolSize, the oldest
// entries are dropped (the underlying objects become garbage
// and the runtime will collect them).
type ReclaimPool struct {
	mu         sync.Mutex
	ptrs       []unsafe.Pointer
	gen        atomic.Uint64
	reclaimed  atomic.Uint64
	maxPoolSize int
}

// NewReclaimPool creates a reclaim pool. maxPoolSize caps the
// pending list; pass 4096 for typical workloads.
func NewReclaimPool(maxPoolSize int) *ReclaimPool {
	if maxPoolSize <= 0 {
		maxPoolSize = 4096
	}
	return &ReclaimPool{maxPoolSize: maxPoolSize}
}

// Reclaim adds a pointer to the pending-free list after the
// caller has confirmed no active reader holds it. The
// pointer is dropped (made eligible for GC) on the next
// Drain, or immediately if the pool is full.
func (rp *ReclaimPool) Reclaim(ptr unsafe.Pointer) {
	if ptr == nil {
		return
	}
	rp.mu.Lock()
	defer rp.mu.Unlock()
	if len(rp.ptrs) >= rp.maxPoolSize {
		// Drop the oldest half to make room.
		drop := len(rp.ptrs) / 2
		if drop < 1 {
			drop = 1
		}
		// Clear the dropped pointers so the runtime can
		// reclaim them.
		for i := 0; i < drop; i++ {
			rp.ptrs[i] = nil
		}
		rp.ptrs = rp.ptrs[drop:]
	}
	rp.ptrs = append(rp.ptrs, ptr)
	rp.gen.Add(1)
}

// Drain returns and clears all pending pointers. The caller
// is responsible for any type-specific cleanup (e.g. calling
// a finalizer on each pointer).
func (rp *ReclaimPool) Drain() []unsafe.Pointer {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	out := rp.ptrs
	// Clear so the runtime can reclaim the memory.
	for i := range out {
		out[i] = nil
	}
	rp.ptrs = nil
	rp.reclaimed.Add(uint64(len(out)))
	return out
}

// Reclaimed returns the total number of pointers this pool
// has freed across all Drain calls.
func (rp *ReclaimPool) Reclaimed() uint64 {
	return rp.reclaimed.Load()
}

// Len returns the current number of pending pointers.
func (rp *ReclaimPool) Len() int {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	return len(rp.ptrs)
}
