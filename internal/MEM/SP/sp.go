package sp

import (
	"sync"

	df "github.com/cyw0ng95/razordata/internal/FIL/DF"
	EC "github.com/cyw0ng95/razordata/internal/LOG/EC"
)

const (
	BlockSize      = df.DefaultBlockSize
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

// New creates a new SyncPool with standard heap allocation.
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

// Options configures the SyncPool behavior.
type Options struct {
	EnableHugePages bool // REQ001207: pre-allocate huge pages for buffer pool
}

// NewWithOptions creates a SyncPool with optional huge page support.
// When EnableHugePages is true and hugetlbfs is available, a huge page-
// backed region is pre-allocated and sliced into pages, pre-populating
// the pagePool. This reduces TLB miss rate dramatically (512x with 2MB
// pages vs 65,536 with 4KB pages for a 256MB buffer pool).
// Falls back to standard heap allocation when hugetlbfs is unavailable
// or mmap fails.
func NewWithOptions(opts Options) *syncPool {
	sp := &syncPool{}
	sp.iterPool.New = func() any {
		b := make([]byte, IterBufferSize)
		return &b
	}
	sp.walPool.New = func() any {
		b := make([]byte, WALBufSize)
		return &b
	}

	// Always use heap allocation if huge pages are disabled or unsupported.
	// hugePageEnabled() probes /dev/hugepages/2M to check hugetlbfs availability.
	if !opts.EnableHugePages || !hugePageEnabled() {
		sp.pagePool.New = func() any {
		b := make([]byte, BlockSize)
			return &b
		}
		return sp
	}

	// Pre-allocate a huge page-backed region and slice into BlockSize pages.
	// 16384 pages x 4KB = 64 MB upfront covers the majority of buffer pool
	// hits without excessive pre-allocation.
	pageCount := 16384
	region, fd, err := mmapHugePages(pageCount)
	_ = region
	_ = fd
	if err != nil || region == nil {
		// hugetlbfs unavailable or insufficient pages reserved.
		sp.pagePool.New = func() any {
		b := make([]byte, BlockSize)
			return &b
		}
		return sp
	}

	// Pre-populate the pool with pre-sliced pages from the huge page region.
	sp.pagePool.New = func() any {
		b := make([]byte, BlockSize)
		return &b
	}
	for i := 0; i < 1024; i++ {
		offset := i * BlockSize
		if offset+BlockSize > len(region) {
			break
		}
		s := region[offset:offset+BlockSize]
		sp.pagePool.Put(&s)
	}

	return sp
}

// Get returns a buffer of at least size bytes.
func (sp *syncPool) Get(size int) []byte {
	EC.BUG_ON(size <= 0, "sp.Get: non-positive size %d", size)
	if size <= BlockSize {
		if p := sp.pagePool.Get(); p != nil {
			b := *p.(*[]byte)
			EC.BUG_ON(len(b) != BlockSize, "sp.Get: page pool returned buffer with len %d, want %d", len(b), BlockSize)
			return b[:size]
		}
	}
	if size <= IterBufferSize {
		if p := sp.iterPool.Get(); p != nil {
			b := *p.(*[]byte)
			EC.BUG_ON(len(b) != IterBufferSize, "sp.Get: iter pool returned buffer with len %d, want %d", len(b), IterBufferSize)
			return b[:size]
		}
	}
	if size <= WALBufSize {
		if p := sp.walPool.Get(); p != nil {
			b := *p.(*[]byte)
			EC.BUG_ON(len(b) != WALBufSize, "sp.Get: WAL pool returned buffer with len %d, want %d", len(b), WALBufSize)
			return b[:size]
		}
	}
	return make([]byte, size)
}

// Put returns buf to the pool.
func (sp *syncPool) Put(buf []byte) {
	EC.BUG_ON(len(buf) == 0, "sp.Put: zero-length buffer")
	EC.WARN_ON(cap(buf) != BlockSize && cap(buf) != IterBufferSize && cap(buf) != WALBufSize, "sp.Put: unexpected buffer capacity %d", cap(buf))
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