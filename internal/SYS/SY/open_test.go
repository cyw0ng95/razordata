package SY

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SYS/AP"
)

func TestOpen_Success(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "db")
	eng, err := Open(context.Background(), dir, AP.Options{
		PageSize:     4096,
		MemTableSize: 1024 * 1024,
		BufferPoolMB: 64,
		WALSizeMB:    16,
		MaxLevel:     3,
		LogLevel:     8,
		LogFormat:    "text",
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if eng == nil {
		t.Fatal("Open returned nil engine")
	}
	if err := eng.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestOpen_InvalidOptions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "db")
	eng, err := Open(context.Background(), dir, AP.Options{
		PageSize: 123, // invalid page size
	})
	if err == nil {
		_ = eng.Close(context.Background())
		t.Fatal("expected error for invalid page size, got nil")
	}
}
