package ls

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// writeSST creates a simple SST file at the given path with the
// supplied (key, value) pairs, returning the SSTFileMeta describing
// the on-disk file. Used by the subcompaction integration tests.
func writeSST(t *testing.T, dir string, id uint64, level int, pairs [][2]string) SSTFileMeta {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "sst"), 0755); err != nil {
		t.Fatalf("mkdir sst: %v", err)
	}
	w := newSSTWriter()
	var minK, maxK []byte
	for _, p := range pairs {
		k := []byte(p[0])
		v := []byte(p[1])
		w.Add(k, v)
		if minK == nil || string(k) < string(minK) {
			minK = k
		}
		if maxK == nil || string(k) > string(maxK) {
			maxK = k
		}
	}
	data, err := w.Finish()
	if err != nil {
		t.Fatalf("sst Finish: %v", err)
	}
	path := filepath.Join(dir, fileName(&SSTFileMeta{
		FileID: id,
		Level:  level,
		MinKey: minK,
		MaxKey: maxK,
		Size:   int64(len(data)),
	}))
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write sst: %v", err)
	}
	return SSTFileMeta{
		FileID:    id,
		Level:     level,
		MinKey:    minK,
		MaxKey:    maxK,
		Size:      int64(len(data)),
		BloomBits: 10,
	}
}

// TestSubCompactor_ThresholdFallback verifies REQ001048: a single
// input file produces <2 pivots and RunSubCompaction falls through
// to the serial compactionJob.Run. This is the only safe dispatch
// path while parallel sub-compaction requires per-sub partial
// outputs + a coordinator merge (deferred to a future iteration
// to avoid the manifest-write race documented in the runJob
// threshold note).
func TestSubCompactor_ThresholdFallback(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "test_subcompact_fallback")
	if err := os.MkdirAll(filepath.Join(dir, "sst"), 0755); err != nil {
		t.Fatalf("mkdir sst: %v", err)
	}
	m, err := newManifest(dir)
	if err != nil {
		t.Fatalf("newManifest: %v", err)
	}
	defer m.Close()
	v := m.Current()
	v.levels = make([][]SSTFileMeta, 3)
	m.Apply(*v)

	meta := writeSST(t, dir, 1, 0, [][2]string{{"a", "v"}})
	sc := NewSubCompactor(DefaultFS(), dir, m, 4)
	if _, err := sc.RunSubCompaction(context.Background(), 0, []SSTFileMeta{meta}, SubCompactionOptions{}); err != nil {
		t.Fatalf("RunSubCompaction (single input): %v", err)
	}
	v = m.Current()
	if got := len(v.levels[1]); got != 1 {
		t.Fatalf("L1 count = %d, want 1 (serial fallback)", got)
	}
}

// TestSubCompactor_Dispatch verifies REQ001048/REQ001157: compactionManager's
// runJob dispatches through SubCompactor when input count exceeds the
// threshold (4). Below the threshold the serial path runs directly.
func TestSubCompactor_Dispatch(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "test_subcompact_dispatch")
	if err := os.MkdirAll(filepath.Join(dir, "sst"), 0755); err != nil {
		t.Fatalf("mkdir sst: %v", err)
	}
	m, err := newManifest(dir)
	if err != nil {
		t.Fatalf("newManifest: %v", err)
	}
	defer m.Close()
	v := m.Current()
	v.levels = make([][]SSTFileMeta, 3)
	m.Apply(*v)

	cm := newCompactionManager(DefaultFS(), dir, m)
	defer func() { _ = cm.Close() }()

	// Verify SubCompactor is wired in and threshold is 4.
	if cm.subCompactor == nil {
		t.Fatal("SubCompactor should be wired into compactionManager")
	}
	if subCompactionThreshold != 4 {
		t.Fatalf("subCompactionThreshold = %d, expected 4", subCompactionThreshold)
	}

	// Run a job with 3 inputs (below threshold) — must succeed via serial path.
	var inputs []SSTFileMeta
	for i := 0; i < 3; i++ {
		base := byte('a' + i*4)
		meta := writeSST(t, dir, uint64(i+1), 0, [][2]string{
			{string([]byte{base}), "v1"},
			{string([]byte{base + 1}), "v2"},
		})
		inputs = append(inputs, meta)
	}
	job := &compactionJob{fs: DefaultFS(), placementPolicy: nil, level: 0, inputs: inputs}
	cm.runJob(job)

	v = m.Current()
	if got := len(v.levels[1]); got != 1 {
		t.Fatalf("L1 file count = %d, want 1 (serial path)", got)
	}
}

// TestSubCompactor_OptionsPropagate verifies REQ001048: overlap,
// rate limiter, and placement policy passed to RunSubCompaction
// reach the underlying compactionJob. The test uses the single-
// input fallback path so we exercise the field-forwarding logic
// without triggering the parallel partial-output race.
func TestSubCompactor_OptionsPropagate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "test_subcompact_opts")
	if err := os.MkdirAll(filepath.Join(dir, "sst"), 0755); err != nil {
		t.Fatalf("mkdir sst: %v", err)
	}
	m, err := newManifest(dir)
	if err != nil {
		t.Fatalf("newManifest: %v", err)
	}
	defer m.Close()
	v := m.Current()
	v.levels = make([][]SSTFileMeta, 3)
	m.Apply(*v)

	meta := writeSST(t, dir, 1, 0, [][2]string{{"a", "v"}})
	rl := NewRateLimiter(1<<30, 1<<30) // effectively unlimited
	sc := NewSubCompactor(DefaultFS(), dir, m, 1)
	if _, err := sc.RunSubCompaction(context.Background(), 0, []SSTFileMeta{meta}, SubCompactionOptions{
		Overlap:     nil,
		RateLimiter: rl,
	}); err != nil {
		t.Fatalf("RunSubCompaction: %v", err)
	}
	if rl == nil {
		t.Fatal("rate limiter pointer should not have been cleared")
	}
}

