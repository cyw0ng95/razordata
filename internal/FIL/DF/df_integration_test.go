package df

import (
	"context"
	"path/filepath"
	"testing"
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
