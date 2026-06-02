package fl

import (
	"testing"

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
