package OP

import "sync"

// retainedPool is a generic pool with a retained-slice layer that
// survives GC cycles. sync.Pool clears on every GC, which caused high
// miss rates for large buffers under GC pressure. The retained slice
// holds up to retainCount buffers permanently; Get first tries
// retained, then sync.Pool, then allocates. Put fills retained first,
// then pool. REQ002244.
type retainedPool[T any] struct {
	pool      sync.Pool
	retained  []*T
	mu        sync.Mutex
	minCap    int
	retainCap int
	newFn     func() T
	resetFn   func(b *T)
}

func newRetainedPool[T any](minCap, retainCount int, newFn func() T, resetFn func(b *T)) *retainedPool[T] {
	rp := &retainedPool[T]{
		minCap:    minCap,
		retainCap: retainCount,
		newFn:     newFn,
		resetFn:   resetFn,
	}
	rp.pool.New = func() any {
		b := newFn()
		return &b
	}
	return rp
}

func (rp *retainedPool[T]) get() *T {
	rp.mu.Lock()
	if n := len(rp.retained); n > 0 {
		b := rp.retained[n-1]
		rp.retained = rp.retained[:n-1]
		rp.mu.Unlock()
		rp.resetFn(b)
		return b
	}
	rp.mu.Unlock()
	if b, ok := rp.pool.Get().(*T); ok && b != nil {
		rp.resetFn(b)
		return b
	}
	return nil
}

func (rp *retainedPool[T]) put(b *T) {
	rp.mu.Lock()
	if len(rp.retained) < rp.retainCap {
		rp.retained = append(rp.retained, b)
		rp.mu.Unlock()
		return
	}
	rp.mu.Unlock()
	rp.pool.Put(b)
}

func (rp *retainedPool[T]) warm(n int) {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	for i := 0; i < n && len(rp.retained) < rp.retainCap; i++ {
		b := rp.newFn()
		rp.retained = append(rp.retained, &b)
	}
}