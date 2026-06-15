package fl

import (
	"testing"

	"errors"
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

	if err := f.BatchSync(); err != nil {
		t.Fatalf("BatchSync failed: %v", err)
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

	if err := f.BatchSync(); err != nil {
		t.Fatalf("BatchSync after close failed: %v", err)
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

	done := make(chan struct{}, 3)
	for i := 0; i < 3; i++ {
		go func() {
			fl := f.(*flusher)
			fl.StartBatch()
			fl.EndBatch(nil)
			done <- struct{}{}
		}()
	}
	for i := 0; i < 3; i++ {
		<-done
	}
	if err := f.BatchSync(); err != nil {
		t.Errorf("BatchSync() failed: %v", err)
	}
}

// TestBatchSyncErrorPropagation verifies error propagation.
func TestBatchSyncErrorPropagation(t *testing.T) {
	dir := t.TempDir()
	sm, _ := lf.New(dir)
	defer sm.Close()
	fm, _ := fs.New(dir)
	f, _ := New(dir, sm, fm, nil)
	defer f.Close()

	fl := f.(*flusher)
	fl.StartBatch()
	wantErr := errors.New("write error")
	fl.EndBatch(wantErr)
	gotErr := fl.BatchSync()
	if gotErr != wantErr {
		t.Errorf("BatchSync() = %v; want %v", gotErr, wantErr)
	}
}
