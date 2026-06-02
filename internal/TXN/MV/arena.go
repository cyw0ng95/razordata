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

type Arena struct {
	buf    []byte
	offset atomic.Int64
	size   int64
}

func newArena() *Arena {
	return &Arena{
		buf:  make([]byte, arenaSize),
		size: arenaSize,
	}
}

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
		return newArena()
	},
}

func GetArena() *Arena {
	return arenaPool.Get().(*Arena)
}

func PutArena(a *Arena) {
	a.offset.Store(0)
	arenaPool.Put(a)
}

var arenaPoolSlice = make([]*Arena, runtime.GOMAXPROCS(0))

func threadArena() *Arena {
	goid := getGoroutineID()
	idx := int(goid) % len(arenaPoolSlice)
	if arenaPoolSlice[idx] == nil {
		arenaPoolSlice[idx] = GetArena()
	}
	return arenaPoolSlice[idx]
}

func allocFromThreadArena(n int) []byte {
	for {
		goid := getGoroutineID()
		idx := int(goid) % len(arenaPoolSlice)
		if arenaPoolSlice[idx] == nil {
			arenaPoolSlice[idx] = GetArena()
		}
		a := arenaPoolSlice[idx]
		if ptr := a.Alloc(n); ptr != nil {
			return ptr
		}
		newArena := GetArena()
		arenaPoolSlice[idx] = newArena
	}
}

func allocVersionNode() *VersionNode {
	size := int(unsafe.Sizeof(VersionNode{}))
	mem := allocFromThreadArena(size)
	return (*VersionNode)(unsafe.Pointer(&mem[0]))
}
