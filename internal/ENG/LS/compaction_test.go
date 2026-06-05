package ls

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCompactionManager_BudgetExceeded(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_compaction_budget")

	manifest, err := newManifest(dir)
	if err != nil {
		t.Fatalf("failed to create manifest: %v", err)
	}
	defer manifest.Close()

	cm := newCompactionManager(dir, manifest)
	defer cm.Close()

	v := manifest.Current()

	v.levels = append(v.levels, []SSTFileMeta{
		{FileID: 1, Level: 0, MinKey: []byte("a"), MaxKey: []byte("z"), Size: 10 * 1024 * 1024},
	})

	manifest.Apply(*v)

	cm.MaybeCompact()

	time.Sleep(100 * time.Millisecond)
}

func TestCompactionJob_Run(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_compaction_job")

	if err := os.MkdirAll(filepath.Join(dir, "sst"), 0755); err != nil {
		t.Fatalf("failed to create sst dir: %v", err)
	}

	manifest, err := newManifest(dir)
	if err != nil {
		t.Fatalf("failed to create manifest: %v", err)
	}
	defer manifest.Close()

	v := manifest.Current()
	v.levels = make([][]SSTFileMeta, 3)
	manifest.Apply(*v)

	w := newSSTWriter()
	w.Add([]byte("key1"), []byte("value1"))
	w.Add([]byte("key2"), []byte("value2"))
	sstData, err := w.Finish()
	if err != nil {
		t.Fatalf("failed to finish SST writer: %v", err)
	}

	sstPath := filepath.Join(dir, "sst", "L0_a_z_1.sst")
	if err := os.WriteFile(sstPath, sstData, 0644); err != nil {
		t.Fatalf("failed to write SST file: %v", err)
	}

	job := &compactionJob{
		level: 0,
		inputs: []SSTFileMeta{
			{FileID: 1, Level: 0, MinKey: []byte("a"), MaxKey: []byte("z"), Size: int64(len(sstData)), BloomBits: 10},
		},
		outputs: nil,
		overlap: nil,
	}

	if err := job.Run(manifest, dir); err != nil {
		t.Fatalf("compaction job failed: %v", err)
	}

	newVersion := manifest.Current()
	if len(newVersion.levels[1]) != 1 {
		t.Fatalf("expected 1 file in L1 after compaction, got %d", len(newVersion.levels[1]))
	}
}

func TestCompactionJob_RunReadFileError(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_compaction_read_error")

	if err := os.MkdirAll(filepath.Join(dir, "sst"), 0755); err != nil {
		t.Fatalf("failed to create sst dir: %v", err)
	}

	manifest, err := newManifest(dir)
	if err != nil {
		t.Fatalf("failed to create manifest: %v", err)
	}
	defer manifest.Close()

	v := manifest.Current()
	v.levels = make([][]SSTFileMeta, 3)
	manifest.Apply(*v)

	job := &compactionJob{
		level: 0,
		inputs: []SSTFileMeta{
			{FileID: 999, Level: 0, MinKey: []byte("a"), MaxKey: []byte("z"), Size: 100, BloomBits: 10},
		},
		outputs: nil,
		overlap: nil,
	}

	err = job.Run(manifest, dir)
	if err == nil {
		t.Fatalf("expected error for non-existent SST file")
	}
}

func TestCompactionJob_RunOpenSSTError(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_compaction_open_error")

	if err := os.MkdirAll(filepath.Join(dir, "sst"), 0755); err != nil {
		t.Fatalf("failed to create sst dir: %v", err)
	}

	manifest, err := newManifest(dir)
	if err != nil {
		t.Fatalf("failed to create manifest: %v", err)
	}
	defer manifest.Close()

	v := manifest.Current()
	v.levels = make([][]SSTFileMeta, 3)
	manifest.Apply(*v)

	sstPath := filepath.Join(dir, "sst", "L0_a_z_1.sst")
	if err := os.WriteFile(sstPath, []byte("invalid sst data"), 0644); err != nil {
		t.Fatalf("failed to write SST file: %v", err)
	}

	job := &compactionJob{
		level: 0,
		inputs: []SSTFileMeta{
			{FileID: 1, Level: 0, MinKey: []byte("a"), MaxKey: []byte("z"), Size: 100, BloomBits: 10},
		},
		outputs: nil,
		overlap: nil,
	}

	err = job.Run(manifest, dir)
	if err == nil {
		t.Fatalf("expected error for invalid SST file")
	}
}

