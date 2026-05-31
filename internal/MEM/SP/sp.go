package sp

import (
	"sync"
)

const (
	// BlockSize is the fixed block size (4096 bytes).
	BlockSize = 4096
	// IterBufferSize is the pre-allocated size for LSM tree iterator buffers.
	IterBufferSize = 64 * 1024 // 64 KB
)

// SyncPool provides reusable buffers to avoid allocations on hot paths.
type SyncPool interface {
	Get(size int) []byte
	Put(buf []byte)
}

// syncPool implements SyncPool using sync.Pool.
type syncPool struct {
	pagePool sync.Pool
	iterPool sync.Pool
}

var _ SyncPool = (*syncPool)(nil)

// New creates a new SyncPool. Callers may pass SyncPool(nil) to get a
// default implementation that allocates via make when the pool is empty.
func New() *syncPool {
	sp := &syncPool{}
	sp.pagePool.New = func() any {
		return make([]byte, BlockSize)
	}
	sp.iterPool.New = func() any {
		return make([]byte, IterBufferSize)
	}
	return sp
}

// Get returns a buffer of at least size bytes.
// If a pooled buffer of sufficient size is available, it is returned.
// Otherwise, a new buffer is allocated with make([]byte, size).
func (sp *syncPool) Get(size int) []byte {
	if size <= BlockSize {
		if p := sp.pagePool.Get(); p != nil {
			return p.([]byte)[:size]
		}
	}
	if size <= IterBufferSize {
		if p := sp.iterPool.Get(); p != nil {
			return p.([]byte)[:size]
		}
	}
	return make([]byte, size)
}

// Put returns buf to the pool. Only buffers with capacity BlockSize or
// IterBufferSize are returned to the pool; others are dropped.
func (sp *syncPool) Put(buf []byte) {
	if cap(buf) == BlockSize {
		sp.pagePool.Put(buf[:BlockSize])
	} else if cap(buf) == IterBufferSize {
		sp.iterPool.Put(buf[:IterBufferSize])
	}
}
