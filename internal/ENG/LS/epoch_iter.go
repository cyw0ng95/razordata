package ls

import (
	"sync/atomic"
)

// epochManager abstracts the QSBR epoch manager for iterator registration.
// This allows iterators to register their active epoch so that
// reclamation (e.g., buffer pool page eviction) can wait for readers.
type epochManager interface {
	// RegisterIterator registers an iterator and returns the current epoch.
	// The iterator should use this epoch to determine when it's safe to
	// access borrowed buffers without risk of page eviction.
	RegisterIterator() uint64
	// DeregisterIterator unregisters an iterator from epoch tracking.
	DeregisterIterator(epoch uint64)
}

// globalEpochManager is the QSBR-based epoch manager used by iterators.
// This is a no-op stub that can be wired to the real QSBR manager in TXN/LC.
var globalEpochManager epochManager = &noopEpochManager{}

// SetEpochManager replaces the global epoch manager (for testing).
func SetEpochManager(em epochManager) {
	if em != nil {
		globalEpochManager = em
	}
}

// noopEpochManager is a no-op implementation for environments without QSBR.
type noopEpochManager struct{}

func (n *noopEpochManager) RegisterIterator() uint64   { return 0 }
func (n *noopEpochManager) DeregisterIterator(uint64) {}

// epochRegisteredIterator wraps an iterator and registers it with the
// epoch manager on creation, deregistering on Close.
// This ensures borrowed buffers remain valid for the iterator's lifetime.
type epochRegisteredIterator struct {
	// underlying is the wrapped iterator
	underlying RangeIter
	// epoch is the epoch at registration time
	epoch uint64
	// borrowedBuffers holds all borrowed buffers used by this iterator
	borrowedBuffers []*borrowedBuffer
	// closed tracks whether Close has been called
	closed atomic.Bool
}

// newEpochRegisteredIterator creates a new epoch-registered iterator.
// The iterator is registered with the epoch manager immediately.
func newEpochRegisteredIterator(underlying RangeIter) *epochRegisteredIterator {
	eri := &epochRegisteredIterator{
		underlying:      underlying,
		borrowedBuffers: make([]*borrowedBuffer, 0, 8),
	}
	eri.epoch = globalEpochManager.RegisterIterator()
	return eri
}

// registerBuffer registers a borrowed buffer with the epoch manager.
// The buffer's epoch is set to the iterator's registration epoch.
func (eri *epochRegisteredIterator) registerBuffer(bb *borrowedBuffer) {
	bb.Register(eri.epoch)
	eri.borrowedBuffers = append(eri.borrowedBuffers, bb)
}

// Next advances the underlying iterator.
func (eri *epochRegisteredIterator) Next() bool {
	if eri.closed.Load() {
		return false
	}
	return eri.underlying.Next()
}

// Key returns the key at the current position.
// For zero-copy iterators, this returns a borrowed buffer.
func (eri *epochRegisteredIterator) Key() []byte {
	if eri.closed.Load() {
		return nil
	}
	return eri.underlying.Key()
}

// Value returns the value at the current position.
// For zero-copy iterators, this returns a borrowed buffer.
func (eri *epochRegisteredIterator) Value() []byte {
	if eri.closed.Load() {
		return nil
	}
	return eri.underlying.Value()
}

// Err returns any error encountered.
func (eri *epochRegisteredIterator) Err() error {
	return eri.underlying.Err()
}

// Close deregisters the iterator from the epoch manager and releases
// all borrowed buffers. After Close, all borrowed buffers are invalid.
func (eri *epochRegisteredIterator) Close() error {
	if eri.closed.Swap(true) {
		return nil // already closed
	}

	// Deregister all borrowed buffers
	for _, bb := range eri.borrowedBuffers {
		bb.Deregister()
		putBorrowedBuffer(bb)
	}
	eri.borrowedBuffers = eri.borrowedBuffers[:0]

	// Deregister from epoch manager
	globalEpochManager.DeregisterIterator(eri.epoch)

	// Close underlying iterator
	if eri.underlying != nil {
		return eri.underlying.Close()
	}
	return nil
}

// BorrowedBufferCount returns the number of borrowed buffers registered.
func (eri *epochRegisteredIterator) BorrowedBufferCount() int {
	return len(eri.borrowedBuffers)
}