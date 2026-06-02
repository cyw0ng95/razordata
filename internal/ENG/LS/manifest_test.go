package ls

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestManifestNew(t *testing.T) {
	tmpDir := t.TempDir()

	m, err := newManifest(tmpDir)
	if err != nil {
		t.Fatalf("newManifest failed: %v", err)
	}

	v := m.Current()
	if v == nil {
		t.Error("expected current version to be non-nil")
	}
	if v.num != 0 {
		t.Errorf("expected version 0, got %d", v.num)
	}
	if len(v.levels) != 0 {
		t.Errorf("expected 0 levels, got %d", len(v.levels))
	}
}

func TestManifestApply(t *testing.T) {
	tmpDir := t.TempDir()

	m, err := newManifest(tmpDir)
	if err != nil {
		t.Fatalf("newManifest failed: %v", err)
	}

	v := Version{
		num:     1,
		levels:  [][]SSTFileMeta{{}},
		created: time.Now(),
	}

	if err := m.Apply(v); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	current := m.Current()
	if current.num != 1 {
		t.Errorf("expected version 1, got %d", current.num)
	}

	if err := m.Apply(Version{
		num:     2,
		levels:  [][]SSTFileMeta{{}, {}},
		created: time.Now(),
	}); err != nil {
		t.Fatalf("Apply v2 failed: %v", err)
	}

	current = m.Current()
	if current.num != 2 {
		t.Errorf("expected version 2, got %d", current.num)
	}
}

func TestManifestCheckpoint(t *testing.T) {
	tmpDir := t.TempDir()

	m, err := newManifest(tmpDir)
	if err != nil {
		t.Fatalf("newManifest failed: %v", err)
	}

	m.Apply(Version{
		num:     1,
		levels:  [][]SSTFileMeta{{SSTFileMeta{FileID: 1, Level: 0}}},
		created: time.Now(),
	})

	cp, err := m.Checkpoint()
	if err != nil {
		t.Fatalf("Checkpoint failed: %v", err)
	}

	if cp.VersionNum != 1 {
		t.Errorf("expected version 1, got %d", cp.VersionNum)
	}
	if len(cp.Files) != 1 {
		t.Errorf("expected 1 level, got %d", len(cp.Files))
	}
	if len(cp.Files[0]) != 1 {
		t.Errorf("expected 1 file, got %d", len(cp.Files[0]))
	}
}

func TestManifestPersistence(t *testing.T) {
	tmpDir := t.TempDir()

	m1, err := newManifest(tmpDir)
	if err != nil {
		t.Fatalf("newManifest failed: %v", err)
	}

	m1.Apply(Version{
		num:    1,
		levels: [][]SSTFileMeta{{SSTFileMeta{FileID: 1, Level: 0}}},
	})

	manifestPath := filepath.Join(tmpDir, "manifest")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}

	if len(data) == 0 {
		t.Error("expected manifest file to be non-empty")
	}

	m2, err := newManifest(tmpDir)
	if err != nil {
		t.Fatalf("newManifest (reload) failed: %v", err)
	}

	v := m2.Current()
	if v.num != 1 {
		t.Errorf("expected version 1 after reload, got %d", v.num)
	}
}

func TestManifestMultiLevel(t *testing.T) {
	tmpDir := t.TempDir()

	m, err := newManifest(tmpDir)
	if err != nil {
		t.Fatalf("newManifest failed: %v", err)
	}

	files := [][]SSTFileMeta{
		{SSTFileMeta{FileID: 1, Level: 0, MinKey: []byte("a"), MaxKey: []byte("c")}},
		{SSTFileMeta{FileID: 2, Level: 1, MinKey: []byte("d"), MaxKey: []byte("z")}},
		{SSTFileMeta{FileID: 3, Level: 2, MinKey: []byte("0"), MaxKey: []byte("9")}},
	}

	if err := m.Apply(Version{
		num:    1,
		levels: files,
	}); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	v := m.Current()
	if len(v.levels) != 3 {
		t.Errorf("expected 3 levels, got %d", len(v.levels))
	}
	if len(v.levels[0]) != 1 {
		t.Errorf("expected 1 file in L0, got %d", len(v.levels[0]))
	}
	if len(v.levels[1]) != 1 {
		t.Errorf("expected 1 file in L1, got %d", len(v.levels[1]))
	}
	if len(v.levels[2]) != 1 {
		t.Errorf("expected 1 file in L2, got %d", len(v.levels[2]))
	}
}

func TestManifestEmptyDir(t *testing.T) {
	tmpDir := t.TempDir()

	m, err := newManifest(tmpDir)
	if err != nil {
		t.Fatalf("newManifest failed: %v", err)
	}

	if m.Current().num != 0 {
		t.Errorf("expected version 0, got %d", m.Current().num)
	}
}

func TestManifestClose(t *testing.T) {
	tmpDir := t.TempDir()

	m, err := newManifest(tmpDir)
	if err != nil {
		t.Fatalf("newManifest failed: %v", err)
	}

	if err := m.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}
}
