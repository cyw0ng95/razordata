//go:build debug

package cd

import (
	"math/bits"
	"sync/atomic"
)

// Buffer is a ring buffer for CTE events.
type Buffer struct {
	events   []CTEEvent
	capacity uint64
	head     atomic.Uint64
	written  atomic.Uint64
	dropped  atomic.Int64
}

// NewBuffer creates a ring buffer with capacity rounded up to the next power of 2.
func NewBuffer(capacity int) *Buffer {
	if capacity <= 0 {
		capacity = 1
	}
	capPow2 := uint64(1) << bits.Len(uint(capacity-1))
	return &Buffer{
		events:   make([]CTEEvent, capPow2),
		capacity: capPow2,
	}
}

// Append writes an event to the buffer, returning true if an old event was overwritten.
func (b *Buffer) Append(e CTEEvent) bool {
	pos := b.written.Add(1) - 1
	idx := pos & (b.capacity - 1)
	b.events[idx] = e
	b.head.Add(1)
	overwritten := pos >= b.capacity
	if overwritten {
		b.dropped.Add(1)
	}
	return overwritten
}

// Flush returns all buffered events and resets the buffer.
func (b *Buffer) Flush() []CTEEvent {
	head := b.head.Swap(0)
	b.written.Store(0)
	b.dropped.Store(0)

	if head == 0 {
		return nil
	}

	n := head
	if n > b.capacity {
		n = b.capacity
	}

	start := head - n
	result := make([]CTEEvent, n)
	for i := uint64(0); i < n; i++ {
		result[i] = b.events[(start+i)&(b.capacity-1)]
	}
	return result
}

// Stats returns buffer statistics.
func (b *Buffer) Stats() (cap int, used int64, dropped int64) {
	head := b.head.Load()
	dropped = b.dropped.Load()

	used = int64(head)
	if used > int64(b.capacity) {
		used = int64(b.capacity)
	}

	return int(b.capacity), used, dropped
}