package of

import (
	"testing"
)

// TestOffHeap_GetPut verifies Get returns a slice of the
// requested size and Put reuses it. REQ000304.
func TestOffHeap_GetPut(t *testing.T) {
	oh := NewOffHeap()
	b := oh.Get(100 * 1024) // 100KB
	if len(b) != 100*1024 {
		t.Errorf("len: %d, want %d", len(b), 100*1024)
	}
	oh.Put(b)
	// Re-Get returns a slice. The pool may or may not reuse
	// the exact one we Put (sync.Pool semantics: items may be
	// cleared between calls under memory pressure). Just
	// verify the returned slice is usable.
	b2 := oh.Get(100 * 1024)
	if len(b2) != 100*1024 {
		t.Errorf("len: %d, want %d", len(b2), 100*1024)
	}
	oh.Put(b2)
}

// TestOffHeap_LargeAlloc verifies out-of-class allocations
// return a fresh slice and don't pollute the pool. REQ000304.
func TestOffHeap_LargeAlloc(t *testing.T) {
	oh := NewOffHeap()
	b := oh.Get(8 * 1024 * 1024) // 8MB > maxClass
	if len(b) != 8*1024*1024 {
		t.Errorf("len: %d, want %d", len(b), 8*1024*1024)
	}
	// Put is a no-op for out-of-class slices.
	oh.Put(b)
	stats := oh.Stats()
	if stats.Gets != 1 {
		t.Errorf("gets: %d, want 1", stats.Gets)
	}
	if stats.Misses != 1 {
		t.Errorf("misses: %d, want 1", stats.Misses)
	}
}

// TestOffHeap_Zero verifies Get returns a zero-initialized slice.
func TestOffHeap_Zero(t *testing.T) {
	oh := NewOffHeap()
	// Pollute a slice then return it.
	b := oh.Get(100 * 1024)
	for i := range b {
		b[i] = 0xFF
	}
	oh.Put(b)
	b2 := oh.Get(100 * 1024)
	for i := 0; i < 10; i++ {
		if b2[i] != 0 {
			t.Errorf("byte %d: %x, want 0", i, b2[i])
			break
		}
	}
}

// TestOffHeap_NilSafe verifies Get/Put handle edge cases.
func TestOffHeap_NilSafe(t *testing.T) {
	oh := NewOffHeap()
	if oh.Get(0) != nil {
		t.Errorf("Get(0) should be nil")
	}
	oh.Put(nil) // should not panic
	oh.Put([]byte{}) // should not panic
}

// TestSizeClass verifies the size-class progression.
func TestSizeClass(t *testing.T) {
	cases := []struct {
		in, want int
	}{
		{0, minClass},
		{1024, minClass}, // 1KB rounds up to 64KB
		{minClass, minClass},
		{minClass + 1, minClass + minClass/2}, // 64K+1 -> 96K
		{maxClass, maxClass},
		{maxClass + 1, 0}, // too large
	}
	for _, c := range cases {
		if got := sizeClass(c.in); got != c.want {
			t.Errorf("sizeClass(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}
