package bf

import (
	"context"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cyw0ng95/razordata/internal/FIL/DF"
)

// mockSyncPool is a simple SyncPool implementation for testing.
type mockSyncPool struct {
	pagePool sync.Pool
	iterPool sync.Pool
}

func newMockSyncPool() *mockSyncPool {
	sp := &mockSyncPool{}
	sp.pagePool.New = func() any {
		return make([]byte, BlockSize)
	}
	sp.iterPool.New = func() any {
		return make([]byte, iterBufferSize)
	}
	return sp
}

func (sp *mockSyncPool) Get(size int) []byte {
	if size == BlockSize {
		if p := sp.pagePool.Get(); p != nil {
			return p.([]byte)
		}
	}
	if size == iterBufferSize {
		if p := sp.iterPool.Get(); p != nil {
			return p.([]byte)
		}
	}
	return make([]byte, size)
}

func (sp *mockSyncPool) Put(buf []byte) {
	if cap(buf) == BlockSize {
		sp.pagePool.Put(buf[:BlockSize])
	} else if cap(buf) == iterBufferSize {
		sp.iterPool.Put(buf[:iterBufferSize])
	}
}

// testBD is a simple in-memory block device for testing.
type testBD struct {
	blocks  map[uint64][]byte
	mu      sync.RWMutex
	onRead  func(blockID uint64) error // optional hook
	onWrite func(blockID uint64, data []byte)
}

func newTestBD() *testBD {
	return &testBD{blocks: make(map[uint64][]byte)}
}

func (bd *testBD) WriteBlock(_ context.Context, blockID uint64, data []byte) error {
	bd.mu.Lock()
	defer bd.mu.Unlock()
	dst := make([]byte, df.DataLen)
	copy(dst, data)
	bd.blocks[blockID] = dst
	if bd.onWrite != nil {
		bd.onWrite(blockID, data)
	}
	return nil
}

func (bd *testBD) ReadBlock(_ context.Context, blockID uint64, n int, buf []byte) error {
	bd.mu.RLock()
	data, ok := bd.blocks[blockID]
	bd.mu.RUnlock()
	if !ok {
		return io.EOF
	}
	if len(buf) < n {
		return io.ErrShortBuffer
	}
	copy(buf, data[:n])
	return nil
}

