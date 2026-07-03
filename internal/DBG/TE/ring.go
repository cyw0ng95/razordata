//go:build debug

package te

import (
	"sync/atomic"

	"github.com/cyw0ng95/razordata/internal/LOG/HK"
)

// Event is a single trace event in the ring.
type Event struct {
	Class  string
	Fields map[string]any
	Seq    uint64
}

// Ring is a fixed-capacity lock-free circular buffer.
type Ring struct {
	buf      []Event
	capacity uint64
	head     atomic.Uint64
	written  atomic.Uint64
	dropped  atomic.Int64
}

// NewRing creates a ring buffer with capacity rounded up to the next power of 2.
func NewRing(capacity int) *Ring {
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
	return &Ring{buf: make([]Event, cap64), capacity: cap64}
}

// Append writes an event. Returns true if it overwrote an old event.
func (r *Ring) Append(class string, fields map[string]any) bool {
	pos := r.head.Add(1) - 1
	idx := pos & (r.capacity - 1)
	if pos >= r.capacity {
		r.dropped.Add(1)
	}
	r.buf[idx] = Event{Class: class, Fields: fields, Seq: pos}
	r.written.Add(1)
	return pos >= r.capacity
}

// Stats returns current ring statistics.
func (r *Ring) Stats() hk.TraceStats {
	used := r.written.Load()
	if used > r.capacity {
		used = r.capacity
	}
	return hk.TraceStats{Capacity: int(r.capacity), Used: int(used), Dropped: r.dropped.Load()}
}