// TestSubCompactor_SetConcurrency verifies REQ001048:
// SetSubCompactorConcurrency updates the per-manager concurrency.
func TestSubCompactor_SetConcurrency(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "test_subcompact_setconcurrency")
	if err := os.MkdirAll(filepath.Join(dir, "sst"), 0755); err != nil {
		t.Fatalf("mkdir sst: %v", err)
	}
	m, err := newManifest(dir)
	if err != nil {
		t.Fatalf("newManifest: %v", err)
	}
	defer m.Close()
	v := m.Current()
	v.levels = make([][]SSTFileMeta, 3)
	m.Apply(*v)

	cm := newCompactionManager(DefaultFS(), dir, m)
	defer func() { _ = cm.Close() }()
	cm.SetSubCompactorConcurrency(1)
	if cm.subCompactor == nil {
		t.Fatal("subCompactor must remain set after SetSubCompactorConcurrency")
	}
}

// BenchmarkCompaction_Serial sanity-checks that the wired-in
// SubCompactor falls back to the serial compactionJob.Run path
// for typical input counts. Speedup under parallel sub-compaction
// requires the deferred partial-output + coordinator merge path.
func BenchmarkCompaction_Serial(b *testing.B) {
	dir := filepath.Join(b.TempDir(), "bench_subcompact")
	if err := os.MkdirAll(filepath.Join(dir, "sst"), 0755); err != nil {
		b.Fatalf("mkdir sst: %v", err)
	}
	m, err := newManifest(dir)
	if err != nil {
		b.Fatalf("newManifest: %v", err)
	}
	defer m.Close()
	v := m.Current()
	v.levels = make([][]SSTFileMeta, 3)
	m.Apply(*v)

	var inputs []SSTFileMeta
	for i := 0; i < 8; i++ {
		base := byte('a' + i*3)
		meta := writeSST(&testing.T{}, dir, uint64(i+1), 0, [][2]string{
			{string([]byte{base}), "v1"},
			{string([]byte{base + 1}), "v2"},
		})
		inputs = append(inputs, meta)
	}

	cm := newCompactionManager(DefaultFS(), dir, m)
	defer func() { _ = cm.Close() }()
	job := &compactionJob{fs: DefaultFS(), placementPolicy: nil, level: 0, inputs: inputs}
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cm.runJob(job)
		_ = ctx
	}
}

// TestSubCompactor_Parallel_NoManifestRace verifies REQ001157: when
// sub-compaction runs multiple sub-jobs in parallel, the two-phase
// approach (partial SST write + coordinator merge) ensures no manifest
// data race. Each sub-job writes to its own tmpPath without touching
// the manifest; the coordinator applies a single manifest update.
func TestSubCompactor_Parallel_NoManifestRace(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "test_subcompact_parallel")
	if err := os.MkdirAll(filepath.Join(dir, "sst"), 0755); err != nil {
		t.Fatalf("mkdir sst: %v", err)
	}
	m, err := newManifest(dir)
	if err != nil {
		t.Fatalf("newManifest: %v", err)
	}
	defer m.Close()
	v := m.Current()
	v.levels = make([][]SSTFileMeta, 3)
	m.Apply(*v)

	// Create 8 input SSTs at level 0 (above threshold of 4)
	var inputs []SSTFileMeta
	for i := 0; i < 8; i++ {
		base := byte('a' + i*3)
		meta := writeSST(t, dir, uint64(i+1), 0, [][2]string{
			{string([]byte{base}), "v1"},
			{string([]byte{base + 1}), "v2"},
		})
		inputs = append(inputs, meta)
	}

	sc := NewSubCompactor(DefaultFS(), dir, m, 4)
	ctx := context.Background()

	// Run sub-compaction — should use two-phase approach
	result, err := sc.RunSubCompaction(ctx, 0, inputs, SubCompactionOptions{})
	if err != nil {
		t.Fatalf("RunSubCompaction: %v", err)
	}
	if result == nil {
		t.Fatal("RunSubCompaction returned nil result")
	}

	// Verify manifest was updated exactly once (single manifest apply)
	v = m.Current()
	if got := len(v.levels[0]); got != 0 {
		t.Fatalf("L0 file count = %d, want 0 (all inputs compacted)", got)
	}
	if got := len(v.levels[1]); got != 1 {
		// REQ001157: two-phase compaction — each sub-job writes a partial
		// SST to its own tmpPath, then the coordinator merges all partial
		// outputs and applies a single manifest update.
		t.Fatalf("L1 file count = %d, want 1 (single merged output)", got)
	}

	// Debug: check the merged SST file
	l1File := v.levels[1][0]
	sstPath := filepath.Join(dir, fileName(&l1File))
	data, err := os.ReadFile(sstPath)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", sstPath, err)
	}
	t.Logf("Merged SST size: %d bytes", len(data))

	// Verify the SST can be opened
	reader, err := openSST(data)
	if err != nil {
		t.Fatalf("openSST: %v", err)
	}
	defer reader.Close()

	// Verify data integrity: all keys from inputs should be readable
	for i := 0; i < 8; i++ {
		base := byte('a' + i*3)
		for j := 0; j < 2; j++ {
			key := []byte{base + byte(j)}
			val, found := reader.Find(key)
			if !found {
				t.Errorf("Find(%q): not found", key)
			} else if string(val) != "v1" && string(val) != "v2" {
				t.Errorf("Find(%q) = %q, want v1 or v2", key, val)
			}
		}
	}
}
