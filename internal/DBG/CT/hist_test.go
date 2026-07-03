//go:build debug

package ct

import (
	"testing"
	"time"
)

func TestLatencyHist_Observe(t *testing.T) {
	h := &LatencyHist{}
	h.Observe(1 * time.Nanosecond)
	h.Observe(1 * time.Microsecond)
	h.Observe(1 * time.Millisecond)
	h.Observe(100 * time.Microsecond)

	snap := h.Snapshot()
	if snap.Count != 4 {
		t.Errorf("expected count 4, got %d", snap.Count)
	}
	if snap.Sum <= 0 {
		t.Errorf("expected positive sum, got %d", snap.Sum)
	}
	total := int64(0)
	for _, v := range snap.Buckets {
		total += v
	}
	if total != 4 {
		t.Errorf("expected total 4, got %d", total)
	}
}

func TestLatencyHist_ZeroDuration(t *testing.T) {
	h := &LatencyHist{}
	h.Observe(0)
	snap := h.Snapshot()
	if snap.Count != 1 {
		t.Errorf("expected count 1, got %d", snap.Count)
	}
}
