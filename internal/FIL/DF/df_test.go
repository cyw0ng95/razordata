package df

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/cyw0ng95/razordata/internal/LOG/LG"
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

func TestReadBlockCorruptEOF(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "short.block")

	bd, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	bd.Close()

	// Truncate to zero so pread reads nothing (EOF).
	os.Truncate(path, 0)

	bd, err = Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer bd.Close()

	buf := make([]byte, DataLen)
	ctx := context.Background()
	err = bd.ReadBlock(ctx, 0, 8, buf)
	if err == nil {
		t.Fatalf("ReadBlock on zero-sized file should fail")
	}
}

func TestSizeAfterTruncate(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "trunc.bin")

	bd, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	ctx := context.Background()
	bd.WriteBlock(ctx, 0, []byte("x"))
	bd.WriteBlock(ctx, 1, []byte("y"))
	bd.Sync()
	bd.Close()

	fd, err := unix.Open(path, unix.O_RDWR, 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	unix.Ftruncate(fd, int64(DefaultBlockSize))
	unix.Close(fd)

	bd, err = Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer bd.Close()

	size, err := bd.Size()
	if err != nil {
		t.Fatalf("Size: %v", err)
	}
	if size != int64(DefaultBlockSize) {
		t.Fatalf("expected size %d, got %d", DefaultBlockSize, size)
	}
}

func TestSyncCloseError(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "syncerr.bin")

	bd, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Close first — subsequent operations should be no-ops.
	bd.Close()

	// Second Sync should be safe (fd is -1).
	if err := bd.Sync(); err != nil {
		t.Fatalf("Sync after close: %v", err)
	}
	// Second Close should be safe.
	if err := bd.Close(); err != nil {
		t.Fatalf("Second Close: %v", err)
	}
}

func TestWriteReadLargeBlock(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "large.block")

	bd, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer bd.Close()

	// Write max-sized data.
	data := make([]byte, DataLen-ChecksumLen)
	for i := range data {
		data[i] = byte((i * 31) % 256)
	}

	ctx := context.Background()
	if err := bd.WriteBlock(ctx, 0, data); err != nil {
		t.Fatalf("WriteBlock max: %v", err)
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
		t.Fatalf("ReadBlock max: %v", err)
	}
	for i := range data {
		if buf[i] != data[i] {
			t.Fatalf("byte %d: got %x, want %x", i, buf[i], data[i])
		}
	}
}

func TestSupportsODirect(t *testing.T) {
	if !supportsODirect() {
		t.Skip("O_DIRECT not supported on this system")
	}
	tmp := t.TempDir()
	path := filepath.Join(tmp, "direct.bin")

	bd, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer bd.Close()

	if !bd.direct {
		t.Fatalf("expected O_DIRECT mode")
	}

	ctx := context.Background()
	data := []byte("direct io test")
	if err := bd.WriteBlock(ctx, 0, data); err != nil {
		t.Fatalf("WriteBlock in O_DIRECT mode: %v", err)
	}
	bd.Sync()

	buf := make([]byte, DataLen)
	if err := bd.ReadBlock(ctx, 0, len(data), buf); err != nil {
		t.Fatalf("ReadBlock in O_DIRECT mode: %v", err)
	}
	if string(buf[:len(data)]) != string(data) {
		t.Fatalf("O_DIRECT data mismatch: got %q", string(buf[:len(data)]))
	}
}

func TestErrBigBlockRead(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "bigread.block")

	bd, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer bd.Close()

	ctx := context.Background()

	// buf smaller than DataLen.
	smallBuf := make([]byte, DataLen-ChecksumLen-1)
	err = bd.ReadBlock(ctx, 0, DataLen-ChecksumLen, smallBuf)
	if err != ErrBigBlock {
		t.Fatalf("expected ErrBigBlock, got %v", err)
	}
}

func TestReadBlockNegativeN(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "neg_n.block")

	bd, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer bd.Close()

	ctx := context.Background()
	buf := make([]byte, DataLen)
	err = bd.ReadBlock(ctx, 0, -1, buf)
	if err != ErrBigBlock {
		t.Fatalf("expected ErrBigBlock for n=-1, got %v", err)
	}
}

func TestWriteReadEmptyBlock(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "empty.block")

	bd, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer bd.Close()

	ctx := context.Background()
	empty := []byte{}
	if err := bd.WriteBlock(ctx, 0, empty); err != nil {
		t.Fatalf("WriteBlock empty: %v", err)
	}
	bd.Sync()

	buf := make([]byte, DataLen)
	if err := bd.ReadBlock(ctx, 0, 0, buf); err != nil {
		t.Fatalf("ReadBlock empty: %v", err)
	}
	for i := 0; i < DataLen; i++ {
		if buf[i] != 0 {
			t.Fatalf("block 0 should be all zeros, byte %d = %x", i, buf[i])
		}
	}
}