func TestCompactionJob_RunWriteError(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_compaction_write_error")

	if err := os.MkdirAll(filepath.Join(dir, "sst"), 0755); err != nil {
		t.Fatalf("failed to create sst dir: %v", err)
	}

	manifest, err := newManifest(dir)
	if err != nil {
		t.Fatalf("failed to create manifest: %v", err)
	}
	defer manifest.Close()

	v := manifest.Current()
	v.levels = make([][]SSTFileMeta, 3)
	manifest.Apply(*v)

	w := newSSTWriter()
	w.Add([]byte("key1"), []byte("value1"))
	sstData, err := w.Finish()
	if err != nil {
		t.Fatalf("failed to finish SST writer: %v", err)
	}

	sstPath := filepath.Join(dir, "sst", "L0_a_z_1.sst")
	if err := os.WriteFile(sstPath, sstData, 0644); err != nil {
		t.Fatalf("failed to write SST file: %v", err)
	}

	job := &compactionJob{
		level: 0,
		inputs: []SSTFileMeta{
			{FileID: 1, Level: 0, MinKey: []byte("a"), MaxKey: []byte("z"), Size: int64(len(sstData)), BloomBits: 10},
		},
		outputs: nil,
		overlap: nil,
	}

	// Use a read-only directory for the compaction output
	readOnlyDir := filepath.Join(dir, "readonly")
	if err := os.MkdirAll(readOnlyDir, 0555); err != nil {
		t.Fatalf("failed to create read-only dir: %v", err)
	}

	err = job.Run(manifest, readOnlyDir)
	if err == nil {
		t.Fatalf("expected error for read-only output dir")
	}
}

func TestCompactionManager_NoOpWhenNoFiles(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_compaction_noop")

	manifest, err := newManifest(dir)
	if err != nil {
		t.Fatalf("failed to create manifest: %v", err)
	}
	defer manifest.Close()

	cm := newCompactionManager(dir, manifest)
	defer cm.Close()

	cm.MaybeCompact()
}

func TestCompactionManager_Close(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_compaction_close")

	manifest, err := newManifest(dir)
	if err != nil {
		t.Fatalf("failed to create manifest: %v", err)
	}
	defer manifest.Close()

	cm := newCompactionManager(dir, manifest)
	if err := cm.Close(); err != nil {
		t.Fatalf("failed to close compaction manager: %v", err)
	}
}

func TestCompactionManager_RequestCompaction_EmptyLevel(t *testing.T) {
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

func TestVersion_LevelsInitialized(t *testing.T) {
	v := &Version{
		num:     0,
		levels:  make([][]SSTFileMeta, 0),
		created: time.Now(),
	}

	if len(v.levels) != 0 {
		t.Fatalf("expected 0 levels, got %d", len(v.levels))
	}

	v.levels = append(v.levels, []SSTFileMeta{})
	v.levels = append(v.levels, []SSTFileMeta{})

	if len(v.levels) != 2 {
		t.Fatalf("expected 2 levels, got %d", len(v.levels))
	}
}

func TestLevelBudget_BudgetFor(t *testing.T) {
	budget := defaultBudget

	tests := []struct {
		level    int
		expected int64
	}{
		{0, 4 * 1024 * 1024},
		{1, 32 * 1024 * 1024},
		{2, 320 * 1024 * 1024},
		{3, 320 * 1024 * 1024 * 2},
		{10, 320 * 1024 * 1024 * 9},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			got := budget.budgetFor(tt.level)
			if got != tt.expected {
				t.Errorf("budgetFor(%d) = %d, want %d", tt.level, got, tt.expected)
			}
		})
	}
}

func TestRemoveFiles_Compaction(t *testing.T) {
	files := []SSTFileMeta{
		{FileID: 1},
		{FileID: 2},
		{FileID: 3},
	}
	toRemove := []SSTFileMeta{
		{FileID: 2},
	}

	result := removeFiles(files, toRemove)
	if len(result) != 2 {
		t.Errorf("len(result) = %d, want 2", len(result))
	}
}

func TestRemoveFiles_All(t *testing.T) {
	files := []SSTFileMeta{
		{FileID: 1},
		{FileID: 2},
	}
	toRemove := []SSTFileMeta{
		{FileID: 1},
		{FileID: 2},
	}

	result := removeFiles(files, toRemove)
	if len(result) != 0 {
		t.Errorf("len(result) = %d, want 0", len(result))
	}
}

func TestCloseIterators_Nil(t *testing.T) {
	closeIterators(nil)
}

func TestCloseIterators_Empty(t *testing.T) {
	closeIterators([]*sstIterator{})
}
