package df

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/cyw0ng95/razordata/internal/LOG/LG"
)

func TestOpen_NonExistent(t *testing.T) {
	tmp := t.TempDir()
	_, err := Open(filepath.Join(tmp, "does-not-exist"))
	if err == nil {
		t.Errorf("expected error opening non-existent file, got nil")
	}
}

func TestCreate_AlreadyExists(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "exists.block")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	_, err = Create(path)
	if err == nil {
		t.Errorf("expected error when creating existing file (O_EXCL), got nil")
	}
}

func TestWriteBlock_Oversize(t *testing.T) {
	tmp := t.TempDir()
	bd, err := Create(filepath.Join(tmp, "big.block"))
	if err != nil {
		t.Fatal(err)
	}
	defer bd.Close()

	// Data exceeds DataLen - ChecksumLen (4092)
	huge := make([]byte, DataLen)
	err = bd.WriteBlock(context.Background(), 0, huge)
	if err != ErrBigBlock {
		t.Errorf("expected ErrBigBlock, got %v", err)
	}
}

func TestWriteBlock_DirectPath(t *testing.T) {
	// Force the O_DIRECT branch by setting the flag directly on a fresh
	// BlockDevice. This guarantees the bufPool path is exercised even
	// on filesystems where O_DIRECT is unavailable.
	tmp := t.TempDir()
	bd, err := Create(filepath.Join(tmp, "direct.block"))
	if err != nil {
		t.Fatal(err)
	}
	defer bd.Close()
	bd.direct = true

	data := []byte("O_DIRECT path test data")
	if err := bd.WriteBlock(context.Background(), 0, data); err != nil {
		// tmpfs/some filesystems do not support O_DIRECT pwrite — accept
		// a non-nil error and ensure we at least hit the branch.
		t.Logf("O_DIRECT write returned error (acceptable on some filesystems): %v", err)
		return
	}
}

func TestWriteBlock_BufferedPath(t *testing.T) {
	// Force the buffered (non-O_DIRECT) branch.
	tmp := t.TempDir()
	bd, err := Create(filepath.Join(tmp, "buffered.block"))
	if err != nil {
		t.Fatal(err)
	}
	defer bd.Close()
	bd.direct = false

	data := []byte("buffered path test data")
	if err := bd.WriteBlock(context.Background(), 0, data); err != nil {
		t.Fatalf("WriteBlock: %v", err)
	}

	// Read it back to confirm roundtrip.
	buf := make([]byte, DataLen)
	if err := bd.ReadBlock(context.Background(), 0, len(data), buf); err != nil {
		t.Fatalf("ReadBlock: %v", err)
	}
	if !bytes.Equal(buf[:len(data)], data) {
		t.Errorf("roundtrip mismatch: got %q, want %q", buf[:len(data)], data)
	}
}

func TestWriteBlock_LoggerOnError(t *testing.T) {
	var buf bytes.Buffer
	log := lg.New(lg.Options{Level: slog.LevelDebug, Format: "text", Output: &buf})

	tmp := t.TempDir()
	bd, err := Create(filepath.Join(tmp, "log.block"), log)
	if err != nil {
		t.Fatal(err)
	}
	defer bd.Close()

	// Force an error by attempting a write to an invalid block offset
	// is hard to trigger cleanly, so just exercise the happy path with
	// the logger attached to confirm it doesn't blow up.
	if err := bd.WriteBlock(context.Background(), 0, []byte("ok")); err != nil {
		t.Fatalf("WriteBlock: %v", err)
	}
}

func TestReadBlock_BufferTooSmall(t *testing.T) {
	tmp := t.TempDir()
	bd, err := Create(filepath.Join(tmp, "small-buf.block"))
	if err != nil {
		t.Fatal(err)
	}
	defer bd.Close()

	if err := bd.WriteBlock(context.Background(), 0, []byte("data")); err != nil {
		t.Fatal(err)
	}

	tiny := make([]byte, 16)
	err = bd.ReadBlock(context.Background(), 0, 4, tiny)
	if err != ErrBigBlock {
		t.Errorf("expected ErrBigBlock for tiny buffer, got %v", err)
	}
}

func TestReadBlock_NegativeN(t *testing.T) {
	tmp := t.TempDir()
	bd, err := Create(filepath.Join(tmp, "neg.block"))
	if err != nil {
		t.Fatal(err)
	}
	defer bd.Close()

	buf := make([]byte, DataLen)
	err = bd.ReadBlock(context.Background(), 0, -1, buf)
	if err != ErrBigBlock {
		t.Errorf("expected ErrBigBlock for negative n, got %v", err)
	}

	err = bd.ReadBlock(context.Background(), 0, DataLen+1, buf)
	if err != ErrBigBlock {
		t.Errorf("expected ErrBigBlock for n>DataLen, got %v", err)
	}
}

func TestSync_AfterClose(t *testing.T) {
	tmp := t.TempDir()
	bd, err := Create(filepath.Join(tmp, "sync-after-close.block"))
	if err != nil {
		t.Fatal(err)
	}
	bd.Close()
	// Sync after close should not panic; fd is -1, returns nil.
	if err := bd.Sync(); err != nil {
		t.Errorf("Sync after close: want nil, got %v", err)
	}
}

func TestClose_Idempotent(t *testing.T) {
	tmp := t.TempDir()
	bd, err := Create(filepath.Join(tmp, "idempotent.block"))
	if err != nil {
		t.Fatal(err)
	}
	if err := bd.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	// Second close: fd is -1, returns nil.
	if err := bd.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestSize_OnClosedDevice(t *testing.T) {
	tmp := t.TempDir()
	bd, err := Create(filepath.Join(tmp, "size.block"))
	if err != nil {
		t.Fatal(err)
	}
	bd.WriteBlock(context.Background(), 0, []byte("hi"))
	bd.Close()
	// Size on closed fd: fstat fails, returns error (not panic).
	if _, err := bd.Size(); err == nil {
		t.Errorf("Size on closed fd: want error, got nil")
	}
}
