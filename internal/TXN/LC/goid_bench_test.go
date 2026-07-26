package LC

import (
	"testing"
)

func BenchmarkGoID(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = GoID()
	}
}

func BenchmarkGoIDParallel(b *testing.B) {
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = GoID()
		}
	})
}

func TestGoidOffsetFound(t *testing.T) {
	if goidOffset == ^uintptr(0) {
		t.Fatal("goidOffset not found — GoID() is using slow runtime.Stack path")
	}
	t.Logf("goidOffset = 0x%x (%d bytes)", goidOffset, goidOffset)
}
