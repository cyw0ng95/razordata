//go:build debug

package JD

import (
	te "github.com/cyw0ng95/razordata/internal/DBG/TE"
)

// Buffer is a ring buffer for Join events using TE's generic Ring.
type Buffer struct{ ring *te.Ring[JoinEvent] }

// NewBuffer creates a ring buffer with capacity rounded up to the next power of 2.
func NewBuffer(capacity int) *Buffer {
	return &Buffer{ring: te.NewRing[JoinEvent](capacity)}
}

// Append writes an event to the buffer, returning true if an old event was overwritten.
func (b *Buffer) Append(e JoinEvent) bool { return b.ring.Append(e) }

// Flush returns all buffered events and resets the buffer.
func (b *Buffer) Flush() []JoinEvent { return b.ring.Flush() }

// Stats returns buffer statistics.
func (b *Buffer) Stats() (cap int, used int64, dropped int64) { return b.ring.Stats() }