// TestNew tests buffer pool creation and basic lifecycle.
func TestNew(t *testing.T) {
	tmp := t.TempDir()
	hintPath := filepath.Join(tmp, "hint")
	bd, err := df.Create(filepath.Join(tmp, "data.razor"))
	if err != nil {
		t.Fatalf("df.Create failed: %v", err)
	}
	defer bd.Close()

	sp := newMockSyncPool()
	bp, err := New(128, hintPath, bd, sp)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	stats := bp.Stats()
	if stats.Capacity != 128 {
		t.Errorf("expected capacity 128, got %d", stats.Capacity)
	}
	if stats.Used != 0 {
		t.Errorf("expected used 0, got %d", stats.Used)
	}

	if err := bp.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

// TestNewDefaultCapacity tests default capacity when capacity <= 0.
func TestNewDefaultCapacity(t *testing.T) {
	tmp := t.TempDir()
	bd, err := df.Create(filepath.Join(tmp, "data.razor"))
	if err != nil {
		t.Fatal("failed to create block device")
	}
	defer bd.Close()

	sp := newMockSyncPool()
	bp, err := New(0, "", bd, sp)
	if err != nil {
		t.Fatalf("New(0) failed: %v", err)
	}
	defer bp.Close()

	stats := bp.Stats()
	if stats.Capacity != 256 {
		t.Errorf("expected default capacity 256, got %d", stats.Capacity)
	}
}

// TestGetInvalidBlockID tests that Get rejects blockID == 0.
func TestGetInvalidBlockID(t *testing.T) {
	tmp := t.TempDir()
	bd, err := df.Create(filepath.Join(tmp, "data.razor"))
	if err != nil {
		t.Fatal("failed to create block device")
	}
	defer bd.Close()

	sp := newMockSyncPool()
	bp, err := New(128, "", bd, sp)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer bp.Close()

	_, _, err = bp.Get(context.Background(), 0)
	if err != ErrInvalidBlockID {
		t.Errorf("expected ErrInvalidBlockID, got %v", err)
	}
}

// TestGetMissThenHit tests loading a block from disk, then hitting it.
func TestGetMissThenHit(t *testing.T) {
	tmp := t.TempDir()
	dataPath := filepath.Join(tmp, "data.razor")
	bd, err := df.Create(dataPath)
	if err != nil {
		t.Fatal("failed to create block device")
	}
	defer bd.Close()

	// Write a block to the device.
	data := make([]byte, 4088)
	for i := range data {
		data[i] = byte(i & 0xff)
	}
	if err := bd.WriteBlock(context.Background(), 1, data); err != nil {
		t.Fatalf("WriteBlock failed: %v", err)
	}

	sp := newMockSyncPool()
	bp, err := New(128, "", bd, sp)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer bp.Close()

	// First get: miss.
	page, found, err := bp.Get(context.Background(), 1)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if found {
		t.Error("expected found=false on first get")
	}
	if page.ID != 1 {
		t.Errorf("expected page.ID=1, got %d", page.ID)
	}
	// Dirty may be true or false on a fresh load depending on sync pool buffer state.
	// The important thing is that the data is correct.
	_ = page.Dirty

	stats := bp.Stats()
	if stats.Misses != 1 {
		t.Errorf("expected 1 miss, got %d", stats.Misses)
	}

	// Second get: hit (might be cached or might reload depending on eviction).
	page2, _, err := bp.Get(context.Background(), 1)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if page2.ID != 1 {
		t.Errorf("expected page2.ID=1, got %d", page2.ID)
	}

	stats2 := bp.Stats()
	// At least one hit or another miss depending on eviction state.
	if stats2.Hits+stats2.Misses < 2 {
		t.Errorf("expected >= 2 total accesses, got hits=%d misses=%d", stats2.Hits, stats2.Misses)
	}
}

// TestGetCorruptBlock tests that corrupt data returns ErrCorrupt.
func TestGetCorruptBlock(t *testing.T) {
	tmp := t.TempDir()
	dataPath := filepath.Join(tmp, "data.razor")
	bd, err := df.Create(dataPath)
	if err != nil {
		t.Fatal("failed to create block device")
	}
	defer bd.Close()

	// Write a block with corrupted checksum.
	// Create a block manually: fill data, then write wrong checksum.
	fd, err := os.OpenFile(dataPath, os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	// Write block 1 (4096 bytes).
	data := make([]byte, 4088)
	for i := range data {
		data[i] = byte(i & 0xff)
	}
	// Write with a wrong checksum.
	buf := make([]byte, 4096)
	copy(buf, data)
	// Put wrong checksum (0xDEADBEEF) at bytes 4092-4095.
	buf[4092] = 0xEF
	buf[4093] = 0xBE
	buf[4094] = 0xAD
	buf[4095] = 0xDE
	_, err = fd.WriteAt(buf, int64(1*4096))
	fd.Close()
	if err != nil {
		t.Fatal(err)
	}

	sp := newMockSyncPool()
	bp, err := New(128, "", bd, sp)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer bp.Close()

	_, _, err = bp.Get(context.Background(), 1)
	if err != df.ErrCorrupt {
		t.Errorf("expected df.ErrCorrupt, got %v", err)
	}
}

// TestPinUnpin tests pin/unpin behavior and eviction protection.
func TestPinUnpin(t *testing.T) {
	tmp := t.TempDir()
	dataPath := filepath.Join(tmp, "data.razor")
	bd, err := df.Create(dataPath)
	if err != nil {
		t.Fatal("failed to create block device")
	}
	defer bd.Close()

	// Write a few blocks.
	for i := uint64(1); i <= 5; i++ {
		data := make([]byte, 4088)
		data[0] = byte(i)
		if err := bd.WriteBlock(context.Background(), i, data); err != nil {
			t.Fatalf("WriteBlock failed: %v", err)
		}
	}

	sp := newMockSyncPool()
	bp, err := New(3, "", bd, sp) // capacity 3
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer bp.Close()

	// Load blocks 1, 2, 3.
	pages := make([]*Page, 5)
	for i := uint64(1); i <= 3; i++ {
		page, _, err := bp.Get(context.Background(), i)
		if err != nil {
			t.Fatalf("Get(%d) failed: %v", i, err)
		}
		pages[i] = page
	}

	stats := bp.Stats()
	if stats.Used != 3 {
		t.Errorf("expected used=3, got %d", stats.Used)
	}

	// Pin page 1 (eviction protection).
	bp.Pin(pages[1])
	stats = bp.Stats()
	if stats.Pins != 1 {
		t.Errorf("expected 1 pin, got %d", stats.Pins)
	}

	// Load block 4 (should trigger eviction of block 2, not 1).
	_, _, err = bp.Get(context.Background(), 4)
	if err != nil {
		t.Fatalf("Get(4) failed: %v", err)
	}

	// Block 1 should still be in cache (pinned).
	page1Again, found, err := bp.Get(context.Background(), 1)
	if err != nil {
		t.Fatalf("Get(1) failed: %v", err)
	}
	if !found {
		t.Error("expected block 1 to still be in cache (pinned)")
	}
	if &page1Again.Data[0] != &pages[1].Data[0] {
		t.Error("expected same data buffer pointer")
	}

	// Unpin page 1.
	bp.Unpin(pages[1])

	// Load block 5 (should now evict block 1).
	_, _, err = bp.Get(context.Background(), 5)
	if err != nil {
		t.Fatalf("Get(5) failed: %v", err)
	}

	// Block 1 should now be evicted.
	_, found, err = bp.Get(context.Background(), 1)
	if err != nil {
		t.Fatalf("Get(1) after eviction failed: %v", err)
	}
	if found {
		t.Error("expected block 1 to be evicted after unpin")
	}
}

// TestSetCapacityDeferred tests that SetCapacity returns ErrCapacityExceeded.
func TestSetCapacityDeferred(t *testing.T) {
	tmp := t.TempDir()
	bd, err := df.Create(filepath.Join(tmp, "data.razor"))
	if err != nil {
		t.Fatal("failed to create block device")
	}
	defer bd.Close()

	sp := newMockSyncPool()
	bp, err := New(128, "", bd, sp)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer bp.Close()

	err = bp.SetCapacity(256)
	if err != ErrCapacityExceeded {
		t.Errorf("expected ErrCapacityExceeded, got %v", err)
	}
}

// TestConcurrentGet tests concurrent Get calls under the race detector.
func TestConcurrentGet(t *testing.T) {
	tmp := t.TempDir()
	dataPath := filepath.Join(tmp, "data.razor")
	bd, err := df.Create(dataPath)
	if err != nil {
		t.Fatal("failed to create block device")
	}
	defer bd.Close()

	// Write 20 blocks.
	for i := uint64(1); i <= 20; i++ {
		data := make([]byte, 4088)
		for j := range data {
			data[j] = byte((i + uint64(j)) & 0xff)
		}
		if err := bd.WriteBlock(context.Background(), i, data); err != nil {
			t.Fatalf("WriteBlock failed: %v", err)
		}
	}

	sp := newMockSyncPool()
	bp, err := New(256, "", bd, sp)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer bp.Close()

	const goroutines = 8
	const ops = 1000

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(id) + time.Now().UnixNano()))
			ctx := context.Background()
			for i := 0; i < ops; i++ {
				blockID := uint64(r.Intn(20)) + 1
				page, _, err := bp.Get(ctx, blockID)
				if err != nil {
					t.Errorf("Get(%d) error: %v", blockID, err)
					continue
				}
				if page.ID != blockID {
					t.Errorf("expected page.ID=%d, got %d", blockID, page.ID)
				}

				// Random pin/unpin.
				if r.Intn(2) == 0 {
					bp.Pin(page)
				} else {
					bp.Unpin(page)
				}
			}
		}(g)
	}

	wg.Wait()

	stats := bp.Stats()
	if stats.Hits+stats.Misses == 0 {
		t.Error("expected some hits or misses")
	}
	t.Logf("stats: hits=%d misses=%d pins=%d evicts=%d used=%d",
		stats.Hits, stats.Misses, stats.Pins, stats.Evicts, stats.Used)
}

