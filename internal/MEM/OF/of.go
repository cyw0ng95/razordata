package of

import (
	"sync"
	"sync/atomic"
)

// OffHeap is a large-object pool backed by size-class bins (REQ000304).
const (
	minClass   = 64 * 1024       // 64KB
	maxClass   = 4 * 1024 * 1024 // 4MB
	classCount = 16
)

// sizeClasses is the shared size-class boundaries array (REQ000606).
// Extracted to avoid declaring the same array three times.
var sizeClasses = [classCount]int{
	64 * 1024,
	96 * 1024,
	128 * 1024,
	192 * 1024,
	256 * 1024,
	384 * 1024,
	512 * 1024,
	768 * 1024,
	1 * 1024 * 1024,
	1536 * 1024,
	2 * 1024 * 1024,
	3 * 1024 * 1024,
	4 * 1024 * 1024,
	4 * 1024 * 1024,
	4 * 1024 * 1024,
	4 * 1024 * 1024,
}

func sizeClass(n int) int {
	if n < minClass {
		return minClass
	}
	if n > maxClass {
		return 0
	}
	for _, c := range sizeClasses {
		if n <= c {
			return c
		}
	}
	return 0
}

// OffHeap is the pool itself.
type OffHeap struct {
	// Per-class free lists. Indexed by class index (0..classCount-1).
	pools  [classCount]sync.Pool
	gets   atomic.Uint64
	puts   atomic.Uint64
	misses atomic.Uint64 // gets that had to allocate
}

// classIndex returns the pool index for a size-class value.
func classIndex(c int) int {
	for i, cl := range sizeClasses {
		if cl == c {
			return i
		}
	}
	return classCount - 1
}

// NewOffHeap returns a fresh pool.
func NewOffHeap() *OffHeap {
	oh := &OffHeap{}
	for i := range classCount {
		size := sizeClasses[i]
		oh.pools[i].New = func() any {
			oh.misses.Add(1)
			return make([]byte, size)
		}
	}
	return oh
}

// Get returns a zero-initialized byte slice of at least n bytes.
// For n <= 0 returns nil. For n > maxClass, allocates directly
// (bypasses the pool).
func (oh *OffHeap) Get(n int) []byte {
	if n <= 0 {
		return nil
	}
	oh.gets.Add(1)
	c := sizeClass(n)
	if c == 0 {
		oh.misses.Add(1)
		return make([]byte, n)
	}
	idx := classIndex(c)
	if idx < 0 || idx >= classCount {
		idx = classCount - 1
	}
	b := oh.pools[idx].Get().([]byte)
	for i := 0; i < n && i < len(b); i++ {
		b[i] = 0
	}
	return b[:n]
}

// Put returns a slice to the pool. The slice is NOT zeroed.
// Callers must not retain references to the slice after Put.
func (oh *OffHeap) Put(b []byte) {
	if len(b) == 0 {
		return
	}
	oh.puts.Add(1)
	c := cap(b)
	if c < minClass || c > maxClass {
		return // out-of-class slices are not pooled
	}
	idx := classIndex(c)
	if idx < 0 || idx >= classCount {
		return
	}
	oh.pools[idx].Put(b[:cap(b)])
}

// Stats is a snapshot of pool metrics.
type Stats struct {
	Gets   uint64
	Puts   uint64
	Misses uint64
}

func (oh *OffHeap) Stats() Stats {
	return Stats{
		Gets:   oh.gets.Load(),
		Puts:   oh.puts.Load(),
		Misses: oh.misses.Load(),
	}
}
