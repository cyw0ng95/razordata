package bf

import (
	"context"
	"math/rand"
	"sync"
	"testing"

	"github.com/cyw0ng95/razordata/internal/FIL/DF"
)

// benchSyncPool mirrors the test helper for benchmark stability.
type benchSyncPool struct {
	pagePool sync.Pool
}

func newBenchSP() *benchSyncPool {
	sp := &benchSyncPool{}
	sp.pagePool.New = func() any {
		return make([]byte, BlockSize)
	}
	return sp
}

func (sp *benchSyncPool) Get(size int) []byte {
	if size == BlockSize {
		if p := sp.pagePool.Get(); p != nil {
			return p.([]byte)
		}
	}
	return make([]byte, size)
}

func (sp *benchSyncPool) Put(buf []byte) {
	if cap(buf) == BlockSize {
		sp.pagePool.Put(buf[:BlockSize])
	}
}

func BenchmarkEvictionPressure(b *testing.B) {
	tmp := b.TempDir()
	dataPath := tmp + "/data.razor"
	bd, err := df.Create(dataPath)
	if err != nil {
		b.Fatalf("df.Create failed: %v", err)
	}
	defer bd.Close()

	// Write 100 blocks; pool capacity is 10 — eviction is always active.
	const numBlocks = 100
	for i := uint64(1); i <= numBlocks; i++ {
		data := make([]byte, 4088)
		for j := range data {
			data[j] = byte((i + uint64(j)) & 0xff)
		}
		if err := bd.WriteBlock(context.Background(), i, data); err != nil {
			b.Fatalf("WriteBlock failed: %v", err)
		}
	}

	sp := newBenchSP()
	bp, err := New(10, "", bd, sp) // tiny capacity triggers eviction
	if err != nil {
		b.Fatalf("New failed: %v", err)
	}
	defer bp.Close()

	b.ResetTimer()
	b.ReportAllocs()

	r := rand.New(rand.NewSource(42))
	ctx := context.Background()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			blockID := uint64(r.Intn(int(numBlocks))) + 1
			page, _, err := bp.Get(ctx, blockID)
			if err != nil {
				b.Errorf("Get error: %v", err)
				continue
			}
			if page.ID != blockID {
				b.Errorf("expected page.ID=%d, got %d", blockID, page.ID)
			}
		}
	})
}
