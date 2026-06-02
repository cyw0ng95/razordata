package df

import (
	"context"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestBlockDevice_WriteRead(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "data.razor")

	bd, err := Create(dataPath)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	defer bd.Close()

	data := make([]byte, DataLen-ChecksumLen)
	for i := range data {
		data[i] = byte(i % 256)
	}

	if err := bd.WriteBlock(context.Background(), 1, data); err != nil {
		t.Fatalf("WriteBlock failed: %v", err)
	}

	readBuf := make([]byte, DataLen)
	if err := bd.ReadBlock(context.Background(), 1, DataLen-ChecksumLen, readBuf); err != nil {
		t.Fatalf("ReadBlock failed: %v", err)
	}

	for i := range data {
		if readBuf[i] != data[i] {
			t.Fatalf("data mismatch at index %d: expected 0x%02x, got 0x%02x", i, data[i], readBuf[i])
		}
	}
}

func TestBlockDevice_Close_Idempotent(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "data.razor")

	bd, err := Create(dataPath)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if err := bd.Close(); err != nil {
		t.Fatalf("first close failed: %v", err)
	}

	if err := bd.Close(); err != nil {
		t.Fatalf("second close failed: %v", err)
	}
}

func TestBlockDevice_Size(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "data.razor")

	bd, err := Create(dataPath)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	defer bd.Close()

	size, err := bd.Size()
	if err != nil {
		t.Fatalf("Size failed: %v", err)
	}

	if size != 0 {
		t.Fatalf("expected size 0 for new file, got %d", size)
	}

	data := make([]byte, DataLen-ChecksumLen)
	bd.WriteBlock(context.Background(), 0, data)

	size, err = bd.Size()
	if err != nil {
		t.Fatalf("Size after write failed: %v", err)
	}

	if size != DefaultBlockSize {
		t.Fatalf("expected size %d, got %d", DefaultBlockSize, size)
	}
}

func TestBlockDevice_MultipleBlocks(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "data.razor")

	bd, err := Create(dataPath)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	defer bd.Close()

	blockCount := 10
	for i := uint64(1); i <= uint64(blockCount); i++ {
		data := make([]byte, DataLen-ChecksumLen)
		data[0] = byte(i)
		data[1] = byte(i >> 8)

		if err := bd.WriteBlock(context.Background(), i, data); err != nil {
			t.Fatalf("WriteBlock %d failed: %v", i, err)
		}
	}

	for i := uint64(1); i <= uint64(blockCount); i++ {
		readBuf := make([]byte, DataLen)
		if err := bd.ReadBlock(context.Background(), i, DataLen-ChecksumLen, readBuf); err != nil {
			t.Fatalf("ReadBlock %d failed: %v", i, err)
		}

		if readBuf[0] != byte(i) {
			t.Fatalf("block %d: expected first byte 0x%02x, got 0x%02x", i, byte(i), readBuf[0])
		}
	}
}

func TestBlockDevice_Sync(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "data.razor")

	bd, err := Create(dataPath)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	defer bd.Close()

	data := make([]byte, DataLen-ChecksumLen)
	bd.WriteBlock(context.Background(), 1, data)

	if err := bd.Sync(); err != nil {
		t.Fatalf("Sync failed: %v", err)
	}
}

func TestBlockDevice_ReadWriteInterleaved(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "data.razor")

	bd, err := Create(dataPath)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	defer bd.Close()

	for i := uint64(1); i <= 5; i++ {
		writeData := make([]byte, DataLen-ChecksumLen)
		writeData[0] = byte(i * 10)

		if err := bd.WriteBlock(context.Background(), i, writeData); err != nil {
			t.Fatalf("WriteBlock %d failed: %v", i, err)
		}

		readBuf := make([]byte, DataLen)
		if err := bd.ReadBlock(context.Background(), i, DataLen-ChecksumLen, readBuf); err != nil {
			t.Fatalf("ReadBlock %d failed: %v", i, err)
		}

		if readBuf[0] != byte(i*10) {
			t.Fatalf("block %d: expected 0x%02x, got 0x%02x", i, byte(i*10), readBuf[0])
		}
	}
}

