//go:build debug

package ct

import (
	"math/bits"
	"sync/atomic"
	"time"

	"github.com/cyw0ng95/razordata/internal/LOG/HK"
)

const numBuckets = 20

// LatencyHist is a fixed-bucket log2 latency histogram.
type LatencyHist struct {
	buckets [numBuckets]atomic.Int64
	count   atomic.Int64
	sum     atomic.Int64
}

// Observe records a latency duration.
func (h *LatencyHist) Observe(d time.Duration) {
	ns := d.Nanoseconds()
	if ns <= 0 {
		ns = 1
	}
	bucket := bits.Len64(uint64(ns)) - 1
	if bucket >= numBuckets {
		bucket = numBuckets - 1
	}
	h.buckets[bucket].Add(1)
	h.count.Add(1)
	h.sum.Add(ns)
}

// Snapshot returns the histogram state.
func (h *LatencyHist) Snapshot() hk.LatencyHistSnapshot {
	buckets := make([]int64, numBuckets)
	for i := range h.buckets {
		buckets[i] = h.buckets[i].Load()
	}
	return hk.LatencyHistSnapshot{Buckets: buckets, Count: h.count.Load(), Sum: h.sum.Load()}
}
