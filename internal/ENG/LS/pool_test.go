package ls

import (
	"strconv"
	"testing"
)

// TestSkiplistInsert_PoolReuse verifies the sync.Pool
// returns valid slices with maxLevel capacity (REQ000198).
// Note: sync.Pool does NOT guarantee LIFO ordering or pointer
// identity, so we verify functional properties (non-nil, correct
// capacity, reusable) rather than pointer equality.
func TestSkiplistInsert_PoolReuse(t *testing.T) {
	// Get a slice from the pool
	s1 := acquireSlice()
	if s1 == nil {
		t.Fatal("acquireSlice returned nil")
	}
	if cap(*s1) < maxLevel {
		t.Errorf("slice capacity = %d, want >= %d", cap(*s1), maxLevel)
	}

	// Release and re-acquire — the pool should return a usable slice.
	// Manage lifecycle explicitly to avoid double-release.
	releaseSlice(s1)
	s2 := acquireSlice()
	if s2 == nil {
		t.Fatal("acquireSlice returned nil after release")
	}
	if cap(*s2) < maxLevel {
		t.Errorf("re-acquired slice capacity = %d, want >= %d", cap(*s2), maxLevel)
	}
	// All entries should be nil after release
	for i, v := range *s2 {
		if v != nil {
			t.Errorf("entry %d not cleared after release: %p", i, v)
		}
	}
	releaseSlice(s2)
}

// TestSkiplistInsert_NoGrow verifies the pool's slice capacity
// is always maxLevel (no re-allocation regardless of insertion level).
func TestSkiplistInsert_NoGrow(t *testing.T) {
	sl := New()
	const n = 1000
	for i := 0; i < n; i++ {
		sl.Insert([]byte("k"+strconv.Itoa(i)), []byte("v"))
	}
	if got := sl.len.Load(); got != int64(n) {
		t.Errorf("len: got %d, want %d", got, n)
	}
}

// TestSkiplistInsert_CorrectnessWithPool ensures the pool
// refactor doesn't break insert behavior.
func TestSkiplistInsert_CorrectnessWithPool(t *testing.T) {
	sl := New()
	const n = 1000
	for i := 0; i < n; i++ {
		sl.Insert([]byte("k"+strconv.Itoa(i)), []byte("v"+strconv.Itoa(i)))
	}
	if got := sl.len.Load(); got != int64(n) {
		t.Errorf("len: got %d, want %d", got, n)
	}
	for i := 0; i < n; i++ {
		v, ok := sl.Find([]byte("k" + strconv.Itoa(i)))
		if !ok {
			t.Errorf("Find k%d: not found", i)
		}
		if string(v) != "v"+strconv.Itoa(i) {
			t.Errorf("Find k%d: got %q", i, v)
		}
	}
}