func TestBlockDevice_ReadNonExistentBlock(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "data.razor")

	bd, err := Create(dataPath)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	defer bd.Close()

	readBuf := make([]byte, DataLen)
	err = bd.ReadBlock(context.Background(), 999, DataLen-ChecksumLen, readBuf)
	if err == nil {
		t.Fatal("expected error reading non-existent block")
	}
}

func TestBlockDevice_ReadAfterClose(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "data.razor")

	bd, err := Create(dataPath)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	data := make([]byte, DataLen-ChecksumLen)
	data[0] = 0xAB
	bd.WriteBlock(context.Background(), 1, data)
	bd.Close()

	readBuf := make([]byte, DataLen)
	err = bd.ReadBlock(context.Background(), 1, DataLen-ChecksumLen, readBuf)
	if err == nil {
		t.Fatal("expected error reading after close")
	}
}

func TestBlockDevice_WriteAfterClose(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "data.razor")

	bd, err := Create(dataPath)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	bd.Close()

	data := make([]byte, DataLen-ChecksumLen)
	err = bd.WriteBlock(context.Background(), 1, data)
	if err == nil {
		t.Fatal("expected error writing after close")
	}
}

func TestBlockDevice_MultipleSyncs(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "data.razor")

	bd, err := Create(dataPath)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	defer bd.Close()

	data := make([]byte, DataLen-ChecksumLen)
	bd.WriteBlock(context.Background(), 1, data)

	for i := 0; i < 5; i++ {
		if err := bd.Sync(); err != nil {
			t.Fatalf("Sync %d failed: %v", i, err)
		}
	}
}

func TestBlockDevice_EmptyData(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "data.razor")

	bd, err := Create(dataPath)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	defer bd.Close()

	err = bd.WriteBlock(context.Background(), 1, []byte{})
	if err != nil {
		t.Fatalf("empty data should be allowed, got: %v", err)
	}
}

func TestBlockDevice_MaxSizeData(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "data.razor")

	bd, err := Create(dataPath)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	defer bd.Close()

	data := make([]byte, DataLen-ChecksumLen)
	for i := range data {
		data[i] = byte(i % 256)
	}

	if err := bd.WriteBlock(context.Background(), 1, data); err != nil {
		t.Fatalf("WriteBlock failed: %v", err)
	}

	readBuf := make([]byte, DataLen)
	if err := bd.ReadBlock(context.Background(), 1, DataLen-ChecksumLen, readBuf); err != nil {
		t.Fatalf("ReadBlock failed: %v", err)
	}

	for i := range data {
		if readBuf[i] != data[i] {
			t.Fatalf("data mismatch at index %d", i)
		}
	}
}

func TestBlockDevice_OverflowSizeData(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "data.razor")

	bd, err := Create(dataPath)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	defer bd.Close()

	data := make([]byte, DataLen-ChecksumLen+1)

	err = bd.WriteBlock(context.Background(), 1, data)
	if err == nil {
		t.Fatal("expected error writing data that exceeds max size")
	}
}

func TestBlockDevice_ReadBlockFull(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "data.razor")

	bd, err := Create(dataPath)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	defer bd.Close()

	data := make([]byte, DataLen-ChecksumLen)
	for i := range data {
		data[i] = byte(i % 256)
	}

	if err := bd.WriteBlock(context.Background(), 1, data); err != nil {
		t.Fatalf("WriteBlock failed: %v", err)
	}

	readBuf := make([]byte, DataLen)
	if err := bd.ReadBlockFull(1, readBuf); err != nil {
		t.Fatalf("ReadBlockFull failed: %v", err)
	}

	for i := range data {
		if readBuf[i] != data[i] {
			t.Fatalf("data mismatch at index %d", i)
		}
	}
}

