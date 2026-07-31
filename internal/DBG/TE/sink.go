//go:build debug

package te

import (
	"context"

	"github.com/cyw0ng95/razordata/internal/LOG/HK"
)

// Event is a single trace event in the ring.
type Event struct {
	Class  string
	Fields map[string]any
	Seq    uint64
}

// Sink implements hk.EventSink using a Ring buffer.
type Sink struct{ ring *Ring[Event] }

// NewSink creates a trace sink backed by the given ring.
func NewSink(ring *Ring[Event]) *Sink { return &Sink{ring: ring} }

// Emit writes a trace event to the ring.
func (s *Sink) Emit(_ context.Context, class string, fields map[string]any) {
	s.ring.Append(Event{Class: class, Fields: fields, Seq: s.ring.written.Load() + 1})
}

// Stats returns ring buffer statistics.
func (s *Sink) Stats() hk.TraceStats {
	cap, used, dropped := s.ring.Stats()
	return hk.TraceStats{Capacity: cap, Used: int(used), Dropped: dropped}
}

// NewTraceSink is the constructor called by LOG/HK sink.go init().
func NewTraceSink() hk.EventSink { return NewSink(NewRing[Event](4096)) }