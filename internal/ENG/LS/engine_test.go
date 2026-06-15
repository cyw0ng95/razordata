package ls

import (
	"path/filepath"
	"testing"
)

func TestEngineWriteRead(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_engine")

	e, err := newEngine(dir)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}
	defer e.Close()

	if err := e.Write([]byte("key1"), []byte("value1")); err != nil {
		t.Fatalf("failed to write: %v", err)
	}

	val, err := e.Read([]byte("key1"))
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}

	if string(val) != "value1" {
		t.Fatalf("expected value1, got %s", string(val))
	}
}

func TestEngineReadNotFound(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_engine")

	e, err := newEngine(dir)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}
	defer e.Close()

	_, err = e.Read([]byte("nonexistent"))
	if err != ErrKeyNotFound {
		t.Fatalf("expected ErrKeyNotFound, got %v", err)
	}
}

func TestEngineMultipleWrites(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_engine")

	e, err := newEngine(dir)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}
	defer e.Close()

	keys := []string{"a", "b", "c", "d", "e"}
	values := []string{"1", "2", "3", "4", "5"}

	for i := range keys {
		if err := e.Write([]byte(keys[i]), []byte(values[i])); err != nil {
			t.Fatalf("failed to write %s: %v", keys[i], err)
		}
	}

	for i := range keys {
		val, err := e.Read([]byte(keys[i]))
		if err != nil {
			t.Fatalf("failed to read %s: %v", keys[i], err)
		}
		if string(val) != values[i] {
			t.Fatalf("expected %s, got %s", values[i], string(val))
		}
	}
}

func TestEngineMayContain(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_engine")

	e, err := newEngine(dir)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}
	defer e.Close()

	e.Write([]byte("key1"), []byte("value1"))

	if !e.MayContain([]byte("key1")) {
		t.Fatal("expected MayContain to return true for key1")
	}

	if e.MayContain([]byte("key2")) {
		t.Fatal("expected MayContain to return false for key2")
	}
}

func TestEngineGetStats(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_engine")

	e, err := newEngine(dir)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}
	defer e.Close()

	e.Write([]byte("key1"), []byte("value1"))
	e.Read([]byte("key1"))
	e.Read([]byte("nonexistent"))

	stats := e.GetStats()
	if stats.MemtableHits != 1 {
		t.Fatalf("expected 1 memtable hit, got %d", stats.MemtableHits)
	}
}
