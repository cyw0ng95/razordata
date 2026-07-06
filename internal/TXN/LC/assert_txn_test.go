//go:build debug

package LC

import (
	"testing"
)

func TestTXN_Assert_EpochMonotonic(t *testing.T) {
	em := newEpochManager()
	if em.epoch.Load() != 1 {
		t.Fatalf("expected initial epoch 1, got %d", em.epoch.Load())
	}

	epoch1 := em.EnterEpoch()
	if epoch1 != 1 {
		t.Fatalf("expected epoch 1, got %d", epoch1)
	}

	em.epoch.Add(1)
	epoch2 := em.EnterEpoch()
	if epoch2 < epoch1 {
		t.Fatalf("epoch regression: %d < %d", epoch2, epoch1)
	}

	em.epoch.Add(1)
	epoch3 := em.EnterEpoch()
	if epoch3 < epoch2 {
		t.Fatalf("epoch regression: %d < %d", epoch3, epoch2)
	}
}

func TestTXN_Assert_EpochEnterExit(t *testing.T) {
	em := newEpochManager()
	e1 := em.EnterEpoch()
	if e1 <= 0 {
		t.Fatalf("expected positive epoch, got %d", e1)
	}
	goid := getGoroutineID()
	em.ExitEpoch(goid)

	e2 := em.EnterEpoch()
	if e2 <= 0 {
		t.Fatalf("expected positive epoch after re-enter, got %d", e2)
	}
}