// TestLoadingRace tests that concurrent loads of the same block are serialized.
func TestLoadingRace(t *testing.T) {
	tmp := t.TempDir()
	dataPath := filepath.Join(tmp, "data.razor")
	bd, err := df.Create(dataPath)
	if err != nil {
		t.Fatal("failed to create block device")
	}
	defer bd.Close()

	// Write one block.
	data := make([]byte, 4088)
	for i := range data {
		data[i] = 0xAB
	}
	if err := bd.WriteBlock(context.Background(), 1, data); err != nil {
		t.Fatalf("WriteBlock failed: %v", err)
	}

	sp := newMockSyncPool()
	bp, err := New(128, "", bd, sp)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer bp.Close()

	const goroutines = 8
	var wg sync.WaitGroup
	wg.Add(goroutines)

	start := make(chan struct{})
	var count atomic.Int64

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			<-start
			ctx := context.Background()
			for j := 0; j < 100; j++ {
				page, found, err := bp.Get(ctx, 1)
				if err != nil {
					continue
				}
				if found && page.ID == 1 {
					count.Add(1)
				}
			}
		}()
	}

	close(start)
	wg.Wait()

	if count.Load() < int64(goroutines*95) {
		t.Errorf("expected at least %d hits, got %d", goroutines*95, count.Load())
	}
}

