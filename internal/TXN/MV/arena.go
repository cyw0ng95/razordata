package MV

import (
	"sync"
	"sync/atomic"
)

const (
	youngSize          = 16 * 1024 // 16 KB
	oldSize            = 1 << 20   // 1 MB
	promotionThreshold = youngSize / 2
)

var (
	reclaimMu   sync.Mutex
	pendingOlds [][]byte
)

// Arena is a per-transaction bump allocator for VersionNode storage (REQ000064).
type Arena struct {
	// young is the current young generation. Allocations go here
	// first.
	young     []byte
	youngOff  atomic.Int64
	old       []byte
	oldOff    atomic.Int64
	promoted  atomic.Bool
	initOldMu sync.Mutex
}

// NewArena constructs a fresh, non-pooled Arena.
func NewArena() *Arena {
	return &Arena{
		young: make([]byte, youngSize),
	}
}

var newArena = NewArena

// Alloc allocates n bytes from the arena. Returns nil if exhausted.
func (a *Arena) Alloc(n int) []byte {
	if a.promoted.Load() {
		if off := a.tryAllocOld(n); off >= 0 {
			return a.old[off : off+int64(n)]
		}
		return nil
	}
	if off := a.tryAllocYoung(n); off >= 0 {
		return a.young[off : off+int64(n)]
	}
	a.promote()
	if off := a.tryAllocOld(n); off >= 0 {
		return a.old[off : off+int64(n)]
	}
	return nil
}

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

func (a *Arena) promote() {
	youngUsed := a.youngOff.Load()
	if youngUsed == 0 {
		a.promoted.Store(true)
		return
	}
	a.initOldMu.Lock()
	if a.old == nil {
		a.old = make([]byte, oldSize)
		a.oldOff.Store(0)
	}
	a.initOldMu.Unlock()
	for {
		oldOff := a.oldOff.Load()
		new := oldOff + youngUsed
		if new > oldSize {
			a.promoted.Store(true)
			return
		}
		if a.oldOff.CompareAndSwap(oldOff, new) {
			copy(a.old[oldOff:new], a.young[:youngUsed])
			a.youngOff.Store(0)
			a.promoted.Store(true)
			return
		}
	}
}

func (a *Arena) Remaining() int64 {
	if a.promoted.Load() {
		return oldSize - a.oldOff.Load()
	}
	return int64(youngSize-a.youngOff.Load()) + (oldSize - a.oldOff.Load())
}

func (a *Arena) YoungRemaining() int64 {
	return int64(youngSize - a.youngOff.Load())
}

func (a *Arena) OldRemaining() int64 {
	return oldSize - a.oldOff.Load()
}

func (a *Arena) Size() int64 {
	return int64(youngSize + oldSize)
}

func (a *Arena) YoungSize() int64 {
	return int64(youngSize)
}

func (a *Arena) OldSize() int64 {
	return int64(oldSize)
}

var arenaPool = sync.Pool{
	New: func() any {
		return NewArena()
	},
}

// GetArena acquires an Arena from the pool.
func GetArena() *Arena {
	return arenaPool.Get().(*Arena)
}

// PutArena returns an Arena to the pool (REQ000305).
func PutArena(a *Arena) {
	a.youngOff.Store(0)
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

// ReclaimOldGenerations drains the pending old-generation buffer list (REQ000305).
func ReclaimOldGenerations() {
	reclaimMu.Lock()
	for i := range pendingOlds {
		pendingOlds[i] = nil
	}
	pendingOlds = pendingOlds[:0]
	reclaimMu.Unlock()
}
