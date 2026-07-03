//go:build debug

package te

import (
	"context"

	"github.com/cyw0ng95/razordata/internal/LOG/HK"
)

// Sink implements hk.EventSink using a Ring buffer.
type Sink struct{ ring *Ring }

// NewSink creates a trace sink backed by the given ring.
func NewSink(ring *Ring) *Sink { return &Sink{ring: ring} }

// Emit writes a trace event to the ring.
func (s *Sink) Emit(_ context.Context, class string, fields map[string]any) {
	s.ring.Append(class, fields)
}

// Stats returns ring buffer statistics.
func (s *Sink) Stats() hk.TraceStats { return s.ring.Stats() }

// NewTraceSink is the constructor called by LOG/HK sink.go init().
func NewTraceSink() hk.EventSink { return NewSink(NewRing(4096)) }
