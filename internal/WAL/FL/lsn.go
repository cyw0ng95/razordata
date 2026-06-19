package fl

import (
	"sync/atomic"
)

// LSN is a Log Sequence Number.
type LSN = uint64

type lsnCounter struct {
	value atomic.Uint64
	segNo atomic.Uint64
}

func newLSNCounter() *lsnCounter {
	return &lsnCounter{}
}

// Current returns the last allocated LSN.
func (c *lsnCounter) Current() LSN {
	return c.value.Load()
}

// Next atomically advances the counter and returns the new value.
func (c *lsnCounter) Next() LSN {
	return c.Reserve(1)
}

// Reserve claims n sequential LSNs and returns the start (REQ000541).
func (c *lsnCounter) Reserve(n int) LSN {
	if n <= 0 {
		panic("lsn: Reserve requires n > 0")
	}
	end := c.value.Add(uint64(n))
	return end - uint64(n) + 1
}

// SetSegment updates the current segment number.
func (c *lsnCounter) SetSegment(segNo uint64) {
	c.segNo.Store(segNo)
}
