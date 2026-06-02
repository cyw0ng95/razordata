package ls

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSSTReader_searchIndex(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_sst_search")

	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}

	w := newSSTWriter()
	w.Add([]byte("c"), []byte("3"))
	w.Add([]byte("f"), []byte("6"))
	w.Add([]byte("i"), []byte("9"))

	sstData, err := w.Finish()
	if err != nil {
		t.Fatalf("Finish failed: %v", err)
	}

	reader, err := openSST(sstData)
	if err != nil {
		t.Fatalf("openSST failed: %v", err)
	}

	idx := reader.searchIndex([]byte("e"))
	if idx < 0 {
		t.Fatal("expected non-negative index")
	}
}

func TestSSTReader_readBlock(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_sst_readblock")

	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}

	w := newSSTWriter()
	w.Add([]byte("key1"), []byte("value1"))

	sstData, err := w.Finish()
	if err != nil {
		t.Fatalf("Finish failed: %v", err)
	}

	reader, err := openSST(sstData)
	if err != nil {
		t.Fatalf("openSST failed: %v", err)
	}

	block := reader.readBlock(0, 100)
	if block == nil {
		t.Fatal("expected non-nil block data")
	}
}

func TestSSTReader_mayContain(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_sst_maycontain")

	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}

	w := newSSTWriter()
	w.Add([]byte("key1"), []byte("value1"))

	sstData, err := w.Finish()
	if err != nil {
		t.Fatalf("Finish failed: %v", err)
	}

	reader, err := openSST(sstData)
	if err != nil {
		t.Fatalf("openSST failed: %v", err)
	}

	if !reader.mayContain([]byte("key1")) {
		t.Fatal("expected mayContain to return true for key1")
	}
}

func TestSSTReader_OpenInvalid(t *testing.T) {
	_, err := openSST([]byte("too short"))
	if err != ErrInvalidSSTFormat {
		t.Fatalf("expected ErrInvalidSSTFormat, got %v", err)
	}
}

func TestSSTReader_Close(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_sst_close")

	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}

	w := newSSTWriter()
	w.Add([]byte("key1"), []byte("value1"))

	sstData, err := w.Finish()
	if err != nil {
		t.Fatalf("Finish failed: %v", err)
	}

	reader, err := openSST(sstData)
	if err != nil {
		t.Fatalf("openSST failed: %v", err)
	}

	if err := reader.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

func TestSSTWriter_MultipleBlocks(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_sst_multiblocks")

	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}

	w := newSSTWriter()
	for i := 0; i < 1000; i++ {
		key := []byte(string(rune('a'+i%26)) + string(rune('0'+i/26)))
		val := []byte(string(rune('0' + i%10)))
		w.Add(key, val)
	}

	sstData, err := w.Finish()
	if err != nil {
		t.Fatalf("Finish failed: %v", err)
	}

	if len(sstData) == 0 {
		t.Fatal("expected non-empty SST data")
	}
}

func TestSSTReader_decodePrefix(t *testing.T) {
	key1 := []byte("key1")
	key2 := []byte("key2")
	decodePrefix(key2, key1)
}

func TestSSTReader_Iterator_CloseTwice(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_sst_iter_close")

	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}

	w := newSSTWriter()
	w.Add([]byte("key1"), []byte("value1"))

	sstData, err := w.Finish()
	if err != nil {
		t.Fatalf("Finish failed: %v", err)
	}

	reader, err := openSST(sstData)
	if err != nil {
		t.Fatalf("openSST failed: %v", err)
	}

	iter := reader.Iterator()
	iter.Close()
	iter.Close()
}

func TestSSTReader_Err(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_sst_err")

	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}

	w := newSSTWriter()
	w.Add([]byte("key1"), []byte("value1"))

	sstData, err := w.Finish()
	if err != nil {
		t.Fatalf("Finish failed: %v", err)
	}

	reader, err := openSST(sstData)
	if err != nil {
		t.Fatalf("openSST failed: %v", err)
	}

	iter := reader.Iterator()
	if iter.Err() != nil {
		t.Fatalf("expected nil error, got %v", iter.Err())
	}
}