func TestWriteReadSingleByte(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "single.block")

	bd, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer bd.Close()

	ctx := context.Background()
	for i := uint64(0); i < 256; i++ {
		data := []byte{byte(i)}
		if err := bd.WriteBlock(ctx, i, data); err != nil {
			t.Fatalf("WriteBlock %d: %v", i, err)
		}
	}
	bd.Sync()

	bd, err = Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer bd.Close()

	for i := uint64(0); i < 256; i++ {
		buf := make([]byte, DataLen)
		if err := bd.ReadBlock(ctx, i, 1, buf); err != nil {
			t.Fatalf("ReadBlock %d: %v", i, err)
		}
		if buf[0] != byte(i) {
			t.Fatalf("block %d: got %d, want %d", i, buf[0], byte(i))
		}
	}
}

// TestReadBlockFull tests ReadBlockFull with valid and corrupt data.
func TestReadBlockFull(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "full.block")

	// Write a full block
	bd, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer bd.Close()

	fullData := make([]byte, DataLen-ChecksumLen)
	for i := range fullData {
		fullData[i] = byte(i % 256)
	}

	ctx := context.Background()
	if err := bd.WriteBlock(ctx, 0, fullData); err != nil {
		t.Fatalf("WriteBlock: %v", err)
	}
	if err := bd.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	bd.Close()

	// Reopen and read full block
	bd, err = Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer bd.Close()

	buf := make([]byte, DataLen)
	if err := bd.ReadBlockFull(0, buf); err != nil {
		t.Fatalf("ReadBlockFull: %v", err)
	}
	if string(buf[:len(fullData)]) != string(fullData) {
		t.Errorf("data mismatch")
	}

	// Test with buffer too small
	smallBuf := make([]byte, DataLen-1)
	if err := bd.ReadBlockFull(0, smallBuf); err != ErrBigBlock {
		t.Errorf("expected ErrBigBlock for small buffer, got %v", err)
	}
}

// TestReadBlockFullCorrupt tests ReadBlockFull with corrupted checksum.
func TestReadBlockFullCorrupt(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "corrupt_full.block")

	// Create and write
	bd, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	ctx := context.Background()
	bd.WriteBlock(ctx, 0, []byte("test data"))
	bd.Sync()
	bd.Close()

	// Corrupt checksum
	fd, err := unix.Open(path, unix.O_RDWR, 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	var zeros [4]byte
	unix.Pwrite(fd, zeros[:], int64(DataLen-ChecksumLen))
	unix.Close(fd)

	// Read should fail with ErrCorrupt
	bd, err = Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer bd.Close()

	buf := make([]byte, DataLen)
	if err := bd.ReadBlockFull(0, buf); err != ErrCorrupt {
		t.Errorf("expected ErrCorrupt, got %v", err)
	}
}

// TestSizeClosed tests Size on closed device.
func TestSizeClosed(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "closed.block")

	bd, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	bd.Close()

	_, err = bd.Size()
	if err == nil {
		t.Error("expected error on closed device")
	}
}

// TestWriteBlockMaxSize tests WriteBlock with maximum allowed data size.
func TestWriteBlockMaxSize(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "max.block")

	bd, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer bd.Close()

	maxData := make([]byte, DataLen-ChecksumLen)
	for i := range maxData {
		maxData[i] = byte(i % 256)
	}

	ctx := context.Background()
	if err := bd.WriteBlock(ctx, 0, maxData); err != nil {
		t.Errorf("WriteBlock with max size failed: %v", err)
	}

	// Verify by reading back
	buf := make([]byte, DataLen)
	if err := bd.ReadBlock(ctx, 0, len(maxData), buf); err != nil {
		t.Errorf("ReadBlock failed: %v", err)
	}
	if string(buf[:len(maxData)]) != string(maxData) {
		t.Errorf("data mismatch")
	}
}

// TestOpenFails tests openFile with invalid path.
func TestOpenFails(t *testing.T) {
	// Try to open a file in a non-existent directory
	_, err := openFile("/nonexistent/dir/test.block", false, false, nil)
	if err == nil {
		t.Error("expected error opening non-existent path")
	}
}

// TestFirstLoggerNil tests firstLogger with empty slice.
func TestFirstLoggerNil(t *testing.T) {
	result := lg.FirstLogger(nil)
	if result != nil {
		t.Error("expected nil for nil input")
	}
}

