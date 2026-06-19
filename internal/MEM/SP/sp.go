package sp

import (
	"sync"
)

const (
	BlockSize      = 4096
	IterBufferSize = 64 * 1024  // 64 KB
	WALBufSize     = 256 * 1024 // 256 KB
)

// SyncPool provides reusable buffers to avoid allocations on hot paths.
type SyncPool interface {
	Get(size int) []byte
	Put(buf []byte)
}

type syncPool struct {
	pagePool sync.Pool
	iterPool sync.Pool
	walPool  sync.Pool
}

var _ SyncPool = (*syncPool)(nil)

// New creates a new SyncPool.
func New() *syncPool {
	sp := &syncPool{}
	sp.pagePool.New = func() any {
		b := make([]byte, BlockSize)
		return &b
	}
	sp.iterPool.New = func() any {
		b := make([]byte, IterBufferSize)
		return &b
	}
	sp.walPool.New = func() any {
		b := make([]byte, WALBufSize)
		return &b
	}
	return sp
}

// Get returns a buffer of at least size bytes.
func (sp *syncPool) Get(size int) []byte {
	if size <= BlockSize {
		if p := sp.pagePool.Get(); p != nil {
			return (*p.(*[]byte))[:size]
		}
	}
	if size <= IterBufferSize {
		if p := sp.iterPool.Get(); p != nil {
			return (*p.(*[]byte))[:size]
		}
	}
	if size <= WALBufSize {
		if p := sp.walPool.Get(); p != nil {
			return (*p.(*[]byte))[:size]
		}
	}
	return make([]byte, size)
}

// Put returns buf to the pool.
func (sp *syncPool) Put(buf []byte) {
	switch cap(buf) {
	case BlockSize:
		b := buf[:BlockSize]
		sp.pagePool.Put(&b)
	case IterBufferSize:
		b := buf[:IterBufferSize]
		sp.iterPool.Put(&b)
	case WALBufSize:
		b := buf[:WALBufSize]
		sp.walPool.Put(&b)
	}
}
