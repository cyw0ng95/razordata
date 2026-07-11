package DT

import "sync"

// DeferredFKCheck is a closure that performs a FK validation check.
// Stored in the deferred queue for INITIALLY DEFERRED FKs.
type DeferredFKCheck func() error

var (
	deferredFKQueue []DeferredFKCheck
	deferredFKMu    sync.Mutex
)

// EnqueueDeferredFKCheck adds a FK check to the deferred queue (REQ001311).
func EnqueueDeferredFKCheck(check DeferredFKCheck) {
	deferredFKMu.Lock()
	deferredFKQueue = append(deferredFKQueue, check)
	deferredFKMu.Unlock()
}

// FlushDeferredFKChecks runs all queued deferred FK checks and returns
// the first error. Clears the queue regardless of success or failure.
func FlushDeferredFKChecks() error {
	deferredFKMu.Lock()
	queue := deferredFKQueue
	deferredFKQueue = nil
	deferredFKMu.Unlock()
	for _, check := range queue {
		if err := check(); err != nil {
			return err
		}
	}
	return nil
}

// ClearDeferredFKChecks empties the deferred FK queue (used on ROLLBACK).
func ClearDeferredFKChecks() {
	deferredFKMu.Lock()
	deferredFKQueue = nil
	deferredFKMu.Unlock()
}

// HasDeferredFKChecks returns true if the queue is non-empty.
func HasDeferredFKChecks() bool {
	deferredFKMu.Lock()
	defer deferredFKMu.Unlock()
	return len(deferredFKQueue) > 0
}
