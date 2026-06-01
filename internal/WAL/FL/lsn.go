package fl

import (
	"sync/atomic"

	"github.com/cyw0ng95/razordata/internal/WAL/WR"
)

// LSN is a Log Sequence Number — a monotonically increasing identifier
// for a record's position in the WAL. The encoding is
// `segmentNumber * SegSize + offset`, so segment boundaries and LSN
// order are aligned (R04, R27).
type LSN = uint64

// lsnCounter allocates LSNs atomically. The counter is global per WAL
// instance and protected by an atomic.Uint64 so concurrent writers do
// not need to take a mutex on the hot path.
//
// The Foundation implementation provides basic allocation. The full
// implementation (segment-aware advancement, off-boundary detection)
// lands in the Core FL commit.
type lsnCounter struct {
	value atomic.Uint64
	segNo atomic.Uint64 // current segment number
}

// newLSNCounter returns a fresh counter starting at 0.
func newLSNCounter() *lsnCounter {
	return &lsnCounter{}
}

// Current returns the last allocated LSN. Callers use this to detect
// stale reads (R14: Readers use this to detect stale reads).
func (c *lsnCounter) Current() LSN {
	return c.value.Load()
}

// Next atomically advances the counter and returns the new value. This
// is the LSN the caller should stamp on the next record.
func (c *lsnCounter) Next() LSN {
	return c.value.Add(1)
}

// SetSegment updates the current segment number. The next Next() call
// will produce an LSN that lies in this segment. The implementation
// here is a Foundation stub — the full segment-aware LSN
// (segmentNumber * SegSize + offset) lands in the Core FL commit.
func (c *lsnCounter) SetSegment(segNo uint64) {
	c.segNo.Store(segNo)
}

// LSNFor computes the LSN for a given segment and offset using the
// design's encoding (R04):
//
//	lsn = segmentNumber * SegSize + offset
//
// `offset` is the byte position within the segment at which the record
// starts. This is a pure function — useful for the replayer to derive
// an LSN from a record it reads off disk.
func LSNFor(segmentNumber, offset uint64) LSN {
	return segmentNumber*uint64(wr.SegSize) + offset
}
