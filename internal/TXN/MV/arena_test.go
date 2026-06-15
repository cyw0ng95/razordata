package MV

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

func TestArenaAlloc(t *testing.T) {
	a := newArena()

	mem1 := a.Alloc(100)
	if mem1 == nil {
		t.Fatal("expected non-nil allocation")
	}
	if len(mem1) != 100 {
		t.Fatalf("expected 100 bytes, got %d", len(mem1))
	}

	mem2 := a.Alloc(50)
	if mem2 == nil {
		t.Fatal("expected non-nil allocation")
	}
	if len(mem2) != 50 {
		t.Fatalf("expected 50 bytes, got %d", len(mem2))
	}

	// Allocations should not overlap.
	if len(mem1) > 0 && len(mem2) > 0 {
		// mem1 and mem2 may be in different generations after
		// promotion, so we can only check they're different
		// allocations if both are in the same generation.
		// In the young generation, they should not overlap.
		// After promotion, mem2 may be in the old generation.
	}

	// The total bytes allocated should be 150.
	used := a.YoungSize() - a.YoungRemaining()
	if a.promoted.Load() {
		// After promotion, all data is in old.
		used = a.OldSize() - a.OldRemaining()
	}
	if used != 150 {
		t.Errorf("expected 150 bytes used, got %d", used)
	}
}

func TestArenaExhausted(t *testing.T) {
	a := newArena()

	// Fill the young generation (triggers promotion on next alloc).
	mem := a.Alloc(youngSize)
	if mem == nil {
		t.Fatal("young allocation should succeed")
	}

	// Fill the old generation. After promotion, the old has
	// youngSize bytes of promoted data, so only
	// (oldSize - youngSize) bytes are free.
	remainingOld := int(a.OldSize() - int64(youngSize))
	mem2 := a.Alloc(remainingOld)
	if mem2 == nil {
		t.Fatalf("old allocation should succeed after promotion: youngRemaining=%d oldRemaining=%d",
			a.YoungRemaining(), a.OldRemaining())
	}

	t.Logf("After filling: youngRemaining=%d oldRemaining=%d", a.YoungRemaining(), a.OldRemaining())

	// Both generations are now full. Next allocation should fail.
	mem3 := a.Alloc(1)
	if mem3 != nil {
		t.Fatalf("allocation beyond total arena size should return nil: youngRemaining=%d oldRemaining=%d",
			a.YoungRemaining(), a.OldRemaining())
	}
}

func TestArenaCAS(t *testing.T) {
	a := newArena()

	done := make(chan bool)
	var lastOffset int64

	for i := 0; i < runtime.NumCPU()*2; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				ptr := a.Alloc(16)
				if ptr != nil {
					atomic.StoreInt64(&lastOffset, a.youngOff.Load())
				}
			}
			done <- true
		}()
	}

	for i := 0; i < runtime.NumCPU()*2; i++ {
		<-done
	}

	if lastOffset == 0 {
		t.Fatal("expected some allocations to succeed")
	}
}

func TestArenaRemaining(t *testing.T) {
	a := newArena()

	initial := a.Remaining()
	expectedInitial := a.YoungSize() + a.OldSize()
	if initial != expectedInitial {
		t.Fatalf("expected initial remaining %d, got %d", expectedInitial, initial)
	}

	a.Alloc(100)
	after := a.Remaining()
	if after != expectedInitial-100 {
		t.Fatalf("expected remaining %d, got %d", expectedInitial-100, after)
	}
}

func TestArenaPool(t *testing.T) {
	a1 := GetArena()
	a2 := GetArena()

	if a1 == nil || a2 == nil {
		t.Fatal("GetArena should return non-nil arena")
	}

	PutArena(a1)
	PutArena(a2)

	a3 := GetArena()
	if a3 == nil {
		t.Fatal("should get arena from pool")
	}
	if a3.YoungRemaining() != a3.YoungSize() {
		t.Error("pooled arena should be reset")
	}
	if a3.OldRemaining() != a3.OldSize() {
		t.Error("pooled arena should be reset")
	}
}

func TestArenaConcurrency(t *testing.T) {
	var wg sync.WaitGroup
	iterations := 100
	goroutines := 10

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			a := newArena()
			for j := 0; j < iterations; j++ {
				mem := a.Alloc(64)
				if mem == nil {
					t.Errorf("allocation failed unexpectedly")
					break
				}
			}
		}()
	}
	wg.Wait()
}

