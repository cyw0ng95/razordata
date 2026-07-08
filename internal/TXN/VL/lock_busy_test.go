package VL

import (
	"errors"
	"testing"
	"time"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
)

// TestLockWithTimeout_NoWait verifies that a non-positive timeout
// returns immediately with ErrDeadlockTimeout. REQ001301.
func TestLockWithTimeout_NoWait(t *testing.T) {
	lt := NewLockTable(100 * time.Millisecond)
	defer lt.Unlock(1)

	if err := lt.LockWithTimeout(1, []byte("k"), LockModeExclusive, 0); err != nil {
		t.Fatalf("first LockWithTimeout: %v", err)
	}
	err := lt.LockWithTimeout(2, []byte("k"), LockModeExclusive, 0)
	if !errors.Is(err, ErrDeadlockTimeout) {
		t.Fatalf("second LockWithTimeout(0): want ErrDeadlockTimeout, got %v", err)
	}
}

// TestLockWithTimeout_HonorsBudget verifies LockWithTimeout waits
// roughly the budget then returns ErrDeadlockTimeout. REQ001301.
func TestLockWithTimeout_HonorsBudget(t *testing.T) {
	lt := NewLockTable(100 * time.Millisecond)
	defer lt.Unlock(1)

	if err := lt.LockWithTimeout(1, []byte("k"), LockModeExclusive, 100*time.Millisecond); err != nil {
		t.Fatalf("first LockWithTimeout: %v", err)
	}
	start := time.Now()
	err := lt.LockWithTimeout(2, []byte("k"), LockModeExclusive, 30*time.Millisecond)
	elapsed := time.Since(start)
	if !errors.Is(err, ErrDeadlockTimeout) {
		t.Fatalf("second LockWithTimeout: want ErrDeadlockTimeout, got %v", err)
	}
	if elapsed < 25*time.Millisecond {
		t.Errorf("returned too early: %v (want >= 25ms)", elapsed)
	}
	if elapsed > 200*time.Millisecond {
		t.Errorf("returned too late: %v (want < 200ms)", elapsed)
	}
}

// TestBusyLock_HandlerRetries verifies that an installed busy handler
// causes busyLock to retry on contention. REQ001302.
func TestBusyLock_HandlerRetries(t *testing.T) {
	lt := NewLockTable(50 * time.Millisecond)
	defer lt.Unlock(1)

	// Pre-acquire exclusive lock from txn 1.
	if err := lt.Lock(1, []byte("k"), LockModeExclusive); err != nil {
		t.Fatalf("pre-acquire: %v", err)
	}

	// Install a handler that returns (5ms wait, true) for two attempts
	// — txn 2 should retry once and then still time out because the
	// handler keeps retrying but the lock is never released.
	var attempts int
	SetBusyHandler(func(_ int) (time.Duration, bool) {
		attempts++
		if attempts > 3 {
			return 0, false
		}
		return 5 * time.Millisecond, true
	})
	defer SetBusyHandler(nil)

	start := time.Now()
	err := lt.busyLock(2, []byte("k"), LockModeExclusive)
	elapsed := time.Since(start)
	if !errors.Is(err, ErrDeadlockTimeout) {
		t.Fatalf("want ErrDeadlockTimeout, got %v", err)
	}
	if attempts < 2 {
		t.Errorf("expected >= 2 attempts, got %d", attempts)
	}
	if elapsed < 10*time.Millisecond {
		t.Errorf("busyLock returned too fast: %v", elapsed)
	}
}

// TestBusyLock_NoHandlerUsesTableDefault verifies that with no handler
// installed, busyLock falls back to LockWithTimeout using the table
// default. REQ001301 / REQ001302.
func TestBusyLock_NoHandlerUsesTableDefault(t *testing.T) {
	SetBusyHandler(nil)
	lt := NewLockTable(20 * time.Millisecond)
	defer lt.Unlock(1)

	if err := lt.Lock(1, []byte("k"), LockModeExclusive); err != nil {
		t.Fatalf("pre-acquire: %v", err)
	}
	start := time.Now()
	err := lt.busyLock(2, []byte("k"), LockModeExclusive)
	elapsed := time.Since(start)
	if !errors.Is(err, ErrDeadlockTimeout) {
		t.Fatalf("want ErrDeadlockTimeout, got %v", err)
	}
	if elapsed < 15*time.Millisecond {
		t.Errorf("too fast: %v (expected ~20ms)", elapsed)
	}
}

// TestBusyHandler_RegistryRoundTrip verifies DT-level handler
// registry survives a Set → Get → Unregister cycle. REQ001302.
func TestBusyHandler_RegistryRoundTrip(t *testing.T) {
	name := "rt_handler"
	DT.UnregisterBusyHandler(name)
	defer DT.UnregisterBusyHandler(name)

	DT.SetBusyHandler(name, func(_ int) bool { return false })
	gotName, gotFn := DT.GetBusyHandler()
	if gotName != name {
		t.Fatalf("GetBusyHandler name = %q, want %q", gotName, name)
	}
	if gotFn == nil {
		t.Fatal("GetBusyHandler returned nil callback")
	}
	DT.UnregisterBusyHandler(name)
	gotName, gotFn = DT.GetBusyHandler()
	if gotName != name {
		t.Fatalf("after unregister: name = %q, want %q (registry removal does not clear active name)", gotName, name)
	}
	if gotFn != nil {
		t.Fatal("after unregister: callback should be nil")
	}
}