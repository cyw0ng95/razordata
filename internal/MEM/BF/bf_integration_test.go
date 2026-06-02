package bf

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/cyw0ng95/razordata/internal/FIL/DF"
)

func TestBufferPool_PinUnpin(t *testing.T) {
	dir := t.TempDir()
	bd, err := df.Create(filepath.Join(dir, "data.razor"))
	if err != nil {
		t.Fatalf("failed to create block device: %v", err)
	}
	defer bd.Close()

	bp, err := New(10, filepath.Join(dir, "hint"), bd, newMockSyncPool())
	if err != nil {
		t.Fatalf("failed to create buffer pool: %v", err)
	}
	defer bp.Close()

	page := &Page{ID: 1, Data: make([]byte, BlockSize)}
	bp.Upsert(page)

	bp.Pin(page)
	bp.Unpin(page)
}

func TestBufferPool_MultiplePinUnpin(t *testing.T) {
	dir := t.TempDir()
	bd, err := df.Create(filepath.Join(dir, "data.razor"))
	if err != nil {
		t.Fatalf("failed to create block device: %v", err)
	}
	defer bd.Close()

	bp, err := New(10, filepath.Join(dir, "hint"), bd, newMockSyncPool())
	if err != nil {
		t.Fatalf("failed to create buffer pool: %v", err)
	}
	defer bp.Close()

	var pages []*Page
	for i := uint64(1); i <= 5; i++ {
		page := &Page{ID: i, Data: make([]byte, BlockSize)}
		bp.Upsert(page)
		pages = append(pages, page)
	}

	for _, page := range pages {
		bp.Pin(page)
	}

	for _, page := range pages {
		bp.Unpin(page)
	}
}

func TestBufferPool_Upsert(t *testing.T) {
	dir := t.TempDir()
	bd, err := df.Create(filepath.Join(dir, "data.razor"))
	if err != nil {
		t.Fatalf("failed to create block device: %v", err)
	}
	defer bd.Close()

	bp, err := New(10, filepath.Join(dir, "hint"), bd, newMockSyncPool())
	if err != nil {
		t.Fatalf("failed to create buffer pool: %v", err)
	}
	defer bp.Close()

	page1 := &Page{ID: 1, Data: make([]byte, BlockSize)}
	page1.Data[0] = 0xAB

	if err := bp.Upsert(page1); err != nil {
		t.Fatalf("failed to upsert page: %v", err)
	}

	retrieved, found, err := bp.Get(context.Background(), 1)
	if err != nil {
		t.Fatalf("failed to get page: %v", err)
	}
	if !found {
		t.Fatal("expected page to be found")
	}

	if retrieved.Data[0] != 0xAB {
		t.Fatalf("expected 0xAB, got 0x%02x", retrieved.Data[0])
	}
}

func TestBufferPool_Stats(t *testing.T) {
	dir := t.TempDir()
	bd, err := df.Create(filepath.Join(dir, "data.razor"))
	if err != nil {
		t.Fatalf("failed to create block device: %v", err)
	}
	defer bd.Close()

	bp, err := New(10, filepath.Join(dir, "hint"), bd, newMockSyncPool())
	if err != nil {
		t.Fatalf("failed to create buffer pool: %v", err)
	}
	defer bp.Close()

	stats := bp.Stats()
	if stats.Capacity != 10 {
		t.Fatalf("expected capacity 10, got %d", stats.Capacity)
	}
}

func TestBufferPool_Close_Idempotent(t *testing.T) {
	dir := t.TempDir()
	bd, err := df.Create(filepath.Join(dir, "data.razor"))
	if err != nil {
		t.Fatalf("failed to create block device: %v", err)
	}
	defer bd.Close()

	bp, err := New(10, filepath.Join(dir, "hint"), bd, newMockSyncPool())
	if err != nil {
		t.Fatalf("failed to create buffer pool: %v", err)
	}

	page := &Page{ID: 1, Data: make([]byte, BlockSize)}
	bp.Upsert(page)

	if err := bp.Close(); err != nil {
		t.Fatalf("first close failed: %v", err)
	}

	if err := bp.Close(); err != nil {
		t.Fatalf("second close failed: %v", err)
	}
}

