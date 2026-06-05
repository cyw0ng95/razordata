package MV

import (
	"sync"
	"sync/atomic"
)

const arenaSize = 1 << 20 // 1 MB per arena

// Arena is a per-transaction bump allocator for VersionNode storage.
//
// A single Arena is owned by a single transaction and MUST NOT be shared
// across goroutines concurrently: the per-txn mutex on the *tx* type is
// what serializes Insert/Delete calls and therefore serializes Arena.Alloc.
// Arenas are returned to the sync.Pool via PutArena once the transaction
// is committed or aborted.
type Arena struct {
	buf    []byte
	offset atomic.Int64
	size   int64
}

// NewArena constructs a fresh, non-pooled Arena. Used by Manager.Begin
// to give each transaction a private arena.
//
// Per the iter-05/iter-06 design, the arena is *per-transaction*, not
// per-goroutine. The previous per-goroutine allocation path
// (getGoroutineID + arenaPoolSlice + allocFromThreadArena) was unsound
// because the goroutine-ID source was a process-global counter, not a
// real goroutine identity, and the global slice was unsynchronized.
func NewArena() *Arena {
	return &Arena{
		buf:  make([]byte, arenaSize),
		size: arenaSize,
	}
}

// newArena is an internal alias for NewArena used by tests in this
// package. Kept as a separate name to avoid churn in tests that
// previously called the unexported helper.
var newArena = NewArena

func (a *Arena) Alloc(n int) []byte {
	for {
		old := a.offset.Load()
		new := old + int64(n)
		if new > a.size {
			return nil
		}
		if a.offset.CompareAndSwap(old, new) {
			return a.buf[old:new]
		}
	}
}

func (a *Arena) Remaining() int64 {
	return a.size - a.offset.Load()
}

func (a *Arena) Size() int64 {
	return a.size
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

// PutArena returns an Arena to the pool. The arena's offset is reset
// to 0 so the next GetArena hands out a clean allocation buffer.
func PutArena(a *Arena) {
	a.offset.Store(0)
	arenaPool.Put(a)
}
