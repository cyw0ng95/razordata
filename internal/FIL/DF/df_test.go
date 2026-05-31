package df

import (
	"context"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestCreateOpen(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.block")

	bd, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	bd.Close()

	bd, err = Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer bd.Close()
}

func TestWriteReadBlock(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "rw.block")

	bd, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer bd.Close()

	data := []byte("hello world 12345678901234567890")
	ctx := context.Background()
	if err := bd.WriteBlock(ctx, 0, data); err != nil {
		t.Fatalf("WriteBlock: %v", err)
	}
	if err := bd.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	bd.Close()
	bd, err = Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer bd.Close()

	buf := make([]byte, DataLen)
	if err := bd.ReadBlock(ctx, 0, len(data), buf); err != nil {
		t.Fatalf("ReadBlock: %v", err)
	}
	if string(buf[:len(data)]) != string(data) {
		t.Fatalf("data mismatch: got %q", string(buf[:len(data)]))
	}
}

func TestChecksumCorrupt(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "corrupt.block")

	{
		bd, err := Create(path)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		ctx := context.Background()
		bd.WriteBlock(ctx, 0, []byte("test data"))
		bd.Sync()
		bd.Close()
	}

	fd, err := unix.Open(path, unix.O_RDWR, 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	var zeros [4]byte
	unix.Pwrite(fd, zeros[:], int64(DataLen-ChecksumLen))
	unix.Close(fd)

	bd, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer bd.Close()

	buf := make([]byte, DataLen)
	ctx := context.Background()
	if err := bd.ReadBlock(ctx, 0, 9, buf); err != ErrCorrupt {
		t.Fatalf("expected ErrCorrupt, got %v", err)
	}
}

func TestMultiBlock(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "multi.block")

	bd, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer bd.Close()

	ctx := context.Background()
	for i := uint64(0); i < 10; i++ {
		data := []byte{byte(i), byte(i + 1), byte(i + 2)}
		if err := bd.WriteBlock(ctx, i, data); err != nil {
			t.Fatalf("WriteBlock block %d: %v", i, err)
		}
	}

	for i := uint64(0); i < 10; i++ {
		buf := make([]byte, DataLen)
		if err := bd.ReadBlock(ctx, i, 3, buf); err != nil {
			t.Fatalf("ReadBlock block %d: %v", i, err)
		}
		if buf[0] != byte(i) || buf[1] != byte(i+1) || buf[2] != byte(i+2) {
			t.Fatalf("block %d data mismatch: got %v", i, buf)
		}
	}
}

func TestSize(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "size.block")

	bd, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer bd.Close()

	ctx := context.Background()
	bd.WriteBlock(ctx, 0, []byte("x"))
	bd.WriteBlock(ctx, 1, []byte("y"))

	size, err := bd.Size()
	if err != nil {
		t.Fatalf("Size: %v", err)
	}
	if size != int64(DefaultBlockSize*2) {
		t.Fatalf("expected size %d, got %d", DefaultBlockSize*2, size)
	}
}

func TestErrBigBlock(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "big.block")

	bd, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer bd.Close()

	ctx := context.Background()
	big := make([]byte, DataLen-ChecksumLen+1)
	err = bd.WriteBlock(ctx, 0, big)
	if err != ErrBigBlock {
		t.Fatalf("expected ErrBigBlock on WriteBlock, got %v", err)
	}
}

func TestSync(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "sync.bin")

	bd, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer bd.Close()

	ctx := context.Background()
	bd.WriteBlock(ctx, 0, []byte("sync test"))
	if err := bd.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
}

func TestClose(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "close.bin")

	bd, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := bd.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := bd.Close(); err != nil {
		t.Fatalf("Second Close: %v", err)
	}
}

func TestOpenMissing(t *testing.T) {
	tmp := t.TempDir()
	_, err := Open(filepath.Join(tmp, "nonexistent"))
	if err == nil {
		t.Fatalf("Open of missing file should fail")
	}
}

