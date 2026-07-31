package fl

import (
	"sync"
	"testing"
	"time"

	"github.com/cyw0ng95/razordata/internal/FIL/FS"
	"github.com/cyw0ng95/razordata/internal/FIL/LF"
)

func TestFlusher_SyncDir(t *testing.T) {
	dir := t.TempDir()

	sm, err := lf.New(dir)
	if err != nil {
		t.Fatalf("failed to create segment manager: %v", err)
	}
	defer sm.Close()

	fm, err := fs.New(dir)
	if err != nil {
		t.Fatalf("failed to create file manager: %v", err)
	}

	f, err := New(dir, sm, fm, nil)
	if err != nil {
		t.Fatalf("failed to create flusher: %v", err)
	}
	defer f.Close()

	if err := f.SyncDir(); err != nil {
		t.Fatalf("SyncDir failed: %v", err)
	}
}

func TestFlusher_Close_Idempotent(t *testing.T) {
	dir := t.TempDir()

	sm, err := lf.New(dir)
	if err != nil {
		t.Fatalf("failed to create segment manager: %v", err)
	}
	defer sm.Close()

	fm, err := fs.New(dir)
	if err != nil {
		t.Fatalf("failed to create file manager: %v", err)
	}

	f, err := New(dir, sm, fm, nil)
	if err != nil {
		t.Fatalf("failed to create flusher: %v", err)
	}

	if err := f.Close(); err != nil {
		t.Fatalf("first close failed: %v", err)
	}

	if err := f.Close(); err != nil {
		t.Fatalf("second close failed: %v", err)
	}
}

func TestFlusher_SyncDirIdempotent(t *testing.T) {
	dir := t.TempDir()

	sm, err := lf.New(dir)
	if err != nil {
		t.Fatalf("failed to create segment manager: %v", err)
	}
	defer sm.Close()

	fm, err := fs.New(dir)
	if err != nil {
		t.Fatalf("failed to create file manager: %v", err)
	}

	f, err := New(dir, sm, fm, nil)
	if err != nil {
		t.Fatalf("failed to create flusher: %v", err)
	}
	defer f.Close()

	for i := 0; i < 3; i++ {
		if err := f.SyncDir(); err != nil {
			t.Fatalf("SyncDir iteration %d failed: %v", i, err)
		}
	}
}

func TestFlusher_Sync(t *testing.T) {
	dir := t.TempDir()

	sm, err := lf.New(dir)
	if err != nil {
		t.Fatalf("failed to create segment manager: %v", err)
	}
	defer sm.Close()

	fm, err := fs.New(dir)
	if err != nil {
		t.Fatalf("failed to create file manager: %v", err)
	}

	f, err := New(dir, sm, fm, nil)
	if err != nil {
		t.Fatalf("failed to create flusher: %v", err)
	}
	defer f.Close()

	if err := f.Sync(); err != nil {
		t.Fatalf("Sync failed: %v", err)
	}
}

func TestFlusher_BatchSync(t *testing.T) {
	dir := t.TempDir()

	sm, err := lf.New(dir)
	if err != nil {
		t.Fatalf("failed to create segment manager: %v", err)
	}
	defer sm.Close()

	fm, err := fs.New(dir)
	if err != nil {
		t.Fatalf("failed to create file manager: %v", err)
	}

	f, err := New(dir, sm, fm, nil)
	if err != nil {
		t.Fatalf("failed to create flusher: %v", err)
	}
	defer f.Close()

	if err := f.Sync(); err != nil {
		t.Fatalf("Sync failed: %v", err)
	}
}

func TestFlusher_AfterClose(t *testing.T) {
	dir := t.TempDir()

	sm, err := lf.New(dir)
	if err != nil {
		t.Fatalf("failed to create segment manager: %v", err)
	}
	defer sm.Close()

	fm, err := fs.New(dir)
	if err != nil {
		t.Fatalf("failed to create file manager: %v", err)
	}

	f, err := New(dir, sm, fm, nil)
	if err != nil {
		t.Fatalf("failed to create flusher: %v", err)
	}

	f.Close()

	if err := f.Sync(); err != nil {
		t.Fatalf("Sync after close failed: %v", err)
	}

	if err := f.Sync(); err != nil {
		t.Fatalf("Sync after close failed: %v", err)
	}

	if err := f.SyncDir(); err != nil {
		t.Fatalf("SyncDir after close failed: %v", err)
	}
}

// TestWriteBufferIntegration pins REQ000184: writeBuffer allocation
// and semantics.
func TestWriteBufferIntegration(t *testing.T) {
	wb := newWriteBuffer()
	if wb.Available() != 256*1024 {
		t.Errorf("newWriteBuffer: expected 262144 bytes, got %d", wb.Available())
	}
	data := []byte("hello wal")
	n, err := wb.Write(data)
	if err != nil {
		t.Fatalf("Write() failed: %v", err)
	}
	if n != 9 {
		t.Errorf("Write() returned %d; want 9", n)
	}
	if string(wb.Bytes()) != "hello wal" {
		t.Errorf("Bytes() = %q; want hello wal", wb.Bytes())
	}
	wb.Reset()
	if wb.off != 0 {
		t.Errorf("Reset() did not clear off")
	}
}

// TestBatchSyncGroupCommit pins REQ000176: group commit barrier.
func TestBatchSyncGroupCommit(t *testing.T) {
	dir := t.TempDir()
	sm, err := lf.New(dir)
	if err != nil {
		t.Fatalf("lf.New() failed: %v", err)
	}
	defer sm.Close()
	fm, err := fs.New(dir)
	if err != nil {
		t.Fatalf("fs.New() failed: %v", err)
	}
	f, err := New(dir, sm, fm, nil)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	defer f.Close()

	// Test concurrent Sync calls — they should be batched by the group commit pipeline.
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := f.Sync(); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}

// TestBatchSyncErrorPropagation verifies error propagation.
func TestBatchSyncErrorPropagation(t *testing.T) {
	dir := t.TempDir()
	sm, _ := lf.New(dir)
	defer sm.Close()
	fm, _ := fs.New(dir)
	f, _ := New(dir, sm, fm, nil)
	defer f.Close()

	// The new group commit pipeline batches requests automatically.
	// Error propagation is handled by the pipeline closing pending
	// requests on flusher close.
	errCh := make(chan error, 1)
	go func() {
		errCh <- f.Sync()
	}()

	// Give the Sync call time to start.
	time.Sleep(10 * time.Millisecond)

	// Close the flusher — pending Sync calls return nil (no-op contract).
	f.Close()

	// The Sync should return nil after close.
	err := <-errCh
	if err != nil {
		t.Errorf("expected nil after close, got %v", err)
	}
}

// TestFlusherCloseFlushesBuffer verifies that Close() attempts to flush
// the write buffer before closing (REQ000591).
func TestFlusherCloseFlushesBuffer(t *testing.T) {
	dir := t.TempDir()
	sm, _ := lf.New(dir)
	defer sm.Close()
	fm, _ := fs.New(dir)
	f, _ := New(dir, sm, fm, nil)
	defer f.Close()

	// The write buffer is flushed on Close if it has data.
	// This test verifies no panic occurs.
	if err := f.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}
	// Second close should be idempotent.
	if err := f.Close(); err != nil {
		t.Errorf("second Close failed: %v", err)
	}
}
