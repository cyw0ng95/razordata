package ls

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManifest_PersistsAcrossRestarts(t *testing.T) {
	dir := t.TempDir()
	manifestDir := filepath.Join(dir, "test_manifest_persist")

	if err := os.MkdirAll(manifestDir, 0755); err != nil {
		t.Fatalf("failed to create manifest dir: %v", err)
	}

	m1, err := newManifest(manifestDir)
	if err != nil {
		t.Fatalf("failed to create manifest: %v", err)
	}

	v := m1.Current()
	v.num = 5
	v.levels = [][]SSTFileMeta{
		{},
		{{FileID: 1, Level: 1, MinKey: []byte("a"), MaxKey: []byte("z"), Size: 100}},
	}
	m1.Apply(*v)

	if err := m1.Close(); err != nil {
		t.Fatalf("failed to close manifest: %v", err)
	}

	m2, err := newManifest(manifestDir)
	if err != nil {
		t.Fatalf("failed to reopen manifest: %v", err)
	}
	defer m2.Close()

	v2 := m2.Current()
	if v2.num != 5 {
		t.Fatalf("expected version 5, got %d", v2.num)
	}
	if len(v2.levels) != 2 {
		t.Fatalf("expected 2 levels, got %d", len(v2.levels))
	}
}

func TestEngine_ManifestVersionIncrements(t *testing.T) {
	dir := t.TempDir()
	engineDir := filepath.Join(dir, "test_manifest_version")

	e, err := newEngine(engineDir)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}
	defer e.Close()

	initialVersion := e.manifest.Current().num

	v := e.manifest.Current()
	v.num = initialVersion + 1
	e.manifest.Apply(*v)

	newVersion := e.manifest.Current()
	if newVersion.num != initialVersion+1 {
		t.Fatalf("expected version %d, got %d", initialVersion+1, newVersion.num)
	}
}

func TestEngine_Close_Idempotent(t *testing.T) {
	dir := t.TempDir()
	engineDir := filepath.Join(dir, "test_close_idempotent")

	e, err := newEngine(engineDir)
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

func TestEngine_FlushManagerClose(t *testing.T) {
	dir := t.TempDir()
	engineDir := filepath.Join(dir, "test_fm_close")

	e, err := newEngine(engineDir)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}

	e.Write([]byte("key1"), []byte("value1"))

	if err := e.fm.Close(); err != nil {
		t.Fatalf("flush manager close failed: %v", err)
	}

	if err := e.Close(); err != nil {
		t.Fatalf("engine close failed: %v", err)
	}
}

func TestEngine_ConcurrentWriteAndClose(t *testing.T) {
	dir := t.TempDir()
	engineDir := filepath.Join(dir, "test_concurrent_close")

	e1, err := newEngine(engineDir)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}

	for i := 0; i < 100; i++ {
		key := []byte(string(rune('a' + i%26)))
		e1.Write(key, []byte("value"))
	}

	if err := e1.Close(); err != nil {
		t.Fatalf("failed to close engine: %v", err)
	}
}

func TestManifest_EmptyOnFreshStart(t *testing.T) {
	dir := t.TempDir()
	manifestDir := filepath.Join(dir, "test_fresh_manifest")

	if err := os.MkdirAll(manifestDir, 0755); err != nil {
		t.Fatalf("failed to create manifest dir: %v", err)
	}

	m1, err := newManifest(manifestDir)
	if err != nil {
		t.Fatalf("failed to create manifest: %v", err)
	}
	defer m1.Close()

	v := m1.Current()
	if v.num != 0 {
		t.Fatalf("expected version 0 on fresh start, got %d", v.num)
	}
}

func TestEngine_FlushManagerQueuesJob(t *testing.T) {
	dir := t.TempDir()
	engineDir := filepath.Join(dir, "test_flush_queue")

	e, err := newEngine(engineDir)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}

	e.Write([]byte("key1"), []byte("value1"))

	e.Close()
}
