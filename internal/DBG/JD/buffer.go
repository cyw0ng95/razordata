//go:build debug

package JD

import (
	"math/bits"
	"sync/atomic"
)

type Buffer struct {
	events   []JoinEvent
	capacity uint64
	head     atomic.Uint64
	written  atomic.Uint64
	dropped  atomic.Int64
}

func NewBuffer(capacity int) *Buffer {
	if capacity <= 0 {
		capacity = 1
	}
	capPow2 := uint64(1) << bits.Len(uint(capacity-1))
	return &Buffer{
		events:   make([]JoinEvent, capPow2),
		capacity: capPow2,
	}
}

func (b *Buffer) Append(e JoinEvent) bool {
	pos := b.written.Add(1) - 1
	idx := pos & (b.capacity - 1)
	b.events[idx] = e
	b.head.Add(1)
	return pos >= b.capacity
}

func (b *Buffer) Flush() []JoinEvent {
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
	result := make([]JoinEvent, n)
	for i := uint64(0); i < n; i++ {
		result[i] = b.events[(start+i)&(b.capacity-1)]
	}
	return result
}

func (b *Buffer) Stats() (capacity int, used int64, dropped int64) {
	head := b.head.Load()
	dropped = b.dropped.Load()

	used = int64(head)
	if used > int64(b.capacity) {
		used = int64(b.capacity)
	}

	return int(b.capacity), used, dropped
}