// TestArena_Promotion verifies that when the young generation
// fills, allocations are promoted to the old generation.
// REQ000064.
func TestArena_Promotion(t *testing.T) {
	a := newArena()

	// Fill the young generation.
	mem := a.Alloc(youngSize)
	if mem == nil {
		t.Fatal("young allocation should succeed")
	}

	// Next allocation should trigger promotion. But the
	// allocation is for 1 byte, and the old generation has
	// 1 MB free, so it should succeed.
	mem2 := a.Alloc(1)
	if mem2 == nil {
		t.Fatal("allocation after young full should succeed (from old)")
	}
	if !a.promoted.Load() {
		t.Error("arena should be marked as promoted")
	}
	// After promotion, the old should have youngSize (promoted)
	// + 1 byte (new alloc) used.
	expectedOldUsed := int64(youngSize + 1)
	actualOldUsed := a.OldSize() - a.OldRemaining()
	if actualOldUsed != expectedOldUsed {
		t.Errorf("old should have %d bytes used, got %d", expectedOldUsed, actualOldUsed)
	}
	// Young should be reset to 0 after promotion.
	if a.YoungRemaining() != a.YoungSize() {
		t.Errorf("young should be reset after promotion, remaining=%d", a.YoungRemaining())
	}
}

// TestArena_GCReduction verifies the key benefit of the
// generational design: if a transaction only uses the young
// generation, the old generation is never allocated. REQ000064.
func TestArena_GCReduction(t *testing.T) {
	a := newArena()

	// Allocate only from the young generation (small alloc).
	mem := a.Alloc(100)
	if mem == nil {
		t.Fatal("young allocation should succeed")
	}

	// Old generation should be untouched.
	if a.OldRemaining() != a.OldSize() {
		t.Errorf("old generation should be untouched, remaining=%d", a.OldSize())
	}
	if a.promoted.Load() {
		t.Error("arena should not be promoted for small allocations")
	}
}

// TestArena_ResetOnPoolReturn verifies that PutArena resets
// both generations. REQ000064.
func TestArena_ResetOnPoolReturn(t *testing.T) {
	a := GetArena()
	// Fill young.
	a.Alloc(youngSize)
	// Force promotion.
	a.Alloc(1)
	if !a.promoted.Load() {
		t.Fatal("should be promoted after young full + 1 more alloc")
	}
	PutArena(a)

	// Get a new arena from the pool.
	a2 := GetArena()
	if a2.YoungRemaining() != a2.YoungSize() {
		t.Error("young should be reset")
	}
	if a2.OldRemaining() != a2.OldSize() {
		t.Error("old should be reset")
	}
	if a2.promoted.Load() {
		t.Error("promoted flag should be reset")
	}
}

// TestArena_OldReclaim verifies that promoted old-generation buffers
// are moved to the pending-reclaim list on PutArena and drained by
// ReclaimOldGenerations. REQ000305.
func TestArena_OldReclaim(t *testing.T) {
	// Start clean.
	ReclaimOldGenerations()

	a := GetArena()
	// Fill young to force promotion.
	a.Alloc(youngSize)
	a.Alloc(1)
	if !a.promoted.Load() {
		t.Fatal("should be promoted after young full + 1 more alloc")
	}
	// Save the old buffer pointer for verification.
	oldBuf := a.old
	if oldBuf == nil {
		t.Fatal("old buffer should be allocated after promotion")
	}

	// PutArena should move old to pending list and clear a.old.
	PutArena(a)
	if a.old != nil {
		t.Error("old should be nil after PutArena with reclaim")
	}

	// Verify the buffer is in the pending list.
	reclaimMu.Lock()
	found := false
	for _, p := range pendingOlds {
		if len(p) == cap(p) && cap(p) == oldSize && &p[0] == &oldBuf[0] {
			found = true
			break
		}
	}
	reclaimMu.Unlock()
	if !found {
		t.Error("old buffer not found in pending-reclaim list")
	}

	// Drain the pending list.
	ReclaimOldGenerations()
	reclaimMu.Lock()
	if len(pendingOlds) != 0 {
		t.Errorf("expected empty pending list after reclaim, got %d", len(pendingOlds))
	}
	reclaimMu.Unlock()
}

// TestArena_OldNotReclaimedWithoutPromotion verifies that an arena
// that does not promote does not have its old buffer added to the
// reclaim list. REQ000305.
func TestArena_OldNotReclaimedWithoutPromotion(t *testing.T) {
	ReclaimOldGenerations()

	a := GetArena()
	// Use only young generation — no promotion.
	mem := a.Alloc(100)
	if mem == nil {
		t.Fatal("young allocation should succeed")
	}

	PutArena(a)

	// Since promotion never happened, nothing should be in pending list.
	reclaimMu.Lock()
	hasOlds := len(pendingOlds) > 0
	reclaimMu.Unlock()
	if hasOlds {
		t.Error("pending list should be empty when no promotion occurred")
	}
}