func TestBlock0AndBlock1(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "two_blocks.bin")

	bd, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer bd.Close()

	ctx := context.Background()
	bd.WriteBlock(ctx, 0, []byte("FIRST"))
	bd.WriteBlock(ctx, 1, []byte("SECOND"))

	buf0 := make([]byte, DataLen)
	buf1 := make([]byte, DataLen)
	if err := bd.ReadBlock(ctx, 0, 5, buf0); err != nil {
		t.Fatalf("ReadBlock 0: %v", err)
	}
	if err := bd.ReadBlock(ctx, 1, 6, buf1); err != nil {
		t.Fatalf("ReadBlock 1: %v", err)
	}
	if string(buf0[:5]) != "FIRST" {
		t.Fatalf("block 0: got %q", string(buf0[:5]))
	}
	if string(buf1[:6]) != "SECOND" {
		t.Fatalf("block 1: got %q", string(buf1[:6]))
	}
}

func TestChecksumIsolation(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "isol.bin")

	{
		bd, err := Create(path)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		ctx := context.Background()
		bd.WriteBlock(ctx, 0, []byte("ISOLATED"))
		bd.Sync()
		bd.Close()
	}

	fd, err := unix.Open(path, unix.O_RDWR, 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	var corrupt [1]byte
	corrupt[0] = 'X'
	unix.Pwrite(fd, corrupt[:], 2)
	unix.Close(fd)

	bd, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer bd.Close()

	buf := make([]byte, DataLen)
	ctx := context.Background()
	if err := bd.ReadBlock(ctx, 0, 8, buf); err != ErrCorrupt {
		t.Fatalf("expected ErrCorrupt after data corruption, got %v", err)
	}
}

func TestBlockSizeConstant(t *testing.T) {
	if DefaultBlockSize != 4096 {
		t.Fatalf("DefaultBlockSize should be 4096, got %d", DefaultBlockSize)
	}
	if DataLen != DefaultBlockSize-ChecksumLen {
		t.Fatalf("DataLen=%d, want %d", DataLen, DefaultBlockSize-ChecksumLen)
	}
	if !isPowerOfTwo(DefaultBlockSize) {
		t.Fatalf("DefaultBlockSize must be a power of 2")
	}
}

func isPowerOfTwo(n int) bool {
	return n > 0 && (n&(n-1)) == 0
}

func TestTempBufPoolReuse(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "pool.bin")

	bd, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	ctx := context.Background()

	for i := uint64(0); i < 100; i++ {
		data := []byte{byte(i)}
		if err := bd.WriteBlock(ctx, i, data); err != nil {
			t.Fatalf("WriteBlock %d: %v", i, err)
		}
	}
	bd.Sync()
	bd.Close()

	bd, err = Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer bd.Close()

	for i := uint64(0); i < 100; i++ {
		buf := make([]byte, DataLen)
		if err := bd.ReadBlock(ctx, i, 1, buf); err != nil {
			t.Fatalf("ReadBlock %d: %v", i, err)
		}
		if buf[0] != byte(i) {
			t.Fatalf("block %d: got %d, want %d", i, buf[0], byte(i))
		}
	}
}

func TestMaxDataPerBlock(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "maxdata.bin")

	bd, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer bd.Close()

	ctx := context.Background()
	maxData := make([]byte, DataLen-ChecksumLen)
	for i := range maxData {
		maxData[i] = byte(i % 256)
	}
	if err := bd.WriteBlock(ctx, 0, maxData); err != nil {
		t.Fatalf("WriteBlock max data: %v", err)
	}
	bd.Sync()
	bd.Close()

	bd, err = Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer bd.Close()

	buf := make([]byte, DataLen)
	if err := bd.ReadBlock(ctx, 0, DataLen-ChecksumLen, buf); err != nil {
		t.Fatalf("ReadBlock max data: %v", err)
	}
	for i := range maxData {
		if buf[i] != byte(i%256) {
			t.Fatalf("byte %d: got %x, want %x", i, buf[i], byte(i%256))
		}
	}
}

func TestPersistAndReadBack(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "persist.bin")

	data := []byte("persistent test data 0123456789")
	{
		bd, err := Create(path)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		ctx := context.Background()
		if err := bd.WriteBlock(ctx, 0, data); err != nil {
			t.Fatalf("WriteBlock: %v", err)
		}
		bd.Sync()
		bd.Close()
	}

	{
		bd, err := Open(path)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		defer bd.Close()
		ctx := context.Background()
		buf := make([]byte, DataLen)
		if err := bd.ReadBlock(ctx, 0, len(data), buf); err != nil {
			t.Fatalf("ReadBlock after reopen: %v", err)
		}
		if string(buf[:len(data)]) != string(data) {
			t.Fatalf("data mismatch after reopen")
		}
	}
}
