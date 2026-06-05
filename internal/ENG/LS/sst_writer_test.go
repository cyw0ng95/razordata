package ls

import (
	"bytes"
	"testing"
)

func TestSSTWriterBasic(t *testing.T) {
	w := newSSTWriter()
	w.Add([]byte("key1"), []byte("value1"))
	w.Add([]byte("key2"), []byte("value2"))

	if w.keyCount != 2 {
		t.Errorf("expected keyCount=2, got %d", w.keyCount)
	}

	if !bytes.Equal(w.minKey, []byte("key1")) {
		t.Errorf("expected minKey=key1, got %s", w.minKey)
	}

	if !bytes.Equal(w.maxKey, []byte("key2")) {
		t.Errorf("expected maxKey=key2, got %s", w.maxKey)
	}
}

func TestSSTWriterFinish(t *testing.T) {
	w := newSSTWriter()
	w.Add([]byte("key1"), []byte("value1"))
	w.Add([]byte("key2"), []byte("value2"))

	data, err := w.Finish()
	if err != nil {
		t.Fatalf("Finish failed: %v", err)
	}

	if len(data) == 0 {
		t.Error("expected non-empty SST data")
	}
}

func TestSSTWriterEmpty(t *testing.T) {
	w := newSSTWriter()
	data, err := w.Finish()
	if err != nil {
		t.Fatalf("Finish failed: %v", err)
	}
	if data != nil {
		t.Error("expected nil for empty writer")
	}
}

func TestSSTWriterMultipleBlocks(t *testing.T) {
	w := newSSTWriter()

	for i := 0; i < 100; i++ {
		key := []byte{byte(i)}
		val := []byte{byte(i + 100)}
		w.Add(key, val)
	}

	data, err := w.Finish()
	if err != nil {
		t.Fatalf("Finish failed: %v", err)
	}

	if len(data) == 0 {
		t.Error("expected non-empty SST data")
	}

	if w.keyCount != 100 {
		t.Errorf("expected keyCount=100, got %d", w.keyCount)
	}
}

func TestSSTWriterNotFound(t *testing.T) {
	w := newSSTWriter()
	w.Add([]byte("key1"), []byte("value1"))

	data, err := w.Finish()
	if err != nil {
		t.Fatalf("Finish failed: %v", err)
	}

	if len(data) == 0 {
		t.Error("expected non-empty SST data")
	}
}

func TestSSTWriterReset(t *testing.T) {
	w := newSSTWriter()
	w.Add([]byte("key1"), []byte("value1"))

	w.Reset()

	if w.keyCount != 0 {
		t.Errorf("expected keyCount=0 after reset, got %d", w.keyCount)
	}
	if len(w.blocks) != 0 {
		t.Errorf("expected empty blocks after reset, got %d", len(w.blocks))
	}
}

func TestSSTWriterMinMaxKey(t *testing.T) {
	w := newSSTWriter()

	if w.minKey != nil || w.maxKey != nil {
		t.Error("expected nil initial min/max keys")
	}

	w.Add([]byte("middle"), []byte("value"))

	if !bytes.Equal(w.minKey, []byte("middle")) || !bytes.Equal(w.maxKey, []byte("middle")) {
		t.Error("expected minKey=maxKey=middle after single add")
	}

	w.Add([]byte("a"), []byte("value_a"))
	w.Add([]byte("z"), []byte("value_z"))

	if !bytes.Equal(w.minKey, []byte("a")) {
		t.Errorf("expected minKey=a, got %s", w.minKey)
	}
	if !bytes.Equal(w.maxKey, []byte("z")) {
		t.Errorf("expected maxKey=z, got %s", w.maxKey)
	}
}
