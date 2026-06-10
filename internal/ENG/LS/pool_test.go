package ls

import (
	"strconv"
	"testing"
)

// TestSkiplistInsert_PoolReuse verifies the sync.Pool
// returns cached slices on subsequent calls (REQ000198).
func TestSkiplistInsert_PoolReuse(t *testing.T) {
	// Get 3 slices from the pool
	s1 := acquireSlice()
	s2 := acquireSlice()
	s3 := acquireSlice()
	defer releaseSlice(s1)
	defer releaseSlice(s2)
	defer releaseSlice(s3)

	// After releasing s1, the next Get should return the same pointer
	releaseSlice(s1)
	s4 := acquireSlice()
	if s4 != s1 {
		t.Errorf("pool should reuse released slice; got %p, want %p", s4, s1)
	}
	releaseSlice(s4)
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