// TestWriteReadMultipleBlocks tests writing and reading multiple blocks.
func TestWriteReadMultipleBlocks(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "multi.block")

	bd, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer bd.Close()

	ctx := context.Background()
	blocks := [][]byte{
		[]byte("block 0 data"),
		[]byte("block 1 data"),
		[]byte("block 2 data"),
	}

	// Write blocks
	for i, data := range blocks {
		if err := bd.WriteBlock(ctx, uint64(i), data); err != nil {
			t.Fatalf("WriteBlock %d: %v", i, err)
		}
	}

	// Sync and reopen
	if err := bd.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	bd.Close()

	bd, err = Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer bd.Close()

	// Read and verify
	buf := make([]byte, DataLen)
	for i, expected := range blocks {
		if err := bd.ReadBlock(ctx, uint64(i), len(expected), buf); err != nil {
			t.Fatalf("ReadBlock %d: %v", i, err)
		}
		if string(buf[:len(expected)]) != string(expected) {
			t.Errorf("block %d mismatch", i)
		}
	}
}

// TestReadBlockFullWithLogger tests ReadBlockFull error handling.
func TestReadBlockFullWithLogger(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "bad.block")

	// Create file manually without proper format
	if err := os.WriteFile(path, []byte("invalid"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	bd, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer bd.Close()

	buf := make([]byte, DataLen)
	// Read should fail - file too small
	err = bd.ReadBlockFull(0, buf)
	if err == nil {
		t.Error("expected error reading invalid file")
	}
}

func TestOpenNonExistentFile(t *testing.T) {
	_, err := Open("/non/existent/file/path/razor")
	if err == nil {
		t.Error("expected error opening non-existent file")
	}
}

func TestCreateInNonExistentDir(t *testing.T) {
	_, err := Create("/non/existent/directory/razor")
	if err == nil {
		t.Error("expected error creating file in non-existent directory")
	}
}

func TestWriteBlockErrorPath(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "data.razor")

	bd, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	bd.Close()

	bd2, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer bd2.Close()

	data := make([]byte, DataLen-ChecksumLen)
	for i := range data {
		data[i] = byte(i & 0xff)
	}

	ctx := context.Background()
	err = bd2.WriteBlock(ctx, 0, data)
	if err != nil {
		t.Errorf("WriteBlock: %v", err)
	}
}

func TestSupportsODirectPath(t *testing.T) {
	result := supportsODirect()
	if result {
		t.Log("O_DIRECT is supported on this system")
	} else {
		t.Log("O_DIRECT is not supported on this system")
	}
}

func TestOpenEmptyPath(t *testing.T) {
	_, err := Open("")
	if err == nil {
		t.Error("expected error for empty path")
	}
}

func TestCreateEmptyPath(t *testing.T) {
	_, err := Create("")
	if err == nil {
		t.Error("expected error for empty path")
	}
}

func TestCreateParentDoesNotExist(t *testing.T) {
	_, err := Create("/nonexistent/parent/dir/file.razor")
	if err == nil {
		t.Error("expected error when parent dir does not exist")
	}
}

func TestOpenFileReadOnly(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "readonly.block")

	bd, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	bd.Close()

	bd2, err := openFile(path, true, false, nil)
	if err != nil {
		t.Fatalf("openFile readOnly: %v", err)
	}
	bd2.Close()
}

func TestOpenFileNoCreateNonExistent(t *testing.T) {
	_, err := openFile("/nonexistent/path/no_create.block", false, false, nil)
	if err == nil {
		t.Error("expected error when opening non-existent file without create flag")
	}
}

func TestOpenFileWithLogger(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "withlog.block")

	log := lg.New(lg.Options{Output: io.Discard})
	bd, err := openFile(path, false, true, []lg.Logger{log})
	if err != nil {
		t.Fatalf("openFile with logger: %v", err)
	}
	bd.Close()
}

func TestFirstLoggerWithMultiple(t *testing.T) {
	log1 := lg.New(lg.Options{Output: io.Discard})
	log2 := lg.New(lg.Options{Output: io.Discard})

	result := lg.FirstLogger([]lg.Logger{log1, log2})
	if result != log1 {
		t.Error("expected first logger")
	}
}

func TestSyncOnEmptyDevice(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "sync.razor")
	bd, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	bd.Close()
	err = bd.Sync()
	if err != nil {
		t.Errorf("Sync after Close: %v", err)
	}
}

func TestCloseIdempotentMultiple(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "close.razor")
	bd, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	bd.Close()
	bd.Close()
	bd.Close()
}

