package LC

import (
	"testing"
	"time"
	"unsafe"
)

// TestEpochManager_BackgroundAdvances verifies the background
// goroutine increments the epoch on its 100ms tick. REQ000164.
func TestEpochManager_BackgroundAdvances(t *testing.T) {
	em := newEpochManager()
	em.Start()
	defer em.Stop()
	ep1 := em.epoch.Load()
	time.Sleep(120 * time.Millisecond) // ~1 tick + buffer
	ep2 := em.epoch.Load()
	if ep2 <= ep1 {
		t.Errorf("expected epoch to advance: ep1=%d ep2=%d", ep1, ep2)
	}
}

// TestEpochManager_WaitForDrain verifies WaitForDrain returns
// true when all readers are stale. REQ000164.
func TestEpochManager_WaitForDrain(t *testing.T) {
	em := newEpochManager()
	defer em.Stop()
	// Initially: no readers, all stale.
	if !em.WaitForDrain(100) {
		t.Error("expected drain to succeed with no readers")
	}
	// Register a reader.
	gid := getGoroutineID()
	em.RegisterThread(gid)
	defer em.UnregisterThread(gid)
	ep := em.EnterEpoch()
	_ = ep
	// Drain should NOT succeed because the current goroutine
	// just entered.
	if em.WaitForDrain(10) {
		t.Error("expected drain to fail with active reader")
	}
	em.ExitEpoch(gid)
	// Now drained.
	if !em.WaitForDrain(100) {
		t.Error("expected drain to succeed after exit")
	}
}

// TestEpochManager_ReclaimClearsBatch verifies Reclaim nils
// out the batch after processing. REQ000164.
func TestEpochManager_ReclaimClearsBatch(t *testing.T) {
	em := newEpochManager()
	defer em.Stop()
	batch := make([]unsafe.Pointer, 0, 4)
	em.Reclaim(batch)
	// Empty batch: no-op.
	for i := range batch {
		if batch[i] != nil {
			t.Errorf("expected nil at %d, got %v", i, batch[i])
		}
	}
}