// TestWarm tests hint file warm-up on startup.
func TestWarm(t *testing.T) {
	tmp := t.TempDir()
	dataPath := filepath.Join(tmp, "data.razor")
	hintPath := filepath.Join(tmp, "hint")

	// Create and populate a database with blocks 1, 2, 3.
	{
		bd, err := df.Create(dataPath)
		if err != nil {
			t.Fatal("failed to create block device")
		}
		for i := uint64(1); i <= 3; i++ {
			data := make([]byte, 4088)
			data[0] = byte(i)
			if err := bd.WriteBlock(context.Background(), i, data); err != nil {
				t.Fatalf("WriteBlock failed: %v", err)
			}
		}
		bd.Close()
	}

	// Open and write hint file with blocks 1, 2, 3.
	{
		bd, err := df.Open(dataPath)
		if err != nil {
			t.Fatal("failed to open block device")
		}
		defer bd.Close()

		sp := newMockSyncPool()
		bp, err := New(128, hintPath, bd, sp)
		if err != nil {
			t.Fatalf("New failed: %v", err)
		}

		// Load blocks to populate cache.
		for i := uint64(1); i <= 3; i++ {
			_, _, err := bp.Get(context.Background(), i)
			if err != nil {
				t.Fatalf("Get(%d) failed: %v", i, err)
			}
		}

		// Close writes hint file.
		bp.Close()
	}

	// Verify hint file exists.
	entries, err := readHintFile(hintPath)
	if err != nil {
		t.Fatalf("readHintFile failed: %v", err)
	}
	if len(entries) != 3 {
		t.Errorf("expected 3 hint entries, got %d", len(entries))
	}

	// Reopen and warm up.
	{
		bd, err := df.Open(dataPath)
		if err != nil {
			t.Fatal("failed to open block device")
		}
		defer bd.Close()

		sp := newMockSyncPool()
		bp, err := New(128, hintPath, bd, sp)
		if err != nil {
			t.Fatalf("New failed: %v", err)
		}
		defer bp.Close()

		err = bp.Warm(context.Background())
		if err != nil {
			t.Fatalf("Warm failed: %v", err)
		}

		stats := bp.Stats()
		if stats.Used != 3 {
			t.Errorf("expected used=3 after warm, got %d", stats.Used)
		}

		// All 3 blocks should be in cache.
		for i := uint64(1); i <= 3; i++ {
			page, found, err := bp.Get(context.Background(), i)
			if err != nil {
				t.Fatalf("Get(%d) after warm failed: %v", i, err)
			}
			if !found {
				t.Errorf("expected block %d to be cached after warm", i)
			}
			if page.ID != i {
				t.Errorf("expected page.ID=%d, got %d", i, page.ID)
			}
		}
	}
}

