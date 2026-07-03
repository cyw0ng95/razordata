package hk

import (
	"context"
	"testing"
	"time"
)

func TestNoopSink_Emit(t *testing.T) {
	s := noopSink{}

	// Emit must not panic.
	s.Emit(context.Background(), "test-class", map[string]any{"k": "v"})
	s.Emit(context.Background(), "", nil)

	stats := s.Stats()
	if stats.Capacity != 0 || stats.Used != 0 || stats.Dropped != 0 {
		t.Errorf("expected zero TraceStats, got %+v", stats)
	}
}

func TestNoopMetricSink_Observe(t *testing.T) {
	m := noopMetricSink{}

	// Observe/ObserveLatency must not panic.
	m.Observe("counter", 1)
	m.Observe("counter", -5)
	m.ObserveLatency("hist", time.Millisecond)
	m.ObserveLatency("hist", 0)

	snap := m.Snapshot()
	if len(snap.Counters) != 0 {
		t.Errorf("expected empty Counters, got %v", snap.Counters)
	}
	if len(snap.Histograms) != 0 {
		t.Errorf("expected empty Histograms, got %v", snap.Histograms)
	}
}

func TestNoopProfile_DumpProfile(t *testing.T) {
	p := noopProfile{}

	s, err := p.DumpProfile("cpu", time.Second)
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	if s != "" {
		t.Errorf("expected empty string, got %q", s)
	}
}
