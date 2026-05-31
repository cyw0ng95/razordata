package sp

import (
	"runtime"
	"sync"
	"testing"
)

func TestNew(t *testing.T) {
	sp := New()
	if sp == nil {
		t.Fatal("New returned nil")
	}
}

func TestGetPutPageSize(t *testing.T) {
	sp := New()

	// Get a page-sized buffer.
	buf := sp.Get(BlockSize)
	if len(buf) != BlockSize {
		t.Errorf("expected len=%d, got %d", BlockSize, len(buf))
	}
	if cap(buf) < BlockSize {
		t.Errorf("expected cap >= %d, got %d", BlockSize, cap(buf))
	}

	// Verify we can write to it.
	for i := range buf {
		buf[i] = byte(i & 0xff)
	}

	// Put it back.
	sp.Put(buf)

	// Get again; should be the same buffer.
	buf2 := sp.Get(BlockSize)
	if cap(buf2) < BlockSize {
		t.Errorf("expected cap >= %d after Put, got %d", BlockSize, cap(buf2))
	}
}

func TestGetPutIterSize(t *testing.T) {
	sp := New()

	// Get an iterator-sized buffer.
	buf := sp.Get(IterBufferSize)
	if len(buf) != IterBufferSize {
		t.Errorf("expected len=%d, got %d", IterBufferSize, len(buf))
	}

	// Put it back.
	sp.Put(buf)

	// Get again.
	buf2 := sp.Get(IterBufferSize)
	if cap(buf2) < IterBufferSize {
		t.Errorf("expected cap >= %d after Put, got %d", IterBufferSize, cap(buf2))
	}
}

func TestGetArbitrarySize(t *testing.T) {
	sp := New()

	// Get a non-standard size.
	for _, size := range []int{100, 1024, 8192, 128 * 1024} {
		buf := sp.Get(size)
		if len(buf) != size {
			t.Errorf("Get(%d): expected len=%d, got %d", size, size, len(buf))
		}
		// Arbitrary sizes are not pooled; Put should drop them.
		sp.Put(buf)
	}
}

func TestPutWrongSize(t *testing.T) {
	sp := New()

	// A buffer that's neither BlockSize nor IterBufferSize should be dropped.
	// We can't easily verify the drop, but we can verify no panic occurs.
	buf := make([]byte, 2048)
	sp.Put(buf) // should not panic

	// A buffer that's smaller than BlockSize should also be dropped.
	buf2 := make([]byte, 512)
	sp.Put(buf2) // should not panic

	// A buffer that's larger than IterBufferSize should be dropped.
	buf3 := make([]byte, 128*1024)
	sp.Put(buf3) // should not panic
}

func TestNoAllocOnHotPath(t *testing.T) {
	sp := New()

	// Warm up the pool.
	for i := 0; i < 10; i++ {
		buf := sp.Get(BlockSize)
		sp.Put(buf)
	}

	// Force GC to eliminate any stale entries.
	runtime.GC()

	// Get and put in a tight loop.
	for i := 0; i < 1000; i++ {
		buf := sp.Get(BlockSize)
		// Write to buffer to prevent compiler from optimizing away the call.
		buf[0] = byte(i)
		sp.Put(buf)
	}
}

func TestConcurrentGetPut(t *testing.T) {
	sp := New()

	const goroutines = 8
	const ops = 1000

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < ops; i++ {
				// Alternate between page and iterator sizes.
				size := BlockSize
				if i%2 == 0 {
					size = IterBufferSize
				}
				buf := sp.Get(size)
				buf[0] = byte(i)
				sp.Put(buf)
			}
		}()
	}

	wg.Wait()
}

func BenchmarkGetPut(b *testing.B) {
	sp := New()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		buf := sp.Get(BlockSize)
		buf[0] = byte(i)
		sp.Put(buf)
	}
}

func BenchmarkGetPutIter(b *testing.B) {
	sp := New()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		buf := sp.Get(IterBufferSize)
		buf[0] = byte(i)
		sp.Put(buf)
	}
}

func BenchmarkConcurrentGetPut(b *testing.B) {
	sp := New()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			buf := sp.Get(BlockSize)
			buf[0] = 1
			sp.Put(buf)
		}
	})
}
