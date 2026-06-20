package ls

import (
	"errors"
	"os"
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
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// REQ000629: Sync returns lastErr when a previous flush error was recorded.
func TestEngineSyncWithFlushError(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_sync_flush_error")

	e, err := newEngine(dir)
	if err != nil {
		t.Fatalf("newEngine: %v", err)
	}
	defer e.Close()

	if err := e.Write([]byte("key1"), []byte("value1")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	flushErr := errors.New("previous flush failure")
	e.fm.lastErr.Store(&flushErr)

	err = e.Sync()
	if err == nil {
		t.Fatal("expected Sync to return error from previous flush failure")
	}
	if err.Error() != "previous flush failure" {
		t.Fatalf("expected 'previous flush failure', got %v", err)
	}
}

// REQ000629: Close returns the error when a sub-component fails, but still
// closes the components that precede the failing one.
func TestEngineCloseSubComponentFailure(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_close_subcomponent")

	e, err := newEngine(dir)
	if err != nil {
		t.Fatalf("newEngine: %v", err)
	}

	mockErr := errors.New("flush manager error")
	e.fm.lastErr.Store(&mockErr)

	// engine.Close() should close cm first, then fail on fm
	err = e.Close()
	if err == nil {
		t.Fatal("expected Close to return error")
	}
	if err.Error() != "flush manager error" {
		t.Fatalf("expected 'flush manager error', got %v", err)
	}

	if !e.closed.Load() {
		t.Error("expected closed flag to be set")
	}

	// Compaction manager was closed before the failure
	select {
	case <-e.cm.done:
	default:
		t.Error("compaction manager was not closed before failure")
	}

	// Manifest was NOT closed (it comes after fm in Close order)
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Error("manifest was already closed")
			}
		}()
		e.manifest.Close()
	}()

	// Second Close returns nil (already closed)
	if err := e.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// REQ000629: Read from SST continues gracefully when an SST file is corrupt.
func TestEngineReadFromSSTWithCorruptFile(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_sst_corrupt")

	e, err := newEngine(dir)
	if err != nil {
		t.Fatalf("newEngine: %v", err)
	}
	defer e.Close()

	fileMeta := SSTFileMeta{
		FileID:    1,
		Level:     0,
		MinKey:    []byte("a"),
		MaxKey:    []byte("z"),
		Size:      100,
		BloomBits: 10,
	}

	sstPath := filepath.Join(dir, fileName(&fileMeta))
	if err := os.MkdirAll(filepath.Dir(sstPath), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(sstPath, []byte("corrupt sst data"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	v := e.manifest.Current()
	v.levels = [][]SSTFileMeta{{fileMeta}}
	e.manifest.Apply(*v)

	_, err = e.Read([]byte("key1"))
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// REQ000629: Read from SST continues gracefully when an SST file is missing.
func TestEngineReadFromSSTWithMissingFile(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_sst_missing")

	e, err := newEngine(dir)
	if err != nil {
		t.Fatalf("newEngine: %v", err)
	}
	defer e.Close()

	fileMeta := SSTFileMeta{
		FileID:    999,
		Level:     0,
		MinKey:    []byte("a"),
		MaxKey:    []byte("z"),
		Size:      100,
		BloomBits: 10,
	}

	v := e.manifest.Current()
	v.levels = [][]SSTFileMeta{{fileMeta}}
	e.manifest.Apply(*v)

	_, err = e.Read([]byte("key1"))
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
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

	stats := e.Stats()
	if stats.MemtableHits != 1 {
		t.Fatalf("expected 1 memtable hit, got %d", stats.MemtableHits)
	}
}