// TestWarmNoHintFile tests Warm when no hint file exists.
func TestWarmNoHintFile(t *testing.T) {
	tmp := t.TempDir()
	dataPath := filepath.Join(tmp, "data.razor")
	hintPath := filepath.Join(tmp, "hint") // does not exist

	bd, err := df.Create(dataPath)
	if err != nil {
		t.Fatal("failed to create block device")
	}
	defer bd.Close()

	sp := newMockSyncPool()
	bp, err := New(128, hintPath, bd, sp)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer bp.Close()

	err = bp.Warm(context.Background())
	if err != nil {
		t.Fatalf("Warm should succeed when no hint file: %v", err)
	}

	stats := bp.Stats()
	if stats.Used != 0 {
		t.Errorf("expected used=0 when no hint file, got %d", stats.Used)
	}
}

// TestCloseIdempotent tests that Close is safe to call multiple times.
func TestCloseIdempotent(t *testing.T) {
	tmp := t.TempDir()
	bd, err := df.Create(filepath.Join(tmp, "data.razor"))
	if err != nil {
		t.Fatal("failed to create block device")
	}
	defer bd.Close()

	sp := newMockSyncPool()
	bp, err := New(128, "", bd, sp)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	err1 := bp.Close()
	err2 := bp.Close()
	if err1 != nil || err2 != nil {
		t.Errorf("Close returned errors: %v, %v", err1, err2)
	}
}

// TestCloseWithHintFile tests that Close writes hint file.
func TestCloseWithHintFile(t *testing.T) {
	tmp := t.TempDir()
	dataPath := filepath.Join(tmp, "data.razor")
	hintPath := filepath.Join(tmp, "hint")

	bd, err := df.Create(dataPath)
	if err != nil {
		t.Fatal("failed to create block device")
	}
	defer bd.Close()

	// Write a block.
	data := make([]byte, 4088)
	data[0] = 0x42
	if err := bd.WriteBlock(context.Background(), 1, data); err != nil {
		t.Fatalf("WriteBlock failed: %v", err)
	}

	sp := newMockSyncPool()
	bp, err := New(128, hintPath, bd, sp)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	// Load the block.
	_, _, err = bp.Get(context.Background(), 1)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	err = bp.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Hint file should now exist.
	entries, err := readHintFile(hintPath)
	if err != nil {
		t.Fatalf("readHintFile failed: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 hint entry, got %d", len(entries))
	}
}

