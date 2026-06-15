package MV

import (
	"sync"
	"sync/atomic"
)

// Arena size constants. REQ000064: the arena now has two
// generations — a small "young" generation for short-lived
// allocations and a larger "old" generation for long-lived ones.
//
// REQ000305: the old generation is epoch-reclaimed. When a
// transaction returns its arena via PutArena, the old-generation
// buffer is moved to a pending-reclaim list instead of being kept
// in the arena pool. The epoch manager periodically drains the
// list from its background goroutine, freeing the memory in bulk.
// This reduces GC pressure from large (1 MB) buffers and batches
// version-node reclamation at generation granularity.
//
// Per the iter-05/iter-06 design, the arena is *per-transaction*,
// not per-goroutine. The previous per-goroutine allocation path
// (getGoroutineID + arenaPoolSlice + allocFromThreadArena) was
// unsound because the goroutine-ID source was a process-global
// counter, not a real goroutine identity, and the global slice
// was unsynchronized.
const (
	// youngSize is the size of the young generation. Small enough
	// that frequent reclamation is cheap, large enough to hold
	// typical short-lived allocations (a few VersionNodes).
	youngSize = 16 * 1024 // 16 KB

	// oldSize is the size of the old generation. Larger because
	// the old generation accumulates allocations over the
	// transaction's lifetime.
	oldSize = 1 << 20 // 1 MB

	// promotionThreshold is the fraction of the young generation
	// that must be used before promotion to the old generation is
	// triggered. This avoids promoting trivially-small young
	// generations.
	promotionThreshold = youngSize / 2
)

// Global pending-reclaim list for old-generation buffers.
// REQ000305: buffers are moved here by PutArena and drained by
// the epoch manager's background goroutine.
var (
	reclaimMu   sync.Mutex
	pendingOlds [][]byte
)

// Arena is a per-transaction bump allocator for VersionNode storage.
// REQ000064: the arena now uses a generational design with two
// tiers:
//
//   - Young generation: small (16 KB), used for fresh allocations.
//     When it fills, the surviving data is promoted to the old
//     generation and a new young generation is allocated.
//
//   - Old generation: large (1 MB), accumulates promoted data over
//     the transaction's lifetime. Reset only when the arena is
//     returned to the pool.
//
// A single Arena is owned by a single transaction and MUST NOT be shared
// across goroutines concurrently: the per-txn mutex on the *tx* type is
// what serializes Insert/Delete calls and therefore serializes Arena.Alloc.
// Arenas are returned to the sync.Pool via PutArena once the transaction
// is committed or aborted.
//
// The generational design reduces GC pressure by separating
// short-lived and long-lived allocations. In the original
// single-allocation design, the entire 1 MB arena was kept alive
// for the transaction's lifetime even if only a few KB were used.
// With generations, if a transaction aborts after only using the
// young generation, the old generation is never allocated.
type Arena struct {
	// young is the current young generation. Allocations go here
	// first.
	young []byte

	// youngOff is the bump pointer for the young generation.
	youngOff atomic.Int64

	// old is the old generation. When the young generation fills,
	// it is promoted to the old generation.
	// REQ000305: allocated lazily on first promotion; nil until then.
	old []byte

	// oldOff is the bump pointer for the old generation.
	oldOff atomic.Int64

	// promoted tracks whether the current young generation has
	// been promoted to the old generation. This prevents
	// double-promotion if Alloc is called after promotion but
	// before a new young generation is allocated.
	promoted atomic.Bool

	// initOldMu serializes lazy old allocation. Only hit once per
	// arena lifecycle (on first promotion); negligible contention.
	initOldMu sync.Mutex
}

// NewArena constructs a fresh, non-pooled Arena. Used by Manager.Begin
// to give each transaction a private arena.
// REQ000305: the old generation is no longer pre-allocated. It is
// allocated lazily on first promotion and epoch-reclaimed on PutArena.
func NewArena() *Arena {
	return &Arena{
		young: make([]byte, youngSize),
	}
}

// newArena is an internal alias for NewArena used by tests in this
// package. Kept as a separate name to avoid churn in tests that
// previously called the unexported helper.
var newArena = NewArena

// Alloc allocates n bytes from the arena. Returns nil if the
// allocation cannot be satisfied (arena exhausted).
//
// The allocation strategy (REQ000064):
//  1. If the arena has been promoted, all allocations go to the
//     old generation (the young is "retired" after promotion).
//  2. If not yet promoted, try the young generation first.
//  3. If the young generation cannot satisfy the request, promote
//     the current young generation to the old generation and
//     allocate from the old.
//
// The promotion step is a no-op copy: the young generation's
// contents are copied to the old generation's free space. This is
// amortized O(1) per allocation because promotions are infrequent
// (only when the young generation fills).
func (a *Arena) Alloc(n int) []byte {
	// If already promoted, all allocations go to the old
	// generation. The young is retired (its data has been
	// copied to the old).
	if a.promoted.Load() {
		if off := a.tryAllocOld(n); off >= 0 {
			return a.old[off : off+int64(n)]
		}
		return nil
	}
	// Not yet promoted: try the young generation first.
	if off := a.tryAllocYoung(n); off >= 0 {
		return a.young[off : off+int64(n)]
	}
	// Young generation cannot satisfy the request. Promote
	// the current young to the old, then allocate from old.
	a.promote()
	if off := a.tryAllocOld(n); off >= 0 {
		return a.old[off : off+int64(n)]
	}
	return nil
}

