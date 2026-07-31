//go:build debug

package cd

import (
	te "github.com/cyw0ng95/razordata/internal/DBG/TE"
)

// Buffer is a ring buffer for CTE events using TE's generic Ring.
type Buffer struct{ ring *te.Ring[CTEEvent] }

// NewBuffer creates a ring buffer with capacity rounded up to the next power of 2.
func NewBuffer(capacity int) *Buffer {
	return &Buffer{ring: te.NewRing[CTEEvent](capacity)}
}

// Append writes an event to the buffer, returning true if an old event was overwritten.
func (b *Buffer) Append(e CTEEvent) bool { return b.ring.Append(e) }

// Flush returns all buffered events and resets the buffer.
func (b *Buffer) Flush() []CTEEvent { return b.ring.Flush() }

// Stats returns buffer statistics.
func (b *Buffer) Stats() (cap int, used int64, dropped int64) { return b.ring.Stats() }