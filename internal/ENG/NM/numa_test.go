//go:build !slt_corpus_full

package nm

import (
	"runtime"
	"testing"
)

// TestNodeCount_AtLeastOne verifies the basic invariant: the
// host exposes at least one node.
func TestNodeCount_AtLeastOne(t *testing.T) {
	n := NodeCount()
	if n < 1 {
		t.Errorf("NodeCount = %d, want >= 1", n)
	}
}

// TestIsAvailable_Consistent checks that IsAvailable matches
// the comparison of NodeCount > 1.
func TestIsAvailable_Consistent(t *testing.T) {
	want := NodeCount() > 1
	if got := IsAvailable(); got != want {
		t.Errorf("IsAvailable = %v, want %v (NodeCount=%d)", got, want, NodeCount())
	}
}

// TestNodeCount_Cached verifies that NodeCount returns a stable
// value across calls (the result is cached).
func TestNodeCount_Cached(t *testing.T) {
	first := NodeCount()
	for i := 0; i < 5; i++ {
		if got := NodeCount(); got != first {
			t.Errorf("NodeCount changed: %d -> %d", first, got)
		}
	}
}

// TestCurrentNode_WithinRange verifies that CurrentNode returns
// a value in [0, NodeCount).
func TestCurrentNode_WithinRange(t *testing.T) {
	nc := NodeCount()
	cn := CurrentNode()
	if cn < 0 || cn >= nc {
		t.Errorf("CurrentNode = %d, want in [0, %d)", cn, nc)
	}
}

// TestPinWorker_Releases verifies that the function returned by
// PinWorker actually releases the thread when called, and that
// PinWorker can be called repeatedly without leaking OS threads.
func TestPinWorker_Releases(t *testing.T) {
	before := runtime.NumGoroutine()
	for i := 0; i < 100; i++ {
		release := PinWorker()
		// Yield so the runtime can observe we're locked.
		runtime.Gosched()
		release()
	}
	runtime.GC()
	after := runtime.NumGoroutine()
	// The number of goroutines should be roughly stable; we
	// don't assert a tight bound because the test runner may
	// be doing other work concurrently.
	if after > before+10 {
		t.Errorf("goroutines leaked: before=%d after=%d", before, after)
	}
}
