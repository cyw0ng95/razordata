//go:build debug

package te

import (
	"sync/atomic"
)

// Ring is a fixed-capacity lock-free circular buffer for trace events.
type Ring[T any] struct {
	buf      []T
	capacity uint64
	head     atomic.Uint64
	written  atomic.Uint64
	dropped  atomic.Int64
}

// NewRing creates a ring buffer with capacity rounded up to the next power of 2.
func NewRing[T any](capacity int) *Ring[T] {
	if capacity <= 0 {
		capacity = 1024
	}
	cap64 := uint64(capacity)
	cap64--
	cap64 |= cap64 >> 1
	cap64 |= cap64 >> 2
	cap64 |= cap64 >> 4
	cap64 |= cap64 >> 8
	cap64 |= cap64 >> 16
	cap64++
	return &Ring[T]{buf: make([]T, cap64), capacity: cap64}
}

// Append writes an event to the buffer, returning true if an old event was overwritten.
func (r *Ring[T]) Append(e T) bool {
	pos := r.written.Add(1) - 1
	idx := pos & (r.capacity - 1)
	r.buf[idx] = e
	r.head.Add(1)
	overwritten := pos >= r.capacity
	if overwritten {
		r.dropped.Add(1)
	}
	return overwritten
}

// Flush returns all buffered events and resets the buffer.
func (r *Ring[T]) Flush() []T {
	head := r.head.Swap(0)
	r.written.Store(0)
	r.dropped.Store(0)

	if head == 0 {
		return nil
	}

	n := head
	if n > r.capacity {
		n = r.capacity
	}

	start := head - n
	result := make([]T, n)
	for i := uint64(0); i < n; i++ {
		result[i] = r.buf[(start+i)&(r.capacity-1)]
	}
	return result
}

// Stats returns buffer statistics.
func (r *Ring[T]) Stats() (cap int, used int64, dropped int64) {
	head := r.head.Load()
	dropped = r.dropped.Load()

	used = int64(head)
	if used > int64(r.capacity) {
		used = int64(r.capacity)
	}

	return int(r.capacity), used, dropped
}