func TestSizeOnNewDevice(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "size.razor")
	bd, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer bd.Close()
	size, err := bd.Size()
	if err != nil {
		t.Errorf("Size: %v", err)
	}
	if size != 0 {
		t.Errorf("expected size 0, got %d", size)
	}
}

func TestWriteBlockZeroBlockID(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "zero.razor")
	bd, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer bd.Close()
	data := make([]byte, 4088)
	err = bd.WriteBlock(context.Background(), 0, data)
	if err != nil {
		t.Errorf("WriteBlock with blockID=0: %v", err)
	}
}

func TestWriteBlockLargeBlockID(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "large.razor")
	bd, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer bd.Close()
	data := make([]byte, 4088)
	err = bd.WriteBlock(context.Background(), 1000000, data)
	if err != nil {
		t.Errorf("WriteBlock with large blockID: %v", err)
	}
}

func TestMultipleWritesSameBlock(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "multi.razor")
	bd, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer bd.Close()
	data := make([]byte, 4088)
	for i := range data {
		data[i] = byte(i % 256)
	}
	for i := 0; i < 5; i++ {
		if err := bd.WriteBlock(context.Background(), 0, data); err != nil {
			t.Errorf("WriteBlock attempt %d: %v", i, err)
		}
	}
}

func TestReadBlockSmallBuffer(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "small.razor")
	bd, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer bd.Close()
	data := make([]byte, 4088)
	bd.WriteBlock(context.Background(), 0, data)
	buf := make([]byte, 100)
	err = bd.ReadBlock(context.Background(), 0, 4088, buf)
	if err == nil {
		t.Error("expected error for buffer too small")
	}
}

// TestMmapRoundTrip: write blocks via a regular BlockDevice, then
// re-open with OpenMmap and read them back. The mmap path should
// return the same content (and the same checksums).
func TestMmapRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "mmap.dat")
	// Write phase: 4 blocks.
	w, err := Create(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		data := make([]byte, 100)
		for j := range data {
			data[j] = byte(i*10 + j)
		}
		if err := w.WriteBlock(ctx, uint64(i), data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Sync(); err != nil {
		t.Fatal(err)
	}
	w.Close()

	// Read phase via mmap.
	r, err := OpenMmap(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if !r.mmap {
		t.Logf("mmap not active on this platform; falling back to pread")
	}
	for i := 0; i < 4; i++ {
		buf := make([]byte, 4092)
		if err := r.ReadBlock(ctx, uint64(i), 100, buf); err != nil {
			t.Fatalf("block %d: %v", i, err)
		}
		for j := 0; j < 100; j++ {
			if buf[j] != byte(i*10+j) {
				t.Errorf("block %d byte %d: got %d, want %d", i, j, buf[j], i*10+j)
			}
		}
	}
}

// BenchmarkBlockReadPread reads via the standard pread path.
// BenchmarkBlockReadMmap reads via the mmap path (zero-copy on
// warm kernel page cache).
func BenchmarkBlockReadPread(b *testing.B) {
	tmp := b.TempDir()
	path := filepath.Join(tmp, "pread.dat")
	w, err := Create(path)
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	for i := 0; i < 1024; i++ {
		data := make([]byte, DataLen-ChecksumLen)
		for j := range data {
			data[j] = byte(i + j)
		}
		if err := w.WriteBlock(ctx, uint64(i), data); err != nil {
			b.Fatal(err)
		}
	}
	w.Sync()
	w.Close()

	r, err := Open(path)
	if err != nil {
		b.Fatal(err)
	}
	defer r.Close()
	buf := make([]byte, DataLen)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		blockID := uint64(i % 1024)
		if err := r.ReadBlock(ctx, blockID, DataLen-ChecksumLen, buf); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBlockReadMmap(b *testing.B) {
	tmp := b.TempDir()
	path := filepath.Join(tmp, "mmap.dat")
	w, err := Create(path)
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	for i := 0; i < 1024; i++ {
		data := make([]byte, DataLen-ChecksumLen)
		for j := range data {
			data[j] = byte(i + j)
		}
		if err := w.WriteBlock(ctx, uint64(i), data); err != nil {
			b.Fatal(err)
		}
	}
	w.Sync()
	w.Close()

	r, err := OpenMmap(path)
	if err != nil {
		b.Fatal(err)
	}
	defer r.Close()
	buf := make([]byte, DataLen)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		blockID := uint64(i % 1024)
		if err := r.ReadBlock(ctx, blockID, DataLen-ChecksumLen, buf); err != nil {
			b.Fatal(err)
		}
	}
}
