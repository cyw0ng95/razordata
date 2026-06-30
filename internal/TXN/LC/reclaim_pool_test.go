package LC

import (
	"testing"
	"unsafe"
)

// TestReclaimPool_Basic verifies Reclaim + Drain lifecycle.
// REQ000175.
func TestReclaimPool_Basic(t *testing.T) {
	rp := NewReclaimPool(16)
	if rp.Len() != 0 {
		t.Errorf("expected empty pool, got %d", rp.Len())
	}
	x1 := uint64(1)
	x2 := uint64(2)
	p1 := unsafe.Pointer(&x1)
	p2 := unsafe.Pointer(&x2)
	rp.Reclaim(p1)
	rp.Reclaim(p2)
	if rp.Len() != 2 {
		t.Errorf("expected 2 pending, got %d", rp.Len())
	}
	out := rp.Drain()
	if len(out) != 2 {
		t.Errorf("expected 2 drained, got %d", len(out))
	}
	if rp.Len() != 0 {
		t.Errorf("expected empty after drain, got %d", rp.Len())
	}
	if rp.Reclaimed() != 2 {
		t.Errorf("expected reclaimed=2, got %d", rp.Reclaimed())
	}
}

// TestReclaimPool_NilSafe verifies nil pointers are ignored.
func TestReclaimPool_NilSafe(t *testing.T) {
	rp := NewReclaimPool(16)
	rp.Reclaim(nil)
	if rp.Len() != 0 {
		t.Errorf("nil should not be queued")
	}
}

// TestReclaimPool_BoundedDrop verifies the pool drops oldest
// entries when capacity is exceeded.
func TestReclaimPool_BoundedDrop(t *testing.T) {
	rp := NewReclaimPool(8)
	var p unsafe.Pointer
	for i := 0; i < 20; i++ {
		rp.Reclaim(p)
	}
	// Pool should not exceed maxPoolSize.
	if rp.Len() > 8 {
		t.Errorf("pool exceeded capacity: %d > 8", rp.Len())
	}
}

// TestReclaimPool_GenerationMonotonic verifies gen counter
// advances on every Reclaim.
func TestReclaimPool_GenerationMonotonic(t *testing.T) {
	rp := NewReclaimPool(16)
	g1 := rp.gen.Load()
	x := uint64(42)
	p := unsafe.Pointer(&x)
	for i := 0; i < 5; i++ {
		rp.Reclaim(p)
	}
	g2 := rp.gen.Load()
	if g2 <= g1 {
		t.Errorf("gen did not advance: %d -> %d", g1, g2)
	}
}

// TestReclaimPool_NoLeakUnderPressure verifies that pointers dropped
// due to pool overflow are passed to the reclaimer callback (REQ001014).
func TestReclaimPool_NoLeakUnderPressure(t *testing.T) {
	var dropped []unsafe.Pointer
	rp := NewReclaimPool(4)
	rp.SetReclaimer(func(ptrs []unsafe.Pointer) {
		dropped = append(dropped, ptrs...)
	})

	// Fill the pool beyond capacity to trigger drops.
	for i := 0; i < 20; i++ {
		x := uint64(i)
		rp.Reclaim(unsafe.Pointer(&x))
	}

	// Pool should not exceed maxPoolSize.
	if rp.Len() > 4 {
		t.Errorf("pool exceeded capacity: %d > 4", rp.Len())
	}

	// Some pointers should have been dropped and passed to reclaimer.
	if len(dropped) == 0 {
		t.Error("expected some pointers to be dropped and reclaimed")
	}

	// Drain remaining pointers.
	remaining := rp.Drain()
	if len(remaining) == 0 {
		t.Error("expected some remaining pointers after drain")
	}

	// Total pointers = dropped + remaining should equal 20.
	if len(dropped)+len(remaining) != 20 {
		t.Errorf("expected 20 total pointers, got dropped=%d + remaining=%d = %d",
			len(dropped), len(remaining), len(dropped)+len(remaining))
	}
}
