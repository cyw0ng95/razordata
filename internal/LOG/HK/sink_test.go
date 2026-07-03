//go:build !debug

package hk

import (
	"context"
	"testing"
	"time"
)

func TestNoopSink_Emit(t *testing.T) {
	s := noopSink{}
	s.Emit(context.Background(), "test", map[string]any{"key": "val"})
	stats := s.Stats()
	if stats.Capacity != 0 || stats.Dropped != 0 {
		t.Errorf("expected zero stats, got %+v", stats)
	}
}

func TestNoopMetricSink_Observe(t *testing.T) {
	m := noopMetricSink{}
	m.Observe("counter", 42)
	m.ObserveLatency("hist", time.Millisecond)
	snap := m.Snapshot()
	if len(snap.Counters) != 0 || len(snap.Histograms) != 0 {
		t.Errorf("expected empty snapshot, got %+v", snap)
	}
}

func TestNoopProfile_DumpProfile(t *testing.T) {
	p := noopProfile{}
	path, err := p.DumpProfile("heap", time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != "" {
		t.Errorf("expected empty path, got %q", path)
	}
}
