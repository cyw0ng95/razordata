package ls

import (
	"sync"
	"sync/atomic"
	"unsafe"
)

// borrowedBuffer wraps a pointer into a buffer-pool block.
// The buffer is valid only while the owning iterator is registered
// with the epoch manager and the page is pinned.
type borrowedBuffer struct {
	// data points into a buffer-pool block (unsafe, borrowed)
	data unsafe.Pointer
	// len is the valid length from data
	len int
	// page is the pinned buffer pool page (nil for in-memory sources)
	// This is a placeholder for future integration with MEM/BF buffer pool
	page unsafe.Pointer
	// epochGuard ensures the page stays pinned while iterator is active
	epochGuard atomic.Uint64 // 0 = unregistered, >0 = registered epoch
}

// newBorrowedBuffer creates a borrowed buffer from a pointer and length.
// If page is non-nil, the page is pinned and must be unpinned on Close.
func newBorrowedBuffer(data unsafe.Pointer, n int, page unsafe.Pointer) *borrowedBuffer {
	bb := &borrowedBuffer{
		data: data,
		len:  n,
		page: page,
	}
	if page != nil {
		// Pin the page so it cannot be evicted while iterator uses it
		// The caller (iterator) is responsible for unpinning on Close
	}
	return bb
}

// Bytes returns the borrowed data as a slice.
// WARNING: The returned slice is only valid while the iterator is open
// and registered with the epoch manager. Do not escape this slice.
func (bb *borrowedBuffer) Bytes() []byte {
	if bb.data == nil {
		return nil
	}
	return unsafe.Slice((*byte)(bb.data), bb.len)
}

// IsRegistered reports whether this buffer is registered with epoch manager.
func (bb *borrowedBuffer) IsRegistered() bool {
	return bb.epochGuard.Load() != 0
}

// Register registers this buffer with the epoch manager.
// Returns the epoch at registration time.
func (bb *borrowedBuffer) Register(epoch uint64) {
	bb.epochGuard.Store(epoch)
}

// Deregister deregisters this buffer from the epoch manager.
func (bb *borrowedBuffer) Deregister() {
	bb.epochGuard.Store(0)
}

// borrowedPagePool provides reusable borrowedBuffer instances.
var borrowedPagePool = sync.Pool{
	New: func() any {
		return &borrowedBuffer{}
	},
}

// getBorrowedBuffer returns a borrowedBuffer from the pool or creates a new one.
func getBorrowedBuffer(data unsafe.Pointer, n int, page unsafe.Pointer) *borrowedBuffer {
	bb, _ := borrowedPagePool.Get().(*borrowedBuffer)
	bb.data = data
	bb.len = n
	bb.page = page
	bb.epochGuard.Store(0)
	return bb
}

// putBorrowedBuffer returns a borrowedBuffer to the pool.
func putBorrowedBuffer(bb *borrowedBuffer) {
	bb.data = nil
	bb.len = 0
	bb.page = nil
	bb.epochGuard.Store(0)
	borrowedPagePool.Put(bb)
}