func TestBufferPool_Eviction(t *testing.T) {
	dir := t.TempDir()
	bd, err := df.Create(filepath.Join(dir, "data.razor"))
	if err != nil {
		t.Fatalf("failed to create block device: %v", err)
	}
	defer bd.Close()

	bp, err := New(3, filepath.Join(dir, "hint"), bd, newMockSyncPool())
	if err != nil {
		t.Fatalf("failed to create buffer pool: %v", err)
	}
	defer bp.Close()

	for i := uint64(1); i <= 3; i++ {
		page := &Page{ID: i, Data: make([]byte, BlockSize)}
		page.Data[0] = byte(i)
		bp.Upsert(page)
	}

	stats := bp.Stats()
	if stats.Used != 3 {
		t.Fatalf("expected used 3, got %d", stats.Used)
	}

	page4 := &Page{ID: 4, Data: make([]byte, BlockSize)}
	page4.Data[0] = 0x04
	bp.Upsert(page4)

	stats = bp.Stats()
	if stats.Used > 3 {
		t.Fatalf("expected used <= 3 after eviction, got %d", stats.Used)
	}
}

func TestBufferPool_ConcurrentPinUnpin(t *testing.T) {
	dir := t.TempDir()
	bd, err := df.Create(filepath.Join(dir, "data.razor"))
	if err != nil {
		t.Fatalf("failed to create block device: %v", err)
	}
	defer bd.Close()

	bp, err := New(10, filepath.Join(dir, "hint"), bd, newMockSyncPool())
	if err != nil {
		t.Fatalf("failed to create buffer pool: %v", err)
	}
	defer bp.Close()

	page := &Page{ID: 1, Data: make([]byte, BlockSize)}
	bp.Upsert(page)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			bp.Pin(page)
			bp.Unpin(page)
		}()
	}

	wg.Wait()
}

func TestBufferPool_ConcurrentGet(t *testing.T) {
	dir := t.TempDir()
	bd, err := df.Create(filepath.Join(dir, "data.razor"))
	if err != nil {
		t.Fatalf("failed to create block device: %v", err)
	}
	defer bd.Close()

	bp, err := New(10, filepath.Join(dir, "hint"), bd, newMockSyncPool())
	if err != nil {
		t.Fatalf("failed to create buffer pool: %v", err)
	}
	defer bp.Close()

	for i := uint64(1); i <= 10; i++ {
		page := &Page{ID: i, Data: make([]byte, BlockSize)}
		bp.Upsert(page)
	}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				blockID := uint64((idx*10+j)%10 + 1)
				bp.Get(context.Background(), blockID)
			}
		}(i)
	}

	wg.Wait()
}

func TestBufferPool_Warm(t *testing.T) {
	dir := t.TempDir()
	bd, err := df.Create(filepath.Join(dir, "data.razor"))
	if err != nil {
		t.Fatalf("failed to create block device: %v", err)
	}
	defer bd.Close()

	bp, err := New(10, filepath.Join(dir, "hint"), bd, newMockSyncPool())
	if err != nil {
		t.Fatalf("failed to create buffer pool: %v", err)
	}
	defer bp.Close()

	for i := uint64(1); i <= 5; i++ {
		page := &Page{ID: i, Data: make([]byte, BlockSize)}
		page.Data[0] = byte(i)
		bp.Upsert(page)
	}

	bp.Close()

	bp2, err := New(10, filepath.Join(dir, "hint"), bd, newMockSyncPool())
	if err != nil {
		t.Fatalf("failed to create buffer pool for warm: %v", err)
	}
	defer bp2.Close()

	if err := bp2.Warm(context.Background()); err != nil {
		t.Fatalf("warm failed: %v", err)
	}
}
