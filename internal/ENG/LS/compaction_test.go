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

	cm := newCompactionManager(DefaultFS(), dir, manifest, nil)
	defer cm.Close()

	// MaybeCompact kicks off a background compaction when budget is
	// exceeded. Close immediately to drain the goroutine; the test
	// only asserts the path runs without panicking.
	cm.MaybeCompact()
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

	sstPath := filepath.Join(dir, "sst", "L0_61_7a_1.sst")
	if err := os.WriteFile(sstPath, sstData, 0644); err != nil {
		t.Fatalf("failed to write SST file: %v", err)
	}

	job := &compactionJob{
		fs:              DefaultFS(),
		placementPolicy: nil,
		level:           0,
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
		fs:              DefaultFS(),
		placementPolicy: nil,
		level:           0,
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

	sstPath := filepath.Join(dir, "sst", "L0_61_7a_1.sst")
	if err := os.WriteFile(sstPath, []byte("invalid sst data"), 0644); err != nil {
		t.Fatalf("failed to write SST file: %v", err)
	}

	job := &compactionJob{
		fs:              DefaultFS(),
		placementPolicy: nil,
		level:           0,
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

	sstPath := filepath.Join(dir, "sst", "L0_61_7a_1.sst")
	if err := os.WriteFile(sstPath, sstData, 0644); err != nil {
		t.Fatalf("failed to write SST file: %v", err)
	}

	job := &compactionJob{
		fs:              DefaultFS(),
		placementPolicy: nil,
		level:           0,
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

	cm := newCompactionManager(DefaultFS(), dir, manifest, nil)
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

	cm := newCompactionManager(DefaultFS(), dir, manifest, nil)
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

	cm := newCompactionManager(DefaultFS(), dir, manifest, nil)
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

func TestCompactionManager_ManualCompact(t *testing.T) {
	dir := t.TempDir()
	manifest, err := newManifest(dir)
	if err != nil {
		t.Fatalf("newManifest failed: %v", err)
	}
	cm := newCompactionManager(DefaultFS(), dir, manifest, nil)
	defer cm.Close()

	// ManualCompact should not panic when no files exist
	err = cm.ManualCompact()
	if err != nil && err != ErrNoFilesToCompact {
		t.Fatalf("ManualCompact failed: %v", err)
	}
}

func TestCompactionManager_ManualCompact_Concurrent(t *testing.T) {
	dir := t.TempDir()
	manifest, err := newManifest(dir)
	if err != nil {
		t.Fatalf("newManifest failed: %v", err)
	}
	cm := newCompactionManager(DefaultFS(), dir, manifest, nil)
	defer cm.Close()

	// First call may start compaction
	_ = cm.ManualCompact()

	// Second call should return ErrCompactionInProgress (or succeed if first was no-op)
	err = cm.ManualCompact()
	if err != nil && err != ErrCompactionInProgress && err != ErrNoFilesToCompact {
		t.Fatalf("ManualCompact concurrent failed: %v", err)
	}
}

// TestCompactionJob_RunRemovesOverlapFiles verifies REQ000601: when a
// compaction job merges input + overlap into a single output file,
// the overlap files must be removed from level+1 in the new manifest
// version. Without this, the overlap data exists twice (in the old
// overlap file and in the new output), and every subsequent
// compaction re-merges the same data — unbounded disk growth.
func TestCompactionJob_RunRemovesOverlapFiles(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_compaction_overlap")
	if err := os.MkdirAll(filepath.Join(dir, "sst"), 0755); err != nil {
		t.Fatalf("failed to create sst dir: %v", err)
	}

	mfst, err := newManifest(dir)
	if err != nil {
		t.Fatalf("newManifest: %v", err)
	}
	defer mfst.Close()

	v := mfst.Current()
	v.levels = make([][]SSTFileMeta, 3)
	mfst.Apply(*v)

	// Build two SSTs: input (L0) and overlap (L1) with overlapping
	// key ranges so the merge actually consumes the overlap data.
	w := newSSTWriter()
	w.Add([]byte("key1"), []byte("value1"))
	w.Add([]byte("key5"), []byte("value5"))
	inputData, err := w.Finish()
	if err != nil {
		t.Fatalf("input Finish: %v", err)
	}
	w = newSSTWriter()
	w.Add([]byte("key3"), []byte("value3"))
	w.Add([]byte("key7"), []byte("value7"))
	overlapData, err := w.Finish()
	if err != nil {
		t.Fatalf("overlap Finish: %v", err)
	}

	// Use high FileIDs (9001+) to avoid colliding with nextFileID()
	// which is a package-level atomic counter that other tests in
	// the same package may have already advanced.
	input := SSTFileMeta{FileID: 9001, Level: 0, MinKey: []byte("key1"), MaxKey: []byte("key5"), Size: int64(len(inputData)), BloomBits: 10}
	overlap := SSTFileMeta{FileID: 9002, Level: 1, MinKey: []byte("key3"), MaxKey: []byte("key7"), Size: int64(len(overlapData)), BloomBits: 10}
	inputPath := filepath.Join(dir, fileName(&input))
	overlapPath := filepath.Join(dir, fileName(&overlap))
	if err := os.WriteFile(inputPath, inputData, 0644); err != nil {
		t.Fatalf("write input: %v", err)
	}
	if err := os.WriteFile(overlapPath, overlapData, 0644); err != nil {
		t.Fatalf("write overlap: %v", err)
	}

	job := &compactionJob{
		fs:              DefaultFS(),
		placementPolicy: nil,
		level:           0,
		inputs:          []SSTFileMeta{input},
		outputs:         nil,
		overlap:         []SSTFileMeta{overlap},
	}

	if err := job.Run(mfst, dir); err != nil {
		t.Fatalf("Run: %v", err)
	}

	newV := mfst.Current()
	// L0 must be empty (input removed).
	if got := len(newV.levels[0]); got != 0 {
		t.Errorf("expected L0 empty after compaction, got %d files", got)
	}
	// L1 must contain only the new merged output, NOT the old
	// overlap file. The overlap file is identified by its
	// pre-assigned FileID (9002); this is stable across runs.
	if got := len(newV.levels[1]); got != 1 {
		t.Errorf("expected L1 to contain 1 file (merged output), got %d", got)
	}
	for _, f := range newV.levels[1] {
		if f.FileID == overlap.FileID {
			t.Errorf("REQ000601: overlap file %d still present at L1 after compaction; should have been removed", f.FileID)
		}
	}
	// The overlap file's on-disk bytes should be gone too.
	if _, err := os.Stat(overlapPath); !os.IsNotExist(err) {
		t.Errorf("REQ000601: overlap file %s still on disk after compaction", overlapPath)
	}
}

// REQ001304: auto_compact=none does not trigger garbage-ratio compaction.
// Traditional debt-based compaction may still fire.
func TestAutoCompact_Disabled_NoOp(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_auto_compact_disabled")

	if err := os.MkdirAll(filepath.Join(dir, "sst"), 0755); err != nil {
		t.Fatalf("create sst dir: %v", err)
	}

	manifest, err := newManifest(dir)
	if err != nil {
		t.Fatalf("new manifest: %v", err)
	}
	defer manifest.Close()

	// Build a version with L0 files well under budget.
	v := manifest.Current()
	v.levels = make([][]SSTFileMeta, 3)
	v.levels[0] = []SSTFileMeta{
		{FileID: 1, Level: 0, MinKey: []byte("a"), MaxKey: []byte("z"), Size: 1024}, // 1KB
	}
	manifest.Apply(*v)

	cm := newCompactionManager(DefaultFS(), dir, manifest, nil)
	cm.SetAutoCompact("none", 0.3)
	defer cm.Close()

	time.Sleep(50 * time.Millisecond)

	before := len(cm.compactionQueue)
	cm.MaybeCompact()
	after := len(cm.compactionQueue)
	if before != after {
		t.Errorf("auto_compact=none with under-budget data should not enqueue compaction: queue went from %d to %d", before, after)
	}
}

// REQ001304: auto_compact=incremental triggers compaction when budget exceeded.
func TestAutoCompact_Incremental_Triggered(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_auto_compact_incremental")

	if err := os.MkdirAll(filepath.Join(dir, "sst"), 0755); err != nil {
		t.Fatalf("create sst dir: %v", err)
	}

	manifest, err := newManifest(dir)
	if err != nil {
		t.Fatalf("new manifest: %v", err)
	}
	defer manifest.Close()

	// Build a version with L0 files far exceeding budget (4MB default).
	v := manifest.Current()
	v.levels = make([][]SSTFileMeta, 3)
	v.levels[0] = []SSTFileMeta{
		{FileID: 1, Level: 0, MinKey: []byte("a"), MaxKey: []byte("z"), Size: 10 * 1024 * 1024},
	}
	manifest.Apply(*v)

	cm := newCompactionManager(DefaultFS(), dir, manifest, nil)
	defer cm.Close()

	cm.SetAutoCompact("incremental", 0.01)

	// MaybeCompact should queue a compaction job.
	cm.MaybeCompact()
	// The compaction loop drains the channel asynchronously; check that
	// a job was enqueued by observing the queue length before the loop
	// drains it.
	if len(cm.compactionQueue) == 0 {
		t.Error("auto_compact=incremental should enqueue a compaction job when budget exceeded")
	}
}

// REQ001304: auto_compact=full triggers compaction on all levels.
func TestAutoCompact_Full_Triggered(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_auto_compact_full")

	if err := os.MkdirAll(filepath.Join(dir, "sst"), 0755); err != nil {
		t.Fatalf("create sst dir: %v", err)
	}

	manifest, err := newManifest(dir)
	if err != nil {
		t.Fatalf("new manifest: %v", err)
	}
	defer manifest.Close()

	// Build a version with files at multiple levels.
	v := manifest.Current()
	v.levels = make([][]SSTFileMeta, 3)
	v.levels[0] = []SSTFileMeta{
		{FileID: 1, Level: 0, MinKey: []byte("a"), MaxKey: []byte("z"), Size: 10 * 1024 * 1024},
	}
	v.levels[1] = []SSTFileMeta{
		{FileID: 2, Level: 1, MinKey: []byte("a"), MaxKey: []byte("z"), Size: 8 * 1024 * 1024},
	}
	manifest.Apply(*v)

	cm := newCompactionManager(DefaultFS(), dir, manifest, nil)
	defer cm.Close()

	cm.SetAutoCompact("full", 0.01)

	cm.MaybeCompact()
	if len(cm.compactionQueue) == 0 {
		t.Error("auto_compact=full should enqueue compaction jobs when budget exceeded")
	}
}

// REQ001304: auto_compact does not trigger when under budget.
func TestAutoCompact_UnderBudget_NoTrigger(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_auto_compact_under_budget")

	if err := os.MkdirAll(filepath.Join(dir, "sst"), 0755); err != nil {
		t.Fatalf("create sst dir: %v", err)
	}

	manifest, err := newManifest(dir)
	if err != nil {
		t.Fatalf("new manifest: %v", err)
	}
	defer manifest.Close()

	// Build a version with files well under budget.
	v := manifest.Current()
	v.levels = make([][]SSTFileMeta, 3)
	v.levels[0] = []SSTFileMeta{
		{FileID: 1, Level: 0, MinKey: []byte("a"), MaxKey: []byte("z"), Size: 1024}, // 1KB
	}
	manifest.Apply(*v)

	cm := newCompactionManager(DefaultFS(), dir, manifest, nil)
	defer cm.Close()

	cm.SetAutoCompact("incremental", 0.3)

	before := cm.compacting.Load()
	cm.MaybeCompact()
	after := cm.compacting.Load()
	if before || after {
		t.Error("auto_compact should not trigger when under budget")
	}
}

// REQ001304: SetAutoCompact/GetAutoCompact round-trip.
func TestAutoCompact_GetSet(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_auto_compact_getset")

	if err := os.MkdirAll(filepath.Join(dir, "sst"), 0755); err != nil {
		t.Fatalf("create sst dir: %v", err)
	}

	manifest, err := newManifest(dir)
	if err != nil {
		t.Fatalf("new manifest: %v", err)
	}
	defer manifest.Close()

	cm := newCompactionManager(DefaultFS(), dir, manifest, nil)
	defer cm.Close()

	mode, threshold := cm.GetAutoCompact()
	if mode != "" {
		t.Errorf("initial mode: got %q, want %q", mode, "")
	}
	if threshold != 0 {
		t.Errorf("initial threshold: got %f, want 0", threshold)
	}

	cm.SetAutoCompact("incremental", 0.5)
	mode, threshold = cm.GetAutoCompact()
	if mode != "incremental" {
		t.Errorf("after set: mode = %q, want %q", mode, "incremental")
	}
	if threshold != 0.5 {
		t.Errorf("after set: threshold = %f, want 0.5", threshold)
	}
}
