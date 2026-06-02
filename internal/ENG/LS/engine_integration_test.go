package ls

import (
	"path/filepath"
	"testing"
)

func TestEngineWriteAndReadBack(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_engine_write_read")

	e, err := newEngine(dir)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}
	defer e.Close()

	keys := []string{"key1", "key2", "key3", "key4", "key5"}
	values := []string{"val1", "val2", "val3", "val4", "val5"}

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

func TestEngineReadNotFoundInMemtable(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_engine_notfound_memtable")

	e, err := newEngine(dir)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}
	defer e.Close()

	e.Write([]byte("key1"), []byte("value1"))

	_, err = e.Read([]byte("nonexistent"))
	if err != ErrKeyNotFound {
		t.Fatalf("expected ErrKeyNotFound, got %v", err)
	}
}

func TestEngineReadMultipleKeys(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_engine_multi_key")

	e, err := newEngine(dir)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}
	defer e.Close()

	keys := []string{"aaa", "bbb", "ccc", "ddd", "eee"}
	values := []string{"111", "222", "333", "444", "555"}

	for i := range keys {
		e.Write([]byte(keys[i]), []byte(values[i]))
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

func TestEngineStats(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_engine_stats")

	e, err := newEngine(dir)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}
	defer e.Close()

	e.Write([]byte("key1"), []byte("value1"))
	e.Write([]byte("key2"), []byte("value2"))

	e.Read([]byte("key1"))
	e.Read([]byte("key1"))
	e.Read([]byte("nonexistent"))

	stats := e.GetStats()
	if stats.MemtableHits != 2 {
		t.Fatalf("expected 2 memtable hits, got %d", stats.MemtableHits)
	}
}

func TestEngineMayContainFromEngine(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_engine_maycontain")

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

func TestEngineEmpty(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_engine_empty")

	e, err := newEngine(dir)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}
	defer e.Close()

	_, err = e.Read([]byte("anykey"))
	if err != ErrKeyNotFound {
		t.Fatalf("expected ErrKeyNotFound for empty engine, got %v", err)
	}
}

func TestEngineUpdate(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_engine_update")

	e, err := newEngine(dir)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}
	defer e.Close()

	e.Write([]byte("key1"), []byte("value1"))
	val, _ := e.Read([]byte("key1"))
	if string(val) != "value1" {
		t.Fatalf("expected value1, got %s", string(val))
	}

	e.Write([]byte("key1"), []byte("value2"))
	val, _ = e.Read([]byte("key1"))
	if string(val) != "value2" {
		t.Fatalf("expected value2 after update, got %s", string(val))
	}
}

func TestEngineMultipleWritesSameKey(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_engine_multi_write")

	e, err := newEngine(dir)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}
	defer e.Close()

	for i := 0; i < 100; i++ {
		if err := e.Write([]byte("key1"), []byte(string(rune('0'+i%10)))); err != nil {
			t.Fatalf("failed to write iteration %d: %v", i, err)
		}
	}

	val, _ := e.Read([]byte("key1"))
	if len(val) != 1 {
		t.Fatalf("expected single byte value, got %d bytes", len(val))
	}
}
