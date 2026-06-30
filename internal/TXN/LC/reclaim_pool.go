package LC

import (
	"sync"
	"sync/atomic"
	"unsafe"
)

// ReclaimPool collects retired pointers safe to free (REQ000175).
type ReclaimPool struct {
	mu          sync.Mutex
	ptrs        []unsafe.Pointer
	gen         atomic.Uint64
	reclaimed   atomic.Uint64
	maxPoolSize int
	// REQ001014: reclaimer callback for dropped pointers.
	reclaimer func([]unsafe.Pointer)
}

// NewReclaimPool creates a reclaim pool with the given max size.
func NewReclaimPool(maxPoolSize int) *ReclaimPool {
	if maxPoolSize <= 0 {
		maxPoolSize = 4096
	}
	return &ReclaimPool{maxPoolSize: maxPoolSize}
}

// SetReclaimer sets a callback for pointers dropped due to pool overflow.
// The callback should free or reclaim the pointers (REQ001014).
func (rp *ReclaimPool) SetReclaimer(fn func([]unsafe.Pointer)) {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	rp.reclaimer = fn
}

// Reclaim adds a pointer to the pending-free list.
func (rp *ReclaimPool) Reclaim(ptr unsafe.Pointer) {
	if ptr == nil {
		return
	}
	rp.mu.Lock()
	defer rp.mu.Unlock()
	if len(rp.ptrs) >= rp.maxPoolSize {
		drop := len(rp.ptrs) / 2
		if drop < 1 {
			drop = 1
		}
		dropped := rp.ptrs[:drop]
		for i := range dropped {
			rp.ptrs[i] = nil
		}
		rp.ptrs = rp.ptrs[drop:]
		// REQ001014: pass dropped pointers to reclaimer instead of leaking.
		if rp.reclaimer != nil {
			rp.reclaimer(dropped)
		}
	}
	rp.ptrs = append(rp.ptrs, ptr)
	rp.gen.Add(1)
}

// Drain returns and clears all pending pointers.
func (rp *ReclaimPool) Drain() []unsafe.Pointer {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	out := rp.ptrs
	for i := range out {
		out[i] = nil
	}
	rp.ptrs = nil
	rp.reclaimed.Add(uint64(len(out)))
	return out
}

func (rp *ReclaimPool) Reclaimed() uint64 {
	return rp.reclaimed.Load()
}

func (rp *ReclaimPool) Len() int {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	return len(rp.ptrs)
}