func TestBlockDevice_ReadBlockFullCorruptIntegration(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "data.razor")

	bd, err := Create(dataPath)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	defer bd.Close()

	data := make([]byte, DataLen-ChecksumLen)
	for i := range data {
		data[i] = byte(i % 256)
	}

	if err := bd.WriteBlock(context.Background(), 1, data); err != nil {
		t.Fatalf("WriteBlock failed: %v", err)
	}

	rawBuf := make([]byte, DefaultBlockSize)
	fd, _ := unix.Open(dataPath, unix.O_RDWR, 0)
	unix.Pread(fd, rawBuf, int64(1*uint64(DefaultBlockSize)))
	rawBuf[100] ^= 0xFF
	unix.Pwrite(fd, rawBuf, int64(1*uint64(DefaultBlockSize)))
	unix.Close(fd)

	readBuf := make([]byte, DataLen)
	err = bd.ReadBlockFull(1, readBuf)
	if err != ErrCorrupt {
		t.Fatalf("expected ErrCorrupt, got %v", err)
	}
}

func TestBlockDevice_Reopen(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "data.razor")

	bd, err := Create(dataPath)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	writeData := make([]byte, DataLen-ChecksumLen)
	writeData[0] = 0x42
	if err := bd.WriteBlock(context.Background(), 1, writeData); err != nil {
		t.Fatalf("WriteBlock failed: %v", err)
	}
	bd.Close()

	bd2, err := Open(dataPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer bd2.Close()

	readBuf := make([]byte, DataLen)
	if err := bd2.ReadBlock(context.Background(), 1, DataLen-ChecksumLen, readBuf); err != nil {
		t.Fatalf("ReadBlock failed: %v", err)
	}

	if readBuf[0] != 0x42 {
		t.Fatalf("expected 0x42, got 0x%02x", readBuf[0])
	}
}

func TestBlockDevice_SyncAfterWrite(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "data.razor")

	bd, err := Create(dataPath)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	defer bd.Close()

	for i := uint64(1); i <= 3; i++ {
		data := make([]byte, DataLen-ChecksumLen)
		data[0] = byte(i)
		if err := bd.WriteBlock(context.Background(), i, data); err != nil {
			t.Fatalf("WriteBlock %d failed: %v", i, err)
		}
		if err := bd.Sync(); err != nil {
			t.Fatalf("Sync %d failed: %v", i, err)
		}
	}
}

func TestBlockDevice_SizeAfterMultipleWrites(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "data.razor")

	bd, err := Create(dataPath)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	defer bd.Close()

	data := make([]byte, DataLen-ChecksumLen)

	for i := uint64(1); i <= 10; i++ {
		bd.WriteBlock(context.Background(), i, data)
	}

	size, _ := bd.Size()
	expectedSize := int64(11 * DefaultBlockSize)
	if size != expectedSize {
		t.Fatalf("expected size %d, got %d", expectedSize, size)
	}
}

func TestBlockDevice_ReadBlockFullNonExistent(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "data.razor")

	bd, err := Create(dataPath)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	defer bd.Close()

	readBuf := make([]byte, DataLen)
	err = bd.ReadBlockFull(999, readBuf)
	if err == nil {
		t.Fatal("expected error reading non-existent block with ReadBlockFull")
	}
}

func TestBlockDevice_BufferNotModifiedOnError(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "data.razor")

	bd, err := Create(dataPath)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	defer bd.Close()

	initialBuf := make([]byte, DataLen)
	for i := range initialBuf {
		initialBuf[i] = 0xAA
	}

	readBuf := make([]byte, DataLen)
	copy(readBuf, initialBuf)

	err = bd.ReadBlock(context.Background(), 999, DataLen-ChecksumLen, readBuf)
	if err == nil {
		t.Fatal("expected error reading non-existent block")
	}

	for i := range readBuf {
		if readBuf[i] != initialBuf[i] {
			t.Fatalf("buffer was modified despite error at index %d", i)
		}
	}
}