// tryAllocYoung attempts to allocate n bytes from the young
// generation. Returns the offset on success, -1 on failure.
func (a *Arena) tryAllocYoung(n int) int64 {
	for {
		old := a.youngOff.Load()
		new := old + int64(n)
		if new > youngSize {
			return -1
		}
		if a.youngOff.CompareAndSwap(old, new) {
			return old
		}
	}
}

// tryAllocOld attempts to allocate n bytes from the old
// generation. Returns the offset on success, -1 on failure.
func (a *Arena) tryAllocOld(n int) int64 {
	for {
		old := a.oldOff.Load()
		new := old + int64(n)
		if new > oldSize {
			return -1
		}
		if a.oldOff.CompareAndSwap(old, new) {
			return old
		}
	}
}

// promote copies the current young generation to the old
// generation's free space, resets the young offset, and marks
// the arena as promoted. After promotion, subsequent Alloc
// calls go to the old generation.
//
// If the old generation does not have enough free space to hold
// the young generation, promotion fails silently — the arena is
// effectively exhausted. The caller will see nil from the
// subsequent Alloc call.
//
// REQ000305: old is allocated lazily on first promotion and may
// be nil. If nil, a fresh buffer is allocated.
func (a *Arena) promote() {
	// Get the current young offset (the amount of live data).
	youngUsed := a.youngOff.Load()
	if youngUsed == 0 {
		// Nothing to promote.
		a.promoted.Store(true)
		return
	}
	// Lazily allocate old generation on first promotion.
	// initOldMu serializes concurrent promote calls from the
	// same arena (which should not happen in normal use, but
	// the MV concurrency test shares arenas across goroutines).
	if a.old == nil {
		a.initOldMu.Lock()
		if a.old == nil {
			a.old = make([]byte, oldSize)
			a.oldOff.Store(0)
		}
		a.initOldMu.Unlock()
	}
	// Try to reserve space in the old generation.
	for {
		oldOff := a.oldOff.Load()
		new := oldOff + youngUsed
		if new > oldSize {
			// Not enough space in old. Mark as promoted anyway
			// so we don't retry. Subsequent allocations will
			// fail and the caller will see the error.
			a.promoted.Store(true)
			return
		}
		if a.oldOff.CompareAndSwap(oldOff, new) {
			// Copy young → old.
			copy(a.old[oldOff:new], a.young[:youngUsed])
			// Reset the young offset — its data is now in the
			// old generation. This prevents double-counting in
			// Remaining/YoungRemaining.
			a.youngOff.Store(0)
			a.promoted.Store(true)
			return
		}
	}
}

// Remaining returns the total remaining capacity across both
// generations. Used for diagnostics and tests.
func (a *Arena) Remaining() int64 {
	if a.promoted.Load() {
		return oldSize - a.oldOff.Load()
	}
	return int64(youngSize-a.youngOff.Load()) + (oldSize - a.oldOff.Load())
}

// YoungRemaining returns the remaining capacity of the young
// generation. Used for diagnostics.
func (a *Arena) YoungRemaining() int64 {
	return int64(youngSize - a.youngOff.Load())
}

// OldRemaining returns the remaining capacity of the old
// generation. Used for diagnostics.
func (a *Arena) OldRemaining() int64 {
	return oldSize - a.oldOff.Load()
}

// Size returns the total capacity of the arena (young + old).
func (a *Arena) Size() int64 {
	return int64(youngSize + oldSize)
}

// YoungSize returns the capacity of the young generation.
func (a *Arena) YoungSize() int64 {
	return int64(youngSize)
}

// OldSize returns the capacity of the old generation.
func (a *Arena) OldSize() int64 {
	return int64(oldSize)
}

var arenaPool = sync.Pool{
	New: func() any {
		return NewArena()
	},
}

// GetArena acquires an Arena from the pool. Callers MUST eventually
// return the arena via PutArena, exactly once, when the arena is no
// longer referenced by any live VersionNode.
func GetArena() *Arena {
	return arenaPool.Get().(*Arena)
}

// PutArena returns an Arena to the pool. REQ000305: if the arena
// was promoted to the old generation, the old generation buffer is
// moved to a pending-reclaim list instead of being kept in the pool.
// The epoch manager periodically drains this list.
// The young generation is always reset and returned to the pool.
func PutArena(a *Arena) {
	a.youngOff.Store(0)
	// If the old generation was used, move it to the pending-reclaim
	// list so the epoch manager can bulk-free it.
	if a.promoted.Load() && a.old != nil {
		reclaimMu.Lock()
		pendingOlds = append(pendingOlds, a.old)
		reclaimMu.Unlock()
		a.old = nil
	}
	a.oldOff.Store(0)
	a.promoted.Store(false)
	arenaPool.Put(a)
}

// ReclaimOldGenerations drains the pending old-generation buffer
// list. Called by the epoch manager's background goroutine to
// release old arena memory. REQ000305.
func ReclaimOldGenerations() {
	reclaimMu.Lock()
	// Clear the pending list. Go's GC collects the backing arrays.
	for i := range pendingOlds {
		pendingOlds[i] = nil
	}
	pendingOlds = pendingOlds[:0]
	reclaimMu.Unlock()
}
