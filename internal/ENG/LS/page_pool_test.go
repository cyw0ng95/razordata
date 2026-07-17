package ls

import (
	"testing"
)

// TestPagePool_GetPut_RoundTrip verifies REQ001472: the package-level
// pageBufPool returns a fresh buffer on Get and accepts it back on Put.
func TestPagePool_GetPut_RoundTrip(t *testing.T) {
	bufPtr := pageBufPool.Get().(*[]byte)
	if cap(*bufPtr) != PageSize {
		t.Errorf("expected capacity %d, got %d", PageSize, cap(*bufPtr))
	}
	// Fill and return.
	for i := range *bufPtr {
		(*bufPtr)[i] = byte(i % 256)
	}
	pageBufPool.Put(bufPtr)

	// Get again — should get a fresh buffer.
	bufPtr2 := pageBufPool.Get().(*[]byte)
	// The buffer may be the same or different; either way, capacity must match.
	if cap(*bufPtr2) != PageSize {
		t.Errorf("expected capacity %d, got %d", PageSize, cap(*bufPtr2))
	}
}

// TestPreAlloc_CapacityMatchesEstimate verifies REQ001472: page buffer
// pool returns buffers of exactly PageSize capacity.
func TestPreAlloc_CapacityMatchesEstimate(t *testing.T) {
	for i := 0; i < 100; i++ {
		bufPtr := pageBufPool.Get().(*[]byte)
		if cap(*bufPtr) != PageSize {
			t.Errorf("iteration %d: expected cap %d, got %d", i, PageSize, cap(*bufPtr))
		}
		if len(*bufPtr) != PageSize {
			t.Errorf("iteration %d: expected len %d, got %d", i, PageSize, len(*bufPtr))
		}
		pageBufPool.Put(bufPtr)
	}
}
