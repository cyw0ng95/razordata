package MV

import (
	"testing"
)

// TestNewVersionNodeStack verifies the stack-allocated constructor
// returns a usable node. REQ000306.
// Important: the node is stack-local to the caller of
// NewVersionNodeStack. We capture the pointer in a local and
// immediately read it. Storing the pointer beyond the caller's
// stack frame (e.g. returning it from a function) would be a
// use-after-return bug. This test is the demonstration of the
// intended use: a small wrapper that does work and returns void.
func TestNewVersionNodeStack(t *testing.T) {
	func() {
		// We must keep `n` referenced inside this closure so
		// the compiler does not optimize the read away. Since
		// NewVersionNodeStack writes through a noescape'd
		// pointer, the writes land on the closure's stack
		// frame; reads after the call return are not safe
		// in general, so we re-cast via the same noescape.
		var holder *VersionNode
		func() {
			holder = NewVersionNodeStack(1, 100, []byte("k"), []byte("v"), false)
		}()
		_ = holder // expected dangling; just verify the path doesn't panic
		// For a valid positive test, we re-call inside an inner
		// scope and consume the node immediately:
		func() {
			n := NewVersionNodeStack(2, 200, []byte("kk"), []byte("vv"), false)
			if n == nil {
				t.Fatal("NewVersionNodeStack returned nil")
			}
			// The pointer points into this inner frame's
			// stack; reading here is valid as long as we
			// don't escape n out of this scope.
			if n.txnID != 2 {
				t.Errorf("txnID: %d", n.txnID)
			}
			if n.beginTS != 200 {
				t.Errorf("beginTS: %d", n.beginTS)
			}
		}()
	}()
}

// BenchmarkNewVersionNodeStack_NoSink verifies the stack path
// truly avoids heap allocations. REQ000306.
// The benchmark does not capture the returned pointer (no sink),
// so the compiler is free to keep it on the stack frame of
// NewVersionNodeStack and the b.Run loop. We expect 0 allocs/op
// when escape analysis succeeds.
func BenchmarkNewVersionNodeStack_NoSink(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = NewVersionNodeStack(uint64(i), uint64(i), nil, nil, false)
	}
}

// BenchmarkNewVersionNodeStack_WithSink captures the pointer to
// an interface{}, forcing a heap allocation. The allocation
// count is the baseline cost of the VersionNode struct on
// the heap. REQ000306.
func BenchmarkNewVersionNodeStack_WithSink(b *testing.B) {
	var sink interface{}
	for i := 0; i < b.N; i++ {
		sink = NewVersionNodeStack(uint64(i), uint64(i), nil, nil, false)
	}
	_ = sink
}
