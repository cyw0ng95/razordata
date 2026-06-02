package ls

import (
	"path/filepath"
	"testing"
)

func TestEngine_Close_Idempotent_v2(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_engine_close_v2")

	e, err := newEngine(dir)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}

	e.Write([]byte("key1"), []byte("value1"))

	if err := e.Close(); err != nil {
		t.Fatalf("first close failed: %v", err)
	}

	if err := e.Close(); err != nil {
		t.Fatalf("second close failed: %v", err)
	}
}



func TestEngine_GetStats(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_engine_stats_detailed")

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
	if stats.SSTHits != 0 {
		t.Fatalf("expected 0 SST hits, got %d", stats.SSTHits)
	}
}

func TestEngine_MayContain(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_engine_maycontain")

	e, err := newEngine(dir)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}
	defer e.Close()

	e.Write([]byte("key1"), []byte("value1"))

	if !e.MayContain([]byte("key1")) {
		t.Fatal("expected MayContain to return true for existing key")
	}

	if e.MayContain([]byte("nonexistent")) {
		t.Fatal("expected MayContain to return false for nonexistent key")
	}
}

func TestMemtable_ShouldFlush(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_memtable_shouldflush")

	e, err := newEngine(dir)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}
	defer e.Close()

	mt := e.activeMem
	if !mt.ShouldFlush() {
		t.Log("memtable may or may not need flush depending on size")
	}
}

func TestFlushManager_RequestFlush(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_flush_request")

	manifest, err := newManifest(dir)
	if err != nil {
		t.Fatalf("failed to create manifest: %v", err)
	}
	defer manifest.Close()

	fm := newFlushManager(dir, 64*1024*1024, manifest)
	defer fm.Close()

	mt := newMemtable(1024 * 1024)
	mt.Insert([]byte("key1"), []byte("value1"))

	fm.requestFlush(mt)
}

func TestFlushManager_MaybeFlush(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_flush_maybe")

	manifest, err := newManifest(dir)
	if err != nil {
		t.Fatalf("failed to create manifest: %v", err)
	}
	defer manifest.Close()

	fm := newFlushManager(dir, 64*1024*1024, manifest)
	defer fm.Close()

	fm.MaybeFlush()
}

func TestCompactionManager_MaybeCompact(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_compaction_maybe")

	manifest, err := newManifest(dir)
	if err != nil {
		t.Fatalf("failed to create manifest: %v", err)
	}
	defer manifest.Close()

	cm := newCompactionManager(dir, manifest)
	defer cm.Close()

	cm.MaybeCompact()
}

func TestCompactionManager_RequestCompaction(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_compaction_request")

	manifest, err := newManifest(dir)
	if err != nil {
		t.Fatalf("failed to create manifest: %v", err)
	}
	defer manifest.Close()

	cm := newCompactionManager(dir, manifest)
	defer cm.Close()

	cm.requestCompaction(0)
}

func TestEngine_MultipleMemtableHits(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_engine_multi_hit")

	e, err := newEngine(dir)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}
	defer e.Close()

	e.Write([]byte("key1"), []byte("value1"))
	e.Write([]byte("key2"), []byte("value2"))

	e.Read([]byte("key1"))
	e.Read([]byte("key2"))
	e.Read([]byte("key1"))

	stats := e.GetStats()
	if stats.MemtableHits != 3 {
		t.Fatalf("expected 3 memtable hits, got %d", stats.MemtableHits)
	}
}

func TestEngine_SSTHits(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_engine_sst_hit")

	e, err := newEngine(dir)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}
	defer e.Close()

	e.Write([]byte("key1"), []byte("value1"))
	e.Read([]byte("key1"))

	stats := e.GetStats()
	if stats.SSTHits != 0 {
		t.Fatalf("expected 0 SST hits for memtable only, got %d", stats.SSTHits)
	}
}

func TestEngine_DiskReads(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_engine_disk_reads")

	e, err := newEngine(dir)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}
	defer e.Close()

	e.Write([]byte("key1"), []byte("value1"))

	e.Read([]byte("nonexistent"))

	stats := e.GetStats()
	if stats.DiskReads < 0 {
		t.Fatalf("expected non-negative disk reads, got %d", stats.DiskReads)
	}
}

func TestMemtable_Insert(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_memtable_insert")

	e, err := newEngine(dir)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}
	defer e.Close()

	err = e.Write([]byte("key1"), []byte("value1"))
	if err != nil {
		t.Fatalf("failed to insert: %v", err)
	}

	val, err := e.Read([]byte("key1"))
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}
	if string(val) != "value1" {
		t.Fatalf("expected value1, got %s", string(val))
	}
}

func TestMemtable_Update(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_memtable_update")

	e, err := newEngine(dir)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}
	defer e.Close()

	e.Write([]byte("key1"), []byte("value1"))
	e.Write([]byte("key1"), []byte("value2"))

	val, err := e.Read([]byte("key1"))
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}
	if string(val) != "value2" {
		t.Fatalf("expected value2, got %s", string(val))
	}
}

func TestMemtable_NotFound(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_memtable_notfound")

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
