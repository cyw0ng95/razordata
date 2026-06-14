package LC

import (
	"sync"
	"testing"
)

// TestGoID_SameGoroutine verifies the same goroutine gets the
// same ID across calls. REQ000181.
func TestGoID_SameGoroutine(t *testing.T) {
	id1 := GoID()
	id2 := GoID()
	if id1 == 0 {
		t.Fatalf("GoID returned 0 (failed to parse)")
	}
	if id1 != id2 {
		t.Errorf("GoID not stable: %d vs %d", id1, id2)
	}
}

// TestGoID_DifferentGoroutines verifies different goroutines
// get different IDs. REQ000181.
func TestGoID_DifferentGoroutines(t *testing.T) {
	const n = 8
	ids := make([]uint64, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ids[idx] = GoID()
		}(i)
	}
	wg.Wait()
	seen := make(map[uint64]bool)
	for _, id := range ids {
		if id == 0 {
			t.Errorf("GoID returned 0 in goroutine")
			continue
		}
		if seen[id] {
			t.Errorf("duplicate goroutine ID: %d", id)
		}
		seen[id] = true
	}
}

// TestGetGoroutineID_DelegatesToGoID verifies the backward-
// compat function uses GoID. REQ000181.
func TestGetGoroutineID_DelegatesToGoID(t *testing.T) {
	gid := getGoroutineID()
	if gid == 0 {
		t.Fatal("getGoroutineID returned 0")
	}
	if gid != GoID() {
		t.Errorf("getGoroutineID=%d, GoID=%d", gid, GoID())
	}
}
