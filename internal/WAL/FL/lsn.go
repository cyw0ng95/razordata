package fl

import (
	"sync/atomic"
)

// LSN is a Log Sequence Number — a monotonically increasing identifier
// for a record's position in the WAL. The encoding is defined by
// `wr.LSNFor` (segmentNumber * SegSize + offset), so segment
// boundaries and LSN order are aligned (R04, R27). Re-exported as a
// type alias here so callers that import FL don't need to also import
// WR for the LSN type.
type LSN = uint64

// lsnCounter allocates LSNs atomically. The counter is global per WAL
// instance and protected by an atomic.Uint64 so concurrent writers do
// not need to take a mutex on the hot path.
//
// In v1 the writer derives LSNs from the active segment number and
// writeOff directly (see wr.Append), so this counter is currently
// used only as a monotonic "last-allocated LSN" cache for stale-read
// detection (R14). The full implementation (segment-aware
// advancement) lands in a later commit if needed.
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
//
// Equivalent to Reserve(1) — the single-record hot path stays lock-free
// and contention-free for the common case.
func (c *lsnCounter) Next() LSN {
	return c.Reserve(1)
}

// Reserve atomically claims n sequential LSNs and returns the
// inclusive starting LSN. The claimed range is
// [start, start+n-1]; the next call (Next or Reserve) returns
// start+n, so callers can stamp records start, start+1, …, start+n-1
// without further atomic operations.
//
// REQ000541: reduces contention on the LSN counter when a batch of
// records is appended in one call — the writer takes the range once
// and increments locally, instead of taking the atomic on every record.
//
// Panics if n <= 0: a non-positive reservation would silently regress
// the counter and is always a programming error.
func (c *lsnCounter) Reserve(n int) LSN {
	if n <= 0 {
		panic("lsn: Reserve requires n > 0")
	}
	end := c.value.Add(uint64(n))
	return end - uint64(n) + 1
}

// SetSegment updates the current segment number. The next Next() call
// will produce an LSN that lies in this segment. The implementation
// here is a Foundation stub — the full segment-aware LSN
// (segmentNumber * SegSize + offset) lands in the Core FL commit.
func (c *lsnCounter) SetSegment(segNo uint64) {
	c.segNo.Store(segNo)
}
