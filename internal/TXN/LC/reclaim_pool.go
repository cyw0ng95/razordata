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
}

// NewReclaimPool creates a reclaim pool with the given max size.
func NewReclaimPool(maxPoolSize int) *ReclaimPool {
	if maxPoolSize <= 0 {
		maxPoolSize = 4096
	}
	return &ReclaimPool{maxPoolSize: maxPoolSize}
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
		for i := 0; i < drop; i++ {
			rp.ptrs[i] = nil
		}
		rp.ptrs = rp.ptrs[drop:]
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