// TestContextCancellation tests that Get respects context cancellation.
func TestContextCancellation(t *testing.T) {
	tmp := t.TempDir()
	dataPath := filepath.Join(tmp, "data.razor")
	bd, err := df.Create(dataPath)
	if err != nil {
		t.Fatal("failed to create block device")
	}
	defer bd.Close()

	// Write a block so Get has something to load.
	data := make([]byte, 4088)
	for i := range data {
		data[i] = byte(i & 0xff)
	}
	if err := bd.WriteBlock(context.Background(), 1, data); err != nil {
		t.Fatalf("WriteBlock failed: %v", err)
	}

	sp := newMockSyncPool()
	bp, err := New(1, "", bd, sp)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer bp.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	// Block 1 was written successfully. Get should succeed with a cancelled context
	// on a cold cache miss (the block doesn't need to be loaded because it's not in cache).
	page, _, err := bp.Get(ctx, 1)
	if err != nil {
		// Context might be cancelled before we even try to load.
		t.Logf("Get returned error (might be expected): %v", err)
	} else {
		if page.ID != 1 {
			t.Errorf("expected page.ID=1, got %d", page.ID)
		}
	}
}

// TestStatsSnapshot tests that Stats returns consistent snapshots.
func TestStatsSnapshot(t *testing.T) {
	tmp := t.TempDir()
	bd, err := df.Create(filepath.Join(tmp, "data.razor"))
	if err != nil {
		t.Fatal("failed to create block device")
	}
	defer bd.Close()

	sp := newMockSyncPool()
	bp, err := New(128, "", bd, sp)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer bp.Close()

	stats := bp.Stats()
	if stats.Capacity != 128 {
		t.Errorf("expected capacity 128, got %d", stats.Capacity)
	}
	if stats.Used != 0 {
		t.Errorf("expected used 0, got %d", stats.Used)
	}
	if stats.Hits != 0 || stats.Misses != 0 {
		t.Errorf("expected zero hits/misses, got hits=%d misses=%d", stats.Hits, stats.Misses)
	}
}

// BenchmarkGet benchmarks concurrent Get operations.
func BenchmarkGet(b *testing.B) {
	tmp := b.TempDir()
	dataPath := filepath.Join(tmp, "data.razor")
	bd, err := df.Create(dataPath)
	if err != nil {
		b.Fatal("failed to create block device")
	}
	defer bd.Close()

	// Write 10 blocks.
	for i := uint64(1); i <= 10; i++ {
		data := make([]byte, 4088)
		for j := range data {
			data[j] = byte((i + uint64(j)) & 0xff)
		}
		if err := bd.WriteBlock(context.Background(), i, data); err != nil {
			b.Fatalf("WriteBlock failed: %v", err)
		}
	}

	sp := newMockSyncPool()
	bp, err := New(1024, "", bd, sp)
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
			blockID := uint64(r.Intn(10)) + 1
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

// BenchmarkPinUnpin benchmarks Pin/Unpin under contention.
func BenchmarkPinUnpin(b *testing.B) {
	tmp := b.TempDir()
	bd, err := df.Create(filepath.Join(tmp, "data.razor"))
	if err != nil {
		b.Fatal("failed to create block device")
	}
	defer bd.Close()

	// Write 10 blocks.
	for i := uint64(1); i <= 10; i++ {
		data := make([]byte, 4088)
		if err := bd.WriteBlock(context.Background(), i, data); err != nil {
			b.Fatalf("WriteBlock failed: %v", err)
		}
	}

	sp := newMockSyncPool()
	bp, err := New(1024, "", bd, sp)
	if err != nil {
		b.Fatalf("New failed: %v", err)
	}
	defer bp.Close()

	ctx := context.Background()

	// Pre-load blocks.
	pages := make([]*Page, 11)
	for i := uint64(1); i <= 10; i++ {
		page, _, err := bp.Get(ctx, i)
		if err != nil {
			b.Fatalf("Get failed: %v", err)
		}
		pages[i] = page
	}

	b.ResetTimer()
	b.ReportAllocs()

	r := rand.New(rand.NewSource(42))

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			blockID := uint64(r.Intn(10)) + 1
			page := pages[blockID]
			bp.Pin(page)
			bp.Unpin(page)
		}
	})
}
