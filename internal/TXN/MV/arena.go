package MV

import (
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"
)

const arenaSize = 1 << 20 // 1 MB per arena

var goroutineID atomic.Uint64

func getGoroutineID() uint64 {
	goid := goroutineID.Add(1)
	return goid
}

type arena struct {
	buf    []byte
	offset atomic.Int64
	size   int64
}

func newArena() *arena {
	return &arena{
		buf:  make([]byte, arenaSize),
		size: arenaSize,
	}
}

func (a *arena) Alloc(n int) []byte {
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

func (a *arena) remaining() int64 {
	return a.size - a.offset.Load()
}

var arenaPool = sync.Pool{
	New: func() any {
		return newArena()
	},
}

func getArena() *arena {
	return arenaPool.Get().(*arena)
}

func putArena(a *arena) {
	a.offset.Store(0)
	arenaPool.Put(a)
}

var arenaPoolSlice = make([]*arena, runtime.GOMAXPROCS(0))

func threadArena() *arena {
	goid := getGoroutineID()
	idx := int(goid) % len(arenaPoolSlice)
	if arenaPoolSlice[idx] == nil {
		arenaPoolSlice[idx] = getArena()
	}
	return arenaPoolSlice[idx]
}

func allocFromThreadArena(n int) []byte {
	for {
		goid := getGoroutineID()
		idx := int(goid) % len(arenaPoolSlice)
		if arenaPoolSlice[idx] == nil {
			arenaPoolSlice[idx] = getArena()
		}
		a := arenaPoolSlice[idx]
		if ptr := a.Alloc(n); ptr != nil {
			return ptr
		}
		newArena := getArena()
		arenaPoolSlice[idx] = newArena
	}
}

func allocVersionNode() *versionNode {
	size := int(unsafe.Sizeof(versionNode{}))
	mem := allocFromThreadArena(size)
	return (*versionNode)(unsafe.Pointer(&mem[0]))